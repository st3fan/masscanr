package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/st3fan/masscanr/internal/controller"
	"github.com/st3fan/masscanr/internal/runapi"
)

// Overlay database columns in the same SELECT: persisted JSON is not authoritative
// for admission, stop intent, or settlement (including V9 historical rows).
const runSnapshotSQL = `data || jsonb_build_object('snapshot_version',2,'active',active,'settled',NOT active,'stop_requested',stop)`

func (s *Store) ActiveRuns(ctx context.Context) ([]Run, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+runSnapshotSQL+` FROM masscanr.runs WHERE active ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		var r Run
		if err := json.Unmarshal(b, &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (m *Manager) activeRuns(c fiber.Ctx) error {
	q := make(map[string][]string)
	c.Request().URI().QueryArgs().VisitAll(func(k, v []byte) { q[string(k)] = append(q[string(k)], string(v)) })
	if len(q) != 1 || len(q["active"]) != 1 || q["active"][0] != "true" {
		return bad(c, fmt.Errorf("active=true is required; historical runs are listed per scan"))
	}
	runs, err := m.Store.ActiveRuns(c.Context())
	if err != nil {
		return problem(c, err)
	}
	return respond(c, 200, runapi.ActiveRuns{SnapshotVersion: runapi.SnapshotVersion, Runs: runs})
}
func (m *Manager) stopRun(c fiber.Ctx) error {
	id := c.Params("id")
	if err := m.requestStop(c.Context(), id); err != nil {
		return problem(c, err)
	}
	v, err := m.Store.GetRun(c.Context(), id)
	if err != nil {
		return problem(c, err)
	}
	return respond(c, 202, v)
}

func applyEvent(r *Run, e controller.Event, now time.Time) {
	// Keep the original V9 field for existing clients.
	if r.Events == nil {
		r.Events = map[int]controller.Event{}
	}
	r.Events[e.Shard] = e
	if e.Identity != nil {
		r.Summary.Identity = *e.Identity
	}
	if e.Seed != nil {
		r.Summary.Seed = *e.Seed
	}
	if e.RunID != "" {
		r.Summary.RunID = e.RunID
	}
	if e.Shard == 0 && e.ID == "" {
		if e.State != "finding" {
			r.Phase = e.State
		}
		return
	}
	if r.WorkerSnapshots == nil {
		r.WorkerSnapshots = map[string]runapi.Worker{}
	}
	key := strconv.Itoa(e.Shard)
	if e.Shard == 0 {
		key = "instance:" + e.ID
		// Reconciliation can rediscover a known worker without its shard number.
		for k, w := range r.WorkerSnapshots {
			if w.ID == e.ID {
				key = k
				break
			}
		}
	}
	w := r.WorkerSnapshots[key]
	if e.Shard != 0 {
		w.Shard = e.Shard
	}
	if e.ID != "" {
		w.ID = e.ID
	}
	if e.IP != "" {
		w.IP = e.IP
	}
	switch e.State {
	case "deleting", "shutdown", "cleanup failed":
		w.Cleanup, w.CleanupDetail = e.State, e.Detail
	case "finding":
	default:
		w.State = e.State
		if e.Detail != "" {
			w.Detail = e.Detail
		}
	}
	if e.Progress != nil {
		p := *e.Progress
		w.Progress = &p
		at := now
		w.ProgressAt = &at
	}
	if e.Prepared != nil {
		p := *e.Prepared
		w.Prepared = &p
	}
	if e.Found != nil {
		n := *e.Found
		if w.Found != nil {
			n = max(n, *w.Found)
		}
		w.Found = &n
	}
	if e.Progress != nil {
		n := e.Progress.Found
		if w.Found != nil {
			n = max(n, *w.Found)
		}
		w.Found = &n
	}
	if w.Progress != nil && w.Found != nil {
		w.Progress.Found = *w.Found
	}
	r.WorkerSnapshots[key] = w
	// A completed/deleted shard must not hide peers which are still scanning.
	if r.Phase != "cleanup" && r.Phase != "exporting" {
		phase, priority := "provisioning", 0
		for _, worker := range r.WorkerSnapshots {
			candidate, rank := "provisioning", 0
			switch worker.State {
			case "failed", "cancelled":
				candidate, rank = "stopping", 5
			case "scanning":
				candidate, rank = "scanning", 4
			case "waiting", "collecting":
				candidate, rank = worker.State, 3
			case "preparing", "ready":
				candidate, rank = "preparing", 2
			case "completed":
				candidate, rank = "cleanup", 1
			}
			if rank > priority {
				phase, priority = candidate, rank
			}
		}
		r.Phase = phase
	}
}
