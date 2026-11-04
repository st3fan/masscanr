package controller

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/st3fan/masscanr/internal/cloud"
	"github.com/st3fan/masscanr/internal/remote"
	"github.com/st3fan/masscanr/internal/scan"
)

type fakeProvider struct {
	mu               sync.Mutex
	workers          map[string]cloud.Worker
	created, deleted int
	createFail       int
	ambiguous        bool
	deleteFail       bool
	onCreate         func()
}

func newProvider() *fakeProvider {
	return &fakeProvider{workers: map[string]cloud.Worker{"existing": {ID: "existing", IP: "144.202.29.87", Label: "production"}}}
}
func (p *fakeProvider) Validate(context.Context, cloud.Spec) error { return nil }
func (p *fakeProvider) Create(ctx context.Context, s cloud.Spec) (cloud.Worker, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if ctx.Err() != nil {
		return cloud.Worker{}, ctx.Err()
	}
	p.created++
	w := cloud.Worker{ID: fmt.Sprintf("w%d", p.created), IP: fmt.Sprintf("192.0.2.%d", p.created), Password: "secret", Label: cloud.Label, Tags: []string{"masscanr-run-" + s.RunID}, Ready: true}
	p.workers[w.ID] = w
	if p.onCreate != nil {
		p.onCreate()
	}
	if p.created == p.createFail {
		if p.ambiguous {
			return cloud.Worker{}, errors.New("ambiguous create")
		}
		return w, errors.New("create error with ID")
	}
	return w, nil
}
func (p *fakeProvider) Get(ctx context.Context, id string) (cloud.Worker, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	w, ok := p.workers[id]
	if !ok {
		return w, cloud.ErrNotFound
	}
	return w, nil
}
func (p *fakeProvider) List(ctx context.Context, run string) ([]cloud.Worker, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var found []cloud.Worker
	for _, w := range p.workers {
		if w.Label == cloud.Label && slices.Contains(w.Tags, "masscanr-run-"+run) {
			found = append(found, w)
		}
	}
	return found, nil
}
func (p *fakeProvider) Delete(ctx context.Context, w cloud.Worker, run string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if ctx.Err() != nil {
		return fmt.Errorf("cleanup received cancelled context: %w", ctx.Err())
	}
	current, ok := p.workers[w.ID]
	if !ok {
		return nil
	}
	if current.Label != cloud.Label || !slices.Contains(current.Tags, "masscanr-run-"+run) {
		return errors.New("refusing unowned deletion")
	}
	if p.deleteFail {
		return errors.New("delete error")
	}
	delete(p.workers, w.ID)
	p.deleted++
	return nil
}

type fakeRunner struct {
	prepareFail, scanFail, versionMismatch bool
	block                                  bool
}

func (r fakeRunner) Prepare(ctx context.Context, w cloud.Worker) (string, error) {
	if r.prepareFail {
		return "", errors.New("prepare error")
	}
	if r.versionMismatch && w.ID == "w2" {
		return "other", nil
	}
	return "1.3.2", nil
}
func (r fakeRunner) Scan(ctx context.Context, w cloud.Worker, args []string, out io.Writer, progress func(remote.Progress)) error {
	if r.block {
		<-ctx.Done()
		return ctx.Err()
	}
	if !strings.Contains(strings.Join(args, " "), "--seed ") {
		return errors.New("missing seed")
	}
	if _, err := fmt.Fprintf(out, "#masscan\nopen tcp 80 192.0.2.%s ", w.ID[1:]); err != nil {
		return err
	}
	if _, err := fmt.Fprint(out, "1700000000\n# end\n"); err != nil {
		return err
	}
	progress(remote.Progress{Percent: 100, Waiting: true, Found: 1})
	if r.scanFail {
		return errors.New("scan error")
	}
	return ctx.Err()
}

func testConfig() scan.Config {
	c := scan.Defaults()
	c.Targets, _ = scan.ParseTargets([]string{"192.0.2.0/24"})
	return c
}
func testController(p *fakeProvider, r fakeRunner) *Controller {
	timeouts := DefaultTimeouts()
	timeouts.Poll = time.Millisecond
	return &Controller{Provider: p, Runner: r, Timeouts: timeouts}
}

