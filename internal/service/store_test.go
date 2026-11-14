package service

import (
	"context"
	"github.com/jackc/pgx/v5"
	"os"
	"strings"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	url := os.Getenv("MASSCANR_TEST_POSTGRES")
	if url == "" {
		t.Skip("set MASSCANR_TEST_POSTGRES to an admin database DSN for disposable PostgreSQL tests")
	}
	ctx := context.Background()
	admin, e := pgx.Connect(ctx, url)
	if e != nil {
		t.Fatal(e)
	}
	name := "masscanr_test_" + strings.ReplaceAll(newID(), "-", "")
	_, e = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize())
	if e != nil {
		admin.Close(ctx)
		t.Fatal(e)
	}
	t.Cleanup(func() {
		_, e := admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
		if e != nil {
			t.Error(e)
		}
		admin.Close(ctx)
	})
	cfg, e := pgx.ParseConfig(url)
	if e != nil {
		t.Fatal(e)
	}
	cfg.Database = name
	// ConnString retains the original DSN, so append an explicit database override.
	s, e := Open(ctx, cfg.ConnString()+" dbname="+name)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.Pool.Close)
	return s
}
func TestStorePersistenceAndConflicts(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	d := Defaults()
	d.Targets = []string{"192.0.2.1"}
	v := Scan{ID: newID(), Revision: 1, CreatedAt: time.Now().UTC(), Definition: d}
	if e := s.SaveScan(ctx, v, 0, true); e != nil {
		t.Fatal(e)
	}
	got, e := s.GetScan(ctx, v.ID)
	if e != nil || got.Definition.Targets[0] != d.Targets[0] {
		t.Fatal(got, e)
	}
	v.Revision = 2
	if e = s.SaveScan(ctx, v, 0, false); e != ErrConflict {
		t.Fatal(e)
	}
	if e = s.SaveScan(ctx, v, 1, false); e != nil {
		t.Fatal(e)
	}
	if e = s.DeleteScan(ctx, v.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.GetScan(ctx, v.ID); e != ErrNotFound {
		t.Fatal(e)
	}
}
func TestDefinitionValidation(t *testing.T) {
	d := Defaults()
	d.Targets = []string{"192.0.2.0/24"}
	if _, _, e := d.Config(); e != nil {
		t.Fatal(e)
	}
	d.Shards = 0
	if _, _, e := d.Config(); e == nil {
		t.Fatal("accepted zero shards")
	}
}
