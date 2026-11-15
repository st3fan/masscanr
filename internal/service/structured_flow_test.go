package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"github.com/gofiber/fiber/v3"
	"github.com/st3fan/masscanr/internal/backend/registry"
	"github.com/st3fan/masscanr/internal/cloud"
	"github.com/st3fan/masscanr/internal/controller"
	"github.com/st3fan/masscanr/internal/scan"
)

// The full HTTP workflow uses a fake cloud and scanner and never starts the app.
func TestStructuredHTTPWorkflow(t *testing.T) {
	store := testStore(t)
	dc, err := store.WorkflowContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(store, dc, t.TempDir())
	provider := &providerFixture{workers: map[string]cloud.Worker{}}
	runner := &runnerFixture{name: "masscan", started: make(chan struct{})}
	manager.Factory = func(run Run) *controller.Controller {
		backend, _ := registry.Resolve("masscan")
		timeouts := run.Plan.Timeouts
		timeouts.Poll = time.Millisecond
		return &controller.Controller{RunID: run.ID, Backend: backend, Provider: provider, Runner: runner, Timeouts: timeouts}
	}
	if err := dbos.Launch(dc); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dbos.Shutdown(dc, 5*time.Second) })
	t.Setenv("VULTR_API_TOKEN", "fixture-token")
	api := &API{Store: store, Resolve: func(_ context.Context, targets []scan.Target) ([]scan.Target, error) { return targets, nil }}
	api.Extra = func(app *fiber.App) { manager.Routes(api, app) }
	app := api.Handler()

	created := request(app, "POST", "/v1/targets", `{"data":{"type":"targets","attributes":{"name":"External network","entries":["192.0.2.1"]}}}`)
	var target Target
	if created.Code != 201 || decodeTestResource(created.Body.Bytes(), &target) != nil {
		t.Fatalf("create Target: %d %s", created.Code, created.Body)
	}
	created = request(app, "POST", "/v1/scan-plans", `{"data":{"type":"scan-plans","attributes":{"name":"Routine inventory","target_id":"`+target.ID+`"}}}`)
	var plan ScanPlan
	if created.Code != 201 || decodeTestResource(created.Body.Bytes(), &plan) != nil {
		t.Fatalf("create ScanPlan: %d %s", created.Code, created.Body)
	}
	updated := request(app, "PATCH", "/v1/scan-plans/"+plan.ID, `{"data":{"type":"scan-plans","id":"`+plan.ID+`","attributes":{"revision":1,"settings":{"ports":"80","shards":1}}}}`)
	if updated.Code != 200 || decodeTestResource(updated.Body.Bytes(), &plan) != nil || plan.Revision != 2 {
		t.Fatalf("update ScanPlan: %d %s", updated.Code, updated.Body)
	}
	frozen := request(app, "POST", "/v1/scan-plans/"+plan.ID+"/preflight", "")
	var preflight Plan
	if frozen.Code != 201 || decodeTestResource(frozen.Body.Bytes(), &preflight) != nil || preflight.TargetID != target.ID || preflight.ScanPlanID != plan.ID {
		t.Fatalf("preflight: %d %s", frozen.Code, frozen.Body)
	}
	body := `{"data":{"type":"runs","attributes":{"preflight_id":"` + preflight.ID + `","confirm":true}}}`
	req := httptest.NewRequest("POST", "/v1/scan-plans/"+plan.ID+"/start", strings.NewReader(body))
	req.Header.Set("Content-Type", jsonAPIMediaType)
	req.Header.Set("Idempotency-Key", "structured-flow")
	started, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := io.ReadAll(started.Body)
	_ = started.Body.Close()
	var run Run
	if started.StatusCode != 202 || decodeTestResource(payload, &run) != nil || run.TargetID != target.ID || run.ScanPlanID != plan.ID || run.Plan.ID != preflight.ID {
		t.Fatalf("start: %d %s", started.StatusCode, payload)
	}
	settled := waitRun(t, store, run.ID)
	if settled.Phase != "completed" {
		t.Fatalf("run: %+v", settled)
	}
	status := request(app, "GET", "/v1/runs/"+run.ID, "")
	if status.Code != 200 || !strings.Contains(status.Body.String(), `"snapshot_version":2`) {
		t.Fatalf("status: %d %s", status.Code, status.Body)
	}
	query := request(app, "GET", "/v1/scan-results?filter[run_id]="+run.ID+"&filter[port]=80", "")
	var findings []ScanResult
	if query.Code != 200 || decodeTestList(query.Body.Bytes(), &findings) != nil || len(findings) != 1 || findings[0].IP.String() != "192.0.2.1" || !strings.Contains(query.Body.String(), `"state":"complete"`) {
		t.Fatalf("results: %d %s", query.Code, query.Body)
	}
	inspection := request(app, "GET", "/v1/runs/"+run.ID+"/inspection", "")
	if inspection.Code != 200 || !strings.Contains(inspection.Body.String(), `"complete":true`) {
		t.Fatalf("inspection: %d %s", inspection.Code, inspection.Body)
	}
	for _, format := range []string{"csv", "jsonl"} {
		export := request(app, "GET", "/v1/runs/"+run.ID+"/exports/"+format, "")
		if export.Code != 200 || !strings.Contains(export.Body.String(), "192.0.2.1") {
			t.Fatalf("%s export: %d %s", format, export.Code, export.Body)
		}
	}
	var doc map[string]any
	if err := json.Unmarshal(query.Body.Bytes(), &doc); err != nil || doc["meta"] == nil {
		t.Fatalf("missing result coverage: %v", err)
	}
}
