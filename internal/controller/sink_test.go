package controller

import (
	"bytes"
	"errors"
	"io"
	"net/netip"
	"strings"
	"testing"

	"github.com/st3fan/masscanr/internal/scan"
)

func TestFindingSink(t *testing.T) {
	cfg := testConfig()
	valid := scan.Finding{Address: netip.MustParseAddr("192.0.2.1"), Port: 80, Protocol: "tcp", State: "open", Timestamp: 1700000000}
	var out bytes.Buffer
	r := &results{cfg: cfg, out: &out}
	for i := 0; i < 2; i++ {
		if err := r.accept(1, valid); err != nil {
			t.Fatal(err)
		}
	}
	if r.count != 2 || strings.Count(out.String(), "open tcp 80 192.0.2.1 1700000000") != 2 {
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
	r.out = shortSink{}
	if err := r.accept(1, valid); !errors.Is(err, io.ErrShortWrite) {
		t.Fatal(err)
	}
	if r.count != 2 {
		t.Fatal("failed write counted")
	}
}

type shortSink struct{}

func (shortSink) Write(p []byte) (int, error) { return len(p) - 1, nil }
