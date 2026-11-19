package scanfile

import (
	"bytes"
	"net/netip"
	"testing"

	"github.com/st3fan/masscanr/internal/scan"
)

func TestShardedJournalReplaysFrozenExclusionScope(t *testing.T) {
	h := shardedHeader()
	h.Config.RequestedTargets = scan.DirectTargets([]netip.Prefix{netip.MustParsePrefix("192.0.2.0/29")})
	h.Config.Targets = []netip.Prefix{netip.MustParsePrefix("192.0.2.4/30")}
	h.Config.Exclusions = []scan.ExclusionSnapshot{{ID: "policy-a", Revision: 3, Entries: []scan.ExclusionSelector{{Input: "192.0.2.0/30", Prefix: netip.MustParsePrefix("192.0.2.0/30")}}}}
	var out bytes.Buffer
	w, err := NewWriter(&out, h, nil)
	if err != nil {
		t.Fatal(err)
	}
	worker := Worker{Slot: 1, InstanceID: "instance-1", Phase: "ready", Cleanup: "pending"}
	if err := w.Append(Record{Type: "worker", Worker: &worker}, true); err != nil {
		t.Fatal(err)
	}
	start := Attempt{Shard: 1, Slot: 1, Number: 1, InstanceID: worker.InstanceID, Phase: "started"}
	if err := w.Append(Record{Type: "attempt", Attempt: &start}, true); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	state, err := Replay(bytes.NewReader(out.Bytes()), nil)
	if err != nil || state.Complete() || state.Header == nil || state.Header.Config.Exclusions[0].Revision != 3 {
		t.Fatalf("partial replay = %+v, %v", state, err)
	}
	h.Config.Exclusions[0].Entries[0].Prefix = netip.MustParsePrefix("192.0.2.0/31")
	if _, err := h.Config.ValidateSharded(); err == nil {
		t.Fatal("accepted mutated exclusion snapshot")
	}
	h.Config.Exclusions = nil
	if _, err := h.Config.ValidateSharded(); err == nil {
		t.Fatal("accepted missing exclusion snapshot")
	}
	if _, err := Replay(bytes.NewReader(out.Bytes()), nil); err != nil {
		t.Fatalf("frozen journal changed after resource edit: %v", err)
	}
	var invalid bytes.Buffer
	h.Config.Exclusions = []scan.ExclusionSnapshot{{ID: "policy-a", Revision: 3, Entries: []scan.ExclusionSelector{{Input: "192.0.2.0/30", Prefix: netip.MustParsePrefix("192.0.2.0/30")}}}}
	w, err = NewWriter(&invalid, h, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Append(Record{Type: "worker", Worker: &worker}, true); err != nil {
		t.Fatal(err)
	}
	if err := w.Append(Record{Type: "attempt", Attempt: &start}, true); err != nil {
		t.Fatal(err)
	}
	excludedFinding := Finding{Shard: 1, Attempt: 1, IP: netip.MustParseAddr("192.0.2.1"), Port: 80, Protocol: "tcp", State: "open", Timestamp: 7, Target: "192.0.2.0/29"}
	if err := w.Append(Record{Type: "finding", Finding: &excludedFinding}, false); err == nil {
		t.Fatal("accepted excluded finding")
	}
}
