package scan

import (
	"fmt"
	"net/netip"
	"strings"
	"time"
)

// Spec is scanning intent. It contains no cloud placement or credentials.
type Spec struct {
	Targets []netip.Prefix
	Ports   []uint16
	Rate    uint64
	Wait    time.Duration
}

func (c Config) Intent() Spec {
	return Spec{Targets: c.Targets, Ports: c.Ports, Rate: c.Rate, Wait: 10 * time.Second}
}

func (s Spec) Validate() error {
	if len(s.Targets) == 0 {
		return fmt.Errorf("provide at least one IPv4 CIDR")
	}
	for _, p := range s.Targets {
		if !p.IsValid() || !p.Addr().Is4() || p != p.Masked() {
			return fmt.Errorf("targets must be normalized IPv4 CIDRs")
		}
	}
	if len(PrefixUnion(s.Targets)) != len(s.Targets) {
		return fmt.Errorf("targets must not overlap")
	}
	if len(s.Ports) == 0 {
		return fmt.Errorf("provide at least one TCP port")
	}
	for i, p := range s.Ports {
		if i > 0 && p <= s.Ports[i-1] {
			return fmt.Errorf("ports must be sorted and unique")
		}
	}
	if s.Rate == 0 {
		return fmt.Errorf("rate must be positive (packets/second per worker)")
	}
	if s.Wait < 0 || s.Wait%time.Second != 0 {
		return fmt.Errorf("response wait must be nonnegative whole seconds")
	}
	return nil
}

func (s Spec) PortSpec() string { return (Config{Ports: s.Ports}).PortSpec() }

// Shard uses one-based numbering. An adapter owns native numbering conversion.
type Shard struct{ Index, Total int }

func (s Shard) Validate() error {
	if s.Total < 1 || s.Index < 1 || s.Index > s.Total {
		return fmt.Errorf("shard %d is outside 1..%d", s.Index, s.Total)
	}
	return nil
}

type Finding struct {
	Address         netip.Addr
	Port            uint16
	Protocol, State string
	Timestamp       uint64 // Scanner observation time, Unix seconds.
}

// Unknown flags keep missing telemetry distinct from measured zero. Found is
// populated by the accepted-finding sink, never a native packet counter.
type Progress struct {
	Percent, Rate                           float64
	ETA                                     time.Duration
	Found                                   uint64
	Waiting                                 bool
	PercentUnknown, RateUnknown, ETAUnknown bool
}

type Identity struct{ Name, Version, Build string }

func (i Identity) Valid() bool    { return i.Name != "" && i.Version != "" && i.Build != "" }
func (i Identity) String() string { return strings.TrimSpace(i.Name + " " + i.Version + " " + i.Build) }

type Prepared struct {
	Identity          Identity
	Release, Packages string
}

func (p Prepared) String() string {
	return fmt.Sprintf("Alpine %s; %s; %s", p.Release, p.Identity, p.Packages)
}
