package controller

import (
	"bytes"
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/st3fan/masscanr/internal/scan"
	"github.com/st3fan/masscanr/internal/scanfile"
)

type resultBatchSpy struct {
	synced *bool
	err    error
	writes int
}

func (spy *resultBatchSpy) WriteBatch(_ context.Context, seq uint64, findings []scanfile.Finding) error {
	if !*spy.synced || seq == 0 {
		return errors.New("database write before journal sync")
	}
	spy.writes += len(findings)
	return spy.err
}

func TestResultBatchFollowsJournalSyncAndRetriesFailure(t *testing.T) {
	cfg := testConfig()
	var out bytes.Buffer
	synced := false
	dest := scanfile.Destination{Writer: &out, Output: "fixture.scan", SyncFunc: func() error { synced = true; return nil }}
	journal, redactor, err := startJournal(dest, cfg, DefaultTimeouts(), "fixture", 7, func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	notify := journalEvents(journal, redactor, nil)
	for shard := 1; shard <= cfg.Shards; shard++ {
		for _, phase := range []string{"provisioning", "booting", "preparing", "ready"} {
			event := Event{Shard: shard, State: phase}
			if phase != "provisioning" {
				event.ID = "worker" + string(rune('a'+shard))
			}
			if phase == "ready" {
				event.Prepared = &scan.Prepared{Identity: scan.Identity{Name: "masscan", Version: "fixture", Build: "fixture"}}
			}
			notify(event)
		}
	}
	notify(Event{Shard: 1, State: "scanning"})
	spy := &resultBatchSpy{synced: &synced, err: errors.New("database unavailable")}
	results := newResults(journal, cfg, spy)
	finding := scan.Finding{Address: netip.MustParseAddr("192.0.2.1"), Port: 80, Protocol: "tcp", State: "open", Timestamp: 1}
	synced = false
	if err := results.accept(1, finding); err != nil {
		t.Fatal(err)
	}
	if err := results.flush(context.Background()); err == nil || spy.writes != 1 || !synced || results.count != 1 {
		t.Fatal(err, spy.writes, synced, results.count)
	}
	spy.err = nil
	if err := results.flush(context.Background()); err != nil || spy.writes != 2 || !synced {
		t.Fatal(err, spy.writes, synced)
	}
	state, err := scanfile.Replay(bytes.NewReader(out.Bytes()), nil)
	if err != nil || state.Found != 1 {
		t.Fatal(state.Found, err)
	}
}
