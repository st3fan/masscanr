package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/st3fan/masscanr/internal/controller"
	"github.com/st3fan/masscanr/internal/scan"
)

func TestCumulativeWorkerSnapshot(t *testing.T) {
	r := Run{}
	now := time.Now().UTC()
	applyEvent(&r, controller.Event{Shard: 1, ID: "worker", IP: "192.0.2.1", State: "scanning"}, now)
	p := scan.Progress{Percent: 42, RateUnknown: true, ETAUnknown: true, Found: 7}
	applyEvent(&r, controller.Event{Shard: 1, State: "scanning", Progress: &p}, now)
	found := uint64(9)
	applyEvent(&r, controller.Event{Shard: 1, State: "finding", Found: &found}, now.Add(time.Second))
	applyEvent(&r, controller.Event{Shard: 1, State: "completed"}, now.Add(time.Second))
	applyEvent(&r, controller.Event{Shard: 0, ID: "worker", State: "shutdown"}, now.Add(2*time.Second))
	w := r.WorkerSnapshots["1"]
	if len(r.WorkerSnapshots) != 1 || w.ID != "worker" || w.IP != "192.0.2.1" || w.State != "completed" || w.Cleanup != "shutdown" || *w.Found != 9 || !w.ProgressAt.Equal(now) || !w.Progress.RateUnknown {
		t.Fatalf("lost cumulative state: %+v", w)
	}
	p.Percent = 99
	found = 100
	if w.Progress.Percent != 42 || *w.Found != 9 {
		t.Fatal("snapshot aliases event memory")
	}
	applyEvent(&r, controller.Event{Shard: 2, State: "booting"}, now)
	if r.WorkerSnapshots["2"].Progress != nil || r.WorkerSnapshots["2"].Found != nil {
		t.Fatal("unknown became zero")
	}
}

func TestActiveDiscoveryAndHistoricalRunStop(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := reserveFixture(t, s, "masscan")
	m := &Manager{Store: s, cancel: map[string]context.CancelFunc{}}
	api := &API{Store: s}
	api.Extra = func(mux *fiber.App) { m.Routes(api, mux) }
	h := api.Handler()
	list := func() []Run {
		t.Helper()
		w := request(h, "GET", "/v1/runs?active=true", "")
		var out struct {
			Meta struct {
				SnapshotVersion int `json:"snapshot_version"`
			} `json:"meta"`
		}
		var runs []Run
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Meta.SnapshotVersion != 2 || decodeTestList(w.Body.Bytes(), &runs) != nil {
			t.Fatal(w.Code, w.Body)
		}
		return runs
	}
	if got := list(); len(got) != 1 || got[0].ID != a.ID || !got[0].Active || got[0].Settled {
		t.Fatal(got)
	}
	// A scanner may be finished while cleanup still owns the active slot.
	if err := s.mutateRun(ctx, a.ID, func(r *Run) { r.Phase = "completed"; r.Summary.ScanOutcome = "complete" }); err != nil {
		t.Fatal(err)
	}
	if len(list()) != 1 {
		t.Fatal("hid active cleanup")
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE masscanr.runs SET active=false WHERE id=$1`, a.ID); err != nil {
		t.Fatal(err)
	}
	if len(list()) != 0 {
		t.Fatal("settled run remains active")
	}
	b, err := s.ReserveScanPlan(ctx, a.ScanPlanID, a.Plan.ID, "second-run", false)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	m.cancel[b.ID] = func() { called = true }
	// A stale viewer confirms stopping A after B has started for the same scan.
	for range 2 {
		w := request(h, "POST", "/v1/runs/"+a.ID+"/stop", "")
		var got Run
		if w.Code != 202 || decodeTestResource(w.Body.Bytes(), &got) != nil || !got.Settled || got.Active || got.StopRequested {
			t.Fatal(w.Code, w.Body)
		}
	}
	got, err := s.GetRun(ctx, b.ID)
	if err != nil || got.StopRequested || called {
		t.Fatal("historical stop affected newer run", got, err)
	}
	w := request(h, "POST", "/v1/runs/"+b.ID+"/stop", "")
	if w.Code != 202 || !called {
		t.Fatal(w.Code, called)
	}
	got, _ = s.GetRun(ctx, b.ID)
	if !got.Active || !got.StopRequested {
		t.Fatal(got)
	}
	if w := request(h, "POST", "/v1/runs/missing/stop", ""); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if w := request(h, "GET", "/v1/runs", ""); w.Code != 400 {
		t.Fatal(w.Code)
	}
	history := request(h, "GET", "/v1/scan-plans/"+a.ScanPlanID+"/runs", "")
	var runs []Run
	if decodeTestList(history.Body.Bytes(), &runs) != nil || len(runs) != 2 {
		t.Fatal(history.Body)
	}
	for _, r := range runs {
		if r.SnapshotVersion != 2 || r.Active == r.Settled {
			t.Fatal(r)
		}
	}
}

func TestCompletedShardDoesNotHideScanningPeer(t *testing.T) {
	r := Run{Phase: "accepted"}
	now := time.Now()
	applyEvent(&r, controller.Event{Shard: 1, State: "scanning"}, now)
	applyEvent(&r, controller.Event{Shard: 2, State: "completed"}, now)
	applyEvent(&r, controller.Event{Shard: 2, State: "shutdown"}, now)
	if r.Phase != "scanning" {
		t.Fatal(r.Phase)
	}
	applyEvent(&r, controller.Event{State: "cleanup"}, now)
	applyEvent(&r, controller.Event{Shard: 1, State: "shutdown"}, now)
	if r.Phase != "cleanup" {
		t.Fatal(r.Phase)
	}
}
