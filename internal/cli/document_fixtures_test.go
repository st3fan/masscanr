package cli

import (
	"bytes"
	"net/netip"
	"strconv"
	"time"

	"github.com/st3fan/masscanr/internal/scanfile"
)

func fixtureFindings() []scanfile.Finding {
	return []scanfile.Finding{
		{Shard: 1, IP: netip.MustParseAddr("192.0.2.1"), Port: 80, Protocol: "tcp", State: "open", Timestamp: 1700000000},
		{Shard: 2, IP: netip.MustParseAddr("192.0.2.1"), Port: 443, Protocol: "tcp", State: "open", Timestamp: 1700000001},
		{Shard: 2, IP: netip.MustParseAddr("192.0.2.1"), Port: 443, Protocol: "tcp", State: "open", Timestamp: 1700000001},
	}
}

// Generate complete offline documents through the actual typed writer. These
// fixtures do not execute the application, scanners or a cloud client.
func scanFixture(name string, findings []scanfile.Finding, outcome string) string {
	var out bytes.Buffer
	h := scanfile.Header{Format: scanfile.Format, Schema: scanfile.LegacySchema, RunID: "fixture", CreatedAt: time.Date(2026, 11, 7, 15, 0, 0, 0, time.UTC), ControllerVersion: "v6", Label: "masscanr", OwnershipTag: "masscanr-run-fixture", Config: scanfile.Config{Targets: []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}, Ports: "80,443", Backend: name, Seed: "7", Shards: 2, Rate: 500000, Provider: "vultr", Region: "atl", InstanceType: "vc2-1c-1gb", Output: "fixture.scan", Timeouts: map[string]string{"create": "1s", "ready": "1s", "prepare": "1s", "scan": "1s", "cleanup": "1s", "delete": "1s", "poll": "1s"}}}
	w, err := scanfile.NewWriter(&out, h, nil)
	must := func(err error) {
		if err != nil {
			panic(err)
		}
	}
	must(err)
	for i := 1; i <= 2; i++ {
		id := "worker-" + strconv.Itoa(i)
		must(w.Worker(i, "", "", "provisioning", "", ""))
		must(w.Worker(i, id, "", "booting", "", ""))
		must(w.Worker(i, id, "", "preparing", "", ""))
		must(w.Append(scanfile.Record{Type: "backend", Backend: &scanfile.Backend{Shard: i, Name: name, Version: "fixture", Build: "fixture"}}, true))
		must(w.Worker(i, id, "", "ready", "", ""))
	}
	for i := 1; i <= 2; i++ {
		must(w.Worker(i, "", "", "scanning", "", ""))
	}
	for i := range findings {
		must(w.Append(scanfile.Record{Type: "finding", Finding: &findings[i]}, false))
	}
	for i := 1; i <= 2; i++ {
		if outcome == "complete" {
			must(w.Worker(i, "", "", "collecting", "", ""))
			must(w.Worker(i, "", "", "completed", "", ""))
		} else {
			must(w.Worker(i, "", "", outcome, "", ""))
		}
		must(w.Worker(i, "", "", "", "deleting", ""))
		must(w.Worker(i, "", "", "", "complete", ""))
	}
	must(w.Finish(outcome, false, ""))
	must(w.Close())
	return out.String()
}

var exportFixture = scanFixture("masscan", fixtureFindings(), "complete")
