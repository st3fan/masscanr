package service

import (
	"context"
	"github.com/jackc/pgx/v5"
	"os"
	"strings"
	"testing"
)

func testStore(t testing.TB) *Store {
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
