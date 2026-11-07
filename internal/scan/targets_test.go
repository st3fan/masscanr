package scan

import (
	"fmt"
	"net/netip"
	"slices"
	"testing"
	"time"
)

func TestRequestedTargets(t *testing.T) {
	values := []string{"10/8", "10.0.0.42/8", "192.168.0.1", "as001234", "AS1234", "192.168/16", "0/0", "AS4294967295"}
	targets, err := ParseRequestedTargets(values)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, target := range targets {
		got = append(got, target.Target)
	}
	if want := []string{"10.0.0.0/8", "192.168.0.1/32", "AS1234", "192.168.0.0/16", "0.0.0.0/0", "AS4294967295"}; !slices.Equal(got, want) {
		t.Fatal(got)
	}
	if targets[0].Input != "10/8" || len(targets[2].Prefixes) != 0 {
		t.Fatal(targets)
	}
	for _, bad := range []string{"10", "AS0", "AS4294967296", "AS-1", "AS+1", "AS1.2", "1234", "AS", "192.168.001.1", "010/8", "10/-1", "10/08", "10/33", "256/8", "10./8", "10/8/8", "::1", "::/0", "::ffff:192.0.2.1", "example.com", " 10/8", "10/8\n", "0x10/8", "1.2.3.4.5/8", "192.0.2.1-192.0.2.2"} {
		if _, err := ParseRequestedTargets([]string{bad}); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if _, err := ParseRequestedTargets(nil); err == nil {
		t.Fatal("accepted no targets")
	}
	if _, err := ParseRequestedTargets(make([]string, MaxRequestedTargets+1)); err == nil {
		t.Fatal("accepted too many targets")
	}
}

func TestFrozenTargetsAndAttribution(t *testing.T) {
	targets, _ := ParseRequestedTargets([]string{"AS1234", "192.0.2.42", "AS5678", "192.0.2.0/24"})
	for _, i := range []int{0, 2} {
		targets[i].Prefixes = []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("192.0.2.0/25")}
		targets[i].Resolution = &Resolution{Source: RIPEEndpoint, FetchedAt: time.Unix(100, 0), SnapshotAt: time.Unix(90, 0)}
	}
	union := TargetUnion(targets)
	if len(union) != 1 || union[0].String() != "192.0.2.0/24" {
		t.Fatal(union)
	}
	if err := ValidateTargets(targets, union); err != nil {
		t.Fatal(err)
	}
	index := NewAttributionIndex(targets)
	for _, tc := range []struct{ ip, prefix string }{{"192.0.2.42", "192.0.2.0/25"}, {"192.0.2.200", "192.0.2.0/24"}} {
		target, prefix, ok := index.Lookup(netip.MustParseAddr(tc.ip))
		if !ok || target != "AS1234" || prefix != tc.prefix {
			t.Fatal(target, prefix, ok)
		}
	}
	if _, _, ok := index.Lookup(netip.MustParseAddr("198.51.100.1")); ok {
		t.Fatal("matched outside scope")
	}
	targets[0], targets[1] = targets[1], targets[0]
	target, prefix, ok := NewAttributionIndex(targets).Lookup(netip.MustParseAddr("192.0.2.42"))
	if !ok || target != "192.0.2.42/32" || prefix != "" {
		t.Fatal(target, prefix, ok)
	}
	targets[1].Resolution = nil
	if err := ValidateTargets(targets, union); err == nil {
		t.Fatal("accepted missing AS snapshot")
	}
}

func TestPrefixUnionLarge(t *testing.T) {
	var prefixes []netip.Prefix
	for i := 0; i < MaxResolvedPrefixes; i++ {
		prefixes = append(prefixes, netip.MustParsePrefix(fmt.Sprintf("10.%d.%d.0/24", i/256, i%256)))
	}
	if got := PrefixUnion(prefixes); !slices.Equal(got, prefixes) {
		t.Fatal("changed disjoint union")
	}
	prefixes = append(prefixes, netip.MustParsePrefix("10.0.0.0/8"))
	if got := PrefixUnion(prefixes); len(got) != 1 || got[0].String() != "10.0.0.0/8" {
		t.Fatal(got)
	}
}
