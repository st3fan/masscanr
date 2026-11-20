package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/gofiber/fiber/v3"
	"github.com/st3fan/masscanr/internal/cloud"
	"github.com/st3fan/masscanr/internal/scan"
)

type API struct {
	Huma huma.API
	// BrowserOrigins are the exact origins allowed to access /v1 from a browser.
	// Non-browser API clients do not send browser origin/fetch metadata headers.
	BrowserOrigins []string
	PreviewProject string
	PreviewDomain  string
	Resolve        func(context.Context, []scan.Target) ([]scan.Target, error)
	Catalog        cloud.Catalog
	Catalogs       map[string]cloud.Catalog
	Store          *Store
	Ready          func() bool
	Extra          func(*fiber.App)
	Base           context.Context
}

func (a *API) Handler() *fiber.App {
	base := a.Base
	if base == nil {
		base = context.Background()
	}
	app := fiber.New(fiber.Config{
		CaseSensitive: true,
		StrictRouting: true,
		// Allow a small transport margin; decode enforces the 1 MiB JSON limit.
		BodyLimit:   2 << 20,
		ReadTimeout: 10 * time.Second,
		IdleTimeout: 60 * time.Second,
		ErrorHandler: func(c fiber.Ctx, err error) error {
			if errors.Is(err, fiber.ErrRequestEntityTooLarge) {
				return respond(c, 400, map[string]string{"error": "invalid JSON body"})
			}
			if e, ok := err.(*fiber.Error); ok {
				return respond(c, e.Code, map[string]string{"error": e.Message})
			}
			return respond(c, 500, map[string]string{"error": "internal error"})
		},
	})
	app.Use(func(c fiber.Ctx) error {
		if strings.HasPrefix(c.Path(), "/v1/") {
			if !allowedBrowserRequest(c, a.BrowserOrigins, a.PreviewProject, a.PreviewDomain) {
				return respond(c, 403, map[string]string{"error": "browser origin not allowed"})
			}
			if !acceptsResponse(c.Get("Accept"), strings.Contains(c.Path(), "/exports/")) {
				return respond(c, 406, map[string]string{"error": "JSON:API response not acceptable"})
			}
			if len(c.Body()) > 0 {
				contentType, params, err := mime.ParseMediaType(c.Get("Content-Type"))
				if err != nil || contentType != jsonAPIMediaType || invalidJSONAPIParams(params) {
					return respond(c, 415, map[string]string{"error": "JSON:API content type required"})
				}
			}
		}
		// Fiber does not cancel handlers on disconnect. Bound operations and
		// cancel them when the server shuts down.
		duration := 30 * time.Second
		if c.Path() == "/v1/catalog/regions" || c.Path() == "/v1/catalog/instance-types" {
			duration = time.Minute
		}
		if c.Method() == fiber.MethodPost && strings.HasPrefix(c.Path(), "/v1/scan-plans/") && strings.HasSuffix(c.Path(), "/preflight") {
			duration = 2 * time.Minute
		}
		if c.Method() == fiber.MethodGet && strings.HasPrefix(c.Path(), "/v1/runs/") && strings.HasSuffix(c.Path(), "/inspection") {
			duration = 2 * time.Minute
		}
		ctx, cancel := context.WithTimeout(base, duration)
		defer cancel()
		c.SetContext(ctx)
		return c.Next()
	})
	a.initHuma(app)
	a.register("GET", "/healthz", "health", "Health and readiness", nil, func(c fiber.Ctx) error {
		ctx, cancel := context.WithTimeout(c.Context(), time.Second)
		defer cancel()
		if (a.Ready != nil && !a.Ready()) || a.Store.Pool.Ping(ctx) != nil {
			return respond(c, 503, map[string]string{"error": "unavailable"})
		}
		return respond(c, 200, map[string]string{"status": "ready"})
	})
	a.register("POST", "/v1/targets", "createTarget", "Create a reusable target", Target{}, a.createTarget)
	a.register("GET", "/v1/targets", "listTargets", "List targets", Target{}, a.listTargets)
	a.register("GET", "/v1/targets/by-name/:name", "getTargetByName", "Get a target by name", Target{}, a.getTargetByName)
	a.register("PATCH", "/v1/targets/by-name/:name", "updateTargetByName", "Update or archive a target by name", Target{}, a.patchTargetByName)
	a.register("GET", "/v1/targets/:id", "getTarget", "Get a target", Target{}, a.getTarget)
	a.register("PATCH", "/v1/targets/:id", "updateTarget", "Update or archive a target", Target{}, a.patchTarget)
	a.register("POST", "/v1/exclusions", "createExclusion", "Create a reusable exclusion", Exclusion{}, a.createExclusion)
	a.register("GET", "/v1/exclusions", "listExclusions", "List exclusions", Exclusion{}, a.listExclusions)
	a.register("GET", "/v1/exclusions/:id", "getExclusion", "Get an exclusion", Exclusion{}, a.getExclusion)
	a.register("PATCH", "/v1/exclusions/:id", "updateExclusion", "Update or archive an exclusion", Exclusion{}, a.patchExclusion)
	a.register("POST", "/v1/scan-plans", "createScanPlan", "Create a reusable scan plan", ScanPlan{}, a.createScanPlan)
	a.register("GET", "/v1/scan-plans", "listScanPlans", "List scan plans", ScanPlan{}, a.listScanPlans)
	a.register("GET", "/v1/scan-results", "listScanResults", "List positive open port observations", ScanResult{}, a.listScanResults)
	a.register("GET", "/v1/scan-plans/:id", "getScanPlan", "Get a scan plan", ScanPlan{}, a.getScanPlan)
	a.register("POST", "/v1/scan-plans/:id/preflight", "preflightScanPlan", "Resolve and freeze a scan preflight", Plan{}, a.preflightScanPlan)
	a.register("PATCH", "/v1/scan-plans/:id", "updateScanPlan", "Update a scan plan", ScanPlan{}, a.patchScanPlan)
	a.register("GET", "/v1/catalog/regions", "listRegions", "List provider regions", regionResponse{}, a.regions)
	a.register("GET", "/v1/catalog/instance-types", "listInstanceTypes", "List provider instance types", instanceTypeResponse{}, a.instanceTypes)
	if a.Extra != nil {
		a.Extra(app)
	}
	return app
}

