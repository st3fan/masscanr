package scanfile

import (
	"bytes"
	"net/netip"
	"testing"
	"time"

	"github.com/st3fan/masscanr/internal/scan"
)

func shardedHeader() Header {
	targets := []netip.Prefix{netip.MustParsePrefix("192.0.2.0/31")}
	return Header{
		Format: Format, Schema: ShardedSchema, RunID: "fixture", CreatedAt: time.Now().UTC(),
		ControllerVersion: "test", Label: "masscanr", OwnershipTag: "masscanr-run-fixture",
		Config: Config{
			RequestedTargets: scan.DirectTargets(targets), Targets: targets, Ports: "80", Backend: "masscan", Seed: "7",
			Workers: 1, RequestedWorkers: 1, ShardCount: 1, PartitionVersion: scan.PartitionVersion, MaxProbesPerUnit: scan.MaxProbesPerShard,
			Rate: 1000, Provider: "vultr", Region: "atl", InstanceType: "vc2-1c-1gb", Output: "fixture.scan",
			Timeouts: map[string]string{"create": "1s", "ready": "1s", "prepare": "1s", "scan": "1m", "cleanup": "1s", "delete": "1s", "poll": "1s"},
		},
	}
}

func TestShardedJournalRetryAndReplay(t *testing.T) {
	var out bytes.Buffer
	w, err := NewWriter(&out, shardedHeader(), nil)
	if err != nil {
		t.Fatal(err)
	}
	worker := Worker{Slot: 1, InstanceID: "instance-1", Phase: "ready", Cleanup: "pending"}
	if err = w.Append(Record{Type: "worker", Worker: &worker}, true); err != nil {
		t.Fatal(err)
	}
	first := Attempt{Shard: 1, Slot: 1, Number: 1, InstanceID: worker.InstanceID, Phase: "started"}
	if err = w.Append(Record{Type: "attempt", Attempt: &first}, true); err != nil {
		t.Fatal(err)
	}
	finding := Finding{Shard: 1, Attempt: 1, IP: netip.MustParseAddr("192.0.2.1"), Port: 80, Protocol: "tcp", State: "open", Timestamp: 7, Target: "192.0.2.0/31"}
	if err = w.Append(Record{Type: "finding", Finding: &finding}, false); err != nil {
		t.Fatal(err)
	}
	first.Phase = "failed"
	if err = w.Append(Record{Type: "attempt", Attempt: &first}, true); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	partial, err := Replay(bytes.NewReader(out.Bytes()), nil)
	if err != nil || partial.Complete() || partial.Found != 1 {
		t.Fatalf("partial=%+v err=%v", partial, err)
	}
	w, err = ResumeWriter(&out, partial, nil)
	if err != nil {
		t.Fatal(err)
	}
	second := Attempt{Shard: 1, Slot: 1, Number: 2, InstanceID: worker.InstanceID, Phase: "started"}
	if err = w.Append(Record{Type: "attempt", Attempt: &second}, true); err != nil {
		t.Fatal(err)
	}
	finding.Attempt = 2
	if err = w.Append(Record{Type: "finding", Finding: &finding}, false); err != nil {
		t.Fatal(err)
	}
	second.Phase = "completed"
	if err = w.Append(Record{Type: "attempt", Attempt: &second}, true); err != nil {
		t.Fatal(err)
	}
	worker.Cleanup = "complete"
	if err = w.Append(Record{Type: "worker", Worker: &worker}, true); err != nil {
		t.Fatal(err)
	}
	final := Final{ScanOutcome: "complete", CleanupOutcome: "complete", Found: 2, CompletedShards: 1, ShardCount: 1, Undeleted: []string{}}
	if err = w.Append(Record{Type: "finished", Finished: &final}, true); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	var observations int
	state, err := Replay(bytes.NewReader(out.Bytes()), func(Finding) error { observations++; return nil })
	if err != nil || !state.Complete() || state.Found != 2 || state.CompletedShards() != 1 || observations != 2 {
		t.Fatalf("state=%+v observations=%d err=%v", state, observations, err)
	}
}

func TestShardedJournalRejectsOutOfUnitFinding(t *testing.T) {
	var out bytes.Buffer
	w, err := NewWriter(&out, shardedHeader(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	worker := Worker{Slot: 1, InstanceID: "instance-1", Phase: "ready", Cleanup: "pending"}
	if err = w.Append(Record{Type: "worker", Worker: &worker}, true); err != nil {
		t.Fatal(err)
	}
	start := Attempt{Shard: 1, Slot: 1, Number: 1, InstanceID: worker.InstanceID, Phase: "started"}
	if err = w.Append(Record{Type: "attempt", Attempt: &start}, true); err != nil {
		t.Fatal(err)
	}
	finding := Finding{Shard: 1, Attempt: 1, IP: netip.MustParseAddr("192.0.2.2"), Port: 80, Protocol: "tcp", State: "open", Timestamp: 7, Target: "192.0.2.0/31"}
	if err = w.Append(Record{Type: "finding", Finding: &finding}, false); err == nil {
		t.Fatal("accepted finding outside frozen shard")
	}
}
