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

func Open(ctx context.Context, url string) (*Store, error) {
	p, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("invalid database configuration")
	}
	s := &Store{p}
	err = s.tx(ctx, func(tx pgx.Tx) error {
		_, e := tx.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS masscanr;
 CREATE TABLE IF NOT EXISTS masscanr.version (version integer PRIMARY KEY);
 INSERT INTO masscanr.version VALUES (1) ON CONFLICT DO NOTHING;`)
		if e != nil {
			return e
		}
		var v int
		if e = tx.QueryRow(ctx, `SELECT max(version) FROM masscanr.version`).Scan(&v); e != nil {
			return e
		}
		if v != 1 {
			return fmt.Errorf("unsupported database schema")
		}
		_, e = tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS masscanr.scans(id text PRIMARY KEY, data jsonb NOT NULL);
 CREATE TABLE IF NOT EXISTS masscanr.plans(id text PRIMARY KEY, scan_id text NOT NULL REFERENCES masscanr.scans(id) ON DELETE CASCADE, data jsonb NOT NULL);
 CREATE TABLE IF NOT EXISTS masscanr.runs(id text PRIMARY KEY, scan_id text NOT NULL, request_key text UNIQUE NOT NULL, active boolean NOT NULL DEFAULT true, attempted boolean NOT NULL DEFAULT false, stop boolean NOT NULL DEFAULT false, data jsonb NOT NULL);
 CREATE UNIQUE INDEX IF NOT EXISTS one_active_run ON masscanr.runs(active) WHERE active;`)
		return e
	})
	if err != nil {
		p.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) tx(ctx context.Context, f func(pgx.Tx) error) error {
	tx, e := s.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(context.Background())
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(746592016)`); e != nil {
		return e
	}
	if e = f(tx); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func readJSON[T any](ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, sql string, args ...any) (T, error) {
	var v T
	var b []byte
	e := q.QueryRow(ctx, sql, args...).Scan(&b)
	if errors.Is(e, pgx.ErrNoRows) {
		return v, ErrNotFound
	}
	if e == nil {
		e = json.Unmarshal(b, &v)
	}
	return v, e
}
func (s *Store) GetScan(ctx context.Context, id string) (Scan, error) {
	return readJSON[Scan](ctx, s.Pool, `SELECT data FROM masscanr.scans WHERE id=$1`, id)
}
func (s *Store) GetRun(ctx context.Context, id string) (Run, error) {
	return readJSON[Run](ctx, s.Pool, `SELECT data FROM masscanr.runs WHERE id=$1`, id)
}
func (s *Store) SaveScan(ctx context.Context, v Scan, revision int64, create bool) error {
	return s.tx(ctx, func(tx pgx.Tx) error {
		if !create {
			old, e := readJSON[Scan](ctx, tx, `SELECT data FROM masscanr.scans WHERE id=$1`, v.ID)
			if e != nil {
				return e
			}
			if old.Revision != revision {
				return ErrConflict
			}
			var active bool
			if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM masscanr.runs WHERE scan_id=$1 AND active)`, v.ID).Scan(&active); e != nil {
				return e
			}
			if active {
				return ErrConflict
			}
			v.CreatedAt = old.CreatedAt
		}
		b, e := json.Marshal(v)
		if e != nil {
			return e
		}
		_, e = tx.Exec(ctx, `INSERT INTO masscanr.scans VALUES($1,$2) ON CONFLICT(id) DO UPDATE SET data=excluded.data`, v.ID, b)
		return e
	})
}
func (s *Store) DeleteScan(ctx context.Context, id string) error {
	return s.tx(ctx, func(tx pgx.Tx) error {
		var active bool
		e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM masscanr.runs WHERE scan_id=$1 AND active)`, id).Scan(&active)
		if e != nil {
			return e
		}
		if active {
			return ErrConflict
		}
		r, e := tx.Exec(ctx, `DELETE FROM masscanr.scans WHERE id=$1`, id)
		if e == nil && r.RowsAffected() == 0 {
			return ErrNotFound
		}
		return e
	})
}
func (s *Store) List(ctx context.Context, table, scanID, after string, limit int) ([]json.RawMessage, error) {
	if table != "scans" && table != "runs" {
		return nil, fmt.Errorf("invalid table")
	}
	query := `SELECT data FROM masscanr.` + table + ` WHERE id>$1`
	args := []any{after, limit}
	if table == "runs" {
		query += ` AND scan_id=$3`
		args = append(args, scanID)
	}
	query += ` ORDER BY id LIMIT $2`
	rows, e := s.Pool.Query(ctx, query, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []json.RawMessage{}
	for rows.Next() {
		var b []byte
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		out = append(out, json.RawMessage(b))
	}
	return out, rows.Err()
}
