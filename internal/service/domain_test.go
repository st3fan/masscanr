package service

import (
	"context"
	"testing"
	"time"
)

func TestNormalizeTargetEntries(t *testing.T) {
	entries, err := NormalizeTargetEntries([]string{"192.0.2.1", "198.51.100.42/24", "as03333"})
	if err != nil {
		t.Fatal(err)
	}
	want := []struct{ kind, selector string }{
		{"ip", "192.0.2.1/32"},
		{"cidr", "198.51.100.0/24"},
		{"as", "AS3333"},
	}
	for i, entry := range entries {
		if entry.ID == "" || entry.Position != i || entry.Kind != want[i].kind || entry.Selector != want[i].selector {
			t.Fatalf("entry %d: %+v", i, entry)
		}
	}
	for _, inputs := range [][]string{
		nil,
		{"192.0.2.1", "192.0.2.1/32"},
		{"AS3333", "as03333"},
		{"2001:db8::1"},
	} {
		if _, err := NormalizeTargetEntries(inputs); err == nil {
			t.Fatalf("accepted invalid or duplicate entries %v", inputs)
		}
	}
}

func TestScanPlanSettingsRequireSupportedProviderAndValidOptions(t *testing.T) {
	settings := DefaultScanPlanSettings()
	if _, _, err := settings.Config([]string{"192.0.2.0/24"}); err != nil {
		t.Fatal(err)
	}
	settings.Provider = "other"
	if _, _, err := settings.Config([]string{"192.0.2.0/24"}); err == nil {
		t.Fatal("accepted unsupported provider")
	}
	settings.Provider = "vultr"
	settings.Shards = 0
	if _, _, err := settings.Config([]string{"192.0.2.0/24"}); err == nil {
		t.Fatal("accepted zero workers")
	}
}

func TestFreshDomainSchemaConstraints(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	var version int
	if err := store.Pool.QueryRow(ctx, `SELECT version FROM masscanr.version`).Scan(&version); err != nil || version != 3 {
		t.Fatalf("schema version %d: %v", version, err)
	}
	now := time.Now().UTC()
	targetID, planID := newID(), newID()
	if _, err := store.Pool.Exec(ctx, `INSERT INTO masscanr.targets(id,name,revision,created_at,updated_at) VALUES($1,$2,1,$3,$3)`, targetID, "External Network", now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO masscanr.target_entries(id,target_id,position,kind,selector,input) VALUES($1,$2,0,'ip','192.0.2.1/32','192.0.2.1')`, newID(), targetID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO masscanr.target_entries(id,target_id,position,kind,selector,input) VALUES($1,$2,1,'cidr','192.0.2.1/32','192.0.2.1/32')`, newID(), targetID); err == nil {
		t.Fatal("accepted duplicate normalized selector")
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO masscanr.scan_plans(id,target_id,name,revision,provider,settings,created_at,updated_at) VALUES($1,$2,'Daily',1,'vultr','{}',$3,$3)`, planID, targetID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO masscanr.scan_plans(id,target_id,name,revision,provider,settings,created_at,updated_at) VALUES($1,$2,'Bad',1,'vultr','{}',$3,$3)`, newID(), newID(), now); err == nil {
		t.Fatal("accepted plan with missing Target")
	}
	if _, err := store.Pool.Exec(ctx, `INSERT INTO masscanr.scan_plans(id,target_id,name,revision,provider,settings,created_at,updated_at) VALUES($1,$2,'Bad',1,'other','{}',$3,$3)`, newID(), targetID, now); err == nil {
		t.Fatal("accepted unsupported provider")
	}
}
