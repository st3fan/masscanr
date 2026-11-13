package service

import (
	"context"
	"encoding/json"
	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"github.com/st3fan/masscanr/internal/scanfile"
	"net/http"
	"os"
	"time"
)

func (m *Manager) stop(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, e := m.Store.GetScan(r.Context(), id); e != nil {
		problem(w, e)
		return
	}
	var runID string
	e := m.Store.Pool.QueryRow(r.Context(), `SELECT id FROM masscanr.runs WHERE scan_id=$1 ORDER BY (data->>'created_at') DESC LIMIT 1`, id).Scan(&runID)
	if e != nil {
		problem(w, ErrNotFound)
		return
	}
	if e = m.requestStop(r.Context(), runID); e != nil {
		problem(w, e)
		return
	}
	v, e := m.Store.GetRun(r.Context(), runID)
	if e != nil {
		problem(w, e)
		return
	}
	respond(w, 202, v)
}
func (m *Manager) requestStop(ctx context.Context, id string) error {
	_, e := m.Store.Pool.Exec(ctx, `UPDATE masscanr.runs SET stop=true WHERE id=$1 AND active`, id)
	m.mu.Lock()
	if cancel := m.cancel[id]; cancel != nil {
		cancel()
	}
	m.mu.Unlock()
	return e
}

// PrepareRecovery runs before DBOS Launch, under the exclusive service lock.
// Any execution attempted by the previous process is cleanup-only.
func (m *Manager) PrepareRecovery(ctx context.Context) ([]Run, error) {
	rows, e := m.Store.Pool.Query(ctx, `SELECT data,attempted FROM masscanr.runs WHERE active`)
	if e != nil {
		return nil, e
	}
	var out []Run
	var attemptedIDs []string
	for rows.Next() {
		var b []byte
		var attempted bool
		if e = rows.Scan(&b, &attempted); e != nil {
			rows.Close()
			return nil, e
		}
		var r Run
		if e = json.Unmarshal(b, &r); e != nil {
			rows.Close()
			return nil, e
		}
		out = append(out, r)
		if attempted {
			attemptedIDs = append(attemptedIDs, r.ID)
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	for _, id := range attemptedIDs {
		if e = m.requestStop(ctx, id); e != nil {
			return nil, e
		}
		if e = dbos.CancelWorkflow(m.DBOS, id); e != nil {
			return nil, e
		}
		if e = m.replay(ctx, id); e != nil {
			return nil, e
		}
	}
	return out, nil
}
func (m *Manager) ResumeReserved(runs []Run) error {
	for _, r := range runs {
		var attempted bool
		if e := m.Store.Pool.QueryRow(context.Background(), `SELECT attempted FROM masscanr.runs WHERE id=$1`, r.ID).Scan(&attempted); e != nil {
			return e
		}
		if attempted {
			if _, e := dbos.RunWorkflow(m.DBOS, m.cleanupWorkflow, r.ID, dbos.WithWorkflowID(r.ID+"-cleanup-"+newID())); e != nil {
				return e
			}
		} else if e := m.Launch(r.ID); e != nil {
			return e
		}
	}
	return nil
}
func (m *Manager) cleanupWorkflow(ctx dbos.Context, id string) (string, error) {
	_, e := dbos.RunAsStep(ctx, func(c context.Context) (string, error) {
		if e := m.replay(c, id); e != nil {
			return "", e
		}
		return "", m.finish(c, id)
	}, dbos.WithStepName("owned-cleanup"))
	return id, e
}
func (m *Manager) replay(ctx context.Context, id string) error {
	r, e := m.Store.GetRun(ctx, id)
	if e != nil {
		return e
	}
	var state scanfile.State
	if r.Artifact != "" {
		if f, err := os.Open(r.Artifact); err == nil {
			state, err = scanfile.Replay(f, nil)
			f.Close()
			if err != nil {
				state = scanfile.State{}
			}
		}
	}
	return m.Store.mutateRun(ctx, id, func(r *Run) {
		r.Phase = "interrupted"
		r.Summary.ScanOutcome = "interrupted"
		if state.Header != nil && state.Header.RunID == id && state.Finished != nil {
			r.Summary.ScanOutcome = state.Finished.ScanOutcome
			r.Summary.Found = state.Finished.Found
			if state.Complete() {
				r.Phase = "completed"
			} else {
				r.Phase = state.Finished.ScanOutcome
			}
		}
	})
}
func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	m.closing = true
	m.mu.Unlock()
	rows, e := m.Store.Pool.Query(ctx, `SELECT id FROM masscanr.runs WHERE active`)
	if e != nil {
		return e
	}
	var ids []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		if e = m.requestStop(ctx, id); e != nil {
			return e
		}
	}
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		var n int
		if e = m.Store.Pool.QueryRow(ctx, `SELECT count(*) FROM masscanr.runs WHERE active`).Scan(&n); e != nil {
			return e
		}
		if n == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}
