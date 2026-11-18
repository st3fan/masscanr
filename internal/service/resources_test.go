package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/st3fan/masscanr/internal/scan"
)

func TestTargetAndScanPlanResources(t *testing.T) {
	store := testStore(t)
	app := (&API{Store: store}).Handler()
	create := request(app, "POST", "/v1/targets", `{"data":{"type":"targets","attributes":{"name":"External Network","entries":["192.0.2.1","AS3333"]}}}`)
	if create.Code != 201 || create.Header().Get("Content-Type") != jsonAPIMediaType {
		t.Fatalf("create target: %d %s", create.Code, create.Body)
	}
	var target Target
	if err := decodeTestResource(create.Body.Bytes(), &target); err != nil || target.ID == "" || target.Revision != 1 || len(target.Entries) != 2 {
		t.Fatalf("target = %+v, %v", target, err)
	}
	duplicate := request(app, "POST", "/v1/targets", `{"data":{"type":"targets","attributes":{"name":"Duplicate","entries":["192.0.2.1","192.0.2.1/32"]}}}`)
	if duplicate.Code != 400 || !strings.Contains(duplicate.Body.String(), `"errors"`) {
		t.Fatalf("duplicate target: %d %s", duplicate.Code, duplicate.Body)
	}
	firstPlan := request(app, "POST", "/v1/scan-plans", `{"data":{"type":"scan-plans","attributes":{"name":"Daily TCP","target_id":"`+target.ID+`"}}}`)
	if firstPlan.Code != 201 {
		t.Fatalf("create plan: %d %s", firstPlan.Code, firstPlan.Body)
	}
	var plan ScanPlan
	if err := decodeTestResource(firstPlan.Body.Bytes(), &plan); err != nil || plan.TargetID != target.ID || plan.Settings.Provider != "vultr" {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
	secondPlan := request(app, "POST", "/v1/scan-plans", `{"data":{"type":"scan-plans","attributes":{"name":"Weekly TCP","target_id":"`+target.ID+`"}}}`)
	if secondPlan.Code != 201 {
		t.Fatalf("second plan: %d %s", secondPlan.Code, secondPlan.Body)
	}
	listPlans := request(app, "GET", "/v1/scan-plans", "")
	var plans []ScanPlan
	if err := decodeTestList(listPlans.Body.Bytes(), &plans); err != nil || len(plans) != 2 {
		t.Fatalf("plans = %+v, %v", plans, err)
	}
	stale := request(app, "PATCH", "/v1/scan-plans/"+plan.ID, `{"data":{"type":"scan-plans","id":"`+plan.ID+`","attributes":{"revision":9,"settings":{"ports":"443"}}}}`)
	if stale.Code != 409 {
		t.Fatalf("stale plan: %d %s", stale.Code, stale.Body)
	}
	update := request(app, "PATCH", "/v1/scan-plans/"+plan.ID, `{"data":{"type":"scan-plans","id":"`+plan.ID+`","attributes":{"revision":1,"settings":{"ports":"443"}}}}`)
	var updated ScanPlan
	if update.Code != 200 || decodeTestResource(update.Body.Bytes(), &updated) != nil || updated.Revision != 2 || updated.Settings.Ports != "443" {
		t.Fatalf("update plan: %d %s", update.Code, update.Body)
	}
	updateTarget := request(app, "PATCH", "/v1/targets/"+target.ID, `{"data":{"type":"targets","id":"`+target.ID+`","attributes":{"revision":1,"entries":["192.0.2.1","198.51.100.0/24"]}}}`)
	var changed Target
	if updateTarget.Code != 200 || decodeTestResource(updateTarget.Body.Bytes(), &changed) != nil || changed.Revision != 2 || changed.Entries[0].ID != target.Entries[0].ID {
		t.Fatalf("update target: %d %s", updateTarget.Code, updateTarget.Body)
	}
	archive := request(app, "PATCH", "/v1/targets/"+target.ID, `{"data":{"type":"targets","id":"`+target.ID+`","attributes":{"revision":2,"archived":true}}}`)
	var archived Target
	if archive.Code != 200 || decodeTestResource(archive.Body.Bytes(), &archived) != nil || archived.ArchivedAt == nil {
		t.Fatalf("archive target: %d %s", archive.Code, archive.Body)
	}
	blocked := request(app, "POST", "/v1/scan-plans", `{"data":{"type":"scan-plans","attributes":{"name":"Too late","target_id":"`+target.ID+`"}}}`)
	if blocked.Code != 409 {
		t.Fatalf("plan on archived target: %d %s", blocked.Code, blocked.Body)
	}
	get := request(app, "GET", "/v1/targets/"+target.ID, "")
	var got Target
	if get.Code != 200 || decodeTestResource(get.Body.Bytes(), &got) != nil || got.ArchivedAt == nil {
		t.Fatalf("get archived target: %d %s", get.Code, get.Body)
	}
	listed := request(app, "GET", "/v1/targets", "")
	var targets []Target
	if err := decodeTestList(listed.Body.Bytes(), &targets); err != nil || len(targets) != 1 {
		t.Fatalf("targets = %+v, %v", targets, err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(firstPlan.Body.Bytes(), &doc); err != nil || !strings.Contains(string(doc["data"]), `"target"`) {
		t.Fatalf("missing Target relationship: %s", firstPlan.Body)
	}
}

func TestArchiveScanPlanPreservesAcceptedRunAndBlocksNewWork(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	target, err := store.CreateTarget(ctx, "Archive scope", []string{"192.0.2.0/24"})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := store.CreateScanPlan(ctx, "Archive plan", target.ID, DefaultScanPlanSettings())
	if err != nil {
		t.Fatal(err)
	}
	app := (&API{Store: store, Resolve: func(_ context.Context, targets []scan.Target) ([]scan.Target, error) { return targets, nil }}).Handler()
	path := "/v1/scan-plans/" + plan.ID
	preflightResponse := request(app, "POST", path+"/preflight", "")
	var preflight Plan
	if preflightResponse.Code != 201 || decodeTestResource(preflightResponse.Body.Bytes(), &preflight) != nil {
		t.Fatalf("preflight: %d %s", preflightResponse.Code, preflightResponse.Body)
	}
	run, err := store.ReserveScanPlan(ctx, plan.ID, preflight.ID, "accepted-key", true)
	if err != nil {
		t.Fatal(err)
	}
	archiveTarget := true
	if _, err := store.UpdateTarget(ctx, target.ID, target.Revision, TargetPatch{Archived: &archiveTarget}); err != nil {
		t.Fatal(err)
	}
	badArchive := request(app, "PATCH", path, `{"data":{"type":"scan-plans","id":"`+plan.ID+`","attributes":{"revision":1,"archived":true,"name":"other"}}}`)
	if badArchive.Code != 400 {
		t.Fatalf("mixed archive: %d %s", badArchive.Code, badArchive.Body)
	}
	archive := request(app, "PATCH", path, `{"data":{"type":"scan-plans","id":"`+plan.ID+`","attributes":{"revision":1,"archived":true}}}`)
	var archived ScanPlan
	if archive.Code != 200 || decodeTestResource(archive.Body.Bytes(), &archived) != nil || archived.ArchivedAt == nil || archived.Revision != 2 {
		t.Fatalf("archive: %d %s", archive.Code, archive.Body)
	}
	for _, body := range []string{
		`{"data":{"type":"scan-plans","id":"` + plan.ID + `","attributes":{"revision":1,"archived":true}}}`,
		`{"data":{"type":"scan-plans","id":"` + plan.ID + `","attributes":{"revision":2,"name":"changed"}}}`,
	} {
		response := request(app, "PATCH", path, body)
		if response.Code != 409 {
			t.Fatalf("edit archived plan: %d %s", response.Code, response.Body)
		}
	}
	if response := request(app, "POST", path+"/preflight", ""); response.Code != 409 {
		t.Fatalf("preflight archived plan: %d %s", response.Code, response.Body)
	}
	if _, err := store.ReserveScanPlan(ctx, plan.ID, preflight.ID, "new-key", true); err != ErrConflict {
		t.Fatalf("accepted stale preflight: %v", err)
	}
	retry, err := store.ReserveScanPlan(ctx, plan.ID, preflight.ID, "accepted-key", true)
	if err != nil || retry.ID != run.ID {
		t.Fatalf("idempotent retry: %+v, %v", retry, err)
	}
	got := request(app, "GET", path, "")
	var saved ScanPlan
	if got.Code != 200 || decodeTestResource(got.Body.Bytes(), &saved) != nil || saved.ArchivedAt == nil {
		t.Fatalf("get archived plan: %d %s", got.Code, got.Body)
	}
	history, err := store.ListRunsForPlan(ctx, plan.ID, "", 10)
	if err != nil || len(history) != 1 || !strings.Contains(string(history[0]), run.ID) {
		t.Fatalf("archived run history: %s, %v", history, err)
	}
}
