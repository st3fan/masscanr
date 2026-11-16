package scan

import (
	"net/netip"
	"slices"
	"testing"
)

func TestPartitionProbeBudgetAndCoverage(t *testing.T) {
	targets := []netip.Prefix{netip.MustParsePrefix("192.0.2.0/31")}
	m, err := Partition(targets, 1024)
	if err != nil || m.Probes != 2048 || m.Count != 1 || m.AddressesPerUnit != 1024 {
		t.Fatalf("manifest=%+v err=%v", m, err)
	}
	unit, err := m.At(1)
	if err != nil || unit.Probes != 2048 || !slices.Equal(unit.Targets, targets) {
		t.Fatalf("unit=%+v err=%v", unit, err)
	}

	// 250 disjoint /22s represent 256,000 addresses without enumeration.
	large := make([]netip.Prefix, 250)
	for i := range large {
		large[i] = netip.PrefixFrom(ipv4Address(uint64(i)*1024), 22)
	}
	m, err = Partition(large, 65535)
	if err != nil || m.Addresses != 256000 || m.Probes != 16776960000 || m.AddressesPerUnit != 16 || m.Count != 16000 {
		t.Fatalf("large manifest=%+v err=%v", m, err)
	}
	for _, index := range []uint64{1, 17, 15999, 16000} {
		unit, err = m.At(index)
		if err != nil || unit.Addresses != 16 || unit.Probes != 1048560 || len(unit.Targets) == 0 {
			t.Fatalf("large unit %d=%+v err=%v", index, unit, err)
		}
	}
}

func TestPartitionExactDisjointScopes(t *testing.T) {
	targets := []netip.Prefix{
		netip.MustParsePrefix("192.0.2.0/29"),
		netip.MustParsePrefix("198.51.100.0/30"),
		netip.MustParsePrefix("203.0.113.5/32"),
	}
	m, err := Partition(targets, 200000) // Invalid port count is rejected.
	if err == nil || m.Count != 0 {
		t.Fatalf("accepted invalid port count: %+v", m)
	}
	m, err = Partition(targets, 65536) // 16 addresses per shard; one unit.
	if err != nil || m.Count != 1 {
		t.Fatalf("manifest=%+v err=%v", m, err)
	}
	unit, err := m.At(1)
	if err != nil || unit.Addresses != 13 || unit.Probes != 851968 || !slices.Equal(unit.Targets, targets) {
		t.Fatalf("unit=%+v err=%v", unit, err)
	}

	// A /20 split into four 1,024-address shards preserves every address once.
	m, err = Partition([]netip.Prefix{netip.MustParsePrefix("192.0.0.0/20")}, 1024)
	if err != nil || m.Count != 4 {
		t.Fatalf("manifest=%+v err=%v", m, err)
	}
	var covered []netip.Prefix
	for i := uint64(1); i <= m.Count; i++ {
		u, e := m.At(i)
		if e != nil || u.Probes != MaxProbesPerShard || len(u.Targets) != 1 {
			t.Fatalf("unit %d=%+v err=%v", i, u, e)
		}
		covered = append(covered, u.Targets...)
	}
	if len(covered) != 4 || covered[0].String() != "192.0.0.0/22" || covered[3].String() != "192.0.12.0/22" {
		t.Fatal(covered)
	}
	if _, err := m.At(5); err == nil {
		t.Fatal("accepted nonexistent shard")
	}
}
