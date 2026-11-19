package scan

import (
	"net/netip"
	"slices"
	"testing"
)

func TestSubtractPrefixesKeepsExactCompactScope(t *testing.T) {
	requested := []netip.Prefix{netip.MustParsePrefix("192.0.2.0/29"), netip.MustParsePrefix("192.0.2.0/30")}
	excluded := []netip.Prefix{netip.MustParsePrefix("192.0.2.1/32"), netip.MustParsePrefix("192.0.2.6/32"), netip.MustParsePrefix("198.51.100.0/24")}
	got, err := SubtractPrefixes(requested, excluded)
	if err != nil {
		t.Fatal(err)
	}
	want := []netip.Prefix{
		netip.MustParsePrefix("192.0.2.0/32"),
		netip.MustParsePrefix("192.0.2.2/31"),
		netip.MustParsePrefix("192.0.2.4/31"),
		netip.MustParsePrefix("192.0.2.7/32"),
	}
	if !slices.Equal(got, want) {
		t.Fatalf("scope = %v, want %v", got, want)
	}
	full, err := SubtractPrefixes([]netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}, []netip.Prefix{netip.MustParsePrefix("192.0.2.1/32")})
	if err != nil || len(full) != 32 {
		t.Fatalf("/0 minus one address = %d prefixes, %v", len(full), err)
	}
	for _, p := range full {
		if p.Contains(netip.MustParseAddr("192.0.2.1")) {
			t.Fatalf("excluded address in %s", p)
		}
	}
	all, err := SubtractPrefixes(requested, []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")})
	if err != nil || len(all) != 0 {
		t.Fatalf("fully excluded scope = %v, %v", all, err)
	}
}

func TestSubtractPrefixesRejectsInvalidAndFragmentedScope(t *testing.T) {
	if _, err := SubtractPrefixes([]netip.Prefix{netip.MustParsePrefix("2001:db8::/32")}, nil); err == nil {
		t.Fatal("accepted IPv6 scope")
	}
	if _, err := SubtractPrefixes([]netip.Prefix{netip.MustParsePrefix("192.0.2.1/24")}, nil); err == nil {
		t.Fatal("accepted unmasked scope")
	}
	// Remove every other address from a /16. The result exceeds the
	// representable frozen-scope limit without enumerating IPs in production.
	exclusions := make([]netip.Prefix, 0, 32768)
	for i := 0; i < 65536; i += 2 {
		exclusions = append(exclusions, netip.PrefixFrom(netip.AddrFrom4([4]byte{192, 0, byte(i >> 8), byte(i)}), 32))
	}
	_, err := SubtractPrefixes([]netip.Prefix{netip.MustParsePrefix("192.0.0.0/16"), netip.MustParsePrefix("198.51.100.0/24")}, exclusions)
	if err == nil {
		t.Fatal("accepted excessive fragmentation")
	}
}
