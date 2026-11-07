//go:build linux

package integration

import (
	"bytes"
	"context"
	"fmt"
	"github.com/st3fan/masscanr/internal/backend/zmap"
	"github.com/st3fan/masscanr/internal/scan"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func zmapBinary(t *testing.T) string {
	t.Helper()
	program := os.Getenv("ZMAP_TEST_BINARY")
	if program == "" {
		t.Skip("requires isolated packaged ZMap compatibility image")
	}
	return program
}

func TestZMapCapabilities(t *testing.T) {
	program := zmapBinary(t)
	for _, check := range []struct {
		args []string
		want []string
	}{
		{[]string{"--config", "/dev/null", "--disable-syslog", "--version"}, []string{"zmap v4.3.4"}},
		{[]string{"--config", "/dev/null", "--disable-syslog", "--probe-module", "tcp_synscan", "--list-output-fields"}, []string{"saddr", "sport", "timestamp_ts", "success", "classification", "repeat"}},
	} {
		data, err := exec.CommandContext(t.Context(), program, check.args...).Output()
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range check.want {
			if !strings.Contains(string(data), want) {
				t.Fatalf("missing %s: %s", want, data)
			}
		}
	}
	data, err := exec.CommandContext(t.Context(), "apk", "--no-network", "info", "--installed", "-v", "zmap").Output()
	if err != nil || strings.TrimSpace(string(data)) != "zmap-4.3.4-r0" {
		t.Fatal(err, string(data))
	}
	checksum, err := exec.CommandContext(t.Context(), "sha256sum", program).Output()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s; %s", strings.TrimSpace(string(data)), strings.TrimSpace(string(checksum)))
	preparation := (zmap.Backend{}).Preparation()
	outputs := make([]string, len(preparation.Probes))
	for i, probe := range preparation.Probes {
		result, err := exec.CommandContext(t.Context(), probe.Command.Executable, probe.Command.Args...).Output()
		if err != nil {
			t.Fatal(err)
		}
		outputs[i] = string(result)
	}
	if identity, err := preparation.Verify(outputs, strings.TrimSpace(string(data))); err != nil || !identity.Valid() {
		t.Fatal(identity, err)
	}
}

// Explicit network metadata avoids interface discovery/ARP. Dryrun's source
// path uses a TCP metadata socket and prints packets instead of send_batch;
// these checks also run with --network none and --cap-drop ALL. They send no
// probes, including to the documentation-only addresses in these fixtures.
func zmapDryArgs(allow, ports string, shards, shard int, seed uint64) []string {
	return []string{"--config", "/dev/null", "--disable-syslog", "--verbosity", "0", "--dryrun", "--interface", "lo", "--source-ip", "192.0.2.200", "--source-mac", "00:00:00:00:00:01", "--gateway-mac", "00:00:00:00:00:02", "--allowlist-file", allow, "--blocklist-file", "/dev/null", "--target-ports", ports, "--seed", strconv.FormatUint(seed, 10), "--shards", strconv.Itoa(shards), "--shard", strconv.Itoa(shard), "--sender-threads", "1", "--probes", "1", "--rate", "500000", "--cooldown-time", "10", "--output-module", "csv", "--output-fields", "saddr,sport,timestamp_ts,success,classification,repeat", "--output-filter", "success = 1 && repeat = 0", "--dedup-method", "window", "--dedup-window-size", "1000000", "--output-file", "-", "--quiet"}
}

func TestZMapSharding(t *testing.T) {
	program := zmapBinary(t)
	portPattern := regexp.MustCompile(`tcp \{ source: \d+ \| dest: (\d+) \|`)
	ipPattern := regexp.MustCompile(`ip \{ saddr: [^ ]+ \| daddr: ([^ ]+) \|`)
	cases := []struct {
		scope, ports  string
		ips, selected []string
	}{
		{"192.0.2.1/32\n", "80", []string{"192.0.2.1"}, []string{"80"}},
		{"192.0.2.1/32\n192.0.2.4/31\n", "0,22,443", []string{"192.0.2.1", "192.0.2.4", "192.0.2.5"}, []string{"0", "22", "443"}},
		{"192.0.2.0/30\n", "21-23,443,65535", []string{"192.0.2.0", "192.0.2.1", "192.0.2.2", "192.0.2.3"}, []string{"21", "22", "23", "443", "65535"}},
	}
	sawEmpty := false
	for caseID, tc := range cases {
		allow := filepath.Join(t.TempDir(), "targets.txt")
		if err := os.WriteFile(allow, []byte(tc.scope), 0600); err != nil {
			t.Fatal(err)
		}
		for _, seed := range []uint64{0, 42, math.MaxInt64} {
			for _, shards := range []int{1, 2, 3, 5} {
				t.Run(fmt.Sprintf("scope%d/seed%d/shards%d", caseID, seed, shards), func(t *testing.T) {
					seen := map[string]int{}
					for shard := 0; shard < shards; shard++ {
						ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
						args := zmapDryArgs(allow, tc.ports, shards, shard, seed)
						if shards <= len(tc.ips)*len(tc.selected) {
							cfg := scan.Defaults()
							cfg.Targets, _ = scan.ParseTargets(strings.Fields(tc.scope))
							cfg.Ports, _ = scan.ParsePorts(tc.ports)
							plan, err := (zmap.Backend{}).Build(cfg.Intent(), scan.Shard{Index: shard + 1, Total: shards}, seed)
							if err != nil {
								t.Fatal(err)
							}
							if plan.Files[0].Content != tc.scope {
								t.Fatal("adapter changed scope", plan.Files)
							}
							args = append(plan.Command.Args, "--dryrun", "--interface", "lo", "--source-ip", "192.0.2.200", "--source-mac", "00:00:00:00:00:01", "--gateway-mac", "00:00:00:00:00:02", "--verbosity", "0")
						}
						command := exec.CommandContext(ctx, program, args...)
						command.Dir = filepath.Dir(allow)
						var stderr bytes.Buffer
						command.Stderr = &stderr
						data, err := command.Output()
						cancel()
						if shards > len(tc.ips)*len(tc.selected) {
							if err == nil || !strings.Contains(stderr.String(), "senders * shards > allowed probes") {
								t.Fatal("missing tiny-scope rejection", err, stderr.String())
							}
							return
						}
						if err != nil {
							t.Fatal(err, stderr.String())
						}
						ports, ips := portPattern.FindAllStringSubmatch(string(data), -1), ipPattern.FindAllStringSubmatch(string(data), -1)
						if len(ports) == 0 {
							sawEmpty = true
						}
						if len(ports) != len(ips) {
							t.Fatal("unrecognized native packet output", string(data))
						}
						for i := range ports {
							key := ips[i][1] + ":" + ports[i][1]
							if old, exists := seen[key]; exists {
								t.Fatalf("duplicate %s in shards %d and %d", key, old, shard)
							}
							seen[key] = shard
						}
					}
					for _, ip := range tc.ips {
						for _, port := range tc.selected {
							key := ip + ":" + port
							if _, exists := seen[key]; !exists {
								t.Errorf("missing %s", key)
							}
						}
					}
					if len(seen) != len(tc.ips)*len(tc.selected) {
						t.Fatalf("unexpected native coverage: %v", seen)
					}
				})
			}
		}
	}

	if !sawEmpty {
		t.Fatal("native fixture no longer exercises an empty shard")
	}
}
