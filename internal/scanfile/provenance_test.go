package scanfile

import (
	"bytes"
	"encoding/json"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/st3fan/masscanr/internal/scan"
)

func provenanceHeader() Header {
	h := testHeader()
	h.Schema = Schema
	h.ControllerVersion = "v8"
	h.Config.RequestedTargets, _ = scan.ParseRequestedTargets([]string{"AS1234", "192.0.2.1"})
	t := &h.Config.RequestedTargets[0]
	t.Prefixes = []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("192.0.2.0/25")}
	t.Resolution = &scan.Resolution{Source: scan.RIPEEndpoint, SnapshotAt: time.Unix(90, 0), FetchedAt: time.Unix(100, 0)}
	return h
}

func provenanceFixture(t *testing.T) string {
	t.Helper()
	var out bytes.Buffer
	w, err := NewWriter(&out, provenanceHeader(), nil)
	if err != nil {
		t.Fatal(err)
	}
	prepared(t, w)
	f := sampleFinding()
	f.Target, f.Prefix = "AS1234", "192.0.2.0/25"
	for range 2 {
		if err := w.Append(Record{Type: "finding", Finding: &f}, false); err != nil {
			t.Fatal(err)
		}
	}
	complete(t, w)
	return out.String()
}

func alterRecords(t *testing.T, data string, change func(*Record)) string {
	t.Helper()
	var out bytes.Buffer
	for _, line := range strings.Split(strings.TrimSuffix(data, "\n"), "\n") {
		var r Record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatal(err)
		}
		change(&r)
		if err := json.NewEncoder(&out).Encode(r); err != nil {
			t.Fatal(err)
		}
	}
	return out.String()
}

func TestProvenanceReplayAndLegacy(t *testing.T) {
	data := provenanceFixture(t)
	var findings []Finding
	s, err := Replay(strings.NewReader(data), func(f Finding) error { findings = append(findings, f); return nil })
	if err != nil || !s.Complete() || s.Header.Schema != 2 || len(findings) != 2 || findings[0].Target != "AS1234" || findings[0].Prefix != "192.0.2.0/25" {
		t.Fatal(s.Found, findings, err)
	}
	legacy := alterRecords(t, data, func(r *Record) {
		if r.Scan != nil {
			r.Scan.Schema = LegacySchema
		}
	})
	s, err = Replay(strings.NewReader(legacy), func(f Finding) error {
		if f.Target != "" || f.Prefix != "" {
			t.Fatal("trusted legacy extension provenance", f)
		}
		return nil
	})
	if err != nil || !s.Complete() || len(s.Header.Config.RequestedTargets) != 0 {
		t.Fatal(err)
	}
}

func TestRejectCorruptProvenance(t *testing.T) {
	data := provenanceFixture(t)
	changes := map[string]func(*Record){
		"missing mappings": func(r *Record) {
			if r.Scan != nil {
				r.Scan.Config.RequestedTargets = nil
			}
		},
		"broadened worker union": func(r *Record) {
			if r.Scan != nil {
				r.Scan.Config.Targets = []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}
			}
		},
		"changed input identity": func(r *Record) {
			if r.Scan != nil {
				r.Scan.Config.RequestedTargets[0].Input = "AS9999"
			}
		},
		"missing snapshot": func(r *Record) {
			if r.Scan != nil {
				r.Scan.Config.RequestedTargets[0].Resolution = nil
			}
		},
		"future snapshot": func(r *Record) {
			if r.Scan != nil {
				r.Scan.Config.RequestedTargets[0].Resolution.SnapshotAt = time.Unix(101, 0)
			}
		},
		"wrong source": func(r *Record) {
			if r.Scan != nil {
				r.Scan.Config.RequestedTargets[0].Resolution.Source = "https://example.com/"
			}
		},
		"host bits": func(r *Record) {
			if r.Scan != nil {
				r.Scan.Config.RequestedTargets[0].Prefixes[0] = netip.MustParsePrefix("192.0.2.42/24")
			}
		},
		"missing target": func(r *Record) {
			if r.Finding != nil {
				r.Finding.Target = ""
			}
		},
		"wrong target": func(r *Record) {
			if r.Finding != nil {
				r.Finding.Target = "AS9999"
			}
		},
		"wrong first target": func(r *Record) {
			if r.Finding != nil {
				r.Finding.Target = "192.0.2.1/32"
				r.Finding.Prefix = ""
			}
		},
		"missing AS prefix": func(r *Record) {
			if r.Finding != nil {
				r.Finding.Prefix = ""
			}
		},
		"not most specific": func(r *Record) {
			if r.Finding != nil {
				r.Finding.Prefix = "192.0.2.0/24"
			}
		},
		"non-member prefix": func(r *Record) {
			if r.Finding != nil {
				r.Finding.Prefix = "192.0.2.0/26"
			}
		},
		"unsupported schema": func(r *Record) {
			if r.Scan != nil {
				r.Scan.Schema = 99
			}
		},
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			if _, err := Replay(strings.NewReader(alterRecords(t, data, change)), nil); err == nil {
				t.Fatal("accepted corrupt provenance")
			}
		})
	}
}

func TestDirectFindingOmitsPrefixAndRetainsCanonicalTarget(t *testing.T) {
	h := testHeader()
	h.Schema = Schema
	h.Config.RequestedTargets = scan.DirectTargets(h.Config.Targets)
	var out bytes.Buffer
	w, err := NewWriter(&out, h, nil)
	if err != nil {
		t.Fatal(err)
	}
	prepared(t, w)
	f := sampleFinding()
	f.Target = "192.0.2.0/24"
	if err := w.Append(Record{Type: "finding", Finding: &f}, false); err != nil {
		t.Fatal(err)
	}
	complete(t, w)
	if strings.Contains(out.String(), `"prefix":`) {
		t.Fatal("direct finding has prefix")
	}
	if _, err := Replay(&out, nil); err != nil {
		t.Fatal(err)
	}
}

func TestMaximumPrefixMappingFitsAndReplays(t *testing.T) {
	h := provenanceHeader()
	h.Config.RequestedTargets = h.Config.RequestedTargets[:1]
	prefixes := make([]netip.Prefix, scan.MaxResolvedPrefixes)
	for i := range prefixes {
		prefixes[i] = netip.PrefixFrom(netip.AddrFrom4([4]byte{10, byte(i >> 8), byte(i), 1}), 32)
	}
	h.Config.RequestedTargets[0].Prefixes = prefixes
	h.Config.Targets = prefixes
	var out bytes.Buffer
	w, err := NewWriter(&out, h, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if out.Len() > MaxRecordBytes {
		t.Fatal("maximum mapping exceeds record limit", out.Len())
	}
	s, err := Replay(&out, nil)
	if err != nil || len(s.Header.Config.RequestedTargets[0].Prefixes) != scan.MaxResolvedPrefixes {
		t.Fatal(err)
	}
}
