// Package tui renders worker state without sharing the result stream.
package tui

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/st3fan/masscanr/internal/controller"
	"github.com/st3fan/masscanr/internal/scan"
)

// View is the bridge between controller callbacks and the interactive model.
// Callbacks only update in-memory state; terminal I/O happens on a separate loop.
// Configure, Start, Finish, and Stop are called by the CLI in that order.
type View struct {
	mu sync.Mutex
	state
	out        io.Writer
	terminal   bool
	cancel     func()
	stop, done chan struct{}
	stopOnce   sync.Once
	err        error
}

func New(out io.Writer, terminal bool, shards int) *View {
	return &View{out: out, terminal: terminal, state: newState(RunConfig{Scan: scan.Config{Shards: shards}}), stop: make(chan struct{}), done: make(chan struct{})}
}

func (v *View) Configure(cfg RunConfig, cancel func()) {
	v.Config = NewRunConfig(cfg.Scan, controller.Timeouts{Scan: cfg.ScanTimeout, Cleanup: cfg.CleanupTimeout}, cfg.Output, cfg.Exports)
	v.cancel = cancel
}

func (v *View) snapshot() state {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.state.copy()
}

func (v *View) Start() { v.start() }

func (v *View) start(options ...tea.ProgramOption) {
	if !v.terminal {
		close(v.done)
		return
	}
	m := newModel(v.snapshot(), v.snapshot, v.stop, v.cancel)
	options = append([]tea.ProgramOption{tea.WithOutput(v.out), tea.WithoutSignalHandler()}, options...)
	p := tea.NewProgram(m, options...)
	go func() {
		defer close(v.done)
		_, err := p.Run()
		v.mu.Lock()
		v.err = err
		v.mu.Unlock()
		if err != nil && v.cancel != nil {
			v.cancel() // UI failure must still follow the controller's cleanup path.
		}
	}()
}

// Finish publishes an immutable final outcome after scanning, cleanup and export.
// Interactive runs remain navigable until the user quits; plain output returns
// immediately. An external cancellation may dismiss the settled interface.
func (v *View) Finish(ctx context.Context, summary controller.Summary, runErr error) {
	final := &completion{Status: "Complete", Found: summary.Found}
	if runErr != nil {
		final.Status, final.Detail = "Failed", clean(runErr.Error())
	}
	if len(summary.Undeleted) > 0 {
		final.Status = "Failed"
		final.Detail += " VPS deletion unconfirmed: " + clean(strings.Join(summary.Undeleted, ", "))
	}
	v.mu.Lock()
	v.Final, v.Phase = final, final.Status
	if summary.RunID != "" {
		v.RunID = clean(summary.RunID)
	}
	v.mu.Unlock()
	if !v.terminal {
		return
	}
	select {
	case <-v.done:
	case <-ctx.Done():
		v.Stop()
	}
}

// Stop waits for terminal restoration before the CLI writes its final summary.
func (v *View) Stop() error {
	v.stopOnce.Do(func() { close(v.stop) })
	<-v.done
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.err
}

func (v *View) Update(e controller.Event) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if e.Shard == 0 && e.ID == "" {
		v.runEvent(e)
		return
	}
	changed := v.state.apply(e, time.Now())
	if v.Phase != "Cleanup" && v.Phase != "Exporting" {
		v.Phase = runPhase(v.state)
	}
	if !changed || v.terminal {
		return
	}
	key := fmt.Sprint(e.Shard)
	if e.Shard == 0 {
		key = "instance:" + e.ID
	}
	r := v.rows[key]
	fmt.Fprintf(v.out, "shard %d %-14s id=%s ip=%s", e.Shard, clean(e.State), r.ID, r.IP)
	if e.Detail != "" {
		fmt.Fprintf(v.out, " %s", clean(e.Detail))
	}
	fmt.Fprintln(v.out)
}

// Remote diagnostics must not inject terminal escape sequences or new rows.
// Keep all printable text so a long diagnostic remains inspectable by scrolling.
func clean(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsPrint(r) {
			b.WriteRune(r)
		} else {
			b.WriteByte(' ')
		}
	}
	return b.String()
}
