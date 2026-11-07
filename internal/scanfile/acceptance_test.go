package scanfile

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLargeDocumentStreaming(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "large-*.scan")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	w, err := NewWriter(file, testHeader(), nil)
	if err != nil {
		t.Fatal(err)
	}
	prepared(t, w)
	f := sampleFinding()
	const count = 100000
	for range count {
		if err := w.Append(Record{Type: "finding", Finding: &f}, false); err != nil {
			t.Fatal(err)
		}
	}
	complete(t, w)
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	var found uint64
	s, err := Replay(file, func(Finding) error { found++; return nil })
	if err != nil || !s.Complete() || found != count || s.Found != count || s.Counts[1] != count || len(s.Workers) != 1 {
		t.Fatal(found, s.Found, err)
	}
}

func TestPeriodicSyncFailureIsSurfacedWithoutAnotherAppend(t *testing.T) {
	f := &failingFile{}
	done := make(chan error, 1)
	w, err := NewWriter(f, testHeader(), func(err error) { done <- err })
	if err != nil {
		t.Fatal(err)
	}
	prepared(t, w)
	w.mu.Lock()
	f.failAt = f.syncs + 1
	w.mu.Unlock()
	finding := sampleFinding()
	if err = w.Append(Record{Type: "finding", Finding: &finding}, false); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if !strings.Contains(err.Error(), "sync failure") {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("periodic persistence failure was not surfaced")
	}
	if err = w.Close(); err == nil {
		t.Fatal("lost asynchronous persistence failure")
	}
	s, replayErr := Replay(strings.NewReader(f.String()), nil)
	if replayErr != nil || s.Finished != nil || s.Found != 1 {
		t.Fatal(s.Found, replayErr)
	}
}

func TestMissingRequiredFieldsAndCaseAliasesAreRejected(t *testing.T) {
	data := fixture(t)
	for _, bad := range []string{
		strings.Replace(data, `"seq":1,`, "", 1),
		strings.Replace(data, `"port":443,`, "", 1),
		strings.Replace(data, `"timestamp":18446744073709551615`, `"timestamp":null`, 1),
		strings.Replace(data, `"type":"scan"`, `"type":"scan","Type":"finding"`, 1),
		strings.Replace(data, `"rate_per_worker":500000`, `"rate_per_worker":500000,"RATE_PER_WORKER":1`, 1),
	} {
		if _, err := Replay(strings.NewReader(bad), nil); err == nil {
			t.Fatal("accepted an ambiguous/missing value")
		}
	}
}

func BenchmarkWriteFindings(b *testing.B) {
	w, err := NewWriter(io.Discard, testHeader(), nil)
	if err != nil {
		b.Fatal(err)
	}
	defer w.Close()
	prepared(b, w)
	f := sampleFinding()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err = w.Append(Record{Type: "finding", Finding: &f}, false); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "records/s")
}

func BenchmarkReplay(b *testing.B) {
	var out bytes.Buffer
	w, err := NewWriter(&out, testHeader(), nil)
	if err != nil {
		b.Fatal(err)
	}
	prepared(b, w)
	f := sampleFinding()
	const count = 10000
	for range count {
		if err = w.Append(Record{Type: "finding", Finding: &f}, false); err != nil {
			b.Fatal(err)
		}
	}
	complete(b, w)
	data := out.Bytes()
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for range b.N {
		s, err := Replay(bytes.NewReader(data), nil)
		if err != nil || s.Found != count {
			b.Fatal(s.Found, err)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(b.N*count)/b.Elapsed().Seconds(), "records/s")
}
