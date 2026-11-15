package service

import (
	"context"
	"testing"

	"github.com/st3fan/masscanr/internal/scan"
)

func TestScanPlanPreflightRevisionsAndFrozenRun(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	target, err := store.CreateTarget(ctx, "External Network", []string{"192.0.2.1"})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := store.CreateScanPlan(ctx, "Daily TCP", target.ID, DefaultScanPlanSettings())
	if err != nil {
		t.Fatal(err)
	}
	app := (&API{Store: store, Resolve: func(_ context.Context, targets []scan.Target) ([]scan.Target, error) { return targets, nil }}).Handler()
	makePreflight := func() Plan {
		t.Helper()
		response := request(app, "POST", "/v1/scan-plans/"+plan.ID+"/preflight", "")
		if response.Code != 201 {
			t.Fatalf("preflight: %d %s", response.Code, response.Body)
		}
		var preflight Plan
		if err := decodeTestResource(response.Body.Bytes(), &preflight); err != nil {
			t.Fatal(err)
		}
		if preflight.TargetID != target.ID || preflight.ScanPlanID != plan.ID || preflight.TargetRevision != target.Revision || preflight.ScanPlanRevision != plan.Revision {
			t.Fatalf("preflight identity: %+v", preflight)
		}
		return preflight
	}
	staleTarget := makePreflight()
	entries := []string{"192.0.2.2"}
	target, err = store.UpdateTarget(ctx, target.ID, 1, TargetPatch{Entries: &entries})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReserveScanPlan(ctx, plan.ID, staleTarget.ID, "stale-target", true); err != ErrConflict {
		t.Fatalf("accepted stale Target preflight: %v", err)
	}
	stalePlan := makePreflight()
	plan.Settings.Ports = "443"
	plan, err = store.UpdateScanPlan(ctx, plan, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReserveScanPlan(ctx, plan.ID, stalePlan.ID, "stale-plan", true); err != ErrConflict {
		t.Fatalf("accepted stale ScanPlan preflight: %v", err)
	}
	approved := makePreflight()
	run, err := store.ReserveScanPlan(ctx, plan.ID, approved.ID, "approved", true)
	if err != nil || run.TargetID != target.ID || run.ScanPlanID != plan.ID || run.Plan.Config.RequestedTargets[0].Target != "192.0.2.2/32" {
		t.Fatalf("reserved run %+v: %v", run, err)
	}
	retry, err := store.ReserveScanPlan(ctx, plan.ID, approved.ID, "approved", true)
	if err != nil || retry.ID != run.ID {
		t.Fatalf("idempotent retry %+v: %v", retry, err)
	}
	if _, err = store.ReserveScanPlan(ctx, plan.ID, approved.ID, "second-run", true); err != ErrConflict {
		t.Fatalf("accepted simultaneous run: %v", err)
	}
	entries = []string{"198.51.100.1"}
	target, err = store.UpdateTarget(ctx, target.ID, target.Revision, TargetPatch{Entries: &entries})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.GetRun(ctx, run.ID)
	if err != nil || saved.Plan.Config.RequestedTargets[0].Target != "192.0.2.2/32" || saved.TargetID != target.ID {
		t.Fatalf("run snapshot changed after Target edit: %+v %v", saved, err)
	}
	archive := true
	if _, err = store.UpdateTarget(ctx, target.ID, target.Revision, TargetPatch{Archived: &archive}); err != nil {
		t.Fatal(err)
	}
	blocked := request(app, "POST", "/v1/scan-plans/"+plan.ID+"/preflight", "")
	if blocked.Code != 409 {
		t.Fatalf("preflight archived Target: %d %s", blocked.Code, blocked.Body)
	}
}
