package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/gofiber/fiber/v3"
	"github.com/st3fan/masscanr/internal/cloud"
	"github.com/st3fan/masscanr/internal/scan"
)

type API struct {
	Huma    huma.API
	Resolve func(context.Context, []scan.Target) ([]scan.Target, error)
	Catalog cloud.Catalog
	Store   *Store
	Ready   func() bool
	Extra   func(*fiber.App)
	Base    context.Context
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
		if c.Method() == fiber.MethodPost && strings.HasPrefix(c.Path(), "/v1/scans/") && strings.HasSuffix(c.Path(), "/preflight") {
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
	a.register("POST", "/v1/scans", "createScan", "Create a scan definition", Scan{}, a.create)
	a.register("POST", "/v1/scans/:id/preflight", "preflightScan", "Resolve an authorized scan plan", Plan{}, a.preflight)
	a.register("GET", "/v1/scans", "listScans", "List scan definitions", Scan{}, a.list)
	a.register("GET", "/v1/scans/:id", "getScan", "Get a scan definition", Scan{}, a.get)
	a.register("GET", "/v1/catalog/regions", "listRegions", "List provider regions", regionResponse{}, a.regions)
	a.register("GET", "/v1/catalog/instance-types", "listInstanceTypes", "List provider instance types", instanceTypeResponse{}, a.instanceTypes)
	a.register("PATCH", "/v1/scans/:id", "updateScan", "Partially update a scan definition", Scan{}, a.patch)
	a.register("DELETE", "/v1/scans/:id", "deleteScan", "Delete a scan definition", nil, a.remove)
	if a.Extra != nil {
		a.Extra(app)
	}
	return app
}

func decode(c fiber.Ctx, v any) error {
	if len(c.Body()) > 1<<20 {
		return fmt.Errorf("invalid JSON body")
	}
	var envelope struct {
		Data struct {
			Type       string          `json:"type"`
			ID         string          `json:"id"`
			Attributes json.RawMessage `json:"attributes"`
		} `json:"data"`
	}
	d := json.NewDecoder(bytes.NewReader(c.Body()))
	d.DisallowUnknownFields()
	if e := d.Decode(&envelope); e != nil {
		return fmt.Errorf("invalid JSON body")
	}
	if e := d.Decode(new(any)); e != io.EOF {
		return fmt.Errorf("expected one JSON object")
	}
	if envelope.Data.Type != requestResourceType(c) || len(envelope.Data.Attributes) == 0 || string(envelope.Data.Attributes) == "null" {
		return fmt.Errorf("invalid JSON:API resource")
	}
	if c.Method() == fiber.MethodPatch && envelope.Data.ID == "" {
		return fmt.Errorf("resource ID is required")
	}
	if id := c.Params("id"); envelope.Data.ID != "" && envelope.Data.ID != id {
		return fmt.Errorf("resource ID does not match URL")
	}
	d = json.NewDecoder(bytes.NewReader(envelope.Data.Attributes))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return fmt.Errorf("invalid JSON:API attributes")
	}
	return nil
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
func (a *API) create(c fiber.Ctx) error {
	d := Defaults()
	if e := decode(c, &d); e != nil {
		return bad(c, e)
	}
	if _, _, e := d.Config(); e != nil {
		return bad(c, e)
	}
	now := time.Now().UTC()
	v := Scan{ID: newID(), Revision: 1, CreatedAt: now, UpdatedAt: now, Definition: d}
	if e := a.Store.SaveScan(c.Context(), v, 0, true); e != nil {
		return problem(c, e)
	}
	c.Set("Location", "/v1/scans/"+v.ID)
	return respond(c, 201, v)
}
func (a *API) get(c fiber.Ctx) error {
	v, e := a.Store.GetScan(c.Context(), c.Params("id"))
	if e != nil {
		return problem(c, e)
	}
	return respond(c, 200, v)
}
func (a *API) replace(c fiber.Ctx) error {
	var in struct {
		Revision   int64      `json:"revision"`
		Definition Definition `json:"definition"`
	}
	in.Definition = Defaults()
	if e := decode(c, &in); e != nil {
		return bad(c, e)
	}
	if in.Revision < 1 {
		return bad(c, fmt.Errorf("revision must be positive"))
	}
	if _, _, e := in.Definition.Config(); e != nil {
		return bad(c, e)
	}
	v := Scan{ID: c.Params("id"), Revision: in.Revision + 1, UpdatedAt: time.Now().UTC(), Definition: in.Definition}
	if e := a.Store.SaveScan(c.Context(), v, in.Revision, false); e != nil {
		return problem(c, e)
	}
	return a.get(c)
}
func (a *API) patch(c fiber.Ctx) error {
	var in struct {
		Revision   int64                      `json:"revision"`
		Definition map[string]json.RawMessage `json:"definition"`
	}
	if e := decode(c, &in); e != nil {
		return bad(c, e)
	}
	if in.Revision < 1 || len(in.Definition) == 0 {
		return bad(c, fmt.Errorf("revision and changed definition attributes are required"))
	}
	current, e := a.Store.GetScan(c.Context(), c.Params("id"))
	if e != nil {
		return problem(c, e)
	}
	if current.Revision != in.Revision {
		return problem(c, ErrConflict)
	}
	b, e := json.Marshal(current.Definition)
	if e != nil {
		return problem(c, e)
	}
	var fields map[string]json.RawMessage
	if e = json.Unmarshal(b, &fields); e != nil {
		return problem(c, e)
	}
	for key, value := range in.Definition {
		if _, ok := fields[key]; !ok {
			return bad(c, fmt.Errorf("unknown definition attribute %q", key))
		}
		fields[key] = value
	}
	b, e = json.Marshal(fields)
	if e != nil {
		return problem(c, e)
	}
	var definition Definition
	if e = json.Unmarshal(b, &definition); e != nil {
		return bad(c, fmt.Errorf("invalid definition attributes"))
	}
	if _, _, e = definition.Config(); e != nil {
		return bad(c, e)
	}
	current.Definition = definition
	current.Revision++
	current.UpdatedAt = time.Now().UTC()
	if e = a.Store.SaveScan(c.Context(), current, in.Revision, false); e != nil {
		return problem(c, e)
	}
	return respond(c, 200, current)
}
func (a *API) remove(c fiber.Ctx) error {
	if e := a.Store.DeleteScan(c.Context(), c.Params("id")); e != nil {
		return problem(c, e)
	}
	return respond(c, 204, nil)
}
func (a *API) list(c fiber.Ctx) error { return a.listTable(c, "scans", "") }
func (a *API) listTable(c fiber.Ctx, table, id string) error {
	limit := 100
	if s := c.Query("limit"); s != "" {
		n, e := strconv.Atoi(s)
		if e != nil || n < 1 || n > 1000 {
			return bad(c, fmt.Errorf("limit must be 1..1000"))
		}
		limit = n
	}
	v, e := a.Store.List(c.Context(), table, id, c.Query("after"), limit)
	if e != nil {
		return problem(c, e)
	}
	return respond(c, 200, v)
}
