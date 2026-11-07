package cli

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/st3fan/masscanr/internal/scan"
	"github.com/st3fan/masscanr/internal/scanfile"
)

func attributedFixture(t *testing.T, as bool) string {
	t.Helper()
	var out bytes.Buffer
	for _, line := range strings.Split(strings.TrimSuffix(exportFixture, "\n"), "\n") {
		var r scanfile.Record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatal(err)
		}
		if r.Scan != nil {
			r.Scan.Schema = scanfile.Schema
			if as {
				r.Scan.Config.RequestedTargets, _ = scan.ParseRequestedTargets([]string{"AS1234"})
				r.Scan.Config.RequestedTargets[0].Prefixes = r.Scan.Config.Targets
				r.Scan.Config.RequestedTargets[0].Resolution = &scan.Resolution{Source: scan.RIPEEndpoint, SnapshotAt: time.Unix(90, 0), FetchedAt: time.Unix(100, 0)}
			} else {
				r.Scan.Config.RequestedTargets = scan.DirectTargets(r.Scan.Config.Targets)
			}
		}
		if r.Finding != nil {
			r.Finding.Target = "192.0.2.0/24"
			if as {
				r.Finding.Target, r.Finding.Prefix = "AS1234", "192.0.2.0/24"
			}
		}
		if err := json.NewEncoder(&out).Encode(r); err != nil {
			t.Fatal(err)
		}
	}
	return out.String()
}

func TestProvenanceExportsAcrossSchemasAndFormats(t *testing.T) {
	for name, data := range map[string]string{"legacy": exportFixture, "AS": attributedFixture(t, true), "direct": attributedFixture(t, false)} {
		t.Run(name, func(t *testing.T) {
			var automaticCSV, automaticJSON bytes.Buffer
			if err := writeExports(strings.NewReader(data), &automaticCSV, &automaticJSON); err != nil {
				t.Fatal(err)
			}
			for format, want := range map[string]string{"csv": automaticCSV.String(), "jsonl": automaticJSON.String()} {
				var out bytes.Buffer
				if _, err := writeFormat(strings.NewReader(data), &out, format, false); err != nil || out.String() != want {
					t.Fatal(err, out.String(), want)
				}
			}
			rows, err := csv.NewReader(&automaticCSV).ReadAll()
			if err != nil || len(rows) != 4 || len(rows[0]) != 8 {
				t.Fatal(rows, err)
			}
			target, prefix := "", ""
			if name == "AS" {
				target, prefix = "AS1234", "192.0.2.0/24"
			}
			if name == "direct" {
				target = "192.0.2.0/24"
			}
			for _, row := range rows[1:] {
				if row[6] != target || row[7] != prefix {
					t.Fatal(row)
				}
			}
			for _, line := range strings.Split(strings.TrimSuffix(automaticJSON.String(), "\n"), "\n") {
				var value map[string]json.RawMessage
				if err := json.Unmarshal([]byte(line), &value); err != nil {
					t.Fatal(err)
				}
				if (value["target"] != nil) != (name != "legacy") || (value["prefix"] != nil) != (name == "AS") {
					t.Fatal(line)
				}
			}
			var list bytes.Buffer
			if _, err := writeFormat(strings.NewReader(data), &list, "list", false); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(list.String(), "AS1234") || strings.Count(list.String(), "open tcp") != 3 {
				t.Fatal(list.String())
			}
		})
	}
}
