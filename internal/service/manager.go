package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5"
	"github.com/st3fan/masscanr/internal/backend/registry"
	"github.com/st3fan/masscanr/internal/cloud"
	"github.com/st3fan/masscanr/internal/cloud/digitalocean"
	"github.com/st3fan/masscanr/internal/cloud/vultr"
	"github.com/st3fan/masscanr/internal/controller"
	"github.com/st3fan/masscanr/internal/remote"
	"github.com/st3fan/masscanr/internal/results"
	"github.com/st3fan/masscanr/internal/scanfile"
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
		return &controller.Controller{RunID: r.ID, Backend: b, Provider: providerFor(r), Runner: remote.NewSSH(), Timeouts: r.Plan.Timeouts, ShardStore: s, ExpectedIdentity: r.Summary.Identity}
	}
	dbos.RegisterWorkflow(dc, m.cleanupWorkflow, dbos.WithWorkflowName("masscanr.cleanup.v10"))
	dbos.RegisterWorkflow(dc, m.workflow, dbos.WithWorkflowName("masscanr.scan.v10"))
	return m
}

func providerFor(r Run) cloud.Provider {
	if r.Plan.Config.Provider == "digitalocean" {
		return digitalocean.New(os.Getenv("DIGITALOCEAN_API_TOKEN"), os.Getenv("DIGITALOCEAN_SSH_SEED"), r.ID)
	}
	return vultr.New(os.Getenv("VULTR_API_TOKEN"))
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
	if stop {
		return m.Store.mutateRun(ctx, id, func(r *Run) { r.Phase = "cancelled"; r.Summary.ScanOutcome = "cancelled" })
	}
	r, e := m.Store.GetRun(ctx, id)
	if e != nil {
		return e
	}
	resuming := attempted && r.Phase == "resuming"
	if attempted && !resuming {
		return m.Store.mutateRun(ctx, id, func(r *Run) { r.Phase = "interrupted"; r.Summary.ScanOutcome = "interrupted" })
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
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if resuming {
		flags = os.O_RDWR | os.O_APPEND
	}
	file, e := os.OpenFile(path, flags, 0600)
	if e != nil {
		return m.Store.mutateRun(ctx, id, func(r *Run) {
			r.Phase = "failed"
			r.Summary.ScanOutcome = "failed"
			r.Error = "artifact creation failed"
		})
	}
	var resumeState scanfile.State
	if resuming {
		resumeState, e = scanfile.Replay(file, nil)
		if e == nil && (resumeState.Header == nil || resumeState.Header.RunID != id || resumeState.Truncated || resumeState.Finished != nil || len(resumeState.ActiveShardAttempts()) != 0) {
			e = fmt.Errorf("recovered journal is not resumable")
		}
		if e == nil {
			_, e = file.Seek(0, 2)
		}
		if e != nil {
			file.Close()
			return e
		}
	}
	dest := privateArtifact{Destination: scanfile.Destination{Writer: file, SyncFunc: file.Sync, Output: path, Exports: [2]string{path[:len(path)-5] + ".csv", path[:len(path)-5] + ".jsonl"}}, secrets: []string{os.Getenv("VULTR_API_TOKEN"), os.Getenv("DIGITALOCEAN_API_TOKEN"), os.Getenv("DIGITALOCEAN_SSH_SEED")}}
	runner := m.Factory(r)
	runner.ShardStore = m.Store
	if resuming {
		if e = m.Store.Pool.QueryRow(ctx, `SELECT execution_epoch FROM masscanr.runs WHERE id=$1`, id).Scan(&runner.Epoch); e != nil {
			file.Close()
			return e
		}
	}
	if runner.ResultSink == nil {
		runner.ResultSink = m.Store.ResultSink(r.ID, r.TargetID)
	}
	summary, runErr := runner.RunSharded(c, r.Plan.Config, dest, func(event controller.Event) {
		eventCtx, stopEvent := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopEvent()
		err := m.Store.mutateRun(eventCtx, id, func(r *Run) {
			applyEvent(r, event, time.Now().UTC())
		})
		if err != nil {
			cancel()
		}
	}, resumeState)
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
		if e = results.ExportResults(path); e != nil {
			message = "export failed; journal retained"
		}
	}
	resultCtx, cancelResults := context.WithTimeout(context.Background(), 2*time.Minute)
	resultErr := m.Store.FinalizeRunResults(resultCtx, id, path)
	cancelResults()
	if resultErr != nil {
		phase = "failed"
		message = "result persistence incomplete; journal retained"
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
			r.Phase = "cleanup_incomplete"
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
func (m *Manager) Routes(a *API, app *fiber.App) {
	artifacts := artifactAPI{dir: m.Dir, base: a.Base, getRun: m.Store.GetRun}
	a.register("POST", "/v1/scan-plans/:id/start", "startScanPlan", "Start a confirmed scan plan", Run{}, m.startScanPlan)
	a.register("POST", "/v1/runs/:id/stop", "stopRun", "Request run stop", Run{}, m.stopRun)
	a.register("GET", "/v1/runs", "listRuns", "List run history or active runs", Run{}, m.listRuns)
	a.register("GET", "/v1/runs/:id", "getRun", "Get a run", Run{}, func(c fiber.Ctx) error {
		v, e := m.Store.GetRun(c.Context(), c.Params("id"))
		if e != nil {
			return problem(c, e)
		}
		return respond(c, 200, v)
	})
	a.register("GET", "/v1/runs/:id/workers", "listRunWorkers", "List physical worker instances for a run", RunWorker{}, m.listRunWorkers)
	a.register("GET", "/v1/runs/:id/inspection", "inspectRun", "Inspect saved run journal", inspection{}, artifacts.inspect)
	a.register("GET", "/v1/runs/:id/exports/:format", "exportRun", "Download a complete export", nil, artifacts.export)
	a.register("GET", "/v1/scan-plans/:id/runs", "listScanPlanRuns", "List runs for a scan plan", Run{}, a.listPlanRuns)
}

func (m *Manager) listRunWorkers(c fiber.Ctx) error {
	id := c.Params("id")
	if _, err := m.Store.GetRun(c.Context(), id); err != nil {
		return problem(c, err)
	}
	limit, err := resourceLimit(c)
	if err != nil {
		return bad(c, err)
	}
	workers, err := m.Store.ListShardWorkers(c.Context(), id, c.Query("after"), limit)
	if err != nil {
		return problem(c, err)
	}
	return respond(c, 200, workers)
}

type privateArtifact struct {
	scanfile.Destination
	secrets []string
}

func (d privateArtifact) Sanitize(s string) string {
	for _, secret := range d.secrets {
		if secret != "" {
			s = strings.ReplaceAll(s, secret, "[REDACTED]")
		}
	}
	return s
}

// startScanPlan reserves an immutable preflight before launching its DBOS workflow.
func (m *Manager) startScanPlan(c fiber.Ctx) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return respond(c, 503, map[string]string{"error": "shutting down"})
	}
	var in struct {
		PreflightID string `json:"preflight_id"`
		Confirm     bool   `json:"confirm"`
	}
	if err := decode(c, &in); err != nil {
		return bad(c, err)
	}
	if in.PreflightID == "" {
		return bad(c, fmt.Errorf("preflight_id is required"))
	}
	key := c.Get("Idempotency-Key")
	if key == "" || len(key) > 200 {
		return bad(c, fmt.Errorf("Idempotency-Key of 1..200 bytes required"))
	}
	preflight, err := m.Store.GetPreflight(c.Context(), in.PreflightID)
	if err != nil {
		return problem(c, err)
	}
	switch preflight.Config.Provider {
	case "", "vultr":
		if os.Getenv("VULTR_API_TOKEN") == "" {
			return respond(c, 503, map[string]string{"error": "VULTR_API_TOKEN is required to start scans"})
		}
	case "digitalocean":
		if os.Getenv("DIGITALOCEAN_API_TOKEN") == "" || os.Getenv("DIGITALOCEAN_SSH_SEED") == "" {
			return respond(c, 503, map[string]string{"error": "DIGITALOCEAN_API_TOKEN and DIGITALOCEAN_SSH_SEED are required to start scans"})
		}
	default:
		return respond(c, 503, map[string]string{"error": "unsupported provider"})
	}
	run, err := m.Store.ReserveScanPlan(c.Context(), c.Params("id"), in.PreflightID, key, in.Confirm)
	if err != nil {
		return problem(c, err)
	}
	if err = m.Launch(run.ID); err != nil {
		return respond(c, 503, map[string]string{"error": "workflow launch pending; retry with the same idempotency key"})
	}
	return respond(c, 202, run)
}
