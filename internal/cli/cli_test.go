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
)

func TestCLIFlagsAndResultsWithoutInfrastructure(t *testing.T) {
	t.Setenv("VULTR_API_KEY", "fixture-key")
	path := filepath.Join(t.TempDir(), "scan.list")
	var stdout, stderr bytes.Buffer
	called := false
	factory := func(key string, timeouts controller.Timeouts) runFunc {
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
			_, err := io.WriteString(out, "open tcp 80 192.0.2.1 1700000000\n")
			return controller.Summary{RunID: "fixture", Found: 1}, err
		}
	}
	err := execute(t.Context(), []string{"192.0.2.1/24", "--ports=22,80,443,8000-8100", "--output", path}, &stdout, &stderr, factory)
	if err != nil || !called {
		t.Fatal(err, called)
	}
	data, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(data), "open tcp") || stdout.Len() != 0 || !strings.Contains(stderr.String(), "192.0.2.0/24") {
		t.Fatal(string(data), stdout.String(), stderr.String())
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	if err := execute(t.Context(), []string{"192.0.2.0/24", "--output", path}, &stdout, &stderr, factory); err == nil {
		t.Fatal("overwrote results")
	}
}

func TestCLIValidationDoesNotBuildController(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"--version"}, {}, {"::/0"}, {"192.0.2.0/24", "--shards=0"}, {"192.0.2.0/24", "--ports=80;reboot"}, {"--ports"}} {
		var out, errout bytes.Buffer
		build := func(string, controller.Timeouts) runFunc { t.Fatal("controller built before validation"); return nil }
		err := execute(t.Context(), args, &out, &errout, build)
		help := len(args) > 0 && (args[0] == "--help" || args[0] == "--version")
		if (err == nil) != help {
			t.Fatalf("%v: %v", args, err)
		}
	}
}

func TestPartialOutputPreserved(t *testing.T) {
	t.Setenv("VULTR_API_KEY", "fixture-key")
	path := filepath.Join(t.TempDir(), "partial.list")
	var stdout, stderr bytes.Buffer
	build := func(string, controller.Timeouts) runFunc {
		return func(_ context.Context, _ scan.Config, out io.Writer, _ func(controller.Event)) (controller.Summary, error) {
			io.WriteString(out, "# status incomplete\n")
			return controller.Summary{}, errors.New("fixture scan failed")
		}
	}
	if err := execute(t.Context(), []string{"192.0.2.0/24", "--output", path}, &stdout, &stderr, build); err == nil {
		t.Fatal("success on failure")
	}
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), "incomplete") || !strings.Contains(stderr.String(), "Partial results") {
		t.Fatal(err, stderr.String())
	}
}

func TestDotenvKeyParsing(t *testing.T) {
	previous, exists := os.LookupEnv("VULTR_API_KEY")
	os.Unsetenv("VULTR_API_KEY")
	t.Cleanup(func() {
		if exists {
			os.Setenv("VULTR_API_KEY", previous)
		} else {
			os.Unsetenv("VULTR_API_KEY")
		}
	})
	for _, content := range []string{"VULTR_API_KEY=fixture-key # comment\n", "export VULTR_API_KEY='fixture-key'\n", "VULTR_API_KEY=\"fixture-key\" # comment\n"} {
		path := filepath.Join(t.TempDir(), ".env")
		os.WriteFile(path, []byte(content), 0600)
		key, err := apiKey(path)
		if err != nil || key != "fixture-key" {
			t.Fatal("dotenv parsing failed", err)
		}
	}
	t.Setenv("VULTR_API_KEY", "environment-key")
	if key, err := apiKey("missing-file"); err != nil || key != "environment-key" {
		t.Fatal(err)
	}
}
