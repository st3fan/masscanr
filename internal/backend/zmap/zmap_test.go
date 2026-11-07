package zmap

import (
	"errors"
	"math"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/st3fan/masscanr/internal/scan"
)

func testSpec() scan.Spec {
	cfg := scan.Defaults()
	cfg.Targets, _ = scan.ParseTargets([]string{"192.0.2.0/30", "198.51.100.1/32"})
	cfg.Ports, _ = scan.ParsePorts("0,22,443,8000-8002,65535")
	return cfg.Intent()
}
func TestPlanAndLimits(t *testing.T) {
	b := Backend{}
	s := testSpec()
	for shard := 1; shard <= 3; shard++ {
		plan, err := b.Build(s, scan.Shard{Index: shard, Total: 3}, math.MaxInt64)
		if err != nil {
			t.Fatal(err)
		}
		cmd := strings.Join(plan.Command.Argv(), " ")
		for _, want := range []string{"--config /dev/null", "--allowlist-file targets.txt", "--blocklist-file /dev/null", "--target-ports 0,22,443,8000-8002,65535", "--rate 500000", "--seed 9223372036854775807", "--shard " + strconv.Itoa(shard-1), "--shards 3", "--sender-threads 1", "--probes 1", "--cooldown-time 10", "--dedup-method window", "--status-updates-file /dev/stderr", "--quiet"} {
			if !strings.Contains(cmd, want) {
				t.Fatal(cmd)
			}
		}
		if len(plan.Files) != 1 || plan.Files[0].Content != "192.0.2.0/30\n198.51.100.1/32\n" {
			t.Fatal(plan.Files)
		}
	}
	if _, err := b.Build(s, scan.Shard{Index: 1, Total: 1}, math.MaxInt64+1); err == nil {
		t.Fatal("accepted overflowing seed")
	}
	for _, change := range []func(*scan.Spec){
		func(s *scan.Spec) { s.Rate = math.MaxInt32 + 1 },
		func(s *scan.Spec) { s.Targets = nil },
		func(s *scan.Spec) { s.Targets = []netip.Prefix{netip.MustParsePrefix("0.0.0.0/32")} },
		func(s *scan.Spec) { s.Wait = -time.Second },
	} {
		bad := testSpec()
		change(&bad)
		if err := b.Validate(bad, 1); err == nil {
			t.Fatal("accepted unsupported intent", bad)
		}
	}
	tiny := testSpec()
	tiny.Targets = []netip.Prefix{netip.MustParsePrefix("192.0.2.1/32")}
	tiny.Ports = []uint16{80}
	if err := b.Validate(tiny, 2); err == nil {
		t.Fatal("accepted more workers than targets")
	}
	tiny.Targets = []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}
	if err := b.Validate(tiny, 256); err == nil {
		t.Fatal("accepted upstream small-group assertion")
	}
	if err := b.Validate(tiny, 255); err != nil {
		t.Fatal(err)
	}
	s.Ports, _ = scan.ParsePorts("0-65535")
	s.Rate = math.MaxInt32
	if err := b.Validate(s, 65535); err != nil {
		t.Fatal(err)
	}
}

func TestResults(t *testing.T) {
	data := Fields + "\n192.0.2.1,22,1700000000,1,synack,0\n192.0.2.1,443,1700000001,1,synack,0\n192.0.2.1,443,1700000002,1,synack,1\n192.0.2.2,80,1700000003,0,rst,0\n"
	var found []scan.Finding
	if err := (&decoder{}).Results(strings.NewReader(data), func(f scan.Finding) error { found = append(found, f); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 || found[0].Port != 22 || found[1].Port != 443 || found[1].Timestamp != 1700000001 {
		t.Fatal(found)
	}
	for _, bad := range []string{"", Fields + "\n192.0.2.1,80,1,maybe,synack,0", Fields + "\n192.0.2.1,65536,1,1,synack,0", Fields + "\n192.0.2.1,80,unknown,1,synack,0", Fields + "\n192.0.2.1,80,1,1,rst,0", Fields + "\n::1,80,1,1,synack,0", Fields + "\n" + strings.Repeat("x", 1<<20)} {
		if err := (&decoder{}).Results(strings.NewReader(bad), func(scan.Finding) error { return nil }); err == nil {
			t.Fatal("accepted malformed output")
		}
	}
	sentinel := errors.New("sink failed")
	if err := (&decoder{}).Results(strings.NewReader(data), func(scan.Finding) error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
}

// Exact pinned monitor.c layout, including the extra empty data column.
func statusRow(senders, remaining string) string {
	return strings.Join([]string{"2026-11-07 03:00:00", "1", remaining, "9.090909", "0.000000", senders, "100", "100", "100", "3", "3", "3", "5", "5", "5", "0", "0", "0", "0", "", "0", "0"}, ",")
}
func TestStatus(t *testing.T) {
	d := &decoder{}
	if _, _, err := d.Status(StatusHeader); err != nil {
		t.Fatal(err)
	}
	p, ok, err := d.Status(statusRow("1", "10"))
	if err != nil || !ok || !p.PercentUnknown || p.Waiting || p.Rate != 100 || p.ETA != 10*time.Second || p.Found != 0 {
		t.Fatal(p, ok, err)
	}
	p, ok, err = d.Status(statusRow("0", "3"))
	if err != nil || !ok || p.PercentUnknown || !p.Waiting || p.Percent != 100 || p.Rate != 0 || p.ETA != 3*time.Second {
		t.Fatal(p, ok, err)
	}
	for _, line := range []string{statusRow("2", "3"), statusRow("1", "NaN"), statusRow("1", "-1"), "2026-11-07 03:00:00,broken"} {
		if _, _, err := d.Status(line); err == nil {
			t.Fatal("accepted malformed status", line)
		}
	}
	if _, ok, err := d.Status("Nov 07 [INFO] receiver initialized"); err != nil || ok {
		t.Fatal(ok, err)
	}
	if _, _, err := (&decoder{}).Status(statusRow("1", "3")); err == nil {
		t.Fatal("accepted missing schema")
	}
}

func TestPreparationRejectsDrift(t *testing.T) {
	p := (Backend{}).Preparation()
	outputs := []string{"zmap v4.3.4\n", BinarySHA256 + "  /usr/sbin/zmap\n", "saddr string: source\nsport int: target port\ntimestamp_ts int: epoch\nsuccess bool: successful\nclassification string: response\nrepeat bool: repeat\n"}
	id, err := p.Verify(outputs, "zmap-4.3.4-r0")
	if err != nil || !id.Valid() {
		t.Fatal(id, err)
	}
	for i := range outputs {
		bad := append([]string(nil), outputs...)
		bad[i] = "unsupported"
		if _, err := p.Verify(bad, "zmap-4.3.4-r0"); err == nil {
			t.Fatal("accepted incompatible probe", i)
		}
	}
	if _, err := p.Verify(outputs, "zmap-4.3.4-r1"); err == nil {
		t.Fatal("accepted unverified revision")
	}
}
