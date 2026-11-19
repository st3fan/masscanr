package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func exclusionNameConflict(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "exclusions_name_unique" {
		return fmt.Errorf("%w: exclusion name already exists", ErrConflict)
	}
	return err
}

func writeExclusionEntries(ctx context.Context, tx pgx.Tx, exclusion Exclusion) error {
	for _, entry := range exclusion.Entries {
		if _, err := tx.Exec(ctx, `INSERT INTO masscanr.exclusion_entries(id,exclusion_id,position,kind,selector,input) VALUES($1,$2,$3,$4,$5,$6)`, entry.ID, exclusion.ID, entry.Position, entry.Kind, entry.Selector, entry.Input); err != nil {
			return err
		}
	}
	return nil
}

func readExclusion(ctx context.Context, tx pgx.Tx, id string) (Exclusion, error) {
	var exclusion Exclusion
	err := tx.QueryRow(ctx, `SELECT id,name,revision,created_at,updated_at,archived_at FROM masscanr.exclusions WHERE id=$1`, id).Scan(&exclusion.ID, &exclusion.Name, &exclusion.Revision, &exclusion.CreatedAt, &exclusion.UpdatedAt, &exclusion.ArchivedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Exclusion{}, ErrNotFound
	}
	if err != nil {
		return Exclusion{}, err
	}
	rows, err := tx.Query(ctx, `SELECT id,position,kind,selector,input FROM masscanr.exclusion_entries WHERE exclusion_id=$1 ORDER BY position`, id)
	if err != nil {
		return Exclusion{}, err
	}
	defer rows.Close()
	exclusion.Entries = []ExclusionEntry{}
	for rows.Next() {
		var entry ExclusionEntry
		if err = rows.Scan(&entry.ID, &entry.Position, &entry.Kind, &entry.Selector, &entry.Input); err != nil {
			return Exclusion{}, err
		}
		exclusion.Entries = append(exclusion.Entries, entry)
	}
	return exclusion, rows.Err()
}

func (s *Store) CreateExclusion(ctx context.Context, name string, inputs []string) (Exclusion, error) {
	if !validName(name) {
		return Exclusion{}, fmt.Errorf("exclusion name must contain 1..200 bytes")
	}
	entries, err := NormalizeExclusionEntries(inputs)
	if err != nil {
		return Exclusion{}, err
	}
	now := time.Now().UTC()
	exclusion := Exclusion{ID: newID(), Name: strings.TrimSpace(name), Revision: 1, CreatedAt: now, UpdatedAt: now, Entries: entries}
	err = s.tx(ctx, func(tx pgx.Tx) error {
		if _, e := tx.Exec(ctx, `INSERT INTO masscanr.exclusions(id,name,revision,created_at,updated_at) VALUES($1,$2,$3,$4,$5)`, exclusion.ID, exclusion.Name, exclusion.Revision, now, now); e != nil {
			return exclusionNameConflict(e)
		}
		return writeExclusionEntries(ctx, tx, exclusion)
	})
	return exclusion, err
}

func (s *Store) GetExclusion(ctx context.Context, id string) (Exclusion, error) {
	var exclusion Exclusion
	err := s.tx(ctx, func(tx pgx.Tx) error {
		var e error
		exclusion, e = readExclusion(ctx, tx, id)
		return e
	})
	return exclusion, err
}

type ExclusionPatch struct {
	Entries  *[]string `json:"entries"`
	Archived *bool     `json:"archived"`
}

func (s *Store) UpdateExclusion(ctx context.Context, id string, revision int64, patch ExclusionPatch) (Exclusion, error) {
	if revision < 1 || patch.Entries == nil && patch.Archived == nil {
		return Exclusion{}, fmt.Errorf("revision and changed attributes are required")
	}
	var updated Exclusion
	err := s.tx(ctx, func(tx pgx.Tx) error {
		old, e := readExclusion(ctx, tx, id)
		if e != nil {
			return e
		}
		if old.Revision != revision || old.ArchivedAt != nil {
			return ErrConflict
		}
		updated = old
		if patch.Entries != nil {
			entries, e := NormalizeExclusionEntries(*patch.Entries)
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
			if !*patch.Archived || patch.Entries != nil {
				return fmt.Errorf("archived may only be set to true on its own")
			}
			now := time.Now().UTC()
			updated.ArchivedAt = &now
		}
		updated.Revision++
		updated.UpdatedAt = time.Now().UTC()
		if _, e = tx.Exec(ctx, `UPDATE masscanr.exclusions SET revision=$2,updated_at=$3,archived_at=$4 WHERE id=$1`, id, updated.Revision, updated.UpdatedAt, updated.ArchivedAt); e != nil {
			return e
		}
		if patch.Entries != nil {
			if _, e = tx.Exec(ctx, `DELETE FROM masscanr.exclusion_entries WHERE exclusion_id=$1`, id); e != nil {
				return e
			}
			return writeExclusionEntries(ctx, tx, updated)
		}
		return nil
	})
	return updated, err
}

func (s *Store) ListExclusions(ctx context.Context, after string, limit int) ([]Exclusion, error) {
	ids, err := s.listResourceIDs(ctx, "exclusions", after, limit)
	if err != nil {
		return nil, err
	}
	out := make([]Exclusion, 0, len(ids))
	for _, id := range ids {
		exclusion, err := s.GetExclusion(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, exclusion)
	}
	return out, nil
}
