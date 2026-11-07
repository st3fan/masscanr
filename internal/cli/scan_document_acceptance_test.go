package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/st3fan/masscanr/internal/backend"
	"github.com/st3fan/masscanr/internal/cloud"
	"github.com/st3fan/masscanr/internal/controller"
	"github.com/st3fan/masscanr/internal/scan"
	"github.com/st3fan/masscanr/internal/scanfile"
)

type documentProvider struct {
	mu           sync.Mutex
	workers      map[string]cloud.Worker
	beforeCreate func(cloud.Spec)
	failCleanup  bool
}

func (p *documentProvider) Validate(context.Context, cloud.Spec) error { return nil }
func (p *documentProvider) Create(_ context.Context, s cloud.Spec) (cloud.Worker, error) {
	p.beforeCreate(s)
	p.mu.Lock()
	defer p.mu.Unlock()
	w := cloud.Worker{ID: fmt.Sprintf("worker-%d", s.Shard), IP: "192.0.2.200", Password: "worker-password", Label: cloud.Label, Tags: []string{"masscanr-run-" + s.RunID}, Ready: true}
	p.workers[w.ID] = w
	return w, nil
}
func (p *documentProvider) Get(_ context.Context, id string) (cloud.Worker, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	w, ok := p.workers[id]
	if !ok {
		return w, cloud.ErrNotFound
	}
	return w, nil
}
func (p *documentProvider) List(_ context.Context, run string) ([]cloud.Worker, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var result []cloud.Worker
	for _, w := range p.workers {
		if w.Label == cloud.Label && slices.Contains(w.Tags, "masscanr-run-"+run) {
			result = append(result, w)
		}
	}
	return result, nil
}
func (p *documentProvider) Delete(ctx context.Context, w cloud.Worker, run string) error {
	current, err := p.Get(ctx, w.ID)
	if errors.Is(err, cloud.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if current.Label != cloud.Label || !slices.Contains(current.Tags, "masscanr-run-"+run) {
		return errors.New("unowned worker")
	}
	if p.failCleanup {
		return errors.New("fixture deletion failure")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.workers, w.ID)
	return nil
}

type documentRunner struct{ name string }

func (r documentRunner) Prepare(context.Context, cloud.Worker, backend.Preparation) (scan.Prepared, error) {
	return scan.Prepared{Identity: scan.Identity{Name: r.name, Version: "fixture", Build: "fixture"}, Release: "3.24.4", Packages: r.name + "-fixture"}, nil
}
func (r documentRunner) Scan(ctx context.Context, _ cloud.Worker, _ backend.Plan, sink func(scan.Finding) error, progress func(scan.Progress)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	progress(scan.Progress{PercentUnknown: true, RateUnknown: true, ETAUnknown: true})
	f := scan.Finding{Address: netip.MustParseAddr("192.0.2.10"), Port: 443, Protocol: "tcp", State: "open", Timestamp: 1700000000}
	for range 2 {
		if err := sink(f); err != nil {
			return err
		}
	}
	return nil
}

func TestScanDocumentFromCLIThroughControllerAndOfflineTools(t *testing.T) {
	for _, name := range []string{"masscan", "zmap"} {
		for _, cleanupFailure := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/cleanupFailure=%v", name, cleanupFailure), func(t *testing.T) {
				t.Setenv("VULTR_API_TOKEN", "fixture-api-token")
				path := filepath.Join(t.TempDir(), "inventory.scan")
				p := &documentProvider{workers: map[string]cloud.Worker{"existing": {ID: "existing", Label: "production"}}, failCleanup: cleanupFailure}
				p.beforeCreate = func(spec cloud.Spec) {
					data, err := os.ReadFile(path)
					if err != nil {
						t.Error(err)
						return
					}
					state, err := scanfile.Replay(bytes.NewReader(data), nil)
					if err != nil || state.Header == nil || state.Header.RunID != spec.RunID || state.Workers[fmt.Sprint(spec.Shard)].Phase != "provisioning" {
						t.Error("creation was not preceded by a persisted intent", err)
					}
				}
				build := func(_ string, timeouts controller.Timeouts, engine backend.Backend) runFunc {
					return (&controller.Controller{Backend: engine, Provider: p, Runner: documentRunner{name: engine.Name()}, Timeouts: timeouts}).Run
				}
				var out, errout bytes.Buffer
				err := execute(t.Context(), []string{"scan", "192.0.2.42/24", "--ports=443", "--backend=" + name, "--output=" + path}, &out, &errout, build, nil)
				if (err != nil) != cleanupFailure {
					t.Fatal(err)
				}
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				state, err := scanfile.Replay(bytes.NewReader(data), nil)
				if err != nil || !state.Complete() || state.Found != 4 || state.Header.Config.Output != path || state.Header.Config.Exports != exportPaths(path) || state.Header.Config.Ports != "443" || state.Header.Config.Targets[0].String() != "192.0.2.0/24" {
					t.Fatal("document lost effective config or results", err)
				}
				if (state.Finished.CleanupOutcome != "complete") != cleanupFailure {
					t.Fatal(state.Finished)
				}
				for _, secret := range []string{"fixture-api-token", "worker-password"} {
					if strings.Contains(string(data)+errout.String()+out.String(), secret) {
						t.Fatal("credential persisted or logged")
					}
				}
				for _, dest := range exportPaths(path) {
					_, err := os.Stat(dest)
					if cleanupFailure && !errors.Is(err, os.ErrNotExist) || !cleanupFailure && err != nil {
						t.Fatal("automatic export policy", dest, err)
					}
				}
				if _, ok := p.workers["existing"]; !ok {
					t.Fatal("unrelated worker changed")
				}
				if cleanupFailure && (!strings.Contains(errout.String(), "Scan complete") || strings.Contains(errout.String(), "Incomplete scan")) {
					t.Fatal(errout.String())
				}
				t.Setenv("VULTR_API_TOKEN", "")
				out.Reset()
				errout.Reset()
				if err := execute(t.Context(), []string{"inspect", path}, &out, &errout, noScanner(t), nil); err != nil || !strings.Contains(out.String(), "Accepted findings: 4") {
					t.Fatal(err, out.String())
				}
				dest := filepath.Join(t.TempDir(), "findings.list")
				if err := execute(t.Context(), []string{"export", path, "--format=list", "--output=" + dest}, &out, &errout, noScanner(t), nil); err != nil {
					t.Fatal(err)
				}
				if cleanupFailure && !strings.Contains(errout.String(), "Worker cleanup is incomplete") {
					t.Fatal("missing cleanup diagnostic", errout.String())
				}
			})
		}
	}
}
