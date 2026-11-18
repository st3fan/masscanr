package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/st3fan/masscanr/internal/controller"
	"github.com/st3fan/masscanr/internal/runapi"
)

// Overlay database columns in the same SELECT: persisted JSON is not authoritative
// for admission, stop intent, or settlement (including V9 historical rows).
const runSnapshotSQL = `data || jsonb_build_object('snapshot_version',3,'active',active,'settled',NOT active,'stop_requested',stop,'summary',COALESCE(data->'summary','{}'::jsonb) || jsonb_build_object('ShardCount',shard_count,'CompletedShards',completed_shards))`

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

func (m *Manager) listRuns(c fiber.Ctx) error {
	q := make(map[string][]string)
	c.Request().URI().QueryArgs().VisitAll(func(k, v []byte) { q[string(k)] = append(q[string(k)], string(v)) })
	if _, ok := q["active"]; ok {
		if len(q) != 1 || len(q["active"]) != 1 || q["active"][0] != "true" {
			return bad(c, fmt.Errorf("active=true cannot be combined with history filters"))
		}
		runs, err := m.Store.ActiveRuns(c.Context())
		if err != nil {
			return problem(c, err)
		}
		return respond(c, 200, runapi.ActiveRuns{SnapshotVersion: runapi.SnapshotVersion, Runs: runs})
	}
	for key, values := range q {
		if len(values) != 1 || (key != "limit" && key != "after" && key != "filter[target_id]" && key != "filter[target_name]" && key != "filter[scan_plan_id]" && key != "filter[phase]") {
			return bad(c, fmt.Errorf("invalid run query parameter: %s", key))
		}
	}
	limit, err := resourceLimit(c)
	if err != nil {
		return bad(c, err)
	}
	filter := RunFilter{TargetID: c.Query("filter[target_id]"), ScanPlanID: c.Query("filter[scan_plan_id]"), Phase: c.Query("filter[phase]"), After: c.Query("after"), Limit: limit}
	targetName := c.Query("filter[target_name]")
	if filter.TargetID != "" && targetName != "" {
		return bad(c, fmt.Errorf("provide only one of filter[target_id] or filter[target_name]"))
	}
	if targetName != "" {
		target, err := m.Store.GetTargetByName(c.Context(), targetName)
		if err != nil {
			return problem(c, err)
		}
		filter.TargetID = target.ID
	}
	runs, err := m.Store.ListRuns(c.Context(), filter)
	if err != nil {
		return bad(c, err)
	}
	resources := make([]jsonAPIResource, 0, len(runs))
	for _, run := range runs {
		resource, err := makeResource("runs", run)
		if err != nil {
			return problem(c, err)
		}
		resources = append(resources, resource)
	}
	links := map[string]any{"self": string(c.Request().URI().RequestURI())}
	if len(runs) == limit {
		values := url.Values{}
		values.Set("limit", strconv.Itoa(limit))
		values.Set("after", runs[len(runs)-1].ID)
		for _, key := range []string{"filter[target_id]", "filter[target_name]", "filter[scan_plan_id]", "filter[phase]"} {
			if len(q[key]) > 0 && q[key][0] != "" {
				values.Set(key, q[key][0])
			}
		}
		links["next"] = c.Path() + "?" + values.Encode()
	}
	return sendJSONAPI(c, 200, map[string]any{"data": resources, "links": links, "meta": map[string]any{"snapshot_version": runapi.SnapshotVersion}})
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
	if e.ShardCount > 0 {
		r.Summary.ShardCount = e.ShardCount
	}
	if e.CompletedShards > r.Summary.CompletedShards {
		r.Summary.CompletedShards = e.CompletedShards
	}
	if e.Slot > 0 {
		applyShardedEvent(r, e, now)
		return
	}
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

func applyShardedEvent(r *Run, e controller.Event, now time.Time) {
	if r.WorkerSnapshots == nil {
		r.WorkerSnapshots = map[string]runapi.Worker{}
	}
	key := strconv.Itoa(e.Slot)
	w := r.WorkerSnapshots[key]
	w.Slot = e.Slot
	w.Shard = 0
	if e.AssignedShards > 0 {
		w.AssignedShards = e.AssignedShards
	}
	if e.CompletedForSlot > w.CompletedShards {
		w.CompletedShards = e.CompletedForSlot
	}
	if e.ID != "" && e.ID != w.ID {
		w.ID = e.ID
		w.InstanceCount++
		w.Progress, w.ProgressAt, w.Found = nil, nil, nil
	}
	if e.IP != "" {
		w.IP = e.IP
	}
	if e.Shard > 0 && e.State != "completed" {
		if w.CurrentShard != e.Shard || w.Attempt != e.Attempt {
			w.Progress, w.ProgressAt = nil, nil
		}
		w.CurrentShard, w.Attempt = e.Shard, e.Attempt
	}
	switch e.State {
	case "deleting", "shutdown", "cleanup failed":
		w.Cleanup, w.CleanupDetail = e.State, e.Detail
	case "finding":
	case "completed":
		w.CurrentShard = 0
		w.Progress, w.ProgressAt = nil, nil
		if w.CompletedShards == w.AssignedShards {
			w.State = "completed"
		} else {
			w.State = "ready"
		}
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
	if e.Found != nil {
		n := *e.Found
		w.Found = &n
	}
	r.WorkerSnapshots[key] = w
	if r.Phase != "cleanup" && r.Phase != "exporting" {
		phase := "provisioning"
		for _, slot := range r.WorkerSnapshots {
			switch slot.State {
			case "scanning", "retrying":
				phase = "scanning"
			case "preparing", "ready":
				if phase == "provisioning" {
					phase = "preparing"
				}
			}
		}
		r.Phase = phase
	}
}
