package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

func TestTargetNameReferences(t *testing.T) {
	store := testStore(t)
	api := &API{Store: store}
	api.Extra = func(app *fiber.App) { (&Manager{Store: store}).Routes(api, app) }
	app := api.Handler()
	create := request(app, "POST", "/v1/targets", `{"data":{"type":"targets","attributes":{"name":" External Network ","entries":["192.0.2.1"]}}}`)
	var target Target
	if create.Code != 201 || decodeTestResource(create.Body.Bytes(), &target) != nil || target.Name != "External Network" {
		t.Fatalf("create target: %d %s", create.Code, create.Body)
	}
	duplicate := request(app, "POST", "/v1/targets", `{"data":{"type":"targets","attributes":{"name":"external network","entries":["192.0.2.2"]}}}`)
	if duplicate.Code != 409 || !strings.Contains(duplicate.Body.String(), "target name already exists") {
		t.Fatalf("duplicate target: %d %s", duplicate.Code, duplicate.Body)
	}
	namePath := "/v1/targets/by-name/" + url.PathEscape("EXTERNAL NETWORK")
	get := request(app, "GET", namePath, "")
	var got Target
	if get.Code != 200 || decodeTestResource(get.Body.Bytes(), &got) != nil || got.ID != target.ID {
		t.Fatalf("get by name: %d %s", get.Code, get.Body)
	}
	rename := request(app, "PATCH", "/v1/targets/"+target.ID, fmt.Sprintf(`{"data":{"type":"targets","id":%q,"attributes":{"revision":1,"name":"Changed"}}}`, target.ID))
	if rename.Code != 400 {
		t.Fatalf("accepted name change: %d %s", rename.Code, rename.Body)
	}
	wrongID := request(app, "PATCH", namePath, `{"data":{"type":"targets","id":"wrong","attributes":{"revision":1,"entries":["192.0.2.2"]}}}`)
	if wrongID.Code != 400 {
		t.Fatalf("accepted mismatched ID: %d %s", wrongID.Code, wrongID.Body)
	}
	update := request(app, "PATCH", namePath, fmt.Sprintf(`{"data":{"type":"targets","id":%q,"attributes":{"revision":1,"entries":["192.0.2.2"]}}}`, target.ID))
	if update.Code != 200 {
		t.Fatalf("update by name: %d %s", update.Code, update.Body)
	}
	stale := request(app, "PATCH", namePath, fmt.Sprintf(`{"data":{"type":"targets","id":%q,"attributes":{"revision":1,"entries":["192.0.2.3"]}}}`, target.ID))
	if stale.Code != 409 {
		t.Fatalf("stale update by name: %d %s", stale.Code, stale.Body)
	}
	planRequest := `{"data":{"type":"scan-plans","attributes":{"name":"Routine","target_name":"external network"}}}`
	createPlan := request(app, "POST", "/v1/scan-plans", planRequest)
	var plan ScanPlan
	if createPlan.Code != 201 || decodeTestResource(createPlan.Body.Bytes(), &plan) != nil || plan.TargetID != target.ID {
		t.Fatalf("plan by name: %d %s", createPlan.Code, createPlan.Body)
	}
	other, err := store.CreateTarget(context.Background(), "Cloud/Edge 100%", []string{"192.0.2.3"})
	if err != nil {
		t.Fatal(err)
	}
	encoded := request(app, "GET", "/v1/targets/by-name/"+url.PathEscape("Cloud/Edge 100%"), "")
	var encodedTarget Target
	if encoded.Code != 200 || decodeTestResource(encoded.Body.Bytes(), &encodedTarget) != nil || encodedTarget.ID != other.ID {
		t.Fatalf("encoded name: %d %s", encoded.Code, encoded.Body)
	}
	patchPlan := request(app, "PATCH", "/v1/scan-plans/"+plan.ID, fmt.Sprintf(`{"data":{"type":"scan-plans","id":%q,"attributes":{"revision":1,"target_name":"cloud/edge 100%%"}}}`, plan.ID))
	var updatedPlan ScanPlan
	if patchPlan.Code != 200 || decodeTestResource(patchPlan.Body.Bytes(), &updatedPlan) != nil || updatedPlan.TargetID != other.ID {
		t.Fatalf("update plan by name: %d %s", patchPlan.Code, patchPlan.Body)
	}
	bothPatch := request(app, "PATCH", "/v1/scan-plans/"+plan.ID, fmt.Sprintf(`{"data":{"type":"scan-plans","id":%q,"attributes":{"revision":2,"target_id":%q,"target_name":"External Network"}}}`, plan.ID, target.ID))
	if bothPatch.Code != 400 {
		t.Fatalf("accepted both update selectors: %d %s", bothPatch.Code, bothPatch.Body)
	}
	both := request(app, "POST", "/v1/scan-plans", fmt.Sprintf(`{"data":{"type":"scan-plans","attributes":{"name":"Invalid","target_id":%q,"target_name":"External Network"}}}`, target.ID))
	if both.Code != 400 {
		t.Fatalf("accepted both target selectors: %d %s", both.Code, both.Body)
	}
	for _, path := range []string{"/v1/runs?filter%5Btarget_name%5D=external+network", "/v1/scan-results?filter%5Btarget_name%5D=external+network"} {
		response := request(app, "GET", path, "")
		if response.Code != 200 {
			t.Fatalf("filter by name %s: %d %s", path, response.Code, response.Body)
		}
	}
	for _, path := range []string{"/v1/runs", "/v1/scan-results"} {
		response := request(app, "GET", path+"?filter%5Btarget_name%5D=missing", "")
		if response.Code != 404 {
			t.Fatalf("unknown name %s: %d %s", path, response.Code, response.Body)
		}
		response = request(app, "GET", path+"?filter%5Btarget_name%5D=External&filter%5Btarget_id%5D="+target.ID, "")
		if response.Code != 400 {
			t.Fatalf("accepted both filters %s: %d %s", path, response.Code, response.Body)
		}
	}
	archive := request(app, "PATCH", namePath, fmt.Sprintf(`{"data":{"type":"targets","id":%q,"attributes":{"revision":2,"archived":true}}}`, target.ID))
	if archive.Code != 200 {
		t.Fatalf("archive by name: %d %s", archive.Code, archive.Body)
	}
	if response := request(app, "GET", namePath, ""); response.Code != 200 {
		t.Fatalf("archived name lookup: %d %s", response.Code, response.Body)
	}
	if response := request(app, "POST", "/v1/scan-plans", planRequest); response.Code != 409 {
		t.Fatalf("plan on archived name: %d %s", response.Code, response.Body)
	}
	if response := request(app, "POST", "/v1/targets", `{"data":{"type":"targets","attributes":{"name":" EXTERNAL NETWORK ","entries":["192.0.2.3"]}}}`); response.Code != 409 {
		t.Fatalf("reused archived name: %d %s", response.Code, response.Body)
	}
}

