package watch

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPlainReconnectAndSettledFailure(t *testing.T) {
	var calls, mutations atomic.Int32
	c := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			mutations.Add(1)
		}
		n := calls.Add(1)
		if n == 2 {
			w.WriteHeader(503)
			return
		}
		run := fixtureRun("a", n < 3)
		if n >= 3 {
			run.Phase = "cleanup_incomplete"
			run.Summary.ScanOutcome = "failed"
			run.Summary.CleanupOutcome = "incomplete"
			run.Summary.Undeleted = []string{"worker-a"}
		}
		json.NewEncoder(w).Encode(run)
	})
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	s, err := poll(ctx, c, "a", snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err = plain(ctx, c, options{ID: "a", Interval: time.Millisecond}, s, &out)
	if err != nil || ctx.Err() != nil || calls.Load() != 3 || mutations.Load() != 0 {
		t.Fatal(err, ctx.Err(), calls.Load(), mutations.Load())
	}
	for _, want := range []string{"STALE since", "Scan: failed | Cleanup: incomplete", "Undeleted: worker-a"} {
		if !strings.Contains(out.String(), want) {
			t.Fatal(out.String())
		}
	}
}
func TestMultipleViewersOnlyRead(t *testing.T) {
	var mutations atomic.Int32
	c := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			mutations.Add(1)
		}
		json.NewEncoder(w).Encode(fixtureRun("a", false))
	})
	done := make(chan error, 2)
	for range 2 {
		go func() { _, err := poll(t.Context(), c, "a", snapshot{}); done <- err }()
	}
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if mutations.Load() != 0 {
		t.Fatal("viewer mutated service")
	}
}
