package masscan

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/st3fan/masscanr/internal/scan"
)

func TestArgumentsAndDecoder(t *testing.T) {
	cfg := scan.Defaults()
	cfg.Targets, _ = scan.ParseTargets([]string{"192.0.2.0/24"})
	for i := 1; i <= 2; i++ {
		plan, err := (Backend{}).Build(cfg.Intent(), scan.Shard{Index: i, Total: 2}, math.MaxUint64)
		if err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(plan.Command.Argv(), " ")
		for _, want := range []string{"stdbuf -oL masscan --conf /dev/null", "--seed 18446744073709551615", "--rate 500000", fmt.Sprintf("--shards %d/2", i), "--wait 10", "--output-format list", "192.0.2.0/24"} {
			if !strings.Contains(joined, want) {
				t.Fatal(joined)
			}
		}
		var found []scan.Finding
		err = plan.NewDecoder().Results(strings.NewReader("#masscan\nopen tcp 80 192.0.2.1 1700000000\nopen tcp 443 192.0.2.1 1700000001"), func(f scan.Finding) error { found = append(found, f); return nil })
		if err != nil || len(found) != 2 || found[0].Timestamp != 1700000000 || found[1].Port != 443 {
			t.Fatal(found, err)
		}
		sentinel := errors.New("sink failed")
		if err := plan.NewDecoder().Results(strings.NewReader("open tcp 80 192.0.2.1 1\n"), func(scan.Finding) error { return sentinel }); !errors.Is(err, sentinel) {
			t.Fatal(err)
		}
	}
	for _, bad := range []string{"garbage", "open udp 80 192.0.2.1 1", "open tcp 65536 192.0.2.1 1", "open tcp 80 192.0.2.1 nope", strings.Repeat("a", 1<<20)} {
		if err := (decoder{}).Results(strings.NewReader(bad), func(scan.Finding) error { return nil }); err == nil {
			t.Fatal("accepted invalid record")
		}
	}
}

func TestUnrepresentableETAStaysUnknown(t *testing.T) {
	for _, line := range []string{
		"rate:1.00-kpps, 10.00% done, 999999999:00:00 remaining, found=0",
		"rate:0.00-kpps, 100.00% done, waiting 18446744073709551615-secs, found=0",
	} {
		p, ok := ParseProgress(line)
		if !ok || !p.ETAUnknown || p.ETA != 0 {
			t.Fatal(p, ok)
		}
	}
}

func TestFedoraPreparationIdentity(t *testing.T) {
	p, err := (Backend{}).PreparationFor("fedora44")
	if err != nil || p.Platform != "fedora44" {
		t.Fatal(err, p.Platform)
	}
	outputs := []string{"Masscan version 1.3.2 (fixture)\n", FedoraBinarySHA256 + "  /usr/bin/masscan\n"}
	installed := "masscan-1.3.2-15.fc44.x86_64, coreutils-9.7-1.fc44.x86_64"
	if id, err := p.Verify(outputs, installed); err != nil || !id.Valid() {
		t.Fatal(id, err)
	}
	for _, bad := range []struct {
		outputs   []string
		installed string
	}{
		{[]string{"Masscan version 1.3.3\n", outputs[1]}, installed},
		{[]string{outputs[0], strings.Repeat("a", 64) + "  /usr/bin/masscan\n"}, installed},
		{outputs, "masscan-1.3.2-16.fc45.x86_64, coreutils-9.7-1.fc44.x86_64"},
	} {
		if _, err := p.Verify(bad.outputs, bad.installed); err == nil {
			t.Fatal("accepted incompatible Fedora build")
		}
	}
}
