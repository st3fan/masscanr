package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5"
	"github.com/st3fan/masscanr/internal/backend/registry"
	"github.com/st3fan/masscanr/internal/ripe"
	"github.com/st3fan/masscanr/internal/scan"
)

func (a *API) preflight(c fiber.Ctx) error {
	v, e := a.Store.GetScan(c.Context(), c.Params("id"))
	if e != nil {
		return problem(c, e)
	}
	cfg, t, e := v.Definition.Config()
	if e != nil {
		return bad(c, e)
	}
	resolve := a.Resolve
	if resolve == nil {
		resolve = ripe.New().Resolve
	}
	cfg.RequestedTargets, e = resolve(c.Context(), cfg.RequestedTargets)
	if e != nil {
		return respond(c, 422, map[string]string{"error": "target resolution failed"})
	}
	cfg.Targets = scan.TargetUnion(cfg.RequestedTargets)
	if e = cfg.Validate(); e != nil {
		return bad(c, e)
	}
	b, e := registry.Resolve(cfg.Backend)
	if e == nil {
		e = b.Validate(cfg.Intent(), cfg.Shards)
	}
	if e != nil {
		return bad(c, e)
	}
	work, e := cfg.Intent().Workload()
	if e != nil {
		return bad(c, e)
	}
	p := Plan{ID: newID(), ScanID: v.ID, Revision: v.Revision, ExpiresAt: time.Now().UTC().Add(10 * time.Minute), Config: cfg, Timeouts: t, Workload: work, RequiresConfirmation: work.RequiresConfirmation()}
	e = a.Store.tx(c.Context(), func(tx pgx.Tx) error {
		current, e := readJSON[Scan](c.Context(), tx, `SELECT data FROM masscanr.scans WHERE id=$1`, v.ID)
		if e != nil {
			return e
		}
		if current.Revision != v.Revision {
			return ErrConflict
		}
		data, e := json.Marshal(p)
		if e != nil {
			return e
		}
		_, e = tx.Exec(c.Context(), `INSERT INTO masscanr.plans VALUES($1,$2,$3)`, p.ID, p.ScanID, data)
		return e
	})
	if e != nil {
		return problem(c, e)
	}
	return respond(c, 201, p)
}
func (s *Store) Reserve(ctx context.Context, scanID, planID, key string, confirm bool) (Run, error) {
	var out Run
	e := s.tx(ctx, func(tx pgx.Tx) error {
		old, e := readJSON[Run](ctx, tx, `SELECT `+runSnapshotSQL+` FROM masscanr.runs WHERE request_key=$1`, key)
		if e == nil {
			if old.ScanID != scanID || old.Plan.ID != planID {
				return ErrConflict
			}
			out = old
			return nil
		}
		if e != ErrNotFound {
			return e
		}
		v, e := readJSON[Scan](ctx, tx, `SELECT data FROM masscanr.scans WHERE id=$1`, scanID)
		if e != nil {
			return e
		}
		p, e := readJSON[Plan](ctx, tx, `SELECT data FROM masscanr.plans WHERE id=$1`, planID)
		if e != nil {
			return e
		}
		if p.ScanID != scanID || p.Revision != v.Revision || !time.Now().Before(p.ExpiresAt) || (p.RequiresConfirmation && !confirm) {
			return ErrConflict
		}
		var active bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM masscanr.runs WHERE active)`).Scan(&active); e != nil {
			return e
		}
		if active {
			return ErrConflict
		}
		out = Run{SnapshotVersion: 1, Active: true, ID: newID(), ScanID: scanID, Plan: p, Phase: "accepted", CreatedAt: time.Now().UTC()}
		data, e := json.Marshal(out)
		if e != nil {
			return e
		}
		_, e = tx.Exec(ctx, `INSERT INTO masscanr.runs(id,scan_id,request_key,data) VALUES($1,$2,$3,$4)`, out.ID, scanID, key, data)
		return e
	})
	return out, e
}
