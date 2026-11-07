package tui

import (
	"fmt"
	"math"
	"net/netip"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/st3fan/masscanr/internal/controller"
	"github.com/st3fan/masscanr/internal/scan"
)

func TestConfigEffectiveSettingsAndScrolling(t *testing.T) {
	m := fixtureModel(4)
	cfg := scan.Defaults()
	cfg.Shards = 4
	for i := range 40 {
		cfg.Targets = append(cfg.Targets, netip.MustParsePrefix(fmt.Sprintf("192.0.%d.0/24", i)))
	}
	cfg.Ports, _ = scan.ParsePorts("22,80,443,8000-8100")
	m.state.Config = NewRunConfig(cfg, controller.DefaultTimeouts(), "-", [2]string{"inventory.csv", "inventory.jsonl"})
	m.state.Seed, m.state.SeedKnown = 42, true
	m.tab = 1
	all := strings.Join(m.configLines(), "\n")
	for _, want := range []string{"40 CIDRs · 10240 IPs", "192.0.39.0/24", "22,80,443,8000-8100", "Port count            104", "2000000 pps", "500000 pps", "stdout (streaming native list)", "inventory.csv", "inventory.jsonl", "24h0m0s", "5m0s", "42"} {
		if !strings.Contains(all, want) {
			t.Fatalf("missing %q:\n%s", want, all)
		}
	}
	seen := ""
	for range len(m.configLines()) {
		seen += m.View().Content + "\n"
		m, _ = key(m, tea.KeyDown, 0)
	}
	for _, line := range m.configLines() {
		if !strings.Contains(seen, line) {
			t.Fatalf("line is unreachable: %q", line)
		}
	}
	bottom := m.configOffset
	for range 100 {
		m, _ = key(m, tea.KeyPgDown, 0)
	}
	if m.configOffset != bottom {
		t.Fatal("overscrolled beyond content")
	}
	m, _ = key(m, tea.KeyUp, 0)
	if m.configOffset != bottom-1 {
		t.Fatal("cannot immediately scroll back from end")
	}
}

func TestConfigFullIPv4AndLongValues(t *testing.T) {
	m := fixtureModel(4)
	m.state.Config.Scan.Targets = []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}
	m.state.Config.Scan.Rate = math.MaxUint64
	m.state.Config.Output = strings.Repeat("long-path/", 50) + "END.list"
	m.width = 30
	lines := m.configLines()
	text := strings.Join(lines, "\n")
	if !strings.Contains(text, "4294967296") || !strings.Contains(strings.ReplaceAll(text, "\n", ""), "73786976294838206460") || !strings.Contains(text, "END.list") {
		t.Fatal("overflowed count or truncated output path", text)
	}
	m.width = 120
	if !strings.Contains(strings.Join(m.configLines(), "\n"), "Nmap 7.95 top 100 TCP") {
		t.Fatal("default ports not identified")
	}
}
