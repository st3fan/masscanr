package service

import (
	"net/http"
	"slices"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// The application routes form the migration inventory. The Huma cutover adds
// documentation endpoints, but each of these operations must remain present.
var applicationRoutes = []string{
	"DELETE /v1/scans/:id",
	"GET /healthz",
	"GET /v1/catalog/instance-types",
	"GET /v1/catalog/regions",
	"GET /v1/runs",
	"GET /v1/runs/:id",
	"GET /v1/runs/:id/exports/:format",
	"GET /v1/runs/:id/inspection",
	"GET /v1/scans",
	"GET /v1/scans/:id",
	"GET /v1/scans/:id/runs",
	"POST /v1/runs/:id/stop",
	"POST /v1/scans",
	"POST /v1/scans/:id/preflight",
	"POST /v1/scans/:id/start",
	"POST /v1/scans/:id/stop",
	"PUT /v1/scans/:id",
}

func TestApplicationRouteInventory(t *testing.T) {
	api := &API{Store: &Store{}}
	manager := &Manager{Store: api.Store}
	api.Extra = func(app *fiber.App) { manager.Routes(api, app) }
	app := api.Handler()
	var actual []string
	for _, route := range app.GetRoutes(true) {
		if route.Method == http.MethodHead {
			continue // Fiber adds HEAD for each GET route.
		}
		actual = append(actual, route.Method+" "+route.Path)
	}
	slices.Sort(actual)
	if !slices.Equal(actual, applicationRoutes) {
		t.Fatalf("application routes = %q, want %q", actual, applicationRoutes)
	}
}
