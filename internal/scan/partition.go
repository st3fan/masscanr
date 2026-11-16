package scan

import (
	"fmt"
	"math/bits"
	"net/netip"
	"sort"
)

// MaxProbesPerShard bounds one retryable unit of address/port work. A shard
// scans all selected ports for its assigned addresses.
const MaxProbesPerShard uint64 = 1024 * 1024

// PartitionVersion identifies the deterministic address-order partitioning
// used by a frozen run. Older runs must retain their version on replay.
const PartitionVersion = 1

// WorkShard is one logical unit, independent of the instance that executes it.
type WorkShard struct {
	Index     uint64
	Targets   []netip.Prefix
	Addresses uint64
	Probes    uint64
}

type addressSpan struct {
	start   uint64 // Inclusive offset in the normalized target union.
	firstIP uint64
	length  uint64
}

// Manifest derives shard scopes on demand. It retains one span per effective
// prefix, not one row per address or shard.
type Manifest struct {
	Version          int
	Count            uint64
	Addresses        uint64
	Ports            uint64
	Probes           uint64
	AddressesPerUnit uint64
	spans            []addressSpan
}

// Partition requires normalized, nonoverlapping IPv4 scope. Port count is the
// count of distinct selected ports, after parsing ranges and deduplication.
func Partition(targets []netip.Prefix, ports int) (Manifest, error) {
	if ports < 1 || ports > 65536 {
		return Manifest{}, fmt.Errorf("port count must be 1..65536")
	}
	if len(targets) == 0 {
		return Manifest{}, fmt.Errorf("effective target scope is empty")
	}
	ordered := PrefixUnion(targets)
	if len(ordered) != len(targets) {
		return Manifest{}, fmt.Errorf("effective target scope must be disjoint")
	}
	m := Manifest{Version: PartitionVersion, Ports: uint64(ports), AddressesPerUnit: MaxProbesPerShard / uint64(ports)}
	for _, prefix := range ordered {
		if !prefix.IsValid() || !prefix.Addr().Is4() || prefix != prefix.Masked() {
			return Manifest{}, fmt.Errorf("effective target scope requires masked IPv4 prefixes")
		}
		length := uint64(1) << (32 - prefix.Bits())
		m.spans = append(m.spans, addressSpan{start: m.Addresses, firstIP: ipv4Number(prefix.Addr()), length: length})
		m.Addresses += length
	}
	if m.Addresses > uint64(1)<<32 {
		return Manifest{}, fmt.Errorf("effective target scope exceeds IPv4 space")
	}
	m.Probes = m.Addresses * m.Ports // At most 2^48.
	m.Count = (m.Addresses-1)/m.AddressesPerUnit + 1
	return m, nil
}

func ipv4Number(addr netip.Addr) uint64 {
	b := addr.As4()
	return uint64(b[0])<<24 | uint64(b[1])<<16 | uint64(b[2])<<8 | uint64(b[3])
}

func ipv4Address(number uint64) netip.Addr {
	return netip.AddrFrom4([4]byte{byte(number >> 24), byte(number >> 16), byte(number >> 8), byte(number)})
}

// At derives one exact shard scope without enumerating preceding addresses or
// constructing the full manifest. Index is one-based.
func (m Manifest) At(index uint64) (WorkShard, error) {
	if m.Version != PartitionVersion || index < 1 || index > m.Count || m.AddressesPerUnit == 0 {
		return WorkShard{}, fmt.Errorf("shard %d is outside frozen manifest", index)
	}
	start := (index - 1) * m.AddressesPerUnit
	end := min(start+m.AddressesPerUnit, m.Addresses)
	result := WorkShard{Index: index, Addresses: end - start, Probes: (end - start) * m.Ports}
	firstSpan := sort.Search(len(m.spans), func(i int) bool {
		return m.spans[i].start+m.spans[i].length > start
	})
	for i := firstSpan; i < len(m.spans) && m.spans[i].start < end; i++ {
		span := m.spans[i]
		lo := max(start, span.start)
		hi := min(end, span.start+span.length)
		result.Targets = appendCIDRRange(result.Targets, span.firstIP+(lo-span.start), hi-lo)
	}
	return result, nil
}

func appendCIDRRange(dst []netip.Prefix, first, length uint64) []netip.Prefix {
	for length > 0 {
		alignment := min(bits.TrailingZeros64(first), 32)
		power := min(alignment, bits.Len64(length)-1)
		dst = append(dst, netip.PrefixFrom(ipv4Address(first), 32-power))
		step := uint64(1) << power
		first += step
		length -= step
	}
	return dst
}
