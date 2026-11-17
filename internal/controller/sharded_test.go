package controller

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/st3fan/masscanr/internal/cloud"
	"github.com/st3fan/masscanr/internal/scan"
	"github.com/st3fan/masscanr/internal/scanfile"
)

type shardMemory struct {
	mu       sync.Mutex
	count    uint64
	workers  int
	current  map[int]string
	next     map[int]uint64
	attempts map[uint64]int
	finished uint64
}

func (m *shardMemory) InitShardRun(_ context.Context, _ string, manifest scan.Manifest, requested int) (int, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.count, m.workers = manifest.Count, min(requested, int(manifest.Count))
	m.current, m.next, m.attempts = map[int]string{}, map[int]uint64{}, map[uint64]int{}
	for slot := 1; slot <= m.workers; slot++ {
		m.next[slot] = uint64(slot)
	}
	return m.workers, 1, nil
}
func (m *shardMemory) RecordShardWorker(_ context.Context, _ string, _ int64, slot int, id, _, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.current[slot] = id
	return nil
}
func (*shardMemory) UpdateShardWorker(context.Context, string, int64, string, string, string, string) error {
	return nil
}
func (m *shardMemory) BeginShardAttempt(_ context.Context, _ string, _ int64, slot int, id string) (ShardLease, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current[slot] != id {
		return ShardLease{}, false, errors.New("wrong worker")
	}
	index := m.next[slot]
	if index > m.count {
		return ShardLease{}, true, nil
	}
	m.attempts[index]++
	return ShardLease{Index: index, Slot: slot, Attempt: m.attempts[index], InstanceID: id}, false, nil
}
func (m *shardMemory) FinishShardAttempt(_ context.Context, _ string, _ int64, lease ShardLease, success bool, _ uint64, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if success {
		m.next[lease.Slot] += uint64(m.workers)
		m.finished++
	}
	return nil
}
func (*shardMemory) UpdateShardProgress(context.Context, string, int64, ShardLease, *float64) error {
	return nil
}

func TestShardedControllerCapsWorkersAtLogicalShardCount(t *testing.T) {
	p := newProvider()
	store := &shardMemory{}
	c := testController(p, fakeRunner{scan: func(context.Context, cloud.Worker, []string, io.Writer, func(scan.Progress)) error { return nil }})
	c.RunID, c.ShardStore = "fixture-shards", store
	cfg := testConfig()
	cfg.Workers = 4
	var out bytes.Buffer
	summary, err := c.RunSharded(t.Context(), cfg, &out, nil, scanfile.State{})
	if err != nil {
		t.Fatal(err)
	}
	state, err := scanfile.Replay(bytes.NewReader(out.Bytes()), nil)
	if err != nil || !state.Complete() || state.CompletedShards() != 1 || state.Header.Config.Workers != 1 || state.Header.Config.RequestedWorkers != 4 || summary.ShardCount != 1 || summary.CompletedShards != 1 || p.created != 1 || p.deleted != 1 {
		t.Fatalf("summary=%+v state=%+v created=%d deleted=%d err=%v", summary, state, p.created, p.deleted, err)
	}
}

func TestShardedControllerRetriesWholeShardOnReplacement(t *testing.T) {
	p := newProvider()
	store := &shardMemory{}
	var calls int
	c := testController(p, fakeRunner{scan: func(context.Context, cloud.Worker, []string, io.Writer, func(scan.Progress)) error {
		calls++
		if calls == 1 {
			return errors.New("worker failed")
		}
		return nil
	}})
	c.RunID, c.ShardStore = "fixture-retry", store
	cfg := testConfig()
	cfg.Workers = 1
	var out bytes.Buffer
	summary, err := c.RunSharded(t.Context(), cfg, &out, nil, scanfile.State{})
	if err != nil {
		t.Fatal(err)
	}
	state, err := scanfile.Replay(bytes.NewReader(out.Bytes()), nil)
	if err != nil || !state.Complete() || summary.CompletedShards != 1 || p.created != 2 || p.deleted != 2 || calls != 2 {
		t.Fatalf("summary=%+v state=%+v created=%d deleted=%d calls=%d err=%v", summary, state, p.created, p.deleted, calls, err)
	}
	attempts := state.ShardAttempts()
	if len(attempts) != 4 || attempts[0].Phase != "started" || attempts[1].Phase != "failed" || attempts[2].Number != 2 || attempts[3].Phase != "completed" {
		t.Fatalf("attempt sequence: %+v", attempts)
	}
}

