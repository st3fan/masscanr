// Package zmap implements the verified Alpine ZMap 4.3.4-r0 TCP SYN backend.
package zmap

import (
	"fmt"
	"math"
	"math/bits"
	"net/netip"
	"strconv"
	"strings"

	"github.com/st3fan/masscanr/internal/backend"
	"github.com/st3fan/masscanr/internal/scan"
)

const Fields = "saddr,sport,timestamp_ts,success,classification,repeat"
const BinarySHA256 = "fee78888264d48120a71df24bef0963f3dbf6c40970460528614eb75e7d3c229"

type Backend struct{}

func (Backend) Name() string    { return "zmap" }
func (Backend) MaxSeed() uint64 { return math.MaxInt64 }

func (Backend) Validate(s scan.Spec, shards int) error {
	if err := s.Validate(); err != nil {
		return err
	}
	if s.Rate > math.MaxInt32 {
		return fmt.Errorf("ZMap rate must be 1..2147483647 packets/second per worker")
	}
	if s.Wait.Seconds() > math.MaxInt32 {
		return fmt.Errorf("ZMap response wait exceeds 2147483647 seconds")
	}
	if shards < 1 || shards > 65535 {
		return fmt.Errorf("ZMap shards must be 1..65535")
	}
	var addresses uint64
	for _, p := range s.Targets {
		addresses += uint64(1) << (32 - p.Bits())
		// Upstream always excludes 0.0.0.0, even with an empty blocklist.
		if p.Contains(netip.IPv4Unspecified()) {
			addresses--
		}
	}
	if addresses == 0 {
		return fmt.Errorf("ZMap scope has no eligible addresses (0.0.0.0 is always excluded)")
	}
	if uint64(shards) > addresses*uint64(len(s.Ports)) {
		return fmt.Errorf("ZMap shards exceed eligible IP/port pairs")
	}
	if bits.Len64(addresses-1)+bits.Len64(uint64(len(s.Ports))-1) <= 8 && shards >= 256 {
		return fmt.Errorf("ZMap small scopes require fewer than 256 shards")
	}
	return nil
}

func (Backend) Preparation() backend.Preparation {
	return backend.Preparation{
		Packages: []string{"zmap=4.3.4-r0"},
		Probes: []backend.Probe{
			{Command: backend.Command{Executable: "zmap", Args: []string{"--config", "/dev/null", "--disable-syslog", "--version"}}, ExitCodes: []int{0}},
			{Command: backend.Command{Executable: "sha256sum", Args: []string{"/usr/sbin/zmap"}}, ExitCodes: []int{0}},
			{Command: backend.Command{Executable: "zmap", Args: []string{"--config", "/dev/null", "--disable-syslog", "--probe-module", "tcp_synscan", "--list-output-fields"}}, ExitCodes: []int{0}},
		},
		Verify: func(outputs []string, installed string) (scan.Identity, error) {
			if strings.TrimSpace(outputs[0]) != "zmap v4.3.4" || installed != "zmap-4.3.4-r0" {
				return scan.Identity{}, fmt.Errorf("unsupported ZMap build; require Alpine zmap-4.3.4-r0")
			}
			build, err := backend.BinaryBuild(installed, outputs[1])
			if err != nil {
				return scan.Identity{}, err
			}
			if strings.Fields(outputs[1])[0] != BinarySHA256 {
				return scan.Identity{}, fmt.Errorf("unsupported ZMap executable checksum for Alpine x64")
			}
			fields := map[string]string{}
			for _, line := range strings.Split(outputs[2], "\n") {
				parts := strings.Fields(line)
				if len(parts) >= 2 {
					fields[parts[0]] = strings.TrimSuffix(parts[1], ":")
				}
			}
			for name, kind := range map[string]string{"saddr": "string", "sport": "int", "timestamp_ts": "int", "success": "bool", "classification": "string", "repeat": "bool"} {
				if fields[name] != kind {
					return scan.Identity{}, fmt.Errorf("ZMap missing expected field %s (%s)", name, kind)
				}
			}
			return scan.Identity{Name: "zmap", Version: "4.3.4", Build: build}, nil
		},
	}
}

func (b Backend) Build(s scan.Spec, shard scan.Shard, seed uint64) (backend.Plan, error) {
	if err := b.Validate(s, shard.Total); err != nil {
		return backend.Plan{}, err
	}
	if err := shard.Validate(); err != nil {
		return backend.Plan{}, err
	}
	if seed > b.MaxSeed() {
		return backend.Plan{}, fmt.Errorf("ZMap seed exceeds 9223372036854775807")
	}
	var allow strings.Builder
	for _, p := range s.Targets {
		fmt.Fprintln(&allow, p)
	}
	args := []string{"--config", "/dev/null", "--disable-syslog", "--allowlist-file", "targets.txt", "--blocklist-file", "/dev/null", "--target-ports", s.PortSpec(), "--rate", strconv.FormatUint(s.Rate, 10), "--seed", strconv.FormatUint(seed, 10), "--shards", strconv.Itoa(shard.Total), "--shard", strconv.Itoa(shard.Index - 1), "--sender-threads", "1", "--probes", "1", "--cooldown-time", strconv.FormatInt(int64(s.Wait.Seconds()), 10), "--probe-module", "tcp_synscan", "--validate-source-port", "enable", "--output-module", "csv", "--output-fields", Fields, "--output-filter", "success = 1 && repeat = 0", "--dedup-method", "window", "--dedup-window-size", "1000000", "--output-file", "-", "--status-updates-file", "/dev/stderr", "--quiet", "--max-sendto-failures", "0"}
	return backend.Plan{Command: backend.Command{Executable: "zmap", Args: args}, Files: []backend.File{{Name: "targets.txt", Content: allow.String()}}, NewDecoder: func() backend.Decoder { return &decoder{} }}, nil
}
