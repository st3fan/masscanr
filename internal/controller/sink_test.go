package controller

import (
	"bytes"
	"errors"
	"io"
	"net/netip"
	"strings"
	"testing"

	"github.com/st3fan/masscanr/internal/scan"
	"github.com/st3fan/masscanr/internal/scanfile"
)

func TestFindingSink(t *testing.T) {
	cfg := testConfig()
	valid := scan.Finding{Address: netip.MustParseAddr("192.0.2.1"), Port: 80, Protocol: "tcp", State: "open", Timestamp: 1700000000}
	var out bytes.Buffer
	dest := &switchSink{Writer: &out}
	journal, _, err := startJournal(dest, cfg, DefaultTimeouts(), "fixture", 7, func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	notify := journalEvents(journal, &redactor{}, nil)
	for i := 1; i <= cfg.Shards; i++ {
		for _, phase := range []string{"provisioning", "booting", "preparing", "ready"} {
			e := Event{Shard: i, State: phase}
			if phase != "provisioning" {
				e.ID = string(rune('a' + i))
			}
			if phase == "ready" {
				e.Prepared = &scan.Prepared{Identity: scan.Identity{Name: "masscan", Version: "1.3.2", Build: "fixture"}}
			}
			notify(e)
		}
	}
	notify(Event{Shard: 1, State: "scanning"})
	r := &results{cfg: cfg, journal: journal}
	for i := 0; i < 2; i++ {
		if err := r.accept(1, valid); err != nil {
			t.Fatal(err)
		}
	}
	if err := journal.Worker(1, "", "", "waiting", "", ""); err != nil {
		t.Fatal(err)
	}
	state, err := scanfile.Replay(strings.NewReader(out.String()), nil)
	if err != nil || r.count != 2 || state.Found != 2 {
		t.Fatal(out.String(), r.count)
	}
	for _, bad := range []scan.Finding{
		{Address: netip.MustParseAddr("203.0.113.1"), Port: 80, Protocol: "tcp", State: "open"},
		{Address: valid.Address, Port: 65535, Protocol: "tcp", State: "open"},
		{Address: valid.Address, Port: 80, Protocol: "udp", State: "open"},
		{Address: valid.Address, Port: 80, Protocol: "tcp", State: "closed"},
		{Port: 80, Protocol: "tcp", State: "open"},
	} {
		if err := r.accept(1, bad); err == nil {
			t.Fatal("accepted", bad)
		}
	}
	if r.count != 2 {
		t.Fatal("invalid findings counted", r.count)
	}
	dest.Writer = shortSink{}
	_ = journal.Worker(1, "", "", "scanning", "", "")
	if err := r.accept(1, valid); !errors.Is(err, io.ErrShortWrite) {
		t.Fatal(err)
	}
	if r.count != 2 {
		t.Fatal("failed write counted")
	}
}

type shortSink struct{}

func (shortSink) Write(p []byte) (int, error) { return len(p) - 1, nil }

type switchSink struct{ io.Writer }