func TestShardedControllerDelegatesMultipleUnitsToOneWorker(t *testing.T) {
	p := newProvider()
	store := &shardMemory{}
	c := testController(p, fakeRunner{scan: func(context.Context, cloud.Worker, []string, io.Writer, func(scan.Progress)) error { return nil }})
	c.RunID, c.ShardStore = "fixture-queue", store
	cfg := testConfig()
	cfg.Targets, _ = scan.ParseTargets([]string{"192.0.2.0/27"})
	cfg.Ports, _ = scan.ParsePorts("1-65535")
	cfg.Workers = 1
	var out bytes.Buffer
	summary, err := c.RunSharded(t.Context(), cfg, &out, nil, scanfile.State{})
	if err != nil {
		t.Fatal(err)
	}
	state, err := scanfile.Replay(bytes.NewReader(out.Bytes()), nil)
	if err != nil || !state.Complete() || summary.ShardCount != 2 || summary.CompletedShards != 2 || p.created != 1 || p.deleted != 1 || len(state.ShardAttempts()) != 4 {
		t.Fatalf("summary=%+v created=%d deleted=%d attempts=%+v err=%v", summary, p.created, p.deleted, state.ShardAttempts(), err)
	}
}

func TestShardedControllerStopsAfterTwoRetries(t *testing.T) {
	p := newProvider()
	store := &shardMemory{}
	c := testController(p, fakeRunner{scanFail: true})
	c.RunID, c.ShardStore = "fixture-exhausted", store
	cfg := testConfig()
	cfg.Workers = 1
	var out bytes.Buffer
	summary, err := c.RunSharded(t.Context(), cfg, &out, nil, scanfile.State{})
	if err == nil {
		t.Fatal("accepted failed shard")
	}
	state, replayErr := scanfile.Replay(bytes.NewReader(out.Bytes()), nil)
	if replayErr != nil || state.Finished == nil || state.Finished.ScanOutcome != "failed" || summary.CompletedShards != 0 || p.created != 3 || p.deleted != 3 || len(state.ShardAttempts()) != 6 {
		t.Fatalf("summary=%+v created=%d deleted=%d attempts=%+v replay=%v", summary, p.created, p.deleted, state.ShardAttempts(), replayErr)
	}
}

func TestShardedRecoveryRejectsProviderChange(t *testing.T) {
	provider := newProvider()
	controller := testController(provider, fakeRunner{scan: func(context.Context, cloud.Worker, []string, io.Writer, func(scan.Progress)) error { return nil }})
	controller.RunID, controller.ShardStore = "fixture-provider", &shardMemory{}
	cfg := testConfig()
	var journal bytes.Buffer
	if _, err := controller.RunSharded(t.Context(), cfg, &journal, nil, scanfile.State{}); err != nil {
		t.Fatal(err)
	}
	state, err := scanfile.Replay(bytes.NewReader(journal.Bytes()), nil)
	if err != nil {
		t.Fatal(err)
	}
	state.Finished = nil // Model the unfinished journal after a service restart.
	cfg.Provider = "digitalocean"
	var output bytes.Buffer
	if _, err := controller.RunSharded(t.Context(), cfg, &output, nil, state); err == nil || !strings.Contains(err.Error(), "differs from frozen preflight") {
		t.Fatalf("accepted provider change on recovery: %v", err)
	}
}
