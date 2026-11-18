package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func validName(name string) bool { return strings.TrimSpace(name) != "" && len(name) <= 200 }

func (s *Store) CreateTarget(ctx context.Context, name string, inputs []string) (Target, error) {
	if !validName(name) {
		return Target{}, fmt.Errorf("target name must contain 1..200 bytes")
	}
	entries, err := NormalizeTargetEntries(inputs)
	if err != nil {
		return Target{}, err
	}
	now := time.Now().UTC()
	target := Target{ID: newID(), Name: strings.TrimSpace(name), Revision: 1, CreatedAt: now, UpdatedAt: now, Entries: entries}
	err = s.tx(ctx, func(tx pgx.Tx) error {
		if _, e := tx.Exec(ctx, `INSERT INTO masscanr.targets(id,name,revision,created_at,updated_at) VALUES($1,$2,$3,$4,$5)`, target.ID, target.Name, target.Revision, now, now); e != nil {
			return targetNameConflict(e)
		}
		return writeTargetEntries(ctx, tx, target)
	})
	return target, err
}

func targetNameConflict(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "targets_name_unique" {
		return fmt.Errorf("%w: target name already exists", ErrConflict)
	}
	return err
}

func writeTargetEntries(ctx context.Context, tx pgx.Tx, target Target) error {
	for _, entry := range target.Entries {
		if _, err := tx.Exec(ctx, `INSERT INTO masscanr.target_entries(id,target_id,position,kind,selector,input) VALUES($1,$2,$3,$4,$5,$6)`, entry.ID, target.ID, entry.Position, entry.Kind, entry.Selector, entry.Input); err != nil {
			return err
		}
	}
	return nil
}

func readTarget(ctx context.Context, tx pgx.Tx, id string) (Target, error) {
	var target Target
	err := tx.QueryRow(ctx, `SELECT id,name,revision,created_at,updated_at,archived_at FROM masscanr.targets WHERE id=$1`, id).Scan(&target.ID, &target.Name, &target.Revision, &target.CreatedAt, &target.UpdatedAt, &target.ArchivedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Target{}, ErrNotFound
	}
	if err != nil {
		return Target{}, err
	}
	rows, err := tx.Query(ctx, `SELECT id,position,kind,selector,input FROM masscanr.target_entries WHERE target_id=$1 ORDER BY position`, id)
	if err != nil {
		return Target{}, err
	}
	defer rows.Close()
	target.Entries = []TargetEntry{}
	for rows.Next() {
		var entry TargetEntry
		if err = rows.Scan(&entry.ID, &entry.Position, &entry.Kind, &entry.Selector, &entry.Input); err != nil {
			return Target{}, err
		}
		target.Entries = append(target.Entries, entry)
	}
	return target, rows.Err()
}

func (s *Store) GetTarget(ctx context.Context, id string) (Target, error) {
	var target Target
	err := s.tx(ctx, func(tx pgx.Tx) error {
		var e error
		target, e = readTarget(ctx, tx, id)
		return e
	})
	return target, err
}

