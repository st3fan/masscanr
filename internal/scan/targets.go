package scan

import (
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Limits keep resolution and the self-contained scan header bounded. Prefixes
// count memberships across targets, including overlaps, rather than IPs.
const (
	MaxRequestedTargets = 1024
	MaxResolvedPrefixes = 32768
	MaxTargetBytes      = 64
	RIPEEndpoint        = "https://stat.ripe.net/data/ris-prefixes/data.json"
)

// Target preserves command-line identity independently of the worker union.
// Prefixes are sorted and unique but retain contained origin prefixes.
type Target struct {
	Input      string         `json:"input"`
	Target     string         `json:"target"`
	Prefixes   []netip.Prefix `json:"prefixes"`
	Resolution *Resolution    `json:"resolution,omitempty"`
}

type Resolution struct {
	Source     string    `json:"source"`
	FetchedAt  time.Time `json:"fetched_at"`
	SnapshotAt time.Time `json:"snapshot_at"`
}

func (t Target) IsAS() bool { return strings.HasPrefix(t.Target, "AS") }

// ParseRequestedTargets is local only: AS membership is deliberately unresolved.
func ParseRequestedTargets(values []string) ([]Target, error) {
	if len(values) == 0 || len(values) > MaxRequestedTargets {
		return nil, fmt.Errorf("provide 1..%d IPv4 CIDR, address, or AS targets", MaxRequestedTargets)
	}
	var result []Target
	seen := make(map[string]bool)
	for _, value := range values {
		t, err := parseTarget(value)
		if err != nil {
			return nil, err
		}
		if !seen[t.Target] {
			seen[t.Target] = true
			result = append(result, t)
		}
	}
	return result, nil
}

func parseTarget(value string) (Target, error) {
	t := Target{Input: value}
	invalid := func() (Target, error) {
		return Target{}, fmt.Errorf("invalid IPv4 CIDR, address, or AS target %q", value)
	}
	if len(value) == 0 || len(value) > MaxTargetBytes {
		return Target{}, fmt.Errorf("target must contain 1..%d bytes", MaxTargetBytes)
	}
	if len(value) > 2 && strings.EqualFold(value[:2], "AS") {
		number := value[2:]
		if !decimal(number) {
			return invalid()
		}
		n, err := strconv.ParseUint(number, 10, 32)
		if err != nil || n == 0 {
			return invalid()
		}
		t.Target = "AS" + strconv.FormatUint(n, 10)
		return t, nil
	}
	address, mask, hasMask := strings.Cut(value, "/")
	if hasMask {
		octets := strings.Split(address, ".")
		if len(octets) > 4 || !decimal(mask) || (len(mask) > 1 && mask[0] == '0') {
			return invalid()
		}
		for _, octet := range octets {
			if !decimal(octet) || (len(octet) > 1 && octet[0] == '0') {
				return invalid()
			}
		}
		for len(octets) < 4 {
			octets = append(octets, "0")
		}
		address = strings.Join(octets, ".")
	} else {
		mask = "32"
	}
	p, err := netip.ParsePrefix(address + "/" + mask)
	if err != nil || !p.Addr().Is4() {
		return invalid()
	}
	p = p.Masked()
	t.Target = p.String()
	t.Prefixes = []netip.Prefix{p}
	return t, nil
}

func decimal(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// SortPrefixes masks, sorts and deduplicates without discarding nested prefixes.
func SortPrefixes(prefixes []netip.Prefix) []netip.Prefix {
	result := slices.Clone(prefixes)
	for i := range result {
		result[i] = result[i].Masked()
	}
	slices.SortFunc(result, func(a, b netip.Prefix) int {
		if c := a.Addr().Compare(b.Addr()); c != 0 {
			return c
		}
		return a.Bits() - b.Bits()
	})
	return slices.Compact(result)
}

// PrefixUnion removes containment in O(n log n), without enumerating addresses
// or merging ranges into broader prefixes that might obscure provenance.
func PrefixUnion(prefixes []netip.Prefix) []netip.Prefix {
	ordered := SortPrefixes(prefixes)
	result := ordered[:0]
	for _, p := range ordered {
		if len(result) == 0 || !result[len(result)-1].Contains(p.Addr()) {
			result = append(result, p)
		}
	}
	return result
}

func TargetUnion(targets []Target) []netip.Prefix {
	var prefixes []netip.Prefix
	for _, t := range targets {
		prefixes = append(prefixes, t.Prefixes...)
	}
	return PrefixUnion(prefixes)
}

// ValidateTargets verifies the frozen mapping and exact worker union. It does
// not contact RIPE; replay uses the recorded snapshot, never current routing.
func ValidateTargets(targets []Target, union []netip.Prefix) error {
	if len(targets) == 0 || len(targets) > MaxRequestedTargets {
		return fmt.Errorf("invalid requested target count")
	}
	seen := make(map[string]bool)
	total := 0
	for _, t := range targets {
		parsed, err := parseTarget(t.Input)
		if err != nil || parsed.Target != t.Target || seen[t.Target] {
			return fmt.Errorf("invalid or duplicate requested target identity")
		}
		seen[t.Target] = true
		total += len(t.Prefixes)
		if len(t.Prefixes) == 0 || total > MaxResolvedPrefixes {
			return fmt.Errorf("target mappings require 1..%d IPv4 prefix memberships", MaxResolvedPrefixes)
		}
		for _, p := range t.Prefixes {
			if !p.IsValid() || !p.Addr().Is4() || p != p.Masked() {
				return fmt.Errorf("target mapping requires masked IPv4 prefixes")
			}
		}
		if !slices.Equal(t.Prefixes, SortPrefixes(t.Prefixes)) {
			return fmt.Errorf("target prefixes must be sorted and unique")
		}
		if t.IsAS() {
			r := t.Resolution
			if r == nil || r.Source != RIPEEndpoint || r.FetchedAt.IsZero() || r.SnapshotAt.IsZero() || r.SnapshotAt.After(r.FetchedAt) {
				return fmt.Errorf("AS target requires valid RIPE snapshot provenance")
			}
		} else if t.Resolution != nil || !slices.Equal(t.Prefixes, parsed.Prefixes) {
			return fmt.Errorf("direct target mapping disagrees with its CIDR")
		}
	}
	if !slices.Equal(TargetUnion(targets), union) {
		return fmt.Errorf("worker scope disagrees with requested target mappings")
	}
	return nil
}

// AttributionIndex has at most one entry per resolved prefix. Lookup visits
// only the 33 possible IPv4 prefix lengths, independent of mapping size.
type AttributionIndex struct{ prefixes map[netip.Prefix]attribution }
type attribution struct {
	order  int
	target string
	as     bool
}

func NewAttributionIndex(targets []Target) *AttributionIndex {
	x := &AttributionIndex{prefixes: make(map[netip.Prefix]attribution)}
	for i, t := range targets {
		for _, p := range t.Prefixes {
			if _, exists := x.prefixes[p]; !exists {
				x.prefixes[p] = attribution{order: i, target: t.Target, as: t.IsAS()}
			}
		}
	}
	return x
}

// Lookup chooses the first target, then its longest matching origin prefix.
func (x *AttributionIndex) Lookup(ip netip.Addr) (target, prefix string, ok bool) {
	if x == nil || !ip.Is4() {
		return "", "", false
	}
	best := MaxRequestedTargets
	for bits := 32; bits >= 0; bits-- {
		p := netip.PrefixFrom(ip, bits).Masked()
		if a, found := x.prefixes[p]; found && a.order < best {
			best, target, ok = a.order, a.target, true
			prefix = ""
			if a.as {
				prefix = p.String()
			}
		}
	}
	return
}
