package scanfile

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/st3fan/masscanr/internal/scan"
)

func parsePorts(s string) ([]uint16, error) { return scan.ParsePorts(s) }

// Destination retains fsync and destination metadata even when stdout is mirrored.
type Destination struct {
	io.Writer
	SyncFunc func() error
	Output   string
	Exports  [2]string
}

func (d Destination) Sync() error {
	if d.SyncFunc != nil {
		return d.SyncFunc()
	}
	return nil
}
func (d Destination) ScanPaths() (string, [2]string) { return d.Output, d.Exports }

// Writer owns a single bounded buffer and a one-second flush/sync loop. Lifecycle
// barriers sync immediately. The error callback must not call back into Writer.
type Writer struct {
	mu        sync.Mutex
	out       *bufio.Writer
	syncFile  func() error
	state     State
	err       error
	dirty     bool
	closed    bool
	onError   func(error)
	stop      chan struct{}
	done      chan struct{}
	closeOnce sync.Once
}

func NewWriter(out io.Writer, h Header, onError func(error)) (*Writer, error) {
	w := &Writer{out: bufio.NewWriterSize(CheckedWriter{out}, 64<<10), onError: onError, stop: make(chan struct{}), done: make(chan struct{})}
	if s, ok := out.(interface{ Sync() error }); ok {
		w.syncFile = s.Sync
	}
	if err := w.Append(Record{Type: "scan", Scan: &h}, true); err != nil {
		return nil, err
	}
	go func() {
		defer close(w.done)
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-w.stop:
				return
			case <-t.C:
				w.mu.Lock()
				w.flush()
				w.mu.Unlock()
			}
		}
	}()
	return w, nil
}

func (w *Writer) fail(err error) error {
	if err != nil && w.err == nil {
		w.err = fmt.Errorf("scan document: %w", err)
		if w.onError != nil {
			w.onError(w.err)
		}
	}
	return w.err
}

func (w *Writer) flush() error {
	if w.err != nil {
		return w.err
	}
	if !w.dirty {
		return nil
	}
	if err := w.out.Flush(); err != nil {
		return w.fail(err)
	}
	if w.syncFile != nil {
		if err := w.syncFile(); err != nil {
			return w.fail(err)
		}
	}
	w.dirty = false
	return nil
}

func (w *Writer) Append(r Record, barrier bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.append(r, barrier)
}
func (w *Writer) append(r Record, barrier bool) error {
	if w.err != nil {
		return w.err
	}
	if w.closed {
		return fmt.Errorf("scan document is closed")
	}
	r.Seq = w.state.Seq + 1
	r.At = time.Now().UTC()
	data, err := json.Marshal(r)
	if err != nil {
		return w.fail(err)
	}
	if len(data)+1 > MaxRecordBytes {
		return w.fail(fmt.Errorf("record exceeds %d bytes", MaxRecordBytes))
	}
	// Decode our own record so mutable caller slices, maps and pointers cannot
	// change the stored configuration or state after acceptance.
	var owned Record
	if err = json.Unmarshal(data, &owned); err != nil {
		return w.fail(err)
	}
	if err = w.state.Apply(owned); err != nil {
		return w.fail(err)
	}
	data = append(data, '\n')
	n, err := w.out.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return w.fail(err)
	}
	w.dirty = true
	if barrier {
		return w.flush()
	}
	return nil
}

// Worker and Finish derive snapshots under the same lock as findings so final
// counts and per-worker outcomes cannot race with accepted discoveries.
func (w *Writer) Worker(shard int, id, ip, phase, cleanup, detail string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	key := workerKey(Worker{Shard: shard, InstanceID: id})
	state, ok := w.state.Workers[key]
	if !ok {
		state = Worker{Shard: shard, InstanceID: id, Phase: "pending", Cleanup: "pending"}
	}
	if id != "" {
		state.InstanceID = id
	}
	if ip != "" {
		state.IP = ip
	}
	if phase != "" {
		state.Phase = phase
	}
	if cleanup != "" {
		state.Cleanup = cleanup
	}
	state.Detail = detail
	return w.append(Record{Type: "worker", Worker: &state}, true)
}

func (w *Writer) Finish(scanOutcome string, reconciliation bool, detail string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	final := Final{ScanOutcome: scanOutcome, CleanupOutcome: "complete", Found: w.state.Found, Shards: w.state.Outcomes(), Undeleted: w.state.Undeleted(), ReconciliationUnresolved: reconciliation, Error: detail}
	if len(final.Undeleted) > 0 || reconciliation {
		final.CleanupOutcome = "incomplete"
	}
	return w.append(Record{Type: "finished", Finished: &final}, true)
}

func (w *Writer) Err() error { w.mu.Lock(); defer w.mu.Unlock(); return w.err }

func (w *Writer) Close() error {
	w.closeOnce.Do(func() {
		close(w.stop)
		<-w.done
		w.mu.Lock()
		defer w.mu.Unlock()
		w.flush()
		w.closed = true
	})
	return w.Err()
}

// CheckedWriter converts a short write with a nil error into io.ErrShortWrite
// before bufio can mistake it for a successful append.
type CheckedWriter struct{ io.Writer }

func (w CheckedWriter) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	if n != len(p) {
		err = errors.Join(err, io.ErrShortWrite)
	}
	return n, err
}