func (s *Store) GetTargetByName(ctx context.Context, name string) (Target, error) {
	name = strings.TrimSpace(name)
	if !validName(name) {
		return Target{}, ErrNotFound
	}
	var target Target
	err := s.tx(ctx, func(tx pgx.Tx) error {
		var id string
		if e := tx.QueryRow(ctx, `SELECT id FROM masscanr.targets WHERE lower(btrim(name))=lower($1)`, name).Scan(&id); e != nil {
			if errors.Is(e, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return e
		}
		var e error
		target, e = readTarget(ctx, tx, id)
		return e
	})
	return target, err
}

type TargetPatch struct {
	Entries  *[]string `json:"entries"`
	Archived *bool     `json:"archived"`
}

func (s *Store) UpdateTarget(ctx context.Context, id string, revision int64, patch TargetPatch) (Target, error) {
	if revision < 1 || patch.Entries == nil && patch.Archived == nil {
		return Target{}, fmt.Errorf("revision and changed attributes are required")
	}
	var updated Target
	err := s.tx(ctx, func(tx pgx.Tx) error {
		old, err := readTarget(ctx, tx, id)
		if err != nil {
			return err
		}
		if old.Revision != revision || old.ArchivedAt != nil {
			return ErrConflict
		}
		updated = old
		if patch.Entries != nil {
			entries, e := NormalizeTargetEntries(*patch.Entries)
			if e != nil {
				return e
			}
			existing := make(map[string]string, len(old.Entries))
			for _, entry := range old.Entries {
				existing[entry.Selector] = entry.ID
			}
			for i := range entries {
				if id := existing[entries[i].Selector]; id != "" {
					entries[i].ID = id
				}
			}
			updated.Entries = entries
		}
		if patch.Archived != nil {
			if !*patch.Archived {
				return fmt.Errorf("archived may only be set to true")
			}
			now := time.Now().UTC()
			updated.ArchivedAt = &now
		}
		updated.Revision++
		updated.UpdatedAt = time.Now().UTC()
		if _, err = tx.Exec(ctx, `UPDATE masscanr.targets SET revision=$2,updated_at=$3,archived_at=$4 WHERE id=$1`, updated.ID, updated.Revision, updated.UpdatedAt, updated.ArchivedAt); err != nil {
			return err
		}
		if patch.Entries != nil {
			if _, err = tx.Exec(ctx, `DELETE FROM masscanr.target_entries WHERE target_id=$1`, updated.ID); err != nil {
				return err
			}
			return writeTargetEntries(ctx, tx, updated)
		}
		return nil
	})
	return updated, err
}

func (s *Store) ListTargets(ctx context.Context, after string, limit int) ([]Target, error) {
	ids, err := s.listResourceIDs(ctx, "targets", after, limit)
	if err != nil {
		return nil, err
	}
	out := make([]Target, 0, len(ids))
	for _, id := range ids {
		target, err := s.GetTarget(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, target)
	}
	return out, nil
}

func (s *Store) CreateScanPlan(ctx context.Context, name, targetID string, settings ScanPlanSettings) (ScanPlan, error) {
	if !validName(name) {
		return ScanPlan{}, fmt.Errorf("scan plan name must contain 1..200 bytes")
	}
	now := time.Now().UTC()
	plan := ScanPlan{ID: newID(), TargetID: targetID, Name: strings.TrimSpace(name), Revision: 1, CreatedAt: now, UpdatedAt: now, Settings: settings}
	err := s.tx(ctx, func(tx pgx.Tx) error {
		target, e := readTarget(ctx, tx, targetID)
		if e != nil {
			return e
		}
		if target.ArchivedAt != nil {
			return ErrConflict
		}
		if _, _, e = settings.Config(target.Inputs()); e != nil {
			return e
		}
		data, e := json.Marshal(settings)
		if e != nil {
			return e
		}
		_, e = tx.Exec(ctx, `INSERT INTO masscanr.scan_plans(id,target_id,name,revision,provider,settings,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, plan.ID, plan.TargetID, plan.Name, plan.Revision, settings.Provider, data, now, now)
		return e
	})
	return plan, err
}

func readScanPlan(ctx context.Context, tx pgx.Tx, id string) (ScanPlan, error) {
	var plan ScanPlan
	var settings []byte
	err := tx.QueryRow(ctx, `SELECT id,target_id,name,revision,settings,created_at,updated_at FROM masscanr.scan_plans WHERE id=$1`, id).Scan(&plan.ID, &plan.TargetID, &plan.Name, &plan.Revision, &settings, &plan.CreatedAt, &plan.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ScanPlan{}, ErrNotFound
	}
	if err == nil {
		err = json.Unmarshal(settings, &plan.Settings)
	}
	return plan, err
}

func (s *Store) GetScanPlan(ctx context.Context, id string) (ScanPlan, error) {
	var plan ScanPlan
	err := s.tx(ctx, func(tx pgx.Tx) error {
		var e error
		plan, e = readScanPlan(ctx, tx, id)
		return e
	})
	return plan, err
}

func (s *Store) UpdateScanPlan(ctx context.Context, plan ScanPlan, revision int64) (ScanPlan, error) {
	if revision < 1 || !validName(plan.Name) {
		return ScanPlan{}, fmt.Errorf("revision and scan plan name are required")
	}
	err := s.tx(ctx, func(tx pgx.Tx) error {
		old, e := readScanPlan(ctx, tx, plan.ID)
		if e != nil {
			return e
		}
		if old.Revision != revision {
			return ErrConflict
		}
		target, e := readTarget(ctx, tx, plan.TargetID)
		if e != nil {
			return e
		}
		if target.ArchivedAt != nil {
			return ErrConflict
		}
		if _, _, e = plan.Settings.Config(target.Inputs()); e != nil {
			return e
		}
		data, e := json.Marshal(plan.Settings)
		if e != nil {
			return e
		}
		plan.Name = strings.TrimSpace(plan.Name)
		plan.Revision = revision + 1
		plan.CreatedAt = old.CreatedAt
		plan.UpdatedAt = time.Now().UTC()
		_, e = tx.Exec(ctx, `UPDATE masscanr.scan_plans SET target_id=$2,name=$3,revision=$4,provider=$5,settings=$6,updated_at=$7 WHERE id=$1`, plan.ID, plan.TargetID, strings.TrimSpace(plan.Name), plan.Revision, plan.Settings.Provider, data, plan.UpdatedAt)
		return e
	})
	return plan, err
}

func (s *Store) ListScanPlans(ctx context.Context, after string, limit int) ([]ScanPlan, error) {
	ids, err := s.listResourceIDs(ctx, "scan_plans", after, limit)
	if err != nil {
		return nil, err
	}
	out := make([]ScanPlan, 0, len(ids))
	for _, id := range ids {
		plan, err := s.GetScanPlan(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, plan)
	}
	return out, nil
}

func (s *Store) listResourceIDs(ctx context.Context, table, after string, limit int) ([]string, error) {
	if table != "targets" && table != "scan_plans" || limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("invalid resource list")
	}
	rows, err := s.Pool.Query(ctx, `SELECT id FROM masscanr.`+table+` WHERE id>$1 ORDER BY id LIMIT $2`, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
