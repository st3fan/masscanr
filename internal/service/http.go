package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/st3fan/masscanr/internal/scan"
	"io"
	"net/http"
	"strconv"
	"time"
)

type API struct {
	Resolve func(context.Context, []scan.Target) ([]scan.Target, error)
	Store   *Store
	Ready   func() bool
	Extra   func(*http.ServeMux)
}

func (a *API) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, c := context.WithTimeout(r.Context(), time.Second)
		defer c()
		if (a.Ready != nil && !a.Ready()) || a.Store.Pool.Ping(ctx) != nil {
			respond(w, 503, map[string]string{"error": "unavailable"})
			return
		}
		respond(w, 200, map[string]string{"status": "ready"})
	})
	m.HandleFunc("POST /v1/scans", a.create)
	m.HandleFunc("POST /v1/scans/{id}/preflight", a.preflight)
	m.HandleFunc("GET /v1/scans", a.list)
	m.HandleFunc("GET /v1/scans/{id}", a.get)
	m.HandleFunc("PUT /v1/scans/{id}", a.replace)
	m.HandleFunc("DELETE /v1/scans/{id}", a.remove)
	if a.Extra != nil {
		a.Extra(m)
	}
	return m
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return fmt.Errorf("invalid JSON body")
	}
	if e := d.Decode(new(any)); e != io.EOF {
		return fmt.Errorf("expected one JSON object")
	}
	return nil
}
func respond(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}
func problem(w http.ResponseWriter, e error) {
	code := 500
	message := "internal error"
	if errors.Is(e, ErrNotFound) {
		code = 404
		message = e.Error()
	}
	if errors.Is(e, ErrConflict) {
		code = 409
		message = e.Error()
	}
	respond(w, code, map[string]string{"error": message})
}
func bad(w http.ResponseWriter, e error) { respond(w, 400, map[string]string{"error": e.Error()}) }
func (a *API) create(w http.ResponseWriter, r *http.Request) {
	d := Defaults()
	if e := decode(w, r, &d); e != nil {
		bad(w, e)
		return
	}
	if _, _, e := d.Config(); e != nil {
		bad(w, e)
		return
	}
	now := time.Now().UTC()
	v := Scan{ID: newID(), Revision: 1, CreatedAt: now, UpdatedAt: now, Definition: d}
	if e := a.Store.SaveScan(r.Context(), v, 0, true); e != nil {
		problem(w, e)
		return
	}
	w.Header().Set("Location", "/v1/scans/"+v.ID)
	respond(w, 201, v)
}
func (a *API) get(w http.ResponseWriter, r *http.Request) {
	v, e := a.Store.GetScan(r.Context(), r.PathValue("id"))
	if e != nil {
		problem(w, e)
		return
	}
	respond(w, 200, v)
}
func (a *API) replace(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision   int64      `json:"revision"`
		Definition Definition `json:"definition"`
	}
	in.Definition = Defaults()
	if e := decode(w, r, &in); e != nil {
		bad(w, e)
		return
	}
	if in.Revision < 1 {
		bad(w, fmt.Errorf("revision must be positive"))
		return
	}
	if _, _, e := in.Definition.Config(); e != nil {
		bad(w, e)
		return
	}
	v := Scan{ID: r.PathValue("id"), Revision: in.Revision + 1, UpdatedAt: time.Now().UTC(), Definition: in.Definition}
	if e := a.Store.SaveScan(r.Context(), v, in.Revision, false); e != nil {
		problem(w, e)
		return
	}
	a.get(w, r)
}
func (a *API) remove(w http.ResponseWriter, r *http.Request) {
	if e := a.Store.DeleteScan(r.Context(), r.PathValue("id")); e != nil {
		problem(w, e)
		return
	}
	respond(w, 204, nil)
}
func (a *API) list(w http.ResponseWriter, r *http.Request) { a.listTable(w, r, "scans", "") }
func (a *API) listTable(w http.ResponseWriter, r *http.Request, table, id string) {
	limit := 100
	if s := r.URL.Query().Get("limit"); s != "" {
		n, e := strconv.Atoi(s)
		if e != nil || n < 1 || n > 1000 {
			bad(w, fmt.Errorf("limit must be 1..1000"))
			return
		}
		limit = n
	}
	v, e := a.Store.List(r.Context(), table, id, r.URL.Query().Get("after"), limit)
	if e != nil {
		problem(w, e)
		return
	}
	respond(w, 200, v)
}
