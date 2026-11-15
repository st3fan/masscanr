package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("conflict")

type Store struct{ Pool *pgxpool.Pool }

type RunFilter struct {
	TargetID   string
	ScanPlanID string
	Phase      string
	After      string
	Limit      int
}

func (s *Store) ListRuns(ctx context.Context, f RunFilter) ([]Run, error) {
	if f.Limit < 1 || f.Limit > 1000 {
		return nil, fmt.Errorf("limit must be 1..1000")
	}
	query := `SELECT ` + runSnapshotSQL + ` FROM masscanr.runs WHERE true`
	args := []any{}
	add := func(v any) string { args = append(args, v); return fmt.Sprintf("$%d", len(args)) }
	if f.TargetID != "" {
		query += ` AND target_id=` + add(f.TargetID)
	}
	if f.ScanPlanID != "" {
		query += ` AND scan_plan_id=` + add(f.ScanPlanID)
	}
	if f.Phase != "" {
		query += ` AND data->>'phase'=` + add(f.Phase)
	}
	if f.After != "" {
		var exists bool
		if err := s.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM masscanr.runs WHERE id=$1)`, f.After).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			return nil, fmt.Errorf("invalid run cursor")
		}
		query += ` AND ((data->>'created_at')::timestamptz,id)<(SELECT (data->>'created_at')::timestamptz,id FROM masscanr.runs WHERE id=` + add(f.After) + `)`
	}
	query += ` ORDER BY (data->>'created_at')::timestamptz DESC,id DESC LIMIT ` + add(f.Limit)
	rows, err := s.Pool.Query(ctx, query, args...)
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
		var run Run
		if err := json.Unmarshal(b, &run); err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

// Open initializes the current schema on a fresh development database.
// Earlier schemas are deliberately unsupported and must be reset.
func Open(ctx context.Context, url string) (*Store, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("invalid database configuration")
	}
	store := &Store{Pool: pool}
	err = store.tx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS masscanr;
CREATE TABLE IF NOT EXISTS masscanr.version (version integer PRIMARY KEY);
INSERT INTO masscanr.version VALUES (3) ON CONFLICT DO NOTHING;`); err != nil {
			return err
		}
		var count, version int
		if err := tx.QueryRow(ctx, `SELECT count(*), max(version) FROM masscanr.version`).Scan(&count, &version); err != nil {
			return err
		}
		if count != 1 || version != 3 {
			return fmt.Errorf("unsupported database schema: use a fresh development database")
		}
		return initDomainSchema(ctx, tx)
	})
	if err != nil {
		pool.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) tx(ctx context.Context, f func(pgx.Tx) error) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(746592016)`); err != nil {
		return err
	}
	if err = f(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func readJSON[T any](ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, sql string, args ...any) (T, error) {
	var value T
	var data []byte
	err := q.QueryRow(ctx, sql, args...).Scan(&data)
	if errors.Is(err, pgx.ErrNoRows) {
		return value, ErrNotFound
	}
	if err == nil {
		err = json.Unmarshal(data, &value)
	}
	return value, err
}

func (s *Store) GetRun(ctx context.Context, id string) (Run, error) {
	return readJSON[Run](ctx, s.Pool, `SELECT `+runSnapshotSQL+` FROM masscanr.runs WHERE id=$1`, id)
}

func (s *Store) ListRunsForPlan(ctx context.Context, scanPlanID, after string, limit int) ([]json.RawMessage, error) {
	if limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("limit must be 1..1000")
	}
	rows, err := s.Pool.Query(ctx, `SELECT `+runSnapshotSQL+` FROM masscanr.runs WHERE id>$1 AND scan_plan_id=$3 ORDER BY id LIMIT $2`, after, limit, scanPlanID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []json.RawMessage{}
	for rows.Next() {
		var data []byte
		if err = rows.Scan(&data); err != nil {
			return nil, err
		}
		out = append(out, json.RawMessage(data))
	}
	return out, rows.Err()
}
