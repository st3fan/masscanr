package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/st3fan/masscanr/internal/controller"
	"github.com/st3fan/masscanr/internal/scan"
	"github.com/st3fan/masscanr/internal/tui"
)

type fixtureView struct {
	config tui.RunConfig
	cancel func()
	start  func()
	stop   func() error
	finish func(context.Context, controller.Summary, error)
	events []controller.Event
}

func (v *fixtureView) Configure(cfg tui.RunConfig, cancel func()) { v.config, v.cancel = cfg, cancel }
func (v *fixtureView) Start() {
	if v.start != nil {
		v.start()
	}
}
func (v *fixtureView) Stop() error {
	if v.stop != nil {
		return v.stop()
	}
	return nil
}
func (v *fixtureView) Update(e controller.Event) { v.events = append(v.events, e) }

func (v *fixtureView) Finish(ctx context.Context, summary controller.Summary, err error) {
	if v.finish != nil {
		v.finish(ctx, summary, err)
	}
}

func TestUIConfigAndExportLifecycle(t *testing.T) {
	t.Setenv("VULTR_API_KEY", "fixture-secret")
	path := filepath.Join(t.TempDir(), "scan.list")
	var stdout, stderr bytes.Buffer
	v := &fixtureView{}
	reviewed := false
	v.finish = func(_ context.Context, summary controller.Summary, err error) {
		reviewed = true
		if err != nil || summary.RunID != "fixture" || strings.Contains(stderr.String(), "Scan complete") {
			t.Fatal("invalid review state", summary, err)
		}
		for _, name := range exportPaths(path) {
			if _, err := os.Stat(name); err != nil {
				t.Fatal("review started before export finished", err)
			}
		}
	}
	stops := 0
	v.stop = func() error {
		stops++
		// UI remains active until conversion finishes.
		for _, name := range exportPaths(path) {
			if _, err := os.Stat(name); err != nil {
				t.Fatal("UI stopped before exports", err)
			}
		}
		if strings.Contains(stderr.String(), "Scan complete") {
			t.Fatal("summary written before terminal restoration")
		}
		return nil
	}
	build := func(string, controller.Timeouts) runFunc {
		return func(_ context.Context, cfg scan.Config, out io.Writer, emit func(controller.Event)) (controller.Summary, error) {
			if v.config.Scan.Targets[0].String() != "192.0.2.0/24" || len(v.config.Scan.Targets) != 1 || v.config.Output != path || v.config.Scan.PortSpec() != "22,443" || v.config.Exports != exportPaths(path) {
				t.Fatal(v.config)
			}
			seed := uint64(7)
			emit(controller.Event{State: "validating", RunID: "fixture", Seed: &seed})
			_, err := io.WriteString(out, exportFixture)
			return controller.Summary{RunID: "fixture", Found: 3}, err
		}
	}
	err := executeScanWithView(t.Context(), []string{"192.0.2.42/24", "192.0.2.0/25", "--ports=22,443", "--output", path}, &stdout, &stderr, build, func(io.Writer, bool, int) scanView { return v })
	if err != nil || !reviewed || stops != 1 || len(v.events) != 2 || v.events[1].State != "exporting" {
		t.Fatal(err, stops, v.events)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "Run complete.") || strings.Contains(stderr.String(), "fixture-secret") {
		t.Fatal(stderr.String())
	}
}

func TestUICancellationPreservesResultsAndReportsUndeleted(t *testing.T) {
	t.Setenv("VULTR_API_KEY", "fixture-secret")
	path := filepath.Join(t.TempDir(), "scan.list")
	var stdout, stderr bytes.Buffer
	v := &fixtureView{}
	v.start = func() { v.cancel() } // Same callback used by the Ctrl+C key.
	v.finish = func(_ context.Context, summary controller.Summary, err error) {
		if !errors.Is(err, context.Canceled) || len(summary.Undeleted) != 1 {
			t.Fatal("incomplete result missing from review", err, summary)
		}
	}
	v.stop = func() error { return errors.New("fixture UI error") }
	build := func(string, controller.Timeouts) runFunc {
		return func(ctx context.Context, _ scan.Config, out io.Writer, emit func(controller.Event)) (controller.Summary, error) {
			if !errors.Is(ctx.Err(), context.Canceled) {
				t.Fatal("UI cancellation did not reach controller")
			}
			emit(controller.Event{Shard: 1, ID: "worker-1", State: "cleanup failed"})
			io.WriteString(out, "# status incomplete\n")
			return controller.Summary{RunID: "fixture", Undeleted: []string{"worker-1"}}, ctx.Err()
		}
	}
	err := executeScanWithView(t.Context(), []string{"192.0.2.0/24", "--output", path}, &stdout, &stderr, build, func(io.Writer, bool, int) scanView { return v })
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "terminal UI") || !strings.Contains(stderr.String(), "VPS deletion unconfirmed: worker-1") || strings.Contains(stderr.String(), "Run complete.") {
		t.Fatal(err, stderr.String())
	}
	for _, name := range exportPaths(path) {
		if _, err := os.Stat(name); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("incomplete scan exported", err)
		}
	}
}

func TestNoTUIAndRedirectedStderrRemainPlain(t *testing.T) {
	t.Setenv("VULTR_API_KEY", "fixture-secret")
	for _, flag := range []string{"", "--no-tui"} {
		var stdout, stderr bytes.Buffer
		args := []string{"192.0.2.0/24", "--output", filepath.Join(t.TempDir(), "scan.list")}
		if flag != "" {
			args = append(args, flag)
		}
		build := func(string, controller.Timeouts) runFunc {
			return func(_ context.Context, _ scan.Config, out io.Writer, emit func(controller.Event)) (controller.Summary, error) {
				emit(controller.Event{Shard: 1, State: "scanning"})
				_, err := io.WriteString(out, exportFixture)
				return controller.Summary{}, err
			}
		}
		if err := executeScan(t.Context(), args, &stdout, &stderr, build); err != nil || strings.Contains(stderr.String(), "\x1b") || !strings.Contains(stderr.String(), "scanning") {
			t.Fatal(err, stderr.String())
		}
	}
}
