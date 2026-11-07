// Package scan defines provider-independent scan configuration.
package scan

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
)

const (
	DefaultShards              = 2
	DefaultRate         uint64 = 500000
	DefaultRegion              = "atl"
	DefaultInstanceType        = "vc2-1c-1gb"
)

// TopPorts is the top 100 TCP ports by frequency in Nmap 7.95 nmap-services,
// ordered numerically. Source: https://svn.nmap.org/nmap-releases/nmap-7.95/nmap-services
// Ranking: descending frequency, ascending port to break ties. The 100th
// frequency is 0.003161; the next is 0.003149, so the boundary has no tie.
const TopPorts = "7,9,13,21,22,23,25,26,37,53,79,80,81,88,106,110,111,113,119,135,139,143,144,179,199,389,427,443,444,445,465,513,514,515,543,544,548,554,587,631,646,873,990,993,995,1025,1026,1027,1028,1029,1110,1433,1720,1723,1755,1900,2000,2001,2049,2121,2717,3000,3128,3306,3389,3986,4899,5000,5009,5051,5060,5101,5190,5357,5432,5631,5666,5800,5900,6000,6001,6646,7070,8000,8008,8009,8080,8081,8443,8888,9100,9999,10000,32768,49152,49153,49154,49155,49156,49157"

type Config struct {
	Targets      []netip.Prefix
	Ports        []uint16
	Shards       int
	Rate         uint64
	Region       string
	InstanceType string
}

func Defaults() Config {
	ports, _ := ParsePorts(TopPorts)
	return Config{Ports: ports, Shards: DefaultShards, Rate: DefaultRate, Region: DefaultRegion, InstanceType: DefaultInstanceType}
}

// ParseTargets masks host bits and removes duplicate/contained prefixes.
func ParseTargets(values []string) ([]netip.Prefix, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("provide at least one IPv4 CIDR")
	}
	prefixes := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		p, err := netip.ParsePrefix(value)
		if err != nil || !p.Addr().Is4() {
			return nil, fmt.Errorf("invalid IPv4 CIDR %q", value)
		}
		prefixes = append(prefixes, p.Masked())
	}
	sort.Slice(prefixes, func(i, j int) bool {
		if prefixes[i].Bits() != prefixes[j].Bits() {
			return prefixes[i].Bits() < prefixes[j].Bits()
		}
		return prefixes[i].Addr().Less(prefixes[j].Addr())
	})
	var result []netip.Prefix
	for _, p := range prefixes {
		covered := false
		for _, parent := range result {
			if parent.Contains(p.Addr()) {
				covered = true
				break
			}
		}
		if !covered {
			result = append(result, p)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Addr().Less(result[j].Addr()) })
	return result, nil
}

// ParsePorts accepts masscan-style TCP lists and inclusive ranges, including
// port zero. Protocol prefixes and service names are outside V1's TCP scope.
func ParsePorts(value string) ([]uint16, error) {
	var seen [65536]bool
	for _, part := range strings.Split(value, ",") {
		bounds := strings.Split(strings.TrimSpace(part), "-")
		if len(bounds) > 2 {
			return nil, fmt.Errorf("invalid port range %q", part)
		}
		first, err := strconv.ParseUint(strings.TrimSpace(bounds[0]), 10, 16)
		if err != nil {
			return nil, fmt.Errorf("invalid TCP port %q", part)
		}
		last := first
		if len(bounds) == 2 {
			last, err = strconv.ParseUint(strings.TrimSpace(bounds[1]), 10, 16)
			if err != nil || last < first {
				return nil, fmt.Errorf("invalid port range %q", part)
			}
		}
		for port := first; port <= last; port++ {
			seen[port] = true
		}
	}
	var ports []uint16
	for port, exists := range seen {
		if exists {
			ports = append(ports, uint16(port))
		}
	}
	return ports, nil
}

func (c Config) Validate() error {
	if len(c.Targets) == 0 {
		return fmt.Errorf("provide at least one IPv4 CIDR")
	}
	for _, p := range c.Targets {
		if !p.IsValid() || !p.Addr().Is4() || p != p.Masked() {
			return fmt.Errorf("targets must be normalized IPv4 CIDRs")
		}
	}
	if len(c.Ports) == 0 {
		return fmt.Errorf("provide at least one TCP port")
	}
	if c.Shards < 1 {
		return fmt.Errorf("shards must be positive")
	}
	if c.Rate == 0 {
		return fmt.Errorf("rate must be positive (packets/second per worker)")
	}
	if !identifier(c.Region) || !identifier(c.InstanceType) {
		return fmt.Errorf("region and instance type must be provider identifiers")
	}
	return nil
}

func identifier(s string) bool {
	if s == "" {
		return false
	}
	for _, ch := range s {
		if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '-') {
			return false
		}
	}
	return true
}

// NewSeed is generated once per run and passed unchanged to all workers.
func NewSeed() (uint64, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, err
	}
	// masscan's integer parser accepts unsigned 64-bit values.
	return binary.BigEndian.Uint64(b[:]), nil
}

func (c Config) PortSpec() string {
	var parts []string
	for i := 0; i < len(c.Ports); i++ {
		first, last := c.Ports[i], c.Ports[i]
		for i+1 < len(c.Ports) && uint32(c.Ports[i+1]) == uint32(last)+1 {
			i++
			last = c.Ports[i]
		}
		part := strconv.Itoa(int(first))
		if last != first {
			part += "-" + strconv.Itoa(int(last))
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, ",")
}
