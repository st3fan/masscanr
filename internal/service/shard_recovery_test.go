package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/st3fan/masscanr/internal/backend/registry"
	"github.com/st3fan/masscanr/internal/cloud"
	"github.com/st3fan/masscanr/internal/controller"
	"github.com/st3fan/masscanr/internal/scan"
	"github.com/st3fan/masscanr/internal/scanfile"
)

func TestInterruptedShardedRunResumesAfterOwnedCleanup(t *testing.T) {
	store := testStore(t)
	run := reserveFixture(t, store, "masscan")
	ctx := t.Context()
	dir := t.TempDir()
	path := filepath.Join(dir, run.ID+".scan")
	manifest, err := scan.Partition(run.Plan.Config.Targets, len(run.Plan.Config.Ports))
	if err != nil {
		t.Fatal(err)
	}
	_, epoch, err := store.InitShardRun(ctx, run.ID, manifest, run.Plan.Config.Workers)
	if err != nil {
		t.Fatal(err)
	}
	old := cloud.Worker{ID: "old-worker", IP: "192.0.2.10", Password: "secret", Label: cloud.Label, Tags: []string{"masscanr-run-" + run.ID}, Ready: true}
	if err = store.RecordShardWorker(ctx, run.ID, epoch, 1, old.ID, old.IP, "scanning"); err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.BeginShardAttempt(ctx, run.ID, epoch, 1, old.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Pool.Exec(ctx, `UPDATE masscanr.runs SET attempted=true WHERE id=$1`, run.ID); err != nil {
		t.Fatal(err)
	}
	if err = store.mutateRun(ctx, run.ID, func(r *Run) { r.Artifact = path; r.Phase = "scanning" }); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	config := run.Plan.Config
	header := scanfile.Header{Format: scanfile.Format, Schema: scanfile.ShardedSchema, RunID: run.ID, CreatedAt: time.Now().UTC(), ControllerVersion: "test", Label: "masscanr", OwnershipTag: "masscanr-run-" + run.ID, Config: scanfile.Config{RequestedTargets: config.RequestedTargets, Targets: config.Targets, Ports: config.PortSpec(), Backend: config.Backend, Seed: "7", RequestedWorkers: config.Workers, Workers: 1, ShardCount: manifest.Count, PartitionVersion: scan.PartitionVersion, MaxProbesPerUnit: scan.MaxProbesPerShard, Rate: config.Rate, Provider: "vultr", Region: config.Region, InstanceType: config.InstanceType, Output: path, Timeouts: map[string]string{"create": "45s", "ready": "5m", "prepare": "10m", "scan": "24h", "cleanup": "5m", "delete": "1m", "poll": "2s"}}}
	w, err := scanfile.NewWriter(scanfile.Destination{Writer: file, SyncFunc: file.Sync}, header, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range []scanfile.Record{
		{Type: "worker", Worker: &scanfile.Worker{Slot: 1, InstanceID: old.ID, IP: old.IP, Phase: "ready", Cleanup: "pending"}},
		{Type: "attempt", Attempt: &scanfile.Attempt{Shard: 1, Slot: 1, Number: 1, InstanceID: old.ID, Phase: "started"}},
	} {
		if err = w.Append(record, true); err != nil {
			t.Fatal(err)
		}
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	provider := &providerFixture{workers: map[string]cloud.Worker{old.ID: old}}
	runner := &runnerFixture{name: "masscan", started: make(chan struct{})}
	m := &Manager{Store: store, Dir: dir, cancel: map[string]context.CancelFunc{}}
	m.Factory = func(r Run) *controller.Controller {
		backend, _ := registry.Resolve(r.Plan.Config.Backend)
		return &controller.Controller{RunID: r.ID, Backend: backend, Provider: provider, Runner: runner, Timeouts: r.Plan.Timeouts, ShardStore: store}
	}
	run, err = store.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.prepareShardContinuation(ctx, run); err != nil {
		t.Fatal(err)
	}
	if _, exists := provider.workers[old.ID]; exists {
		t.Fatal("old owned worker remained after reconciliation")
	}
	if err = m.execute(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	recovered, err := store.GetRun(ctx, run.ID)
	if err != nil || recovered.Phase != "completed" || recovered.Summary.CompletedShards != 1 || provider.creates != 1 {
		t.Fatalf("run=%+v creates=%d err=%v", recovered, provider.creates, err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	state, err := scanfile.Replay(f, nil)
	if err != nil || !state.Complete() || len(state.ShardAttempts()) != 4 || state.ShardAttempts()[1].Phase != "failed" || state.ShardAttempts()[3].Phase != "completed" {
		t.Fatalf("journal=%+v err=%v", state.ShardAttempts(), err)
	}
}
