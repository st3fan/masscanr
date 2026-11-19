package scan

import (
	"fmt"
	"net/netip"
)

// MaxEffectivePrefixes bounds the size of frozen scopes and journal headers.
const MaxEffectivePrefixes = 32768

// SubtractPrefixes removes an IPv4 prefix union from another without
// enumerating addresses. Inputs may overlap; the result is disjoint and sorted.
func SubtractPrefixes(requested, excluded []netip.Prefix) ([]netip.Prefix, error) {
	for _, group := range [][]netip.Prefix{requested, excluded} {
		for _, p := range group {
			if !p.IsValid() || !p.Addr().Is4() || p != p.Masked() {
				return nil, fmt.Errorf("scope requires masked IPv4 prefixes")
			}
		}
	}
	if len(requested) > MaxResolvedPrefixes || len(excluded) > MaxResolvedPrefixes {
		return nil, fmt.Errorf("scope exceeds %d input prefixes", MaxResolvedPrefixes)
	}
	want := PrefixUnion(requested)
	remove := PrefixUnion(excluded)
	result := []netip.Prefix{}
	j := 0
	for _, p := range want {
		first := ipv4Number(p.Addr())
		end := first + uint64(1)<<(32-p.Bits())
		for j < len(remove) && prefixEnd(remove[j]) <= first {
			j++
		}
		cursor := first
		for k := j; k < len(remove) && ipv4Number(remove[k].Addr()) < end; k++ {
			lo := ipv4Number(remove[k].Addr())
			hi := prefixEnd(remove[k])
			if lo > cursor {
				result = appendCIDRRange(result, cursor, min(lo, end)-cursor)
			}
			cursor = min(end, max(cursor, hi))
			if cursor == end {
				break
			}
		}
		if cursor < end {
			result = appendCIDRRange(result, cursor, end-cursor)
		}
		if len(result) > MaxEffectivePrefixes {
			return nil, fmt.Errorf("effective scope exceeds %d prefixes", MaxEffectivePrefixes)
		}
	}
	return result, nil
}

func prefixEnd(p netip.Prefix) uint64 {
	return ipv4Number(p.Addr()) + uint64(1)<<(32-p.Bits())
}
