package service

import (
	"bytes"
	"context"
	"encoding/json"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/st3fan/masscanr/internal/scan"
	"github.com/st3fan/masscanr/internal/scanfile"
)

func shardStoreRun(t *testing.T, store *Store) (string, scan.Manifest, []netip.Prefix) {
	t.Helper()
	ctx := t.Context()
	target, err := store.CreateTarget(ctx, "Scope", []string{"192.0.2.0/24"})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := store.CreateScanPlan(ctx, "Plan", target.ID, DefaultScanPlanSettings())
	if err != nil {
		t.Fatal(err)
	}
	run := Run{ID: newID(), TargetID: target.ID, ScanPlanID: plan.ID, Active: true, Phase: "scanning"}
	data, _ := json.Marshal(run)
	if _, err = store.Pool.Exec(ctx, `INSERT INTO masscanr.runs(id,target_id,scan_plan_id,request_key,data) VALUES($1,$2,$3,$4,$5)`, run.ID, target.ID, plan.ID, newID(), data); err != nil {
		t.Fatal(err)
	}
	targets := []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}
	manifest, err := scan.Partition(targets, 1)
	if err != nil {
		t.Fatal(err)
	}
	return run.ID, manifest, targets
}

func TestRecoveryPreservesIncompleteJournalTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.scan")
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	header := scanfile.Header{Format: scanfile.Format, Schema: scanfile.ShardedSchema, RunID: "tail", CreatedAt: time.Now().UTC(), ControllerVersion: "test", Label: "masscanr", OwnershipTag: "masscanr-run-tail", Config: scanfile.Config{RequestedTargets: scan.DirectTargets([]netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}), Targets: []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}, Ports: "80", Backend: "masscan", Seed: "7", RequestedWorkers: 1, Workers: 1, ShardCount: 1, PartitionVersion: scan.PartitionVersion, MaxProbesPerUnit: scan.MaxProbesPerShard, Rate: 1000, Provider: "vultr", Region: "atl", InstanceType: "vc2-1c-1gb", Output: "tail.scan", Timeouts: map[string]string{"create": "1s", "ready": "1s", "prepare": "1s", "scan": "1s", "cleanup": "1s", "delete": "1s", "poll": "1s"}}}
	w, err := scanfile.NewWriter(scanfile.Destination{Writer: file, SyncFunc: file.Sync}, header, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = file.Write([]byte(`{"type":"finding","seq":`)); err != nil {
		t.Fatal(err)
	}
	if _, err = file.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	state, err := scanfile.Replay(file, nil)
	if err != nil || !state.Truncated || state.Seq != 1 {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	if err = preserveAndTrimJournalTail(file, path); err != nil {
		t.Fatal(err)
	}
	if _, err = file.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	state, err = scanfile.Replay(file, nil)
	if err != nil || state.Truncated || state.Seq != 1 {
		t.Fatalf("repaired state=%+v err=%v", state, err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	matches, err := filepath.Glob(path + ".recovery-tail-*")
	if err != nil || len(matches) != 1 {
		t.Fatalf("preserved tails=%v err=%v", matches, err)
	}
	b, err := os.ReadFile(matches[0])
	if err != nil || string(b) != `{"type":"finding","seq":` {
		t.Fatalf("tail=%q err=%v", b, err)
	}
}

func TestShardStoreDurableRetryAndJournalRestore(t *testing.T) {
	store := testStore(t)
	ctx := t.Context()
	id, manifest, targets := shardStoreRun(t, store)
	workers, epoch, err := store.InitShardRun(ctx, id, manifest, 4)
	if err != nil || workers != 1 || epoch != 1 {
		t.Fatalf("workers=%d epoch=%d err=%v", workers, epoch, err)
	}
	if err = store.RecordShardWorker(ctx, id, epoch, 1, "instance-1", "0.0.0.0", "booting"); err != nil {
		t.Fatal(err)
	}
	physical, err := store.ListShardWorkers(ctx, id, "", 100)
	if err != nil || len(physical) != 1 || physical[0].IP != "" {
		t.Fatalf("provisioning worker has a reported IP: %+v err=%v", physical, err)
	}
	if err = store.UpdateShardWorker(ctx, id, epoch, "instance-1", "192.0.2.1", "ready", "pending", ""); err != nil {
		t.Fatal(err)
	}
	if err = store.UpdateShardWorker(ctx, id, epoch, "instance-1", "0.0.0.0", "scanning", "pending", ""); err != nil {
		t.Fatal(err)
	}
	first, done, err := store.BeginShardAttempt(ctx, id, epoch, 1, "instance-1")
	if err != nil || done || first.Index != 1 || first.Attempt != 1 {
		t.Fatalf("first=%+v done=%t err=%v", first, done, err)
	}
	if err = store.FinishShardAttempt(ctx, id, epoch, first, false, 4, "worker lost"); err != nil {
		t.Fatal(err)
	}
	if err = store.UpdateShardWorker(ctx, id, epoch, "instance-1", "0.0.0.0", "shutdown", "complete", ""); err != nil {
		t.Fatal(err)
	}
	if err = store.RecordShardWorker(ctx, id, epoch, 1, "instance-2", "192.0.2.2", "ready"); err != nil {
		t.Fatal(err)
	}
	second, done, err := store.BeginShardAttempt(ctx, id, epoch, 1, "instance-2")
	if err != nil || done || second.Index != 1 || second.Attempt != 2 {
		t.Fatalf("second=%+v done=%t err=%v", second, done, err)
	}
	if err = store.FinishShardAttempt(ctx, id, epoch, second, true, 8, ""); err != nil {
		t.Fatal(err)
	}
	physical, err = store.ListShardWorkers(ctx, id, "", 100)
	if err != nil || len(physical) != 2 || physical[0].Slot != 1 || physical[0].IP != "192.0.2.1" || physical[1].Slot != 1 || physical[1].IP != "192.0.2.2" {
		t.Fatalf("physical workers=%+v err=%v", physical, err)
	}
	// Existing runs can contain the provider's provisioning placeholder.
	if _, err = store.Pool.Exec(ctx, `UPDATE masscanr.run_workers SET ip='0.0.0.0' WHERE run_id=$1 AND instance_id=$2`, id, "instance-2"); err != nil {
		t.Fatal(err)
	}
	physical, err = store.ListShardWorkers(ctx, id, "", 100)
	if err != nil || len(physical) != 2 || physical[1].IP != "" {
		t.Fatalf("legacy placeholder exposed: %+v err=%v", physical, err)
	}
	slots, err := store.WorkerSlots(ctx, id)
	if err != nil || len(slots) != 1 || slots[0].CompletedShards != 1 || slots[0].NextShard != 2 {
		t.Fatalf("slots=%+v err=%v", slots, err)
	}
	if _, _, err = store.BeginShardAttempt(ctx, id, epoch, 1, "instance-1"); err == nil {
		t.Fatal("old physical worker acquired slot")
	}

	// Simulate recovery from the journal as the source of completed work.
	header := scanfile.Header{Format: scanfile.Format, Schema: scanfile.ShardedSchema, RunID: id, CreatedAt: time.Now().UTC(), ControllerVersion: "test", Label: "masscanr", OwnershipTag: "masscanr-run-" + id, Config: scanfile.Config{RequestedTargets: scan.DirectTargets(targets), Targets: targets, Ports: "80", Backend: "masscan", Seed: "7", RequestedWorkers: 4, Workers: 1, ShardCount: 1, PartitionVersion: scan.PartitionVersion, MaxProbesPerUnit: scan.MaxProbesPerShard, Rate: 1000, Provider: "vultr", Region: "atl", InstanceType: "vc2-1c-1gb", Output: "fixture.scan", Timeouts: map[string]string{"create": "1s", "ready": "1s", "prepare": "1s", "scan": "1s", "cleanup": "1s", "delete": "1s", "poll": "1s"}}}
	var out bytes.Buffer
	w, err := scanfile.NewWriter(&out, header, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range []scanfile.Record{
		{Type: "worker", Worker: &scanfile.Worker{Slot: 1, InstanceID: "instance-1", Phase: "ready", Cleanup: "pending"}},
		{Type: "attempt", Attempt: &scanfile.Attempt{Shard: 1, Slot: 1, Number: 1, InstanceID: "instance-1", Phase: "started"}},
		{Type: "attempt", Attempt: &scanfile.Attempt{Shard: 1, Slot: 1, Number: 1, InstanceID: "instance-1", Phase: "failed"}},
		{Type: "worker", Worker: &scanfile.Worker{Slot: 1, InstanceID: "instance-1", Phase: "shutdown", Cleanup: "complete"}},
		{Type: "worker", Worker: &scanfile.Worker{Slot: 1, InstanceID: "instance-2", Phase: "ready", Cleanup: "pending"}},
		{Type: "attempt", Attempt: &scanfile.Attempt{Shard: 1, Slot: 1, Number: 2, InstanceID: "instance-2", Phase: "started"}},
		{Type: "attempt", Attempt: &scanfile.Attempt{Shard: 1, Slot: 1, Number: 2, InstanceID: "instance-2", Phase: "completed"}},
		{Type: "worker", Worker: &scanfile.Worker{Slot: 1, InstanceID: "instance-2", Phase: "shutdown", Cleanup: "complete"}},
	} {
		if err = w.Append(record, true); err != nil {
			t.Fatal(err)
		}
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	state, err := scanfile.Replay(bytes.NewReader(out.Bytes()), nil)
	if err != nil {
		t.Fatal(err)
	}
	newEpoch, err := store.RestoreShardRun(ctx, id, state)
	if err != nil || newEpoch != 2 {
		t.Fatalf("epoch=%d err=%v", newEpoch, err)
	}
	if err = store.RecordShardWorker(ctx, id, epoch, 1, "stale", "", "ready"); err == nil {
		t.Fatal("old controller epoch was not fenced")
	}
	slots, err = store.WorkerSlots(ctx, id)
	if err != nil || len(slots) != 1 || slots[0].CompletedShards != 1 || slots[0].NextShard != 2 {
		t.Fatalf("restored slots=%+v err=%v", slots, err)
	}
	_, done, err = store.BeginShardAttempt(context.Background(), id, newEpoch, 1, "instance-2")
	if err != nil || !done {
		t.Fatalf("completed queue retried: done=%t err=%v", done, err)
	}
}