func decode(c fiber.Ctx, v any) error {
	return decodeForID(c, v, c.Params("id"))
}

func decodeForID(c fiber.Ctx, v any, expectedID string) error {
	_, err := decodeForIDRelationships(c, v, expectedID, false)
	return err
}

func decodeForIDRelationships(c fiber.Ctx, v any, expectedID string, allowRelationships bool) (json.RawMessage, error) {
	if len(c.Body()) > 1<<20 {
		return nil, fmt.Errorf("invalid JSON body")
	}
	var envelope struct {
		Data struct {
			Type          string          `json:"type"`
			ID            string          `json:"id"`
			Attributes    json.RawMessage `json:"attributes"`
			Relationships json.RawMessage `json:"relationships"`
		} `json:"data"`
	}
	d := json.NewDecoder(bytes.NewReader(c.Body()))
	d.DisallowUnknownFields()
	if e := d.Decode(&envelope); e != nil {
		return nil, fmt.Errorf("invalid JSON body")
	}
	if e := d.Decode(new(any)); e != io.EOF {
		return nil, fmt.Errorf("expected one JSON object")
	}
	if envelope.Data.Type != requestResourceType(c) || len(envelope.Data.Attributes) == 0 || string(envelope.Data.Attributes) == "null" {
		return nil, fmt.Errorf("invalid JSON:API resource")
	}
	if c.Method() == fiber.MethodPatch && envelope.Data.ID == "" {
		return nil, fmt.Errorf("resource ID is required")
	}
	if expectedID != "" && envelope.Data.ID != "" && envelope.Data.ID != expectedID {
		return nil, fmt.Errorf("resource ID does not match URL")
	}
	if !allowRelationships && len(envelope.Data.Relationships) != 0 {
		return nil, fmt.Errorf("relationships are not supported for this resource")
	}
	d = json.NewDecoder(bytes.NewReader(envelope.Data.Attributes))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return nil, fmt.Errorf("invalid JSON:API attributes")
	}
	return envelope.Data.Relationships, nil
}

func respond(c fiber.Ctx, status int, v any) error {
	if strings.HasPrefix(c.Path(), "/v1/") {
		return respondJSONAPI(c, status, v)
	}
	c.Set("Content-Type", "application/json")
	c.Status(status)
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.Send(append(b, '\n'))
}
func problem(c fiber.Ctx, e error) error {
	code, message := 500, "internal error"
	if errors.Is(e, ErrNotFound) {
		code, message = 404, e.Error()
	}
	if errors.Is(e, ErrConflict) {
		code, message = 409, e.Error()
	}
	return respond(c, code, map[string]string{"error": message})
}
func bad(c fiber.Ctx, e error) error { return respond(c, 400, map[string]string{"error": e.Error()}) }
