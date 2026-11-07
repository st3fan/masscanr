package tui

import (
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/st3fan/masscanr/internal/controller"
	"github.com/st3fan/masscanr/internal/scan"
)

// Follow the approved Design A example through the real callback/snapshot/render
// path, then verify that 100% and confirmed worker deletion remain distinct.
func TestV2RunWalkthrough(t *testing.T) {
	cfg := scan.Defaults()
	cfg.Shards = 4
	cfg.Targets, _ = scan.ParseTargets([]string{"192.0.2.42/24", "192.0.2.0/25", "198.51.100.0/24", "203.0.113.0/24"})
	cfg.Ports, _ = scan.ParsePorts("1-65535")
	v := New(io.Discard, false, cfg.Shards)
	v.Configure(NewRunConfig(cfg, controller.DefaultTimeouts(), "inventory.scan", [2]string{"inventory.csv", "inventory.jsonl"}), nil)
	v.Start()
	defer v.Stop()
	seed := uint64(123)
	v.Update(controller.Event{State: "validating", RunID: "inventory-001", Seed: &seed})
	v.Update(controller.Event{Shard: 1, ID: "worker-1", State: "booting"})
	if total := v.snapshot().totals(time.Now()); total.VPSs != 1 || total.Percent != 0 || total.ETAKnown {
		t.Fatal(total)
	}
	for i := 1; i <= 4; i++ {
		v.Update(controller.Event{Shard: i, ID: fmt.Sprintf("worker-%d", i), IP: fmt.Sprintf("203.0.113.%d", 10+i), State: "preparing"})
	}
	for i, percent := range []float64{100, 70, 66, 60} {
		found := []uint64{320, 341, 312, 311}[i]
		rate := []float64{0, 480000, 470000, 470000}[i]
		v.Update(controller.Event{Shard: i + 1, State: "scanning", Progress: &scan.Progress{Percent: percent, Rate: rate, Found: found, ETA: 12 * time.Second}})
	}
	for _, phase := range []string{"collecting", "completed", "deleting", "shutdown"} {
		v.Update(controller.Event{Shard: 1, State: phase})
	}
	m := newModel(v.snapshot(), nil, nil, nil)
	m.color, m.width = false, 130
	text := m.View().Content
	for _, want := range []string{"inventory-001", "74.0%", "Rate 1.42M pps", "Found 1284", "1/4 complete", "VPSs remaining 3", "completed / shutdown"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q:\n%s", want, text)
		}
	}
	m.tab = 1
	config := strings.Join(m.configLines(), "\n")
	for _, want := range []string{"3 CIDRs · 768 IPs", "192.0.2.0/24", "1-65535", "123", "inventory.jsonl"} {
		if !strings.Contains(config, want) {
			t.Fatalf("config missing %q", want)
		}
	}
	for i := 2; i <= 4; i++ {
		v.Update(controller.Event{Shard: i, State: "waiting", Progress: &scan.Progress{Percent: 100, Waiting: true}})
	}
	if total := v.snapshot().totals(time.Now()); total.Percent != 100 || total.Complete != 1 || total.VPSs != 3 {
		t.Fatal("100% prematurely finished run", total)
	}
	for i := 2; i <= 4; i++ {
		v.Update(controller.Event{Shard: i, State: "completed"})
		v.Update(controller.Event{Shard: i, State: "deleting"})
		if i == 2 {
			v.Update(controller.Event{Shard: i, State: "cleanup failed", Detail: "retry required"})
		} else {
			v.Update(controller.Event{Shard: i, State: "shutdown"})
		}
	}
	v.Update(controller.Event{State: "cleanup"})
	if total := v.snapshot().totals(time.Now()); total.Complete != 4 || total.VPSs != 1 {
		t.Fatal("cleanup failure hidden", total)
	}
	for _, phase := range []string{"deleting", "shutdown"} {
		v.Update(controller.Event{Shard: 2, State: phase})
	}
	v.Update(controller.Event{State: "exporting"})
	final := v.snapshot()
	if final.Phase != "Exporting" || final.totals(time.Now()).VPSs != 0 || final.rows["2"].CleanupDetail != "" {
		t.Fatal(final)
	}
}
