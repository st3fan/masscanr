package scan

import (
	"net/netip"
	"slices"
)

// Clone owns the full frozen configuration, including nested AS mappings.
func (c Config) Clone() Config {
	c.Targets = slices.Clone(c.Targets)
	c.Ports = slices.Clone(c.Ports)
	c.RequestedTargets = slices.Clone(c.RequestedTargets)
	for i := range c.RequestedTargets {
		t := &c.RequestedTargets[i]
		t.Prefixes = slices.Clone(t.Prefixes)
		if t.Resolution != nil {
			r := *t.Resolution
			t.Resolution = &r
		}
	}
	return c
}

// DirectTargets records canonical provenance for programmatic callers that
// already supply only a normalized prefix union rather than CLI targets.
func DirectTargets(prefixes []netip.Prefix) []Target {
	result := make([]Target, len(prefixes))
	for i, p := range prefixes {
		result[i] = Target{Input: p.String(), Target: p.String(), Prefixes: []netip.Prefix{p}}
	}
	return result
}
