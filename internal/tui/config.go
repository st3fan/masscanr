package tui

import (
	"fmt"
	"math/big"
	"slices"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/st3fan/masscanr/internal/scan"
)

var defaultPorts, _ = scan.ParsePorts(scan.TopPorts)

func (m model) configLines() []string {
	c := m.state.Config
	var addresses uint64
	for _, p := range c.Scan.Targets {
		addresses += uint64(1) << (32 - p.Bits())
	}
	lines := []string{
		fmt.Sprintf("TARGETS  %d CIDRs · %d IPs", len(c.Scan.Targets), addresses),
		"CIDR                     ADDRESSES",
	}
	for _, p := range c.Scan.Targets {
		lines = append(lines, fmt.Sprintf("%-24s %d", p, uint64(1)<<(32-p.Bits())))
	}
	ports := "Custom TCP selection"
	if slices.Equal(c.Scan.Ports, defaultPorts) {
		ports = "Nmap 7.95 top 100 TCP (effective set)"
	}
	requested := new(big.Int).Mul(new(big.Int).SetUint64(c.Scan.Rate), big.NewInt(int64(c.Scan.Shards)))
	seed := "pending"
	if m.state.SeedKnown {
		seed = fmt.Sprint(m.state.Seed)
	}
	output := c.Output
	if output == "-" {
		output = "stdout (streaming native list)"
	}
	lines = append(lines,
		"", "SCAN",
		"Protocol              TCP SYN",
		fmt.Sprintf("Port selection        %s", ports),
		fmt.Sprintf("Port count            %d", len(c.Scan.Ports)),
		"Effective ports       "+c.Scan.PortSpec(),
		fmt.Sprintf("Shards                %d", c.Scan.Shards),
		fmt.Sprintf("Rate / shard          %d pps", c.Scan.Rate),
		"Requested total rate  "+requested.String()+" pps",
		"Shared seed           "+seed,
		"Scan timeout          "+c.ScanTimeout.String(),
		"Cleanup timeout       "+c.CleanupTimeout.String(),
		"", "WORKERS",
		"Provider              Vultr",
		"Region                "+c.Scan.Region,
		"Instance type         "+c.Scan.InstanceType,
		"", "OUTPUT",
		"List                  "+output,
		"Expected CSV          "+c.Exports[0],
		"Expected JSONL        "+c.Exports[1],
		"Exports are produced only after a successful scan.",
	)
	var wrapped []string
	for _, line := range lines {
		wrapped = append(wrapped, strings.Split(ansi.Wrap(clean(line), max(1, m.width), ",/"), "\n")...)
	}
	return wrapped
}
