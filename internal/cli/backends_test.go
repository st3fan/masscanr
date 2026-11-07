package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/st3fan/masscanr/internal/backend"
	"github.com/st3fan/masscanr/internal/controller"
	"github.com/st3fan/masscanr/internal/scan"
)

func TestBackendSelection(t *testing.T) {
	t.Setenv("VULTR_API_KEY", "fixture")
	var configs []scan.Config
	for _, name := range []string{"", "masscan", "zmap"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "scan.list")
			args := []string{"192.0.2.0/24", "--ports=22,443", "--output", path}
			want := name
			if want == "" {
				want = "masscan"
			} else {
				args = append(args, "--backend", name)
			}
			build := func(_ string, _ controller.Timeouts, engine backend.Backend) runFunc {
				if engine.Name() != want {
					t.Fatalf("selected %s, want %s", engine.Name(), want)
				}
				return func(_ context.Context, cfg scan.Config, out io.Writer, _ func(controller.Event)) (controller.Summary, error) {
					configs = append(configs, cfg)
					_, err := io.WriteString(out, "# backend="+want+"\nopen tcp 443 192.0.2.1 1700000000\n# status complete\n")
					return controller.Summary{Identity: scan.Identity{Name: want, Version: "fixture", Build: "fixture"}}, err
				}
			}
			var out, errout bytes.Buffer
			if err := executeScan(t.Context(), args, &out, &errout, build); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(errout.String(), "Backend: "+want) {
				t.Fatal(errout.String())
			}
			data, err := os.ReadFile(exportPaths(path)[1])
			if err != nil || !strings.Contains(string(data), `"port":443`) {
				t.Fatal(err, string(data))
			}
		})
	}
	if !reflect.DeepEqual(configs[0], configs[1]) {
		t.Fatal("omitted and explicit masscan differ")
	}
}
func TestBackendPreflightBeforeOutputOrCredentials(t *testing.T) {
	for _, opts := range [][]string{{"--backend", "unknown"}, {"--backend", "ZMAP"}, {"--backend", "zmap", "--rate=2147483648"}, {"--backend", "zmap", "--shards=65536"}, {"--backend", "zmap", "--ports=80", "--shards=2"}} {
		path := filepath.Join(t.TempDir(), "scan.list")
		var out, errout bytes.Buffer
		args := append([]string{"192.0.2.1/32", "--env-file=/does/not/exist", "--output", path}, opts...)
		err := executeScan(t.Context(), args, &out, &errout, noScanner(t))
		if err == nil || strings.Contains(err.Error(), "VULTR_API_KEY") {
			t.Fatal("backend preflight did not run first", err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("output created before preflight", err)
		}
	}
}