func TestRunHistoryTargetNameFilter(t *testing.T) {
	store := testStore(t)
	run := reserveFixture(t, store, "masscan")
	api := &API{Store: store}
	api.Extra = func(app *fiber.App) { (&Manager{Store: store}).Routes(api, app) }
	app := api.Handler()
	response := request(app, "GET", "/v1/runs?filter%5Btarget_name%5D=fixture+NETWORK", "")
	var listed []Run
	if response.Code != 200 || decodeTestList(response.Body.Bytes(), &listed) != nil || len(listed) != 1 || listed[0].ID != run.ID {
		t.Fatalf("run name filter: %d %s", response.Code, response.Body)
	}
}

func TestTargetNameUniquenessConcurrent(t *testing.T) {
	store := testStore(t)
	results := make(chan error, 2)
	for _, name := range []string{"Fleet", " fleet "} {
		go func(name string) {
			_, err := store.CreateTarget(context.Background(), name, []string{"192.0.2.1"})
			results <- err
		}(name)
	}
	var successes, conflicts int
	for range 2 {
		err := <-results
		if err == nil {
			successes++
		} else if errors.Is(err, ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
}

func TestTargetNameUpgradeRejectsCollisionsWithoutChangingData(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	_, err := store.Pool.Exec(ctx, `DROP INDEX masscanr.targets_name_unique; UPDATE masscanr.version SET version=5`)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, name := range []string{"Fleet", " fleet "} {
		if _, err := store.Pool.Exec(ctx, `INSERT INTO masscanr.targets(id,name,revision,created_at,updated_at) VALUES($1,$2,1,$3,$3)`, newID(), name, now); err != nil {
			t.Fatal(err)
		}
	}
	reopened, err := Open(ctx, store.Pool.Config().ConnString())
	if err == nil || !strings.Contains(err.Error(), "duplicate normalized name") {
		if reopened != nil {
			reopened.Pool.Close()
		}
		t.Fatalf("expected collision, got %v", err)
	}
	var version, count int
	if err := store.Pool.QueryRow(ctx, `SELECT version FROM masscanr.version`).Scan(&version); err != nil || version != 5 {
		t.Fatalf("version after failed upgrade: %d %v", version, err)
	}
	if err := store.Pool.QueryRow(ctx, `SELECT count(*) FROM masscanr.targets`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("targets after failed upgrade: %d %v", count, err)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE masscanr.targets SET name='Second Fleet' WHERE name=' fleet '`); err != nil {
		t.Fatal(err)
	}
	reopened, err = Open(ctx, store.Pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reopened.Pool.Close)
	if err := reopened.Pool.QueryRow(ctx, `SELECT version FROM masscanr.version`).Scan(&version); err != nil || version != 7 {
		t.Fatalf("upgraded version: %d %v", version, err)
	}
	if _, err := reopened.Pool.Exec(ctx, `INSERT INTO masscanr.targets(id,name,revision,created_at,updated_at) VALUES($1,'FLEET',1,$2,$2)`, newID(), now); err == nil {
		t.Fatal("unique index accepted a duplicate")
	}
}
