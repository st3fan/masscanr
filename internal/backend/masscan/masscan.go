// Package masscan implements the default scanner using Alpine's masscan 1.3.2.
package masscan

import (
	"fmt"
	"io"
	"math"
	"net/netip"
	"regexp"
	"strconv"
	"strings"

	"github.com/st3fan/masscanr/internal/backend"
	"github.com/st3fan/masscanr/internal/scan"
)

type Backend struct{}

func (Backend) Name() string    { return "masscan" }
func (Backend) MaxSeed() uint64 { return math.MaxUint64 }
func (Backend) Validate(s scan.Spec, shards int) error {
	if err := s.Validate(); err != nil {
		return err
	}
	if shards < 1 {
		return fmt.Errorf("shards must be positive")
	}
	return nil
}

var versionPattern = regexp.MustCompile(`Masscan version ([^\s]+)`)

func (Backend) Preparation() backend.Preparation {
	return backend.Preparation{
		Packages: []string{"masscan~1.3.2", "coreutils"},
		Probes: []backend.Probe{
			{Command: backend.Command{Executable: "stdbuf", Args: []string{"-oL", "masscan", "--version"}}, ExitCodes: []int{0, 1}},
			{Command: backend.Command{Executable: "sha256sum", Args: []string{"/usr/bin/masscan"}}, ExitCodes: []int{0}},
		},
		Verify: func(outputs []string, installed string) (scan.Identity, error) {
			m := versionPattern.FindStringSubmatch(outputs[0])
			if m == nil {
				return scan.Identity{}, fmt.Errorf("masscan version not recognized")
			}
			if m[1] != "1.3.2" {
				return scan.Identity{}, fmt.Errorf("unsupported masscan version %q; require 1.3.2", m[1])
			}
			fields := strings.Split(installed, ", ")
			if len(fields) != 2 || !strings.HasPrefix(fields[0], "masscan-1.3.2-r") || !strings.HasPrefix(fields[1], "coreutils-") {
				return scan.Identity{}, fmt.Errorf("installed masscan/coreutils package identities were not recognized")
			}
			build, err := backend.BinaryBuild(installed, outputs[1])
			return scan.Identity{Name: "masscan", Version: m[1], Build: build}, err
		},
	}
}
func Arguments(s scan.Spec, shard scan.Shard, seed uint64) ([]string, error) {
	if err := (Backend{}).Validate(s, shard.Total); err != nil {
		return nil, err
	}
	if err := shard.Validate(); err != nil {
		return nil, err
	}
	args := []string{"--conf", "/dev/null", "--ports", s.PortSpec(), "--rate", strconv.FormatUint(s.Rate, 10), "--seed", strconv.FormatUint(seed, 10), "--shards", fmt.Sprintf("%d/%d", shard.Index, shard.Total), "--wait", strconv.FormatInt(int64(s.Wait.Seconds()), 10), "--output-format", "list", "--output-filename", "-", "--open-only", "--nointeractive"}
	for _, p := range s.Targets {
		args = append(args, p.String())
	}
	return args, nil
}
func (Backend) Build(s scan.Spec, shard scan.Shard, seed uint64) (backend.Plan, error) {
	args, err := Arguments(s, shard, seed)
	return backend.Plan{Command: backend.Command{Executable: "stdbuf", Args: append([]string{"-oL", "masscan"}, args...)}, NewDecoder: func() backend.Decoder { return decoder{} }}, err
}

type decoder struct{}

func (decoder) Results(r io.Reader, sink func(scan.Finding) error) error {
	return backend.Records(r, func(line string) error {
		fields := strings.Fields(line)
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			return nil
		}
		if len(fields) != 5 || fields[0] != "open" || fields[1] != "tcp" {
			return fmt.Errorf("invalid masscan list record")
		}
		port, err := strconv.ParseUint(fields[2], 10, 16)
		if err != nil {
			return fmt.Errorf("invalid masscan port")
		}
		ip, err := netip.ParseAddr(fields[3])
		if err != nil || !ip.Is4() {
			return fmt.Errorf("invalid masscan address")
		}
		timestamp, err := strconv.ParseUint(fields[4], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid masscan timestamp")
		}
		return sink(scan.Finding{Address: ip, Port: uint16(port), Timestamp: timestamp, State: "open", Protocol: "tcp"})
	})
}
func (decoder) Status(line string) (scan.Progress, bool, error) {
	p, ok := ParseProgress(line)
	return p, ok, nil
}
