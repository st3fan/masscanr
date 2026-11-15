package service

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/st3fan/masscanr/internal/scan"
)

func TestScanPlanAdmissionIsAtomic(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	target, err := store.CreateTarget(ctx, "External Network", []string{"192.0.2.1"})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := store.CreateScanPlan(ctx, "Daily", target.ID, DefaultScanPlanSettings())
	if err != nil {
		t.Fatal(err)
	}
	app := (&API{Store: store, Resolve: func(_ context.Context, targets []scan.Target) ([]scan.Target, error) { return targets, nil }}).Handler()
	response := request(app, "POST", "/v1/scan-plans/"+plan.ID+"/preflight", "")
	var preflight Plan
	if response.Code != 201 || decodeTestResource(response.Body.Bytes(), &preflight) != nil {
		t.Fatal(response.Code, response.Body)
	}
	var wg sync.WaitGroup
	accepted := make(chan Run, 8)
	failures := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			run, err := store.ReserveScanPlan(ctx, plan.ID, preflight.ID, newID(), true)
			if err == nil {
				accepted <- run
			} else {
				failures <- err
			}
		}()
	}
	wg.Wait()
	close(accepted)
	close(failures)
	if len(accepted) != 1 {
		t.Fatalf("accepted %d runs", len(accepted))
	}
	for err := range failures {
		if err != ErrConflict {
			t.Fatal(err)
		}
	}
	run := <-accepted
	if run.TargetID != target.ID || run.ScanPlanID != plan.ID {
		t.Fatal(run)
	}
}

func TestExpiredScanPlanPreflightCannotStart(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	target, err := store.CreateTarget(ctx, "External Network", []string{"192.0.2.1"})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := store.CreateScanPlan(ctx, "Daily", target.ID, DefaultScanPlanSettings())
	if err != nil {
		t.Fatal(err)
	}
	app := (&API{Store: store, Resolve: func(_ context.Context, targets []scan.Target) ([]scan.Target, error) { return targets, nil }}).Handler()
	response := request(app, "POST", "/v1/scan-plans/"+plan.ID+"/preflight", "")
	var preflight Plan
	if response.Code != 201 || decodeTestResource(response.Body.Bytes(), &preflight) != nil {
		t.Fatal(response.Code, response.Body)
	}
	preflight.ExpiresAt = time.Now().Add(-time.Second)
	data, err := json.Marshal(preflight)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Pool.Exec(ctx, `UPDATE masscanr.scan_preflights SET expires_at=$2,data=$3 WHERE id=$1`, preflight.ID, preflight.ExpiresAt, data); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ReserveScanPlan(ctx, plan.ID, preflight.ID, newID(), true); err != ErrConflict {
		t.Fatalf("accepted expired preflight: %v", err)
	}
	var count int
	if err = store.Pool.QueryRow(ctx, `SELECT count(*) FROM masscanr.runs`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}
