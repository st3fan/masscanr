package service

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"github.com/jackc/pgx/v5"
	"github.com/st3fan/masscanr/internal/backend/registry"
	"github.com/st3fan/masscanr/internal/cli"
	"github.com/st3fan/masscanr/internal/cloud/vultr"
	"github.com/st3fan/masscanr/internal/controller"
	"github.com/st3fan/masscanr/internal/remote"
	"github.com/st3fan/masscanr/internal/scanfile"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Manager struct {
	Store   *Store
	DBOS    dbos.Context
	Dir     string
	Factory func(Run) *controller.Controller
	mu      sync.Mutex
	cancel  map[string]context.CancelFunc
	closing bool
}

func NewManager(s *Store, dc dbos.Context, dir string) *Manager {
	m := &Manager{Store: s, DBOS: dc, Dir: dir, cancel: map[string]context.CancelFunc{}}
	m.Factory = func(r Run) *controller.Controller {
		b, _ := registry.Resolve(r.Plan.Config.Backend)
		return &controller.Controller{RunID: r.ID, Backend: b, Provider: vultr.New(os.Getenv("VULTR_API_TOKEN")), Runner: remote.NewSSH(), Timeouts: r.Plan.Timeouts}
	}
	dbos.RegisterWorkflow(dc, m.workflow, dbos.WithWorkflowName("masscanr.scan.v9"))
	return m
}
func (s *Store) mutateRun(ctx context.Context, id string, f func(*Run)) error {
	return s.tx(ctx, func(tx pgx.Tx) error {
		r, e := readJSON[Run](ctx, tx, `SELECT data FROM masscanr.runs WHERE id=$1`, id)
		if e != nil {
			return e
		}
		f(&r)
		b, e := json.Marshal(r)
		if e != nil {
			return e
		}
		_, e = tx.Exec(ctx, `UPDATE masscanr.runs SET data=$2 WHERE id=$1`, id, b)
		return e
	})
}
func (m *Manager) Launch(id string) error {
	_, e := dbos.RunWorkflow(m.DBOS, m.workflow, id, dbos.WithWorkflowID(id))
	return e
}
func (m *Manager) workflow(ctx dbos.Context, id string) (string, error) {
	_, e := dbos.RunAsStep(ctx, func(c context.Context) (string, error) { return "", m.execute(c, id) }, dbos.WithStepName("execute-once"))
	if e != nil {
		return "", fmt.Errorf("scan execution persistence failed")
	}
	_, e = dbos.RunAsStep(ctx, func(c context.Context) (string, error) { return "", m.finish(c, id) }, dbos.WithStepName("reconcile-and-finish"))
	return id, e
}
func (m *Manager) execute(ctx context.Context, id string) error {
	var attempted, stop bool
	e := m.Store.tx(ctx, func(tx pgx.Tx) error {
		if e := tx.QueryRow(ctx, `SELECT attempted,stop FROM masscanr.runs WHERE id=$1`, id).Scan(&attempted, &stop); e != nil {
			return e
		}
		_, e := tx.Exec(ctx, `UPDATE masscanr.runs SET attempted=true WHERE id=$1`, id)
		return e
	})
	if e != nil {
		return e
	}
	if attempted {
		return m.Store.mutateRun(ctx, id, func(r *Run) { r.Phase = "interrupted"; r.Summary.ScanOutcome = "interrupted" })
	}
	if stop {
		return m.Store.mutateRun(ctx, id, func(r *Run) { r.Phase = "cancelled"; r.Summary.ScanOutcome = "cancelled" })
	}
	r, e := m.Store.GetRun(ctx, id)
	if e != nil {
		return e
	}
	path := filepath.Join(m.Dir, id+".scan")
	if e = m.Store.mutateRun(ctx, id, func(r *Run) { r.Artifact = path; r.Phase = "provisioning" }); e != nil {
		return e
	}
	c, cancel := context.WithCancel(ctx)
	defer cancel()
	m.mu.Lock()
	m.cancel[id] = cancel
	m.mu.Unlock()
	defer func() { m.mu.Lock(); delete(m.cancel, id); m.mu.Unlock() }()
	done := make(chan struct{})
	defer close(done)
	go func() {
		tick := time.NewTicker(200 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-tick.C:
				var stopped bool
				q, stopQuery := context.WithTimeout(context.Background(), time.Second)
				err := m.Store.Pool.QueryRow(q, `SELECT stop FROM masscanr.runs WHERE id=$1`, id).Scan(&stopped)
				stopQuery()
				if err != nil || stopped {
					cancel()
					return
				}
			}
		}
	}()
	file, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return m.Store.mutateRun(ctx, id, func(r *Run) {
			r.Phase = "failed"
			r.Summary.ScanOutcome = "failed"
			r.Error = "artifact creation failed"
		})
	}
	dest := scanfile.Destination{Writer: file, SyncFunc: file.Sync, Output: path, Exports: [2]string{path[:len(path)-5] + ".csv", path[:len(path)-5] + ".jsonl"}}
	summary, runErr := m.Factory(r).Run(c, r.Plan.Config, dest, func(event controller.Event) {
		err := m.Store.mutateRun(context.Background(), id, func(r *Run) {
			if r.Events == nil {
				r.Events = map[int]controller.Event{}
			}
			r.Events[event.Shard] = event
			if event.State != "finding" {
				r.Phase = event.State
			}
		})
		if err != nil {
			cancel()
		}
	})
	syncErr := file.Sync()
	closeErr := file.Close()
	phase := "completed"
	message := ""
	if runErr != nil || syncErr != nil || closeErr != nil {
		phase = "failed"
		message = "scan failed; inspect run journal and cleanup status"
	}
	if summary.ScanOutcome == "cancelled" {
		phase = "cancelled"
	}
	if runErr == nil && syncErr == nil && closeErr == nil {
		if e = cli.ExportResults(path); e != nil {
			message = "export failed; journal retained"
		}
	}
	return m.Store.mutateRun(context.Background(), id, func(r *Run) { r.Phase = phase; r.Summary = summary; r.Error = message })
}
func (m *Manager) finish(ctx context.Context, id string) error {
	r, e := m.Store.GetRun(ctx, id)
	if e != nil {
		return e
	}
	c, cancel := context.WithTimeout(context.Background(), r.Plan.Timeouts.Cleanup)
	defer cancel()
	remaining, unresolved, cleanupErr := m.Factory(r).CleanupRun(c, id)
	e = m.Store.mutateRun(ctx, id, func(r *Run) {
		r.Summary.Undeleted = remaining
		r.Summary.ReconciliationUnresolved = unresolved
		r.Summary.CleanupOutcome = "complete"
		if cleanupErr != nil {
			r.Summary.CleanupOutcome = "incomplete"
			r.Error = "owned worker cleanup incomplete"
		}
		if r.Summary.ScanOutcome == "" {
			r.Summary.ScanOutcome = "interrupted"
			r.Phase = "interrupted"
		}
	})
	if e != nil {
		return e
	}
	_, e = m.Store.Pool.Exec(ctx, `UPDATE masscanr.runs SET active=false WHERE id=$1`, id)
	return e
}
func (m *Manager) Routes(a *API, mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/scans/{id}/start", m.start)
	mux.HandleFunc("GET /v1/runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, e := m.Store.GetRun(r.Context(), r.PathValue("id"))
		if e != nil {
			problem(w, e)
			return
		}
		respond(w, 200, v)
	})
	mux.HandleFunc("GET /v1/scans/{id}/runs", func(w http.ResponseWriter, r *http.Request) { a.listTable(w, r, "runs", r.PathValue("id")) })
}
func (m *Manager) start(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		respond(w, 503, map[string]string{"error": "shutting down"})
		return
	}
	var in struct {
		PlanID  string `json:"plan_id"`
		Confirm bool   `json:"confirm"`
	}
	if e := decode(w, r, &in); e != nil {
		bad(w, e)
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if key == "" || len(key) > 200 {
		bad(w, fmt.Errorf("Idempotency-Key of 1..200 bytes required"))
		return
	}
	if os.Getenv("VULTR_API_TOKEN") == "" {
		respond(w, 503, map[string]string{"error": "VULTR_API_TOKEN is required to start scans"})
		return
	}
	v, e := m.Store.Reserve(r.Context(), r.PathValue("id"), in.PlanID, key, in.Confirm)
	if e != nil {
		problem(w, e)
		return
	}
	if e = m.Launch(v.ID); e != nil {
		respond(w, 503, map[string]string{"error": "workflow launch pending; retry with the same idempotency key"})
		return
	}
	respond(w, 202, v)
}