func TestLifecycleAndFailureCleanup(t *testing.T) {
	for _, tc := range []struct {
		name      string
		configure func(*fakeProvider, *fakeRunner)
		fail      bool
	}{
		{"success", func(*fakeProvider, *fakeRunner) {}, false},
		{"prepare", func(_ *fakeProvider, r *fakeRunner) { r.prepareFail = true }, true},
		{"versions", func(_ *fakeProvider, r *fakeRunner) { r.versionMismatch = true }, true},
		{"scan", func(_ *fakeProvider, r *fakeRunner) { r.scanFail = true }, true},
		{"partial-create", func(p *fakeProvider, _ *fakeRunner) { p.createFail = 2 }, true},
		{"ambiguous-create", func(p *fakeProvider, _ *fakeRunner) { p.createFail = 2; p.ambiguous = true }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newProvider()
			r := fakeRunner{}
			tc.configure(p, &r)
			var output bytes.Buffer
			var events []Event
			summary, err := testController(p, r).Run(t.Context(), testConfig(), &output, func(e Event) { events = append(events, e) })
			if (err != nil) != tc.fail {
				t.Fatal(err)
			}
			if len(p.workers) != 1 || p.workers["existing"].IP != "144.202.29.87" || p.deleted != p.created {
				t.Fatal("left workers or touched existing VM", p.created, p.deleted)
			}
			if len(events) == 0 || len(summary.Undeleted) > 0 {
				t.Fatal(summary)
			}
			if !tc.fail && (summary.Found != 2 || strings.Count(output.String(), "open tcp") != 2 || !strings.Contains(output.String(), "# status complete")) {
				t.Fatal(output.String(), summary)
			}
			if tc.fail && !strings.Contains(output.String(), "# status incomplete") {
				t.Fatal(output.String())
			}
		})
	}
}

func TestCancellationAfterCreateAndDuringScan(t *testing.T) {
	t.Run("creating", func(t *testing.T) {
		p := newProvider()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		p.onCreate = cancel
		_, err := testController(p, fakeRunner{}).Run(ctx, testConfig(), io.Discard, nil)
		if err == nil || p.created != 1 || p.deleted != 1 {
			t.Fatal(err, p.created, p.deleted)
		}
	})
	t.Run("scanning", func(t *testing.T) {
		p := newProvider()
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
		defer cancel()
		_, err := testController(p, fakeRunner{block: true}).Run(ctx, testConfig(), io.Discard, nil)
		if err == nil || p.deleted != 2 {
			t.Fatal(err, p.deleted)
		}
	})
}

type failingOutput struct {
	mu    sync.Mutex
	calls int
}

func (w *failingOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls++
	if w.calls > 1 {
		return 0, io.ErrClosedPipe
	}
	return len(p), nil
}

func TestOutputAndDeletionFailure(t *testing.T) {
	p := newProvider()
	_, err := testController(p, fakeRunner{}).Run(t.Context(), testConfig(), &failingOutput{}, nil)
	if err == nil || p.deleted != 2 {
		t.Fatal(err, p.deleted)
	}
	p = newProvider()
	p.deleteFail = true
	summary, err := testController(p, fakeRunner{}).Run(t.Context(), testConfig(), io.Discard, nil)
	if err == nil || !slices.Equal(summary.Undeleted, []string{"w1", "w2"}) || !strings.Contains(err.Error(), "undeleted instance") {
		t.Fatal(summary, err)
	}
}

func TestResultBoundariesAndValidation(t *testing.T) {
	var out bytes.Buffer
	r := &results{out: &out, cfg: testConfig()}
	w1 := &shardWriter{target: r, shard: 1}
	w2 := &shardWriter{target: r, shard: 2}
	w1.Write([]byte("open tcp 80 192.0.2."))
	w2.Write([]byte("open tcp 80 192.0.2.2 1700000001\n"))
	w1.Write([]byte("1 1700000000\n"))
	if strings.Count(out.String(), "open tcp") != 2 || r.count != 2 {
		t.Fatal(out.String())
	}
	for _, bad := range []string{"open tcp 80 198.51.100.1 1\n", "open tcp 65535 192.0.2.1 1\n", "malformed\n"} {
		writer := &shardWriter{target: r, shard: 1}
		if _, err := writer.Write([]byte(bad)); err == nil {
			t.Error("accepted", bad)
		}
	}
}
