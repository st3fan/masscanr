package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/st3fan/masscanr/internal/backend"
	"github.com/st3fan/masscanr/internal/controller"
	"github.com/st3fan/masscanr/internal/scan"
	"github.com/st3fan/masscanr/internal/tui"
)

func TestConfirmationBeforeAnyScanSideEffects(t *testing.T) {
	t.Setenv("VULTR_API_TOKEN", "fixture")
	for _, name := range []string{"masscan", "zmap"} {
		for _, plain := range []bool{false, true} {
			for _, result := range []error{nil, tui.ErrScanDeclined, context.Canceled, io.EOF, io.ErrClosedPipe} {
				t.Run(name+"/plain="+strconv.FormatBool(plain)+"/"+fmtError(result), func(t *testing.T) {
					t.Chdir(t.TempDir())
					dir := t.TempDir()
					t.Setenv("TMPDIR", dir)
					var stdout, stderr bytes.Buffer
					var approved scan.Config
					confirmed, built, started := false, false, false
					confirm := func(_ context.Context, cfg scan.Config, work scan.Workload, out io.Writer, noTUI bool) error {
						confirmed = true
						approved = cfg
						if work != (scan.Workload{Addresses: 65536, Ports: 100, Probes: 6553600}) || out != &stderr || noTUI != plain || built || started {
							t.Fatal("wrong confirmation", cfg, work, noTUI)
						}
						entries, err := os.ReadDir(dir)
						if err != nil || len(entries) != 0 || stdout.Len() != 0 {
							t.Fatal("side effects before confirmation", entries, err)
						}
						io.WriteString(out, "fixture confirmation\n")
						return result
					}
					build := func(string, controller.Timeouts, backend.Backend) runFunc {
						built = true
						if !confirmed || result != nil {
							t.Fatal("controller built without approval")
						}
						return func(_ context.Context, cfg scan.Config, out io.Writer, _ func(controller.Event)) (controller.Summary, error) {
							if !reflect.DeepEqual(cfg, approved) {
								t.Fatal("scan configuration changed after approval")
							}
							_, err := io.WriteString(out, scanFixture(name, nil, "complete"))
							return controller.Summary{}, err
						}
					}
					newView := func(io.Writer, bool, int) scanView {
						started = true
						return &fixtureView{}
					}
					args := []string{"192.0.2.42/16", "192.0.2.0/24", "--backend", name, "--output=-"}
					if plain {
						args = append(args, "--no-tui")
					}
					err := executeScanWithConfirmation(t.Context(), args, &stdout, &stderr, build, newView, confirm)
					if !confirmed || !errors.Is(err, result) || built != (result == nil) || started != (result == nil) {
						t.Fatal(err, confirmed, built, started)
					}
					if strings.Contains(stdout.String(), "confirmation") || !strings.Contains(stderr.String(), "fixture confirmation") {
						t.Fatal("prompt contaminated results")
					}
					entries, _ := os.ReadDir(dir)
					if len(entries) != 0 {
						t.Fatal("temporary artifacts retained", entries)
					}
				})
			}
		}
	}
}

func fmtError(err error) string {
	if err == nil {
		return "accept"
	}
	return err.Error()
}

func TestLargeNoninteractiveScanFailsBeforeOutputOrCredentials(t *testing.T) {
	t.Setenv("VULTR_API_TOKEN", "")
	for _, backend := range []string{"masscan", "zmap"} {
		for _, plain := range []string{"", "--no-tui"} {
			path := filepath.Join(t.TempDir(), "scan.scan")
			args := []string{"192.0.0.0/16", "--backend=" + backend, "--output=" + path}
			if plain != "" {
				args = append(args, plain)
			}
			var stdout, stderr bytes.Buffer
			err := executeScan(t.Context(), args, &stdout, &stderr, noScanner(t))
			if err == nil || !strings.Contains(err.Error(), "6,553,600 planned probes (100 TCP ports on 65,536 addresses)") || !strings.Contains(err.Error(), "interactive") {
				t.Fatal(err)
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) || stdout.Len() != 0 {
				t.Fatal("created scan output", err, stdout.String())
			}
		}
	}
}

func TestConfirmationSkippedForSmallOrInvalidScans(t *testing.T) {
	t.Setenv("VULTR_API_TOKEN", "")
	for _, args := range [][]string{
		{"192.0.0.0/16", "--ports=443"},
		{"192.0.0.0/19"},
		{"192.0.0.0/16", "--backend=unknown"},
		{"192.0.0.0/16", "--timeout=0"},
		{"--help"}, {"--version"},
	} {
		var stdout, stderr bytes.Buffer
		executeScanWithConfirmation(t.Context(), args, &stdout, &stderr, noScanner(t), nil,
			func(context.Context, scan.Config, scan.Workload, io.Writer, bool) error {
				t.Fatal("unexpected confirmation", args)
				return nil
			})
	}
}

func TestCancelledConfirmationCannotStartScan(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var stdout, stderr bytes.Buffer
	err := executeScanWithConfirmation(ctx, []string{"192.0.0.0/16"}, &stdout, &stderr, noScanner(t), nil,
		func(context.Context, scan.Config, scan.Workload, io.Writer, bool) error {
			cancel()
			return nil
		})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
