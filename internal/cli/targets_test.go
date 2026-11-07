package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/st3fan/masscanr/internal/backend"
	"github.com/st3fan/masscanr/internal/controller"
	"github.com/st3fan/masscanr/internal/scan"
	"github.com/st3fan/masscanr/internal/tui"
)

func fixtureResolver(_ context.Context, targets []scan.Target) ([]scan.Target, error) {
	for i := range targets {
		if targets[i].IsAS() {
			targets[i].Prefixes = []netip.Prefix{netip.MustParsePrefix("192.0.0.0/16"), netip.MustParsePrefix("192.0.2.0/24")}
			targets[i].Resolution = &scan.Resolution{Source: scan.RIPEEndpoint, SnapshotAt: time.Unix(90, 0), FetchedAt: time.Unix(100, 0)}
		}
	}
	return targets, nil
}

func TestResolvedScopeBeforeConfirmation(t *testing.T) {
	for _, name := range []string{"masscan", "zmap"} {
		t.Run(name, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "rejected.scan")
			var stdout, stderr bytes.Buffer
			confirmed := false
			confirm := func(_ context.Context, cfg scan.Config, work scan.Workload, _ io.Writer, _ bool) error {
				confirmed = true
				if work.Probes != 6553600 || len(cfg.Targets) != 1 || len(cfg.RequestedTargets) != 2 || cfg.RequestedTargets[0].Target != "AS1234" || len(cfg.RequestedTargets[0].Prefixes) != 2 {
					t.Fatal(cfg, work)
				}
				if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("artifact before confirmation", err)
				}
				return tui.ErrScanDeclined
			}
			err := executeScanWithResolver(t.Context(), []string{"as001234", "192.0.2.42", "--backend=" + name, "--output=" + output}, &stdout, &stderr, noScanner(t), nil, confirm, fixtureResolver)
			if !errors.Is(err, tui.ErrScanDeclined) || !confirmed || stdout.Len() != 0 {
				t.Fatal(err, confirmed, stdout.String())
			}
			if !strings.Contains(stderr.String(), "AS1234") || !strings.Contains(stderr.String(), "192.0.2.0/24") {
				t.Fatal(stderr.String())
			}
		})
	}
}

func TestInvalidLocalOptionsBeforeResolution(t *testing.T) {
	for _, args := range [][]string{
		{"AS1234", "--ports=oops"}, {"AS1234", "--backend=unknown"}, {"AS1234", "--timeout=0"}, {"AS1234", "--cleanup-timeout=-1s"}, {"AS1234", "--shards=0"}, {"AS1234", "--slug=../bad"}, {"AS1234", "--backend=zmap", "--rate=2147483648"}, {"AS1234", "AS4294967296"},
	} {
		var stdout, stderr bytes.Buffer
		err := executeScanWithResolver(t.Context(), args, &stdout, &stderr, noScanner(t), nil, nil, func(context.Context, []scan.Target) ([]scan.Target, error) {
			t.Fatal("resolved invalid local options", args)
			return nil, nil
		})
		if err == nil {
			t.Fatal("accepted", args)
		}
	}
}

func TestResolutionFailureHasNoSideEffects(t *testing.T) {
	t.Setenv("VULTR_API_TOKEN", "")
	path := filepath.Join(t.TempDir(), "not-started.scan")
	var stdout, stderr bytes.Buffer
	failure := errors.New("fixture lookup failed")
	err := executeScanWithResolver(t.Context(), []string{"192.0.2.1", "AS1234", "--output=" + path}, &stdout, &stderr, noScanner(t), nil, func(context.Context, scan.Config, scan.Workload, io.Writer, bool) error {
		t.Fatal("confirmed partial scope")
		return nil
	}, func(context.Context, []scan.Target) ([]scan.Target, error) { return nil, failure })
	if !errors.Is(err, failure) || stdout.Len() != 0 {
		t.Fatal(err, stdout.String())
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}

func TestApprovedMappingIsOwned(t *testing.T) {
	t.Setenv("VULTR_API_TOKEN", "fixture")
	var stdout, stderr bytes.Buffer
	var approved scan.Config
	halt := errors.New("fixture controller halt")
	confirm := func(_ context.Context, cfg scan.Config, _ scan.Workload, _ io.Writer, _ bool) error {
		approved = cfg.Clone()
		cfg.RequestedTargets[0].Prefixes[0] = netip.MustParsePrefix("0.0.0.0/0")
		cfg.RequestedTargets[0].Resolution.Source = "changed"
		cfg.Targets[0] = netip.MustParsePrefix("0.0.0.0/0")
		return nil
	}
	build := func(string, controller.Timeouts, backend.Backend) runFunc {
		return func(_ context.Context, cfg scan.Config, _ io.Writer, _ func(controller.Event)) (controller.Summary, error) {
			if !reflect.DeepEqual(cfg, approved) {
				t.Fatal("mapping changed after confirmation", cfg, approved)
			}
			return controller.Summary{}, halt
		}
	}
	err := executeScanWithResolver(t.Context(), []string{"AS1234", "--output=" + filepath.Join(t.TempDir(), "scan.scan")}, &stdout, &stderr, build, func(io.Writer, bool, int) scanView { return &fixtureView{} }, confirm, fixtureResolver)
	if !errors.Is(err, halt) {
		t.Fatal(err)
	}
}
