package service

import (
	"context"
	"testing"

	"github.com/st3fan/masscanr/internal/scan"
)

func TestFrozenScanPreflightAndConfirmation(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	target, err := store.CreateTarget(ctx, "External Network", []string{"192.0.2.0/24"})
	if err != nil {
		t.Fatal(err)
	}
	settings := DefaultScanPlanSettings()
	settings.Ports = "0-65535"
	plan, err := store.CreateScanPlan(ctx, "Full TCP", target.ID, settings)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	app := (&API{Store: store, Resolve: func(_ context.Context, targets []scan.Target) ([]scan.Target, error) { calls++; return targets, nil }}).Handler()
	response := request(app, "POST", "/v1/scan-plans/"+plan.ID+"/preflight", "")
	var preflight Plan
	if response.Code != 201 || decodeTestResource(response.Body.Bytes(), &preflight) != nil || !preflight.RequiresConfirmation {
		t.Fatal(response.Code, response.Body)
	}
	if _, err = store.ReserveScanPlan(ctx, plan.ID, preflight.ID, "key", false); err != ErrConflict {
		t.Fatal(err)
	}
	run, err := store.ReserveScanPlan(ctx, plan.ID, preflight.ID, "key", true)
	if err != nil {
		t.Fatal(err)
	}
	same, err := store.ReserveScanPlan(ctx, plan.ID, preflight.ID, "key", true)
	if err != nil || same.ID != run.ID || calls != 1 {
		t.Fatal(same, err, calls)
	}
	archive := true
	if _, err = store.UpdateTarget(ctx, target.ID, target.Revision, TargetPatch{Archived: &archive}); err != nil {
		t.Fatal(err)
	}
	saved, err := store.GetRun(ctx, run.ID)
	if err != nil || saved.Plan.Config.Targets[0].String() != "192.0.2.0/24" {
		t.Fatal(saved, err)
	}
}
