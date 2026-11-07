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

	"github.com/st3fan/masscanr/internal/backend"
	"github.com/st3fan/masscanr/internal/backend/masscan"
	"github.com/st3fan/masscanr/internal/cloud"
	"github.com/st3fan/masscanr/internal/scan"
	"github.com/st3fan/masscanr/internal/scanfile"
)

type fakeProvider struct {
	mu               sync.Mutex
	workers          map[string]cloud.Worker
	created, deleted int
	createFail       int
	ambiguous        bool
	deleteFail       bool
	deleteFailures   map[string]int
	deleteCalls      map[string]int
	staleList        bool
	removed          []cloud.Worker
	beforeDelete     func(context.Context, cloud.Worker) error
	onCreate         func()
}

func newProvider() *fakeProvider {
	return &fakeProvider{workers: map[string]cloud.Worker{"existing": {ID: "existing", IP: "144.202.29.87", Label: "production"}}, deleteFailures: make(map[string]int), deleteCalls: make(map[string]int)}
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
	if p.staleList {
		for _, w := range p.removed {
			if slices.Contains(w.Tags, "masscanr-run-"+run) {
				found = append(found, w)
			}
		}
	}
	return found, nil
}
func (p *fakeProvider) Delete(ctx context.Context, w cloud.Worker, run string) error {
	if p.beforeDelete != nil {
		if err := p.beforeDelete(ctx, w); err != nil {
			return err
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if ctx.Err() != nil {
		return fmt.Errorf("cleanup received cancelled context: %w", ctx.Err())
	}
	p.deleteCalls[w.ID]++
	current, ok := p.workers[w.ID]
	if !ok {
		return nil
	}
	if current.Label != cloud.Label || !slices.Contains(current.Tags, "masscanr-run-"+run) {
		return errors.New("refusing unowned deletion")
	}
	if p.deleteFail || p.deleteFailures[w.ID] > 0 {
		if p.deleteFailures[w.ID] > 0 {
			p.deleteFailures[w.ID]--
		}
		return errors.New("delete error")
	}
	delete(p.workers, w.ID)
	p.deleted++
	p.removed = append(p.removed, current)
	return nil
}

type fakeRunner struct {
	prepareFail, scanFail, versionMismatch bool
	buildMismatch, backendMismatch         bool
	block                                  bool
	scan                                   func(context.Context, cloud.Worker, []string, io.Writer, func(scan.Progress)) error
}

func (r fakeRunner) Prepare(ctx context.Context, w cloud.Worker, _ backend.Preparation) (scan.Prepared, error) {
	if r.prepareFail {
		return scan.Prepared{}, errors.New("prepare error")
	}
	if r.versionMismatch && w.ID == "w2" {
		return scan.Prepared{Identity: scan.Identity{Name: "masscan", Version: "other", Build: "fixture"}}, nil
	}
	identity := scan.Identity{Name: "masscan", Version: "1.3.2", Build: "fixture"}
	if r.buildMismatch && w.ID == "w2" {
		identity.Build = "different"
	}
	if r.backendMismatch && w.ID == "w2" {
		identity.Name = "zmap"
	}
	return scan.Prepared{Identity: identity, Release: "3.24.4"}, nil
}
func (r fakeRunner) Scan(ctx context.Context, w cloud.Worker, plan backend.Plan, sink func(scan.Finding) error, progress func(scan.Progress)) error {
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() { err := plan.NewDecoder().Results(reader, sink); reader.CloseWithError(err); done <- err }()
	err := r.legacyScan(ctx, w, plan.Command.Args, writer, progress)
	writer.Close()
	return errors.Join(err, <-done)
}
func (r fakeRunner) legacyScan(ctx context.Context, w cloud.Worker, args []string, out io.Writer, progress func(scan.Progress)) error {
	if r.scan != nil {
		return r.scan(ctx, w, args, out, progress)
	}
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
	progress(scan.Progress{Percent: 100, Waiting: true, Found: 9999})
	if r.scanFail {
		return errors.New("scan error")
	}
	return ctx.Err()
}

func TestCompletedWorkerShutsDownBeforeOtherShardFinishes(t *testing.T) {
	for _, cancelSlow := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancelSlow=%v", cancelSlow), func(t *testing.T) {
			p := newProvider()
			p.staleList = true
			slowStarted := make(chan struct{})
			releaseSlow := make(chan struct{})
			earlyShutdown := make(chan struct{}, 1)
			r := fakeRunner{scan: func(ctx context.Context, w cloud.Worker, args []string, out io.Writer, progress func(scan.Progress)) error {
				if w.ID == "w2" {
					close(slowStarted)
					select {
					case <-releaseSlow:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				return (fakeRunner{}).legacyScan(ctx, w, args, out, progress)
			}}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			var output bytes.Buffer
			var events []Event
			type result struct {
				summary Summary
				err     error
			}
			done := make(chan result, 1)
			go func() {
				summary, err := testController(p, r).Run(ctx, testConfig(), &output, func(e Event) {
					events = append(events, e)
					if e.Shard == 1 && e.State == "shutdown" {
						select {
						case earlyShutdown <- struct{}{}:
						default:
						}
					}
				})
				done <- result{summary, err}
			}()
			for _, signal := range []<-chan struct{}{slowStarted, earlyShutdown} {
				select {
				case <-signal:
				case <-ctx.Done():
					t.Fatal("completed worker was not shut down while its peer remained scanning")
				}
			}
			if ctx.Err() != nil {
				t.Fatal("shutdown waited for scan cancellation")
			}
			p.mu.Lock()
			_, fastExists := p.workers["w1"]
			_, slowExists := p.workers["w2"]
			p.mu.Unlock()
			if fastExists || !slowExists {
				t.Fatal("incorrect worker lifetime", fastExists, slowExists)
			}
			select {
			case <-done:
				t.Fatal("run ended before slow shard was released")
			default:
			}
			if cancelSlow {
				cancel()
			} else {
				close(releaseSlow)
			}
			var got result
			select {
			case got = <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("run did not finish")
			}
			if (got.err != nil) != cancelSlow || len(got.summary.Undeleted) != 0 {
				t.Fatal(got)
			}
			if p.deleted != 2 || p.deleteCalls["w1"] != 1 || p.deleteCalls["w2"] != 1 || len(p.workers) != 1 {
				t.Fatal("duplicate or missed deletion", p.deleteCalls)
			}
			var states []string
			for _, e := range events {
				if e.Shard == 1 {
					states = append(states, e.State)
				}
			}
			index := slices.Index(states, "shutdown")
			if index < 0 || index != len(states)-1 {
				t.Fatal("shutdown state regressed during final cleanup", states)
			}
			want := uint64(2)
			if cancelSlow {
				want = 1
			}
			if got.summary.Found != want {
				t.Fatal(got.summary)
			}
		})
	}
}

func TestImmediateShutdownFailureRetriesAtFinalCleanup(t *testing.T) {
	p := newProvider()
	p.deleteFailures["w1"] = 1
	releaseSlow := make(chan struct{})
	failedShutdown := make(chan struct{}, 1)
	r := fakeRunner{scan: func(ctx context.Context, w cloud.Worker, args []string, out io.Writer, progress func(scan.Progress)) error {
		if w.ID == "w2" {
			select {
			case <-releaseSlow:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return (fakeRunner{}).legacyScan(ctx, w, args, out, progress)
	}}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	type result struct {
		summary Summary
		err     error
	}
	done := make(chan result, 1)
	var events []Event
	go func() {
		summary, err := testController(p, r).Run(ctx, testConfig(), io.Discard, func(e Event) {
			events = append(events, e)
			if e.Shard == 1 && e.State == "cleanup failed" {
				failedShutdown <- struct{}{}
			}
		})
		done <- result{summary, err}
	}()
	select {
	case <-failedShutdown:
	case <-ctx.Done():
		t.Fatal("missing immediate deletion attempt")
	}
	p.mu.Lock()
	_, stillExists := p.workers["w1"]
	p.mu.Unlock()
	if !stillExists {
		t.Fatal("failed deletion was considered confirmed")
	}
	close(releaseSlow)
	var got result
	select {
	case got = <-done:
	case <-ctx.Done():
		t.Fatal("cleanup retry did not finish")
	}
	if got.err != nil || len(got.summary.Undeleted) != 0 || p.deleteCalls["w1"] != 2 || p.deleteCalls["w2"] != 1 {
		t.Fatal(got, p.deleteCalls)
	}
	var states []string
	for _, e := range events {
		if e.Shard == 1 {
			states = append(states, e.State)
		}
	}
	if slices.Index(states, "shutdown") <= slices.Index(states, "cleanup failed") || states[len(states)-1] != "shutdown" {
		t.Fatal(states)
	}
}

func TestShutdownConfirmationSurvivesScanCancellation(t *testing.T) {
	p := newProvider()
	deleting := make(chan struct{}, 2)
	confirm := make(chan struct{})
	shutdownEvents := make(chan struct{}, 2)
	p.beforeDelete = func(ctx context.Context, _ cloud.Worker) error {
		deleting <- struct{}{}
		select {
		case <-confirm:
			return ctx.Err()
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	safety, stop := context.WithTimeout(t.Context(), 5*time.Second)
	defer stop()
	ctx, cancel := context.WithCancel(safety)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := testController(p, fakeRunner{}).Run(ctx, testConfig(), io.Discard, func(e Event) {
			if e.State == "shutdown" {
				shutdownEvents <- struct{}{}
			}
		})
		done <- err
	}()
	for range 2 {
		select {
		case <-deleting:
		case <-safety.Done():
			t.Fatal("deletion did not begin")
		}
	}
	select {
	case <-shutdownEvents:
		t.Fatal("shutdown reported before confirmed deletion")
	default:
	}
	// Both scanners are already complete. Their independent shutdowns must
	// finish even if the user cancels while provider confirmation is pending.
	cancel()
	close(confirm)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-safety.Done():
		t.Fatal("shutdown did not finish")
	}
	if p.deleted != 2 || len(shutdownEvents) != 2 || len(deleting) != 0 {
		t.Fatal("shutdown not confirmed independently", p.deleted, len(shutdownEvents), len(deleting))
	}
}

func testConfig() scan.Config {
	c := scan.Defaults()
	c.Targets, _ = scan.ParseTargets([]string{"192.0.2.0/24"})
	return c
}
func testController(p *fakeProvider, r fakeRunner) *Controller {
	timeouts := DefaultTimeouts()
	timeouts.Poll = time.Millisecond
	return &Controller{Backend: masscan.Backend{}, Provider: p, Runner: r, Timeouts: timeouts}
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
		{"builds", func(_ *fakeProvider, r *fakeRunner) { r.buildMismatch = true }, true},
		{"backends", func(_ *fakeProvider, r *fakeRunner) { r.backendMismatch = true }, true},
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
			if tc.name == "versions" || tc.name == "builds" || tc.name == "backends" {
				for _, event := range events {
					if event.State == "scanning" {
						t.Fatal("scanner started before identity agreement")
					}
				}
			}
			if len(events) == 0 || len(summary.Undeleted) > 0 {
				t.Fatal(summary)
			}
			state, replayErr := scanfile.Replay(strings.NewReader(output.String()), nil)
			if replayErr != nil || state.Finished == nil || state.Complete() == tc.fail || state.Found != summary.Found {
				t.Fatal("invalid run document", replayErr, output.String())
			}
			if !tc.fail && summary.Found != 2 {
				t.Fatal(summary)
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
	if err == nil || p.created != 0 || p.deleted != 0 {
		t.Fatal(err, p.created, p.deleted)
	}
	p = newProvider()
	p.deleteFail = true
	summary, err := testController(p, fakeRunner{}).Run(t.Context(), testConfig(), io.Discard, nil)
	if err == nil || !slices.Equal(summary.Undeleted, []string{"w1", "w2"}) || !strings.Contains(err.Error(), "undeleted instance") {
		t.Fatal(summary, err)
	}
}

func TestRunMetadataAndCollectionEvents(t *testing.T) {
	var events []Event
	summary, err := testController(newProvider(), fakeRunner{}).Run(t.Context(), testConfig(), io.Discard, func(e Event) { events = append(events, e) })
	if err != nil {
		t.Fatal(err)
	}
	first := events[0]
	if first.State != "validating" || first.Shard != 0 || first.RunID != summary.RunID || first.Seed == nil || *first.Seed != summary.Seed {
		t.Fatal("missing early run identity", first)
	}
	for shard := 1; shard <= 2; shard++ {
		collect, complete := -1, -1
		for i, event := range events {
			if event.Shard != shard {
				continue
			}
			if event.State == "collecting" {
				collect = i
			}
			if event.State == "completed" {
				complete = i
			}
		}
		if collect < 0 || complete <= collect {
			t.Fatal("collection must precede completion", events)
		}
	}
}

func TestFailureStatePreservesErrorsJoinedWithCancellation(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{context.Canceled, "cancelled"},
		{fmt.Errorf("wrapped: %w", context.Canceled), "cancelled"},
		{errors.Join(context.Canceled, context.Canceled), "cancelled"},
		{context.DeadlineExceeded, "failed"},
		{errors.Join(io.ErrClosedPipe, context.Canceled), "failed"},
	} {
		if got := failureState(tc.err); got != tc.want {
			t.Fatalf("%v: %s", tc.err, got)
		}
	}
}

func TestAcceptedFindingCountsOverrideNativePackets(t *testing.T) {
	summary, err := testController(newProvider(), fakeRunner{}).Run(t.Context(), testConfig(), io.Discard, func(e Event) {
		// Separate native streams can race: telemetry may arrive before the
		// result decoder accepts its first finding. Zero is honest then.
		if e.Progress != nil && e.Progress.Found > 1 {
			t.Fatal("native packet count reached progress", e.Progress.Found)
		}
	})
	if err != nil || summary.Found != 2 {
		t.Fatal(summary, err)
	}
}
