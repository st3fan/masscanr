package service

import (
	"context"
	"strings"
	"testing"
)

func TestExclusionResourceLifecycle(t *testing.T) {
	store := testStore(t)
	app := (&API{Store: store}).Handler()
	created := request(app, "POST", "/v1/exclusions", `{"data":{"type":"exclusions","attributes":{"name":"Reserved hosts","entries":["192.0.2.1","198.51.100.42/24"]}}}`)
	var exclusion Exclusion
	if created.Code != 201 || decodeTestResource(created.Body.Bytes(), &exclusion) != nil || exclusion.Revision != 1 || exclusion.Entries[1].Selector != "198.51.100.0/24" {
		t.Fatalf("create: %d %s", created.Code, created.Body)
	}
	for _, body := range []string{
		`{"data":{"type":"exclusions","attributes":{"name":"Bad AS","entries":["AS3333"]}}}`,
		`{"data":{"type":"exclusions","attributes":{"name":"Duplicate","entries":["192.0.2.1","192.0.2.1/32"]}}}`,
	} {
		if r := request(app, "POST", "/v1/exclusions", body); r.Code != 400 {
			t.Fatalf("invalid create: %d %s", r.Code, r.Body)
		}
	}
	duplicateName := request(app, "POST", "/v1/exclusions", `{"data":{"type":"exclusions","attributes":{"name":" reserved HOSTS ","entries":["203.0.113.1"]}}}`)
	if duplicateName.Code != 409 {
		t.Fatalf("duplicate name: %d %s", duplicateName.Code, duplicateName.Body)
	}
	path := "/v1/exclusions/" + exclusion.ID
	list := request(app, "GET", "/v1/exclusions", "")
	var listed []Exclusion
	if list.Code != 200 || decodeTestList(list.Body.Bytes(), &listed) != nil || len(listed) != 1 {
		t.Fatalf("list: %d %s", list.Code, list.Body)
	}
	stale := request(app, "PATCH", path, `{"data":{"type":"exclusions","id":"`+exclusion.ID+`","attributes":{"revision":2,"entries":["192.0.2.2"]}}}`)
	if stale.Code != 409 {
		t.Fatalf("stale update: %d %s", stale.Code, stale.Body)
	}
	updated := request(app, "PATCH", path, `{"data":{"type":"exclusions","id":"`+exclusion.ID+`","attributes":{"revision":1,"entries":["192.0.2.1","203.0.113.0/24"]}}}`)
	var changed Exclusion
	if updated.Code != 200 || decodeTestResource(updated.Body.Bytes(), &changed) != nil || changed.Revision != 2 || changed.Entries[0].ID != exclusion.Entries[0].ID {
		t.Fatalf("update: %d %s", updated.Code, updated.Body)
	}
	archived := request(app, "PATCH", path, `{"data":{"type":"exclusions","id":"`+exclusion.ID+`","attributes":{"revision":2,"archived":true}}}`)
	var retired Exclusion
	if archived.Code != 200 || decodeTestResource(archived.Body.Bytes(), &retired) != nil || retired.ArchivedAt == nil || retired.Revision != 3 {
		t.Fatalf("archive: %d %s", archived.Code, archived.Body)
	}
	if r := request(app, "PATCH", path, `{"data":{"type":"exclusions","id":"`+exclusion.ID+`","attributes":{"revision":3,"entries":["192.0.2.2"]}}}`); r.Code != 409 {
		t.Fatalf("edit archived: %d %s", r.Code, r.Body)
	}
	get := request(app, "GET", path, "")
	var saved Exclusion
	if get.Code != 200 || decodeTestResource(get.Body.Bytes(), &saved) != nil || saved.ArchivedAt == nil || !strings.Contains(get.Body.String(), `"self":"`+path+`"`) {
		t.Fatalf("get archived: %d %s", get.Code, get.Body)
	}
}

func TestVersion7ExclusionUpgradePreservesPlans(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	target, err := store.CreateTarget(ctx, "Existing network", []string{"192.0.2.0/24"})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := store.CreateScanPlan(ctx, "Existing scan", target.ID, DefaultScanPlanSettings())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `DROP TABLE masscanr.exclusion_entries; DROP TABLE masscanr.exclusions; UPDATE masscanr.version SET version=7`); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, store.Pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reopened.Pool.Close)
	var version int
	if err := reopened.Pool.QueryRow(ctx, `SELECT version FROM masscanr.version`).Scan(&version); err != nil || version != 8 {
		t.Fatalf("version = %d, %v", version, err)
	}
	if got, err := reopened.GetScanPlan(ctx, plan.ID); err != nil || got.TargetID != target.ID {
		t.Fatalf("preserved plan = %+v, %v", got, err)
	}
	if _, err := reopened.CreateExclusion(ctx, "New policy", []string{"192.0.2.1"}); err != nil {
		t.Fatal(err)
	}
}
