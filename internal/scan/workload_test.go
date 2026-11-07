package scan

import (
	"net/netip"
	"testing"
)

func TestPlannedWork(t *testing.T) {
	for _, tc := range []struct {
		name    string
		targets []string
		ports   string
		want    Workload
		prompt  bool
	}{
		{"below", []string{"192.0.0.0/19"}, TopPorts, Workload{8192, 100, 819200}, false},
		{"above", []string{"192.0.0.0/18"}, TopPorts, Workload{16384, 100, 1638400}, true},
		{"one port", []string{"192.0.0.0/16"}, "443", Workload{65536, 1, 65536}, false},
		{"top 100", []string{"192.0.0.0/16"}, TopPorts, Workload{65536, 100, 6553600}, true},
		{"small network all ports", []string{"192.0.2.0/28"}, "0-65535", Workload{16, 65536, 1048576}, true},
		{"normalized union", []string{"192.0.2.42/24", "192.0.2.0/25", "192.0.2.0/24", "192.0.2.128/25", "198.51.100.0/24"}, "80-81,81,80-82", Workload{512, 3, 1536}, false},
		{"disjoint aggregate", []string{"192.0.0.0/19", "198.51.96.0/19"}, TopPorts, Workload{16384, 100, 1638400}, true},
		{"entire IPv4", []string{"0.0.0.0/0", "192.0.2.0/24"}, "0-65535", Workload{4294967296, 65536, 281474976710656}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			targets, err := ParseTargets(tc.targets)
			if err != nil {
				t.Fatal(err)
			}
			ports, err := ParsePorts(tc.ports)
			if err != nil {
				t.Fatal(err)
			}
			for _, shards := range []int{1, 2, 8} {
				cfg := Defaults()
				cfg.Targets, cfg.Ports, cfg.Shards = targets, ports, shards
				got, err := cfg.Intent().Workload()
				if err != nil || got != tc.want || got.RequiresConfirmation() != tc.prompt {
					t.Fatalf("shards=%d: got %+v, %v; want %+v, prompt=%v", shards, got, err, tc.want, tc.prompt)
				}
			}
		})
	}
}

func TestProbeConfirmationBoundary(t *testing.T) {
	// A union containing exactly one million addresses, then one extra /32.
	targets, err := ParseTargets([]string{"0.0.0.0/13", "0.8.0.0/14", "0.12.0.0/15", "0.14.0.0/16", "0.15.0.0/18", "0.15.64.0/23", "0.15.66.0/26"})
	if err != nil {
		t.Fatal(err)
	}
	s := Spec{Targets: targets, Ports: []uint16{443}, Rate: 1}
	w, err := s.Workload()
	if err != nil || w.Probes != 1_000_000 || w.RequiresConfirmation() {
		t.Fatal(w, err)
	}
	s.Targets = append(s.Targets, netip.MustParsePrefix("192.0.2.0/32"))
	w, err = s.Workload()
	if err != nil || w.Probes != 1_000_001 || !w.RequiresConfirmation() {
		t.Fatal(w, err)
	}
}

func TestWorkloadRejectsUnnormalizedInput(t *testing.T) {
	for _, s := range []Spec{
		{Targets: []netip.Prefix{netip.MustParsePrefix("::/0")}, Ports: []uint16{80}, Rate: 1},
		{Targets: []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("192.0.2.0/25")}, Ports: []uint16{80}, Rate: 1},
		{Targets: []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}, Ports: []uint16{80, 80}, Rate: 1},
	} {
		if _, err := s.Workload(); err == nil {
			t.Fatal("counted unnormalized input", s)
		}
	}
}
