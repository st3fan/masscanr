package scanfile

import (
	"bytes"
	"errors"
	"io"
	"math"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
)

func testHeader() Header {
	return Header{Format: Format, Schema: Schema, RunID: "fixture", CreatedAt: time.Now().UTC(), ControllerVersion: "v6", Label: "masscanr", OwnershipTag: "masscanr-run-fixture", Config: Config{Targets: []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}, Ports: "80,443", Backend: "masscan", Seed: "18446744073709551615", Shards: 1, Rate: 500000, Provider: "vultr", Region: "atl", InstanceType: "vc2-1c-1gb", Output: "fixture.scan", Timeouts: map[string]string{"create": "1s", "ready": "1s", "prepare": "1s", "scan": "1s", "cleanup": "1s", "delete": "1s", "poll": "1s"}}}
}

func prepared(t testing.TB, w *Writer) {
	t.Helper()
	for _, phase := range []string{"provisioning", "booting", "preparing"} {
		id := "instance"
		if phase == "provisioning" {
			id = ""
		}
		if err := w.Worker(1, id, "", phase, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Append(Record{Type: "backend", Backend: &Backend{Shard: 1, Name: "masscan", Version: "1.3.2", Build: "fixture"}}, true); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"ready", "scanning"} {
		if err := w.Worker(1, "instance", "", phase, "", ""); err != nil {
			t.Fatal(err)
		}
	}
}

func sampleFinding() Finding {
	return Finding{Shard: 1, IP: netip.MustParseAddr("192.0.2.1"), Port: 443, Protocol: "tcp", State: "open", Timestamp: math.MaxUint64}
}

func complete(t testing.TB, w *Writer) {
	t.Helper()
	for _, phase := range []string{"collecting", "completed"} {
		if err := w.Worker(1, "instance", "", phase, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	for _, cleanup := range []string{"deleting", "complete"} {
		if err := w.Worker(1, "instance", "", "", cleanup, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Finish("complete", false, ""); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T) string {
	t.Helper()
	var out bytes.Buffer
	w, err := NewWriter(&out, testHeader(), nil)
	if err != nil {
		t.Fatal(err)
	}
	prepared(t, w)
	for range 2 {
		f := sampleFinding()
		if err := w.Append(Record{Type: "finding", Finding: &f}, false); err != nil {
			t.Fatal(err)
		}
	}
	complete(t, w)
	return out.String()
}

func TestRoundTripAndPartial(t *testing.T) {
	data := fixture(t)
	var findings []Finding
	s, err := Replay(strings.NewReader(data), func(f Finding) error { findings = append(findings, f); return nil })
	if err != nil || !s.Complete() || s.Found != 2 || len(findings) != 2 || findings[0].Timestamp != math.MaxUint64 || s.Header.Config.Seed != "18446744073709551615" {
		t.Fatal(s, findings, err)
	}
	lines := strings.SplitAfter(data, "\n")
	for i := 1; i < len(lines)-1; i++ {
		s, err = Replay(strings.NewReader(strings.Join(lines[:i], "")+`{"type":`), nil)
		if err != nil || s.Complete() || !s.Truncated {
			t.Fatalf("prefix %d: %v %v", i, s, err)
		}
	}
	s, err = Replay(strings.NewReader(data+`{`), nil)
	if err != nil || s.Complete() || !s.Truncated {
		t.Fatal(s, err)
	}
}

func TestRejectCorruption(t *testing.T) {
	data := fixture(t)
	for _, bad := range []string{
		strings.Replace(data, `"schema":1`, `"schema":2`, 1),
		strings.Replace(data, `"seq":2`, `"seq":3`, 1),
		strings.Replace(data, `"type":"scan"`, `"type":"scan","type":"finding"`, 1),
		strings.Replace(data, `"shards":1`, `"shards":999999999`, 1),
		strings.Replace(data, `"ip":"192.0.2.1"`, `"ip":"198.51.100.1"`, 1),
		strings.Replace(data, `"port":443`, `"port":22`, 1),
		strings.Replace(data, `"scan_outcome":"complete"`, `"scan_outcome":"unknown"`, 1),
		strings.Replace(data, `"found":2`, `"found":3`, 1),
		strings.Replace(data, `"phase":"scanning"`, `"phase":"completed"`, 1),
		strings.Replace(data, `"type":"finding"`, `"type":"extension"`, 1),
		data + "{}\n", data + "garbage\n", strings.Repeat("x", MaxRecordBytes) + "\n",
		strings.Replace(data, `"timestamp":18446744073709551615`, `"timestamp":18446744073709551616`, 1),
	} {
		if _, err := Replay(strings.NewReader(bad), nil); err == nil {
			t.Fatal("accepted corrupt document")
		}
	}
}

type failingFile struct {
	bytes.Buffer
	syncs    int
	failAt   int
	writeErr bool
}

func (f *failingFile) Sync() error {
	f.syncs++
	if f.syncs == f.failAt {
		return errors.New("fixture sync failure")
	}
	return nil
}
func (f *failingFile) Write(p []byte) (int, error) {
	if f.writeErr {
		return 0, errors.New("fixture disk failure")
	}
	return f.Buffer.Write(p)
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

func TestPersistenceFailures(t *testing.T) {
	if _, err := NewWriter(shortWriter{}, testHeader(), nil); !errors.Is(err, io.ErrShortWrite) {
		t.Fatal(err)
	}
	f := &failingFile{failAt: 1}
	if _, err := NewWriter(f, testHeader(), nil); err == nil {
		t.Fatal("ignored initial sync failure")
	}
	f = &failingFile{failAt: 2}
	var observed error
	w, err := NewWriter(f, testHeader(), func(err error) { observed = err })
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Worker(1, "", "", "provisioning", "", ""); err == nil || observed == nil {
		t.Fatal("ignored barrier failure", err)
	}
	if err = w.Close(); err == nil {
		t.Fatal("lost sticky error")
	}
}

func TestConcurrentFindingsAndImmutableConfig(t *testing.T) {
	var out bytes.Buffer
	h := testHeader()
	w, err := NewWriter(&out, h, nil)
	if err != nil {
		t.Fatal(err)
	}
	h.Config.Timeouts["scan"] = "bad"
	h.Config.Targets[0] = netip.MustParsePrefix("198.51.100.0/24")
	prepared(t, w)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				f := sampleFinding()
				if err := w.Append(Record{Type: "finding", Finding: &f}, false); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	complete(t, w)
	s, err := Replay(&out, nil)
	if err != nil || s.Found != 800 {
		t.Fatal(s.Found, err)
	}
}
