package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5"
	"github.com/st3fan/masscanr/internal/backend/registry"
	"github.com/st3fan/masscanr/internal/controller"
	"github.com/st3fan/masscanr/internal/ripe"
	"github.com/st3fan/masscanr/internal/scan"
)

func (a *API) preflightScanPlan(c fiber.Ctx) error {
	plan, err := a.Store.GetScanPlan(c.Context(), c.Params("id"))
	if err != nil {
		return problem(c, err)
	}
	if plan.ArchivedAt != nil {
		return problem(c, ErrConflict)
	}
	target, err := a.Store.GetTarget(c.Context(), plan.TargetID)
	if err != nil {
		return problem(c, err)
	}
	if target.ArchivedAt != nil {
		return problem(c, ErrConflict)
	}
	exclusions, err := a.Store.FreezeExclusions(c.Context(), plan.ExclusionIDs)
	if err != nil {
		return problem(c, err)
	}
	cfg, timeouts, err := plan.Settings.Config(target.Inputs())
	if err != nil {
		return bad(c, err)
	}
	resolve := a.Resolve
	if resolve == nil {
		resolve = ripe.New().Resolve
	}
	cfg.RequestedTargets, err = resolve(c.Context(), cfg.RequestedTargets)
	if err != nil {
		return respond(c, 422, map[string]string{"error": "target resolution failed"})
	}
	requestedScope := scan.TargetUnion(cfg.RequestedTargets)
	exclusionUnion, err := scan.ExclusionUnion(exclusions)
	if err != nil {
		return bad(c, err)
	}
	cfg.Targets, err = scan.SubtractPrefixes(requestedScope, exclusionUnion)
	if err != nil {
		return bad(c, err)
	}
	if len(cfg.Targets) == 0 {
		return bad(c, fmt.Errorf("effective target scope is empty after exclusions"))
	}
	excludedScope, err := scan.SubtractPrefixes(requestedScope, cfg.Targets)
	if err != nil {
		return bad(c, err)
	}
	cfg.Exclusions = exclusions
	if err = cfg.Validate(); err != nil {
		return bad(c, err)
	}
	if err = validateFrozenConfigSize(cfg); err != nil {
		return bad(c, err)
	}
	backend, err := registry.Resolve(cfg.Backend)
	if err == nil {
		err = backend.Validate(cfg.Intent(), 1)
	}
	if err != nil {
		return bad(c, err)
	}
	manifest, err := scan.Partition(cfg.Targets, len(cfg.Ports))
	if err != nil {
		return bad(c, err)
	}
	workload, err := cfg.Intent().Workload()
	if err != nil {
		return bad(c, err)
	}
	preflight := Plan{
		ID:       newID(),
		TargetID: target.ID, TargetRevision: target.Revision,
		ScanPlanID: plan.ID, ScanPlanRevision: plan.Revision,
		RequestedScope: requestedScope, ExcludedScope: excludedScope,
		ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
		Config:    cfg, Timeouts: timeouts, Workload: workload,
		ShardCount: manifest.Count, EffectiveWorkers: min(cfg.Workers, int(manifest.Count)), MaxProbesPerShard: scan.MaxProbesPerShard,
		RequiresConfirmation: workload.RequiresConfirmation(),
	}
	err = a.Store.tx(c.Context(), func(tx pgx.Tx) error {
		currentTarget, e := readTarget(c.Context(), tx, target.ID)
		if e != nil {
			return e
		}
		currentPlan, e := readScanPlanLocked(c.Context(), tx, plan.ID)
		if e != nil {
			return e
		}
		if currentTarget.Revision != target.Revision || currentTarget.ArchivedAt != nil || currentPlan.Revision != plan.Revision || currentPlan.ArchivedAt != nil || currentPlan.TargetID != target.ID || !snapshotsMatchPlan(currentPlan.ExclusionIDs, exclusions) {
			return ErrConflict
		}
		if e = validateFrozenExclusions(c.Context(), tx, exclusions); e != nil {
			return e
		}
		data, e := json.Marshal(preflight)
		if e != nil {
			return e
		}
		_, e = tx.Exec(c.Context(), `INSERT INTO masscanr.scan_preflights(id,target_id,scan_plan_id,target_revision,scan_plan_revision,expires_at,data) VALUES($1,$2,$3,$4,$5,$6,$7)`, preflight.ID, target.ID, plan.ID, target.Revision, plan.Revision, preflight.ExpiresAt, data)
		return e
	})
	if err != nil {
		return problem(c, err)
	}
	return respond(c, 201, preflight)
}

func (s *Store) GetPreflight(ctx context.Context, id string) (Plan, error) {
	return readJSON[Plan](ctx, s.Pool, `SELECT data FROM masscanr.scan_preflights WHERE id=$1`, id)
}

func (s *Store) ReserveScanPlan(ctx context.Context, scanPlanID, preflightID, key string, confirm bool) (Run, error) {
	var run Run
	err := s.tx(ctx, func(tx pgx.Tx) error {
		existing, e := readJSON[Run](ctx, tx, `SELECT `+runSnapshotSQL+` FROM masscanr.runs WHERE request_key=$1`, key)
		if e == nil {
			if existing.ScanPlanID != scanPlanID || existing.Plan.ID != preflightID {
				return ErrConflict
			}
			run = existing
			return nil
		}
		if e != ErrNotFound {
			return e
		}
		plan, e := readScanPlanLocked(ctx, tx, scanPlanID)
		if e != nil {
			return e
		}
		target, e := readTarget(ctx, tx, plan.TargetID)
		if e != nil {
			return e
		}
		preflight, e := readJSON[Plan](ctx, tx, `SELECT data FROM masscanr.scan_preflights WHERE id=$1`, preflightID)
		if e != nil {
			return e
		}
		if plan.ArchivedAt != nil || target.ArchivedAt != nil || preflight.ScanPlanID != scanPlanID || preflight.TargetID != target.ID || preflight.TargetRevision != target.Revision || preflight.ScanPlanRevision != plan.Revision || !snapshotsMatchPlan(plan.ExclusionIDs, preflight.Config.Exclusions) || !time.Now().Before(preflight.ExpiresAt) || preflight.RequiresConfirmation && !confirm {
			return ErrConflict
		}
		if e = validateFrozenExclusions(ctx, tx, preflight.Config.Exclusions); e != nil {
			return e
		}
		if e = validatePreflightScope(preflight); e != nil {
			return ErrConflict
		}
		var active bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM masscanr.runs WHERE active)`).Scan(&active); e != nil {
			return e
		}
		if active {
			return ErrConflict
		}
		run = Run{SnapshotVersion: 3, Active: true, ID: newID(), TargetID: target.ID, ScanPlanID: plan.ID, Plan: preflight, Phase: "accepted", CreatedAt: time.Now().UTC(), Summary: controller.Summary{ShardCount: preflight.ShardCount}}
		data, e := json.Marshal(run)
		if e != nil {
			return e
		}
		_, e = tx.Exec(ctx, `INSERT INTO masscanr.runs(id,target_id,scan_plan_id,request_key,workflow_id,shard_count,requested_workers,effective_workers,data) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, run.ID, target.ID, plan.ID, key, run.ID, int64(preflight.ShardCount), preflight.Config.Workers, preflight.EffectiveWorkers, data)
		return e
	})
	return run, err
}
