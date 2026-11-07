package tui

import (
	"net/netip"
	"testing"
	"time"

	"github.com/st3fan/masscanr/internal/controller"
	"github.com/st3fan/masscanr/internal/scan"
)

func TestAggregateProgressAndOutcomes(t *testing.T) {
	now := time.Unix(100, 0)
	cfg := scan.Defaults()
	cfg.Shards = 4
	s := newState(NewRunConfig(cfg, controller.DefaultTimeouts(), "inventory.list", [2]string{}))
	if total := s.totals(now); total.Percent != 0 || total.ETAKnown || total.VPSs != 0 || s.rows["1"].Known {
		t.Fatalf("pending: %+v", total)
	}
	for i, percent := range []float64{100, 70, 66, 60} {
		p := scan.Progress{Percent: percent, Rate: 470000, ETA: time.Duration(i+1) * time.Second, Found: 10}
		e := controller.Event{Shard: i + 1, ID: "worker", State: "scanning", Progress: &p}
		s.apply(e, now)
		s.apply(e, now) // Each progress event is cumulative, not incremental.
		p.Found = 999
	}
	s.apply(controller.Event{Shard: 1, State: "completed"}, now)
	s.apply(controller.Event{Shard: 1, State: "shutdown"}, now)
	total := s.totals(now)
	if total.Percent != 74 || total.Found != 40 || total.Complete != 1 || total.VPSs != 3 || total.Rate != 1410000 || !total.ETAKnown || total.ETA != 4*time.Second {
		t.Fatalf("running: %+v", total)
	}
	total = s.totals(now.Add(staleAfter))
	if total.Rate != 0 || total.ETAKnown || !total.Stale || total.Percent != 74 {
		t.Fatalf("stale: %+v", total)
	}
	s.apply(controller.Event{Shard: 4, State: "failed", Detail: "scan error"}, now)
	s.apply(controller.Event{Shard: 4, State: "cleanup failed", Detail: "delete error"}, now)
	s.apply(controller.Event{Shard: 4, State: "shutdown"}, now)
	r := s.rows["4"]
	if r.State != "failed" || r.Cleanup != "shutdown" || r.Detail != "scan error" || r.CleanupDetail != "" || s.totals(now).Percent != 74 {
		t.Fatalf("failure erased: %+v", r)
	}
	if s.totals(now).ETAKnown {
		t.Fatal("failed shard must leave ETA unknown")
	}
}

func TestWaitingAndReconciledWorkers(t *testing.T) {
	cfg := scan.Defaults()
	cfg.Shards = 1
	s := newState(NewRunConfig(cfg, controller.DefaultTimeouts(), "-", [2]string{}))
	now := time.Now()
	s.apply(controller.Event{Shard: 1, ID: "known", State: "waiting", Progress: &scan.Progress{Percent: 100, Rate: 1000, Waiting: true, ETA: 3 * time.Second}}, now)
	s.apply(controller.Event{ID: "reconciled", State: "deleting"}, now)
	total := s.totals(now)
	if total.Percent != 100 || total.Complete != 0 || total.VPSs != 2 || total.Rate != 0 || !total.ETAKnown {
		t.Fatalf("waiting: %+v", total)
	}
	clone := s.copy()
	s.apply(controller.Event{ID: "reconciled", State: "shutdown"}, now)
	if clone.totals(now).VPSs != 2 || s.totals(now).VPSs != 1 || s.sorted()[0].Shard != 1 {
		t.Fatal("snapshot or reconciliation changed configured shards")
	}
}

func TestConfigSnapshotOwnsSlices(t *testing.T) {
	cfg := scan.Defaults()
	cfg.Targets = []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}
	snapshot := NewRunConfig(cfg, controller.DefaultTimeouts(), "-", [2]string{"out.csv", "out.jsonl"})
	cfg.Targets[0] = netip.MustParsePrefix("198.51.100.0/24")
	cfg.Ports[0] = 65535
	if snapshot.Scan.Targets[0].String() != "192.0.2.0/24" || snapshot.Scan.Ports[0] == 65535 || snapshot.Output != "-" {
		t.Fatal("snapshot aliases caller's configuration")
	}
}
