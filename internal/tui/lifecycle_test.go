package tui

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/st3fan/masscanr/internal/controller"
	"github.com/st3fan/masscanr/internal/remote"
)

func TestRunMetadataAndInterruptedShards(t *testing.T) {
	v := New(io.Discard, false, 3)
	seed := uint64(42)
	v.Update(controller.Event{State: "validating", RunID: "fixture", Seed: &seed})
	seed = 99
	v.Update(controller.Event{Shard: 1, ID: "w1", State: "scanning", Progress: &remote.Progress{Percent: 50}})
	v.Update(controller.Event{Shard: 2, ID: "w2", State: "failed", Detail: "scanner failed"})
	v.Update(controller.Event{State: "cleanup", Detail: "run interrupted"})
	v.Update(controller.Event{Shard: 1, State: "shutdown"})
	s := v.snapshot()
	if s.RunID != "fixture" || s.Seed != 42 || len(s.rows) != 3 || s.Phase != "Cleanup" || s.rows["1"].State != "cancelled" || s.rows["2"].State != "failed" || s.rows["3"].State != "cancelled" || s.totals(time.Now()).Percent != 50.0/3 {
		t.Fatal(s)
	}
	v.Update(controller.Event{State: "exporting"})
	if v.snapshot().Phase != "Exporting" {
		t.Fatal("export phase missing")
	}
}

func TestBubbleTeaCancellationKeepsLoopAliveForCleanup(t *testing.T) {
	v := New(io.Discard, true, 2)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	v.cancel = cancel
	v.start(tea.WithInput(strings.NewReader("\x03")), tea.WithoutRenderer())
	defer v.Stop()
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Ctrl+C did not cancel")
	}
	select {
	case <-v.done:
		t.Fatal("UI quit before cleanup")
	default:
	}
	v.Update(controller.Event{Shard: 1, ID: "worker", State: "deleting"})
	v.Update(controller.Event{Shard: 1, State: "shutdown"})
	if err := v.Stop(); err != nil {
		t.Fatal(err)
	}
	if v.snapshot().rows["1"].Cleanup != "shutdown" {
		t.Fatal("lost final state")
	}
}

func TestConcurrentUpdatesAndSnapshots(t *testing.T) {
	v := New(io.Discard, true, 8)
	var wg sync.WaitGroup
	for shard := 1; shard <= 8; shard++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 200 {
				p := remote.Progress{Percent: float64(i) / 2, Found: uint64(i)}
				v.Update(controller.Event{Shard: shard, State: "scanning", Progress: &p})
				_ = v.snapshot().totals(time.Now())
			}
			v.Update(controller.Event{Shard: shard, State: "failed", Detail: "preserve final error"})
		}()
	}
	wg.Wait()
	for _, r := range v.snapshot().rows {
		if r.State != "failed" || r.Detail != "preserve final error" || r.Progress.Found != 199 {
			t.Fatal(r)
		}
	}
}

type brokenInput struct{}

func (brokenInput) Read([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestUIRuntimeFailureRequestsControllerCancellation(t *testing.T) {
	v := New(io.Discard, true, 1)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	v.cancel = cancel
	v.start(tea.WithInput(brokenInput{}), tea.WithoutRenderer())
	defer v.Stop()
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("UI failure did not cancel controller")
	}
	if err := v.Stop(); err == nil {
		t.Fatal("UI error was discarded")
	}
}

func TestFinalScreenWaitsForExplicitQuit(t *testing.T) {
	for _, runErr := range []error{nil, io.ErrClosedPipe, context.Canceled} {
		t.Run(fmt.Sprint(runErr), func(t *testing.T) {
			v := New(io.Discard, true, 1)
			input, keys := io.Pipe()
			defer input.Close()
			defer keys.Close()
			finalSeen := make(chan struct{})
			var once sync.Once
			v.start(tea.WithInput(input), tea.WithoutRenderer(), tea.WithFilter(func(_ tea.Model, msg tea.Msg) tea.Msg {
				if frame, ok := msg.(frameMsg); ok && frame.state.Final != nil {
					once.Do(func() { close(finalSeen) })
				}
				return msg
			}))
			defer v.Stop()
			returned := make(chan struct{})
			go func() {
				v.Finish(t.Context(), controller.Summary{RunID: "fixture", Found: 42}, runErr)
				close(returned)
			}()
			select {
			case <-finalSeen:
			case <-time.After(2 * time.Second):
				t.Fatal("final state was not rendered")
			}
			select {
			case <-returned:
				t.Fatal("completed screen exited automatically")
			default:
			}
			s := v.snapshot()
			if s.Final.Found != 42 || (runErr != nil && s.Final.Detail != runErr.Error()) {
				t.Fatal("final summary lost", s.Final)
			}
			if _, err := io.WriteString(keys, "2q"); err != nil {
				t.Fatal(err)
			}
			select {
			case <-returned:
			case <-time.After(2 * time.Second):
				t.Fatal("explicit quit did not dismiss completed UI")
			}
			if err := v.Stop(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
