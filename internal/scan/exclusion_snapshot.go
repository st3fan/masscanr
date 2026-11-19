package scan

import (
	"fmt"
	"net/netip"
	"slices"
)

const (
	MaxExclusionResources = 128
	MaxExclusionSelectors = 8192
)

// ExclusionSnapshot retains the exact revision and selectors used by a run.
// Input is preserved for provenance; Prefix is the canonical concrete scope.
type ExclusionSnapshot struct {
	ID       string              `json:"id"`
	Revision int64               `json:"revision"`
	Entries  []ExclusionSelector `json:"entries"`
}

type ExclusionSelector struct {
	Input  string       `json:"input"`
	Prefix netip.Prefix `json:"prefix"`
}

// ExclusionUnion validates and combines frozen selectors. Snapshots are
// ID-sorted so the same resource set has one canonical representation.
func ExclusionUnion(snapshots []ExclusionSnapshot) ([]netip.Prefix, error) {
	if len(snapshots) > MaxExclusionResources {
		return nil, fmt.Errorf("too many exclusion resources")
	}
	prefixes := make([]netip.Prefix, 0)
	for i, snapshot := range snapshots {
		if snapshot.ID == "" || snapshot.Revision < 1 || i > 0 && snapshot.ID <= snapshots[i-1].ID || len(snapshot.Entries) == 0 || len(snapshot.Entries) > MaxRequestedTargets {
			return nil, fmt.Errorf("invalid frozen exclusion resource")
		}
		seen := make(map[netip.Prefix]bool, len(snapshot.Entries))
		for _, entry := range snapshot.Entries {
			parsed, err := parseTarget(entry.Input)
			if err != nil || parsed.IsAS() || len(parsed.Prefixes) != 1 || parsed.Prefixes[0] != entry.Prefix || seen[entry.Prefix] {
				return nil, fmt.Errorf("invalid frozen exclusion selector")
			}
			seen[entry.Prefix] = true
			prefixes = append(prefixes, entry.Prefix)
			if len(prefixes) > MaxExclusionSelectors {
				return nil, fmt.Errorf("too many frozen exclusion selectors")
			}
		}
	}
	return PrefixUnion(prefixes), nil
}

// ValidateEffectiveScope proves that workers see exactly the requested Target
// union less the frozen exclusions. No current resource or AS lookup is used.
func ValidateEffectiveScope(requested []Target, exclusions []ExclusionSnapshot, effective []netip.Prefix) error {
	requestedScope := TargetUnion(requested)
	if err := ValidateTargets(requested, requestedScope); err != nil {
		return err
	}
	excluded, err := ExclusionUnion(exclusions)
	if err != nil {
		return err
	}
	want, err := SubtractPrefixes(requestedScope, excluded)
	if err != nil {
		return err
	}
	if len(want) == 0 || !slices.Equal(want, effective) {
		return fmt.Errorf("effective scope differs from requested targets minus exclusions")
	}
	return nil
}
