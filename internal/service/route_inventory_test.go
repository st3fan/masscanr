package service

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// The application routes must match the Huma OpenAPI document.
var applicationRoutes = []string{
	"GET /healthz",
	"GET /v1/catalog/instance-types",
	"GET /v1/catalog/regions",
	"GET /v1/runs",
	"GET /v1/runs/:id",
	"GET /v1/runs/:id/exports/:format",
	"GET /v1/runs/:id/inspection",
	"GET /v1/runs/:id/workers",
	"GET /v1/scan-plans",
	"GET /v1/scan-plans/:id",
	"GET /v1/scan-plans/:id/runs",
	"GET /v1/scan-results",
	"GET /v1/targets",
	"GET /v1/targets/:id",
	"GET /v1/targets/by-name/:name",
	"PATCH /v1/scan-plans/:id",
	"PATCH /v1/targets/:id",
	"PATCH /v1/targets/by-name/:name",
	"POST /v1/runs/:id/stop",
	"POST /v1/scan-plans",
	"POST /v1/scan-plans/:id/preflight",
	"POST /v1/scan-plans/:id/start",
	"POST /v1/targets",
}

func TestOpenAPICoversApplicationRoutes(t *testing.T) {
	api := &API{Store: &Store{}}
	manager := &Manager{Store: api.Store}
	api.Extra = func(app *fiber.App) { manager.Routes(api, app) }
	app := api.Handler()
	w := request(app, "GET", "/openapi.json", "")
	if w.Code != 200 {
		t.Fatalf("OpenAPI status %d: %s", w.Code, w.Body)
	}
	var spec struct {
		OpenAPI    string                                `json:"openapi"`
		Paths      map[string]map[string]json.RawMessage `json:"paths"`
		Components struct {
			Schemas map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &spec); err != nil || spec.OpenAPI != "3.1.0" {
		t.Fatalf("invalid OpenAPI 3.1 document: %v", err)
	}
	seen := map[string]bool{}
	for _, route := range applicationRoutes {
		method, path, _ := strings.Cut(route, " ")
		path = strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(path, ":id", "{id}"), ":name", "{name}"), ":format", "{format}")
		raw := spec.Paths[path][strings.ToLower(method)]
		if len(raw) == 0 {
			t.Errorf("missing OpenAPI operation %s %s", method, path)
			continue
		}
		var operation struct {
			ID        string                     `json:"operationId"`
			Responses map[string]json.RawMessage `json:"responses"`
		}
		if err := json.Unmarshal(raw, &operation); err != nil || operation.ID == "" || seen[operation.ID] || len(operation.Responses) == 0 {
			t.Errorf("invalid OpenAPI operation %s %s: %v", method, path, err)
		}
		seen[operation.ID] = true
	}
	if len(seen) != len(applicationRoutes) || len(spec.Components.Schemas) == 0 {
		t.Errorf("OpenAPI has %d operation IDs and %d component schemas", len(seen), len(spec.Components.Schemas))
	}
}

func TestOfflineStoplightDocs(t *testing.T) {
	app := (&API{}).Handler()
	w := request(app, "GET", "/docs", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `/openapi.json`) || !strings.Contains(w.Body.String(), `stoplight-9.0.29`) || strings.Contains(w.Body.String(), "https://") {
		t.Fatalf("offline docs: status=%d body=%q", w.Code, w.Body)
	}
	for _, name := range []string{"styles.min.css", "web-components.min.js"} {
		asset := request(app, "GET", "/docs/assets/stoplight-9.0.29/"+name, "")
		if asset.Code != 200 || asset.Body.Len() < 1000 {
			t.Fatalf("bundled %s: status=%d bytes=%d", name, asset.Code, asset.Body.Len())
		}
	}
}

func TestApplicationRouteInventory(t *testing.T) {
	api := &API{Store: &Store{}}
	manager := &Manager{Store: api.Store}
	api.Extra = func(app *fiber.App) { manager.Routes(api, app) }
	app := api.Handler()
	var actual []string
	for _, route := range app.GetRoutes(true) {
		if route.Method == http.MethodHead || !(route.Path == "/healthz" || strings.HasPrefix(route.Path, "/v1/")) {
			continue // Fiber adds HEAD for each GET route.
		}
		actual = append(actual, route.Method+" "+route.Path)
	}
	slices.Sort(actual)
	if !slices.Equal(actual, applicationRoutes) {
		t.Fatalf("application routes = %q, want %q", actual, applicationRoutes)
	}
}
