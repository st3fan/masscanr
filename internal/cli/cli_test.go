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
	"time"

	"github.com/st3fan/masscanr/internal/backend"
	"github.com/st3fan/masscanr/internal/controller"
	"github.com/st3fan/masscanr/internal/scan"
)

func TestCLIFlagsAndResultsWithoutInfrastructure(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("VULTR_API_KEY", "legacy-secret")
	if err := os.WriteFile(".env", []byte("VULTR_API_TOKEN=file-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VULTR_API_TOKEN", "fixture-key")
	path := filepath.Join(t.TempDir(), "scan.list")
	var stdout, stderr bytes.Buffer
	called := false
	factory := func(key string, timeouts controller.Timeouts, _ backend.Backend) runFunc {
		if key != "fixture-key" {
			t.Fatal("wrong key source")
		}
		return func(ctx context.Context, cfg scan.Config, out io.Writer, emit func(controller.Event)) (controller.Summary, error) {
			called = true
			if cfg.PortSpec() != "22,80,443,8000-8100" || cfg.Rate != 500000 || cfg.Shards != 2 || cfg.Region != "atl" || cfg.InstanceType != "vc2-1c-1gb" {
				t.Fatal(cfg)
			}
			if cfg.Targets[0].String() != "192.0.2.0/24" {
				t.Fatal(cfg.Targets)
			}
			emit(controller.Event{Shard: 1, State: "completed"})
			_, err := io.WriteString(out, "open tcp 80 192.0.2.1 1700000000\n# status complete\n")
			return controller.Summary{RunID: "fixture", Found: 1}, err
		}
	}
	err := execute(t.Context(), []string{"scan", "192.0.2.1/24", "--ports=22,80,443,8000-8100", "--output", path}, &stdout, &stderr, factory, nil)
	if err != nil || !called {
		t.Fatal(err, called)
	}
	if strings.Contains(stdout.String()+stderr.String(), "fixture-key") || strings.Contains(stdout.String()+stderr.String(), "file-secret") {
		t.Fatal("credentials leaked to command output")
	}
	data, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(data), "open tcp") || stdout.Len() != 0 || !strings.Contains(stderr.String(), "192.0.2.0/24") {
		t.Fatal(string(data), stdout.String(), stderr.String())
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	for _, export := range exportPaths(path) {
		if _, err := os.Stat(export); err != nil || !strings.Contains(stderr.String(), export) {
			t.Fatal(export, err, stderr.String())
		}
	}
	if err := execute(t.Context(), []string{"scan", "192.0.2.0/24", "--output", path}, &stdout, &stderr, factory, nil); err == nil {
		t.Fatal("overwrote results")
	}
}

func TestCLIValidationDoesNotBuildController(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"--version"}, {}, {"scan", "--help"}, {"help", "scan"}, {"scan"}, {"scan", "::/0"}, {"scan", "192.0.2.0/24", "--shards=0"}, {"scan", "192.0.2.0/24", "--ports=80;reboot"}, {"scan", "--ports"}, {"192.0.2.0/24"}, {"scna"}} {
		var out, errout bytes.Buffer
		build := func(string, controller.Timeouts, backend.Backend) runFunc {
			t.Fatal("controller built before validation")
			return nil
		}
		err := execute(t.Context(), args, &out, &errout, build, nil)
		help := len(args) == 0 || args[0] == "--help" || args[0] == "--version" || args[0] == "help" || len(args) > 1 && args[1] == "--help"
		if (err == nil) != help {
			t.Fatalf("%v: %v", args, err)
		}
	}
}

func TestPartialOutputPreserved(t *testing.T) {
	t.Setenv("VULTR_API_TOKEN", "fixture-key")
	path := filepath.Join(t.TempDir(), "partial.list")
	var stdout, stderr bytes.Buffer
	build := func(string, controller.Timeouts, backend.Backend) runFunc {
		return func(_ context.Context, _ scan.Config, out io.Writer, _ func(controller.Event)) (controller.Summary, error) {
			io.WriteString(out, "# status incomplete\n")
			return controller.Summary{}, errors.New("fixture scan failed")
		}
	}
	if err := execute(t.Context(), []string{"scan", "192.0.2.0/24", "--output", path}, &stdout, &stderr, build, nil); err == nil {
		t.Fatal("success on failure")
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "incomplete") || !strings.Contains(stderr.String(), "Partial results") {
		t.Fatal(err, stderr.String())
	}
	for _, export := range exportPaths(path) {
		if _, err := os.Stat(export); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("exported an incomplete scan", err)
		}
	}
}

func TestScanRequiresEnvironmentTokenBeforeOutputOrController(t *testing.T) {
	for _, tc := range []struct {
		name  string
		token string
		unset bool
	}{
		{name: "unset", unset: true},
		{name: "empty"},
		{name: "whitespace", token: " \t\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			t.Setenv("VULTR_API_TOKEN", tc.token)
			if tc.unset {
				if err := os.Unsetenv("VULTR_API_TOKEN"); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("VULTR_API_KEY", "legacy-secret")
			if err := os.WriteFile(".env", []byte("VULTR_API_TOKEN=file-secret\nVULTR_API_KEY=legacy-secret\n"), 0600); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			build := func(string, controller.Timeouts, backend.Backend) runFunc {
				t.Fatal("controller built without an environment token")
				return nil
			}
			err := executeScan(t.Context(), []string{"192.0.2.0/24", "--output=scan.list"}, &stdout, &stderr, build)
			if err == nil || err.Error() != "VULTR_API_TOKEN is required in the environment" {
				t.Fatalf("unexpected credential error: %v", err)
			}
			if stdout.Len() != 0 || stderr.Len() != 0 {
				t.Fatal("credential failure produced output")
			}
			if _, err := os.Stat("scan.list"); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("output created before credential validation", err)
			}
		})
	}
}

func TestScanRejectsEnvFileFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := executeScan(t.Context(), []string{"192.0.2.0/24", "--env-file=.env"}, &stdout, &stderr, noScanner(t))
	if err == nil || !strings.Contains(err.Error(), "flag provided but not defined: -env-file") {
		t.Fatal("accepted removed credentials option", err)
	}
}

func TestScanHelpDocumentsEnvironmentToken(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := executeScan(t.Context(), []string{"--help"}, &stdout, &stderr, noScanner(t)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "VULTR_API_TOKEN") || strings.Contains(stderr.String(), "--env-file") {
		t.Fatal(stderr.String())
	}
}

func TestOutputFilenameSlugAndUTCTimestamp(t *testing.T) {
	when := time.Date(2026, 11, 4, 23, 29, 52, 86607609, time.FixedZone("EST", -5*60*60))
	for _, tc := range []struct{ slug, want string }{
		{"", "masscanr-20261105T042952.086607609Z.list"},
		{"epag", "masscanr-epag-20261105T042952.086607609Z.list"},
		{"North Office", "masscanr-North Office-20261105T042952.086607609Z.list"},
	} {
		if got := outputFilename(tc.slug, when); got != tc.want {
			t.Errorf("got %q, expected %q", got, tc.want)
		}
	}
}

func TestSlugOutputSelectionWithFakeController(t *testing.T) {
	for _, output := range []string{"", "custom.list", "-"} {
		t.Run("output="+output, func(t *testing.T) {
			t.Chdir(t.TempDir())
			tempDir := t.TempDir()
			t.Setenv("TMPDIR", tempDir)
			t.Setenv("VULTR_API_TOKEN", "fixture-key")
			var stdout, stderr bytes.Buffer
			called := false
			build := func(string, controller.Timeouts, backend.Backend) runFunc {
				return func(_ context.Context, _ scan.Config, out io.Writer, _ func(controller.Event)) (controller.Summary, error) {
					called = true
					_, err := io.WriteString(out, exportFixture)
					return controller.Summary{}, err
				}
			}
			args := []string{"scan", "--slug=office-north", "192.0.2.0/24"}
			if output != "" {
				args = append(args, "--output", output)
			}
			if err := execute(t.Context(), args, &stdout, &stderr, build, nil); err != nil || !called {
				t.Fatal(err)
			}
			files, err := filepath.Glob("*.list")
			if err != nil {
				t.Fatal(err)
			}
			switch output {
			case "-":
				if len(files) != 0 || stdout.String() != exportFixture {
					t.Fatal(files, stdout.String())
				}
			case "custom.list":
				if len(files) != 1 || files[0] != "custom.list" {
					t.Fatal(files)
				}
			default:
				if len(files) != 1 || !strings.HasPrefix(files[0], "masscanr-office-north-") {
					t.Fatal(files)
				}
			}
			for _, extension := range []string{"csv", "jsonl"} {
				exports, err := filepath.Glob("*." + extension)
				if err != nil || len(exports) != 1 {
					t.Fatal(exports, err)
				}
				if output == "custom.list" {
					if exports[0] != "custom."+extension {
						t.Fatal(exports)
					}
				} else if !strings.HasPrefix(exports[0], "masscanr-office-north-") {
					t.Fatal(exports)
				}
			}
			entries, err := os.ReadDir(tempDir)
			if err != nil || len(entries) != 0 {
				t.Fatal("temporary list remains", entries, err)
			}
		})
	}
}

func TestCLIExportFailurePreservesCompletedList(t *testing.T) {
	t.Setenv("VULTR_API_TOKEN", "fixture-key")
	path := filepath.Join(t.TempDir(), "scan.list")
	paths := exportPaths(path)
	if err := os.WriteFile(paths[1], []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	build := func(string, controller.Timeouts, backend.Backend) runFunc {
		return func(_ context.Context, _ scan.Config, out io.Writer, _ func(controller.Event)) (controller.Summary, error) {
			_, err := io.WriteString(out, exportFixture)
			return controller.Summary{Found: 3}, err
		}
	}
	var stdout, stderr bytes.Buffer
	err := execute(t.Context(), []string{"scan", "192.0.2.0/24", "--output", path}, &stdout, &stderr, build, nil)
	if err == nil || !strings.Contains(err.Error(), "scan completed but CSV/JSONL export failed") || !strings.Contains(stderr.String(), "Scan complete") || strings.Contains(stderr.String(), "Incomplete scan") {
		t.Fatal(err, stderr.String())
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != exportFixture {
		t.Fatal("completed list changed", err)
	}
	if _, err := os.Stat(paths[0]); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("partial export remains", err)
	}
}

func TestUnsafeSlugRejectedBeforeControllerConstruction(t *testing.T) {
	for _, slug := range []string{"../escape", "folder/name", "folder\\name", "line\nbreak", "tab\tname", "nul\x00name"} {
		var stdout, stderr bytes.Buffer
		err := execute(t.Context(), []string{"scan", "--slug", slug, "192.0.2.0/24"}, &stdout, &stderr, noScanner(t), nil)
		if err == nil || !strings.Contains(err.Error(), "--slug") {
			t.Fatal("accepted unsafe slug", err)
		}
	}
}
