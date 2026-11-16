package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"github.com/st3fan/masscanr/internal/runapi"
	"github.com/st3fan/masscanr/internal/scanfile"
)

func (m *Manager) requestStop(ctx context.Context, id string) error {
	result, e := m.Store.Pool.Exec(ctx, `UPDATE masscanr.runs SET stop=true WHERE id=$1 AND active`, id)
	if e != nil {
		return e
	}
	if result.RowsAffected() == 0 {
		_, e = m.Store.GetRun(ctx, id)
		return e
	}
	m.mu.Lock()
	if cancel := m.cancel[id]; cancel != nil {
		cancel()
	}
	m.mu.Unlock()
	return e
}

// PrepareRecovery runs before DBOS Launch, under the exclusive service lock.
// Valid sharded journals resume after owned-worker reconciliation; legacy or
// unsafe evidence remains cleanup-only.
func (m *Manager) PrepareRecovery(ctx context.Context) ([]Run, error) {
	rows, e := m.Store.Pool.Query(ctx, `SELECT data,attempted,COALESCE(NULLIF(workflow_id,''),id) FROM masscanr.runs WHERE active`)
	if e != nil {
		return nil, e
	}
	var out []Run
	var attemptedIDs []string
	workflowIDs := map[string]string{}
	for rows.Next() {
		var b []byte
		var attempted bool
		var workflowID string
		if e = rows.Scan(&b, &attempted, &workflowID); e != nil {
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
			workflowIDs[r.ID] = workflowID
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	for _, id := range attemptedIDs {
		if e = dbos.CancelWorkflow(m.DBOS, workflowIDs[id]); e != nil {
			return nil, e
		}
		r, e := m.Store.GetRun(ctx, id)
		if e != nil {
			return nil, e
		}
		if !r.StopRequested && r.Plan.ShardCount > 0 {
			if e = m.prepareShardContinuation(ctx, r); e == nil {
				continue
			}
		}
		if e = m.requestStop(ctx, id); e != nil {
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
		current, e := m.Store.GetRun(context.Background(), r.ID)
		if e != nil {
			return e
		}
		if attempted && current.Phase == "resuming" {
			workflowID := r.ID + "-resume-" + newID()
			if _, e := m.Store.Pool.Exec(context.Background(), `UPDATE masscanr.runs SET workflow_id=$2 WHERE id=$1 AND active`, r.ID, workflowID); e != nil {
				return e
			}
			if _, e := dbos.RunWorkflow(m.DBOS, m.workflow, r.ID, dbos.WithWorkflowID(workflowID)); e != nil {
				return e
			}
		} else if attempted {
			if _, e := dbos.RunWorkflow(m.DBOS, m.cleanupWorkflow, r.ID, dbos.WithWorkflowID(r.ID+"-cleanup-"+newID())); e != nil {
				return e
			}
		} else if e := m.Launch(r.ID); e != nil {
			return e
		}
	}
	return nil
}

// prepareShardContinuation fences old callbacks, deletes only workers with
// this run's ownership tag, closes interrupted attempts in the journal, and
// reconstructs the DB projection from validated evidence.
func (m *Manager) prepareShardContinuation(ctx context.Context, r Run) error {
	if r.Artifact == "" {
		return os.ErrNotExist
	}
	file, err := os.OpenFile(r.Artifact, os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	state, err := scanfile.Replay(file, nil)
	if err != nil || state.Header == nil || state.Header.Schema != scanfile.ShardedSchema || state.Header.RunID != r.ID || state.Finished != nil {
		return fmt.Errorf("run journal cannot continue: %w", err)
	}
	if state.Truncated {
		if err = preserveAndTrimJournalTail(file, r.Artifact); err != nil {
			return err
		}
		if _, err = file.Seek(0, io.SeekStart); err != nil {
			return err
		}
		state, err = scanfile.Replay(file, nil)
		if err != nil || state.Truncated {
			return fmt.Errorf("repaired journal could not be validated: %w", err)
		}
	}
	cleanupCtx, stop := context.WithTimeout(ctx, r.Plan.Timeouts.Cleanup)
	remaining, unresolved, cleanupErr := m.Factory(r).CleanupRun(cleanupCtx, r.ID)
	stop()
	if cleanupErr != nil || unresolved || len(remaining) != 0 {
		return fmt.Errorf("owned worker reconciliation failed: %w", cleanupErr)
	}
	if _, err = file.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	w, err := scanfile.ResumeWriter(scanfile.Destination{Writer: file, SyncFunc: file.Sync}, state, nil)
	if err != nil {
		return err
	}
	for _, a := range state.ActiveShardAttempts() {
		a.Phase, a.Detail = "failed", "controller restarted; retry whole shard"
		if err = w.Append(scanfile.Record{Type: "attempt", Attempt: &a}, true); err != nil {
			break
		}
	}
	if err == nil {
		for _, worker := range state.Workers {
			if worker.Cleanup != "complete" {
				worker.Phase, worker.Cleanup, worker.Detail = "shutdown", "complete", "reconciled after restart"
				if err = w.Append(scanfile.Record{Type: "worker", Worker: &worker}, true); err != nil {
					break
				}
			}
		}
	}
	err = errors.Join(err, w.Close())
	if err != nil {
		return err
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	state, err = scanfile.Replay(file, nil)
	if err != nil {
		return err
	}
	if _, err = m.Store.RestoreShardRun(ctx, r.ID, state); err != nil {
		return err
	}
	if err = m.Store.RebuildRunResults(ctx, r.ID, r.Artifact); err != nil {
		return err
	}
	completedBySlot := make(map[int]uint64)
	for _, attempt := range state.ShardAttempts() {
		if attempt.Phase == "completed" {
			completedBySlot[attempt.Slot]++
		}
	}
	return m.Store.mutateRun(ctx, r.ID, func(r *Run) {
		r.Phase = "resuming"
		r.Summary.Found = state.Found
		r.Summary.CompletedShards = state.CompletedShards()
		r.Summary.ShardCount = state.Header.Config.ShardCount
		if r.WorkerSnapshots == nil {
			r.WorkerSnapshots = map[string]runapi.Worker{}
		}
		for slot := 1; slot <= state.Header.Config.Workers; slot++ {
			key := strconv.Itoa(slot)
			worker := r.WorkerSnapshots[key]
			worker.Slot = slot
			worker.AssignedShards = (state.Header.Config.ShardCount-uint64(slot))/uint64(state.Header.Config.Workers) + 1
			worker.CompletedShards = completedBySlot[slot]
			if id := state.SlotInstance(slot); id != "" {
				worker.ID = id
			}
			worker.Cleanup = "shutdown"
			worker.State = "resuming"
			worker.CurrentShard = 0
			worker.Progress = nil
			worker.ProgressAt = nil
			r.WorkerSnapshots[key] = worker
		}
	})
}

// A process crash can leave a partial final JSON line. Preserve its bytes as
// private evidence before removing that unvalidated tail; complete records
// remain untouched and continue with their original sequence numbers.
func preserveAndTrimJournalTail(file *os.File, path string) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	length := min(info.Size(), int64(scanfile.MaxRecordBytes))
	tail := make([]byte, length)
	if _, err = file.ReadAt(tail, info.Size()-length); err != nil {
		return err
	}
	newline := bytes.LastIndexByte(tail, '\n')
	if newline < 0 || newline == len(tail)-1 {
		return fmt.Errorf("journal has no recoverable trailing record")
	}
	cut := info.Size() - length + int64(newline+1)
	evidence, err := os.OpenFile(path+".recovery-tail-"+newID(), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := evidence.Write(tail[newline+1:])
	writeErr = errors.Join(writeErr, evidence.Sync(), evidence.Close())
	if writeErr != nil {
		return writeErr
	}
	if err = file.Truncate(cut); err != nil {
		return err
	}
	return file.Sync()
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
	if r.Artifact != "" {
		// Recovery publishes only a validated journal prefix; repair failures
		// must not prevent cleanup of owned cloud workers.
		_ = m.Store.RebuildRunResults(ctx, id, r.Artifact)
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
