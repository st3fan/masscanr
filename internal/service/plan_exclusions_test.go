package service

import (
	"context"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/st3fan/masscanr/internal/scan"
)

func exclusionPlanAPI(t *testing.T) (*Store, *API, Target) {
	t.Helper()
	store := testStore(t)
	target, err := store.CreateTarget(t.Context(), "Authorized scope", []string{"192.0.2.0/29"})
	if err != nil {
		t.Fatal(err)
	}
	api := &API{Store: store, Resolve: func(_ context.Context, targets []scan.Target) ([]scan.Target, error) { return targets, nil }}
	return store, api, target
}

func TestPlanExclusionsFreezeEffectiveScopeAndInvalidatePreflight(t *testing.T) {
	store, api, target := exclusionPlanAPI(t)
	ctx := t.Context()
	first, err := store.CreateExclusion(ctx, "Individual", []string{"192.0.2.1"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateExclusion(ctx, "First half", []string{"192.0.2.0/30"})
	if err != nil {
		t.Fatal(err)
	}
	app := api.Handler()
	create := request(app, "POST", "/v1/scan-plans", `{"data":{"type":"scan-plans","attributes":{"name":"Narrow scan","target_id":"`+target.ID+`"},"relationships":{"exclusions":{"data":[{"type":"exclusions","id":"`+second.ID+`"},{"type":"exclusions","id":"`+first.ID+`"}]}}}}`)
	var plan ScanPlan
	if create.Code != 201 || decodeTestResource(create.Body.Bytes(), &plan) != nil || !strings.Contains(create.Body.String(), `"exclusions"`) {
		t.Fatalf("create plan: %d %s", create.Code, create.Body)
	}
	plan, err = store.GetScanPlan(ctx, plan.ID)
	if err != nil || len(plan.ExclusionIDs) != 2 || !slices.IsSorted(plan.ExclusionIDs) {
		t.Fatalf("plan references = %v, %v", plan.ExclusionIDs, err)
	}
	bad := request(app, "POST", "/v1/scan-plans", `{"data":{"type":"scan-plans","attributes":{"name":"Duplicate refs","target_id":"`+target.ID+`"},"relationships":{"exclusions":{"data":[{"type":"exclusions","id":"`+first.ID+`"},{"type":"exclusions","id":"`+first.ID+`"}]}}}}`)
	if bad.Code != 400 {
		t.Fatalf("duplicate references: %d %s", bad.Code, bad.Body)
	}
	for _, body := range []string{
		`{"data":{"type":"scan-plans","attributes":{"name":"Null refs","target_id":"` + target.ID + `"},"relationships":{"exclusions":{"data":null}}}}`,
		`{"data":{"type":"scan-plans","attributes":{"name":"Wrong type","target_id":"` + target.ID + `"},"relationships":{"exclusions":{"data":[{"type":"targets","id":"` + first.ID + `"}]}}}}`,
		`{"data":{"type":"scan-plans","attributes":{"name":"Unknown relation","target_id":"` + target.ID + `"},"relationships":{"other":{"data":[]}}}}`,
		`{"data":{"type":"scan-plans","attributes":{"name":"Wrong attributes","target_id":"` + target.ID + `","exclusion_ids":["` + first.ID + `"]}}}`,
	} {
		if r := request(app, "POST", "/v1/scan-plans", body); r.Code != 400 {
			t.Fatalf("invalid relationship: %d %s", r.Code, r.Body)
		}
	}
	path := "/v1/scan-plans/" + plan.ID
	preflightResponse := request(app, "POST", path+"/preflight", "")
	var preflight Plan
	if preflightResponse.Code != 201 || decodeTestResource(preflightResponse.Body.Bytes(), &preflight) != nil {
		t.Fatalf("preflight: %d %s", preflightResponse.Code, preflightResponse.Body)
	}
	if !slices.Equal(preflight.RequestedScope, []netip.Prefix{netip.MustParsePrefix("192.0.2.0/29")}) || !slices.Equal(preflight.ExcludedScope, []netip.Prefix{netip.MustParsePrefix("192.0.2.0/30")}) || !slices.Equal(preflight.Config.Targets, []netip.Prefix{netip.MustParsePrefix("192.0.2.4/30")}) || len(preflight.Config.Exclusions) != 2 || preflight.Workload.Addresses != 4 {
		t.Fatalf("incorrect frozen scope: %+v", preflight)
	}
	if err := validatePreflightScope(preflight); err != nil {
		t.Fatal(err)
	}
	run, err := store.ReserveScanPlan(ctx, plan.ID, preflight.ID, "accepted-narrow-scope", true)
	if err != nil || !slices.Equal(run.Plan.Config.Targets, preflight.Config.Targets) {
		t.Fatalf("reserve: %+v, %v", run, err)
	}
	entries := []string{"192.0.2.2"}
	_, err = store.UpdateExclusion(ctx, first.ID, first.Revision, ExclusionPatch{Entries: &entries})
	if err != nil {
		t.Fatal(err)
	}
	stale := request(app, "POST", path+"/preflight", "")
	var later Plan
	if stale.Code != 201 || decodeTestResource(stale.Body.Bytes(), &later) != nil {
		t.Fatalf("new preflight after edit: %d %s", stale.Code, stale.Body)
	}
	if later.Config.Exclusions[0].Revision == preflight.Config.Exclusions[0].Revision && later.Config.Exclusions[1].Revision == preflight.Config.Exclusions[1].Revision {
		t.Fatal("new preflight retained old exclusion revision")
	}
	if _, err := store.ReserveScanPlan(ctx, plan.ID, preflight.ID, "new-request", true); err != ErrConflict {
		t.Fatalf("accepted stale preflight: %v", err)
	}
	got, err := store.GetRun(ctx, run.ID)
	if err != nil || !slices.Equal(got.Plan.Config.Targets, preflight.Config.Targets) || got.Plan.Config.Exclusions[0].Entries[0].Input != preflight.Config.Exclusions[0].Entries[0].Input {
		t.Fatalf("historical snapshot changed: %+v, %v", got, err)
	}
	archive := true
	if _, err := store.UpdateExclusion(ctx, second.ID, second.Revision, ExclusionPatch{Archived: &archive}); err != nil {
		t.Fatal(err)
	}
	if r := request(app, "POST", path+"/preflight", ""); r.Code != 409 {
		t.Fatalf("preflight with archived reference: %d %s", r.Code, r.Body)
	}
	if r := request(app, "POST", "/v1/scan-plans", `{"data":{"type":"scan-plans","attributes":{"name":"Archived reference","target_id":"`+target.ID+`"},"relationships":{"exclusions":{"data":[{"type":"exclusions","id":"`+second.ID+`"}]}}}}`); r.Code != 409 {
		t.Fatalf("new plan with archived reference: %d %s", r.Code, r.Body)
	}
	if retry, err := store.ReserveScanPlan(ctx, plan.ID, preflight.ID, "accepted-narrow-scope", true); err != nil || retry.ID != run.ID {
		t.Fatalf("idempotent run changed: %+v, %v", retry, err)
	}
	clear := request(app, "PATCH", path, `{"data":{"type":"scan-plans","id":"`+plan.ID+`","attributes":{"revision":1},"relationships":{"exclusions":{"data":[]}}}}`)
	if clear.Code != 200 {
		t.Fatalf("clear references: %d %s", clear.Code, clear.Body)
	}
	if r := request(app, "POST", path+"/preflight", ""); r.Code != 201 {
		t.Fatalf("preflight after clearing: %d %s", r.Code, r.Body)
	}
	if _, err := store.ReserveScanPlan(ctx, plan.ID, later.ID, "changed-plan", true); err != ErrConflict {
		t.Fatalf("accepted preflight after relationship changed: %v", err)
	}
}

func TestFullyExcludedTargetAndASResolution(t *testing.T) {
	store, api, target := exclusionPlanAPI(t)
	ctx := t.Context()
	exclusion, err := store.CreateExclusion(ctx, "Whole scope", []string{"192.0.2.0/29"})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := store.CreateScanPlan(ctx, "No work", target.ID, DefaultScanPlanSettings(), exclusion.ID)
	if err != nil {
		t.Fatal(err)
	}
	app := api.Handler()
	if r := request(app, "POST", "/v1/scan-plans/"+plan.ID+"/preflight", ""); r.Code != 400 {
		t.Fatalf("fully excluded preflight: %d %s", r.Code, r.Body)
	}
	var count int
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM masscanr.runs`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("run reserved: %d, %v", count, err)
	}
	asTarget, err := store.CreateTarget(ctx, "AS scope", []string{"AS3333"})
	if err != nil {
		t.Fatal(err)
	}
	asPlan, err := store.CreateScanPlan(ctx, "AS narrowed", asTarget.ID, DefaultScanPlanSettings(), exclusion.ID)
	if err != nil {
		t.Fatal(err)
	}
	api.Resolve = func(_ context.Context, targets []scan.Target) ([]scan.Target, error) {
		targets[0].Prefixes = []netip.Prefix{netip.MustParsePrefix("192.0.2.0/28")}
		targets[0].Resolution = &scan.Resolution{Source: scan.RIPEEndpoint, FetchedAt: time.Now().UTC(), SnapshotAt: time.Now().UTC().Add(-time.Minute)}
		return targets, nil
	}
	asApp := api.Handler()
	r := request(asApp, "POST", "/v1/scan-plans/"+asPlan.ID+"/preflight", "")
	var preflight Plan
	if r.Code != 201 || decodeTestResource(r.Body.Bytes(), &preflight) != nil || !slices.Equal(preflight.Config.Targets, []netip.Prefix{netip.MustParsePrefix("192.0.2.8/29")}) {
		t.Fatalf("AS exclusion after resolution: %d %s", r.Code, r.Body)
	}
}

func TestVersion8PlanRelationshipUpgrade(t *testing.T) {
	store := testStore(t)
	ctx := t.Context()
	target, err := store.CreateTarget(ctx, "Existing target", []string{"192.0.2.0/24"})
	if err != nil {
		t.Fatal(err)
	}
	exclusion, err := store.CreateExclusion(ctx, "Existing exclusion", []string{"192.0.2.1"})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := store.CreateScanPlan(ctx, "Existing plan", target.ID, DefaultScanPlanSettings())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Pool.Exec(ctx, `DROP TABLE masscanr.scan_plan_exclusions; UPDATE masscanr.version SET version=8`); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, store.Pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reopened.Pool.Close)
	var version int
	if err := reopened.Pool.QueryRow(ctx, `SELECT version FROM masscanr.version`).Scan(&version); err != nil || version != 9 {
		t.Fatalf("version = %d, %v", version, err)
	}
	if got, err := reopened.GetScanPlan(ctx, plan.ID); err != nil || got.TargetID != target.ID || len(got.ExclusionIDs) != 0 {
		t.Fatalf("preserved plan = %+v, %v", got, err)
	}
	if got, err := reopened.GetExclusion(ctx, exclusion.ID); err != nil || got.Revision != exclusion.Revision {
		t.Fatalf("preserved exclusion = %+v, %v", got, err)
	}
}
