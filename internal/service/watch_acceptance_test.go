package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/st3fan/masscanr/internal/controller"
	"github.com/st3fan/masscanr/internal/scan"
	"github.com/st3fan/masscanr/internal/watch"
)

func TestWatchClientAgainstPersistentService(t *testing.T) {
	s := testStore(t)
	ctx := t.Context()
	manager := &Manager{Store: s, cancel: map[string]context.CancelFunc{}}
	api := &API{Store: s}
	api.Extra = func(mux *http.ServeMux) { manager.Routes(api, mux) }
	server := httptest.NewServer(api.Handler())
	defer server.Close()
	client, err := watch.NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	runs, err := client.Active(ctx)
	if err != nil || len(runs) != 0 {
		t.Fatal(runs, err)
	}
	a := reserveFixture(t, s, "zmap")
	if err := s.mutateRun(ctx, a.ID, func(r *Run) {
		applyEvent(r, controller.Event{Shard: 1, ID: "worker-a", IP: "192.0.2.1", State: "scanning", Progress: &scan.Progress{PercentUnknown: true, RateUnknown: true, ETAUnknown: true, Found: 4}}, time.Now().UTC())
		applyEvent(r, controller.Event{Shard: 1, State: "completed"}, time.Now().UTC())
		applyEvent(r, controller.Event{Shard: 1, State: "shutdown"}, time.Now().UTC())
	}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		runs, err = client.Active(ctx)
		if err != nil || len(runs) != 1 {
			t.Fatal(runs, err)
		}
		w := runs[0].WorkerSnapshots["1"]
		if w.ID != "worker-a" || w.State != "completed" || w.Cleanup != "shutdown" || w.Found == nil || *w.Found != 4 || !w.Progress.PercentUnknown {
			t.Fatal(w)
		}
		if !runs[0].Active || runs[0].Settled {
			t.Fatal("worker shutdown settled run prematurely")
		}
	}
	if err := s.mutateRun(ctx, a.ID, func(r *Run) {
		r.Phase = "completed"
		r.Summary.ScanOutcome = "complete"
		r.Summary.CleanupOutcome = "complete"
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE masscanr.runs SET active=false WHERE id=$1`, a.ID); err != nil {
		t.Fatal(err)
	}
	b, err := s.Reserve(ctx, a.ScanID, a.Plan.ID, "newer-run", false)
	if err != nil {
		t.Fatal(err)
	}
	// Use the actual HTTP watch client against PostgreSQL, not a mocked stop.
	old, err := client.Stop(ctx, a.ID)
	if err != nil || old.Active || !old.Settled {
		t.Fatal(old, err)
	}
	newer, err := client.Get(ctx, b.ID)
	if err != nil || newer.StopRequested || !newer.Active {
		t.Fatal(newer, err)
	}
	runs, err = client.Active(ctx)
	if err != nil || len(runs) != 1 || runs[0].ID != b.ID {
		t.Fatal(runs, err)
	}
}
