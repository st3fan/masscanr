package cli

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/st3fan/masscanr/internal/backend"
	"github.com/st3fan/masscanr/internal/cloud"
	"github.com/st3fan/masscanr/internal/controller"
	"github.com/st3fan/masscanr/internal/ripe"
	"github.com/st3fan/masscanr/internal/scan"
	"github.com/st3fan/masscanr/internal/scanfile"
)

type fixtureTransport func(*http.Request) (*http.Response, error)

func (f fixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestV8MixedTargetsThroughRIPEControllerAndOfflineExports(t *testing.T) {
	for _, name := range []string{"masscan", "zmap"} {
		for _, directFirst := range []bool{false, true} {
			for _, stdoutMode := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/directFirst=%v/stdout=%v", name, directFirst, stdoutMode), func(t *testing.T) {
					t.Chdir(t.TempDir())
					t.Setenv("VULTR_API_TOKEN", "fixture-api-token")
					var calls atomic.Int32
					s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls.Add(1)
						if r.URL.Query().Get("resource") != "AS1234" || r.URL.Query().Get("types") != "o" || r.URL.Query().Get("af") != "v4" {
							t.Errorf("unexpected request %s", r.URL)
						}
						fmt.Fprint(w, `{"status":"ok","status_code":200,"data_call_name":"ris-prefixes","version":"1.0","data":{"resource":"1234","query_time":"2026-11-07T08:00:00","list_prefixes":true,"types":["o"],"af":["v4"],"counts":{"v4":{"originating":4}},"prefixes":{"v4":{"originating":["192.0.0.0/16","192.0.2.0/24","192.0.2.0/25","198.51.100.0/24"]}}}}`)
					}))
					defer s.Close()
					local, _ := url.Parse(s.URL)
					transport := s.Client().Transport
					client := ripe.New()
					client.HTTPClient.Transport = fixtureTransport(func(r *http.Request) (*http.Response, error) {
						r = r.Clone(r.Context())
						r.URL.Scheme = local.Scheme
						r.URL.Host = local.Host
						return transport.RoundTrip(r)
					})
					path := "inventory.scan"
					output := path
					if stdoutMode {
						output = "-"
					}
					p := &documentProvider{workers: map[string]cloud.Worker{}}
					p.beforeCreate = func(cloud.Spec) {
						if !stdoutMode {
							data, err := os.ReadFile(path)
							if err != nil {
								t.Error(err)
								return
							}
							state, err := scanfile.Replay(bytes.NewReader(data), nil)
							if err != nil || state.Header.Schema != 2 || len(state.Header.Config.RequestedTargets) != 3 {
								t.Error("missing durable resolved mapping", err)
							}
						}
					}
					confirmed := false
					confirm := func(_ context.Context, cfg scan.Config, work scan.Workload, _ io.Writer, _ bool) error {
						confirmed = true
						if calls.Load() != 1 || work.Probes != 6579200 || len(cfg.Targets) != 2 || len(cfg.RequestedTargets) != 3 {
							t.Fatal(calls.Load(), cfg, work)
						}
						if _, err := os.Stat(path); !os.IsNotExist(err) {
							t.Fatal("artifact before confirmation", err)
						}
						return nil
					}
					build := func(_ string, timeouts controller.Timeouts, engine backend.Backend) runFunc {
						if !confirmed {
							t.Fatal("controller before confirmation")
						}
						return (&controller.Controller{Backend: engine, Provider: p, Runner: documentRunner{name: name}, Timeouts: timeouts}).Run
					}
					args := []string{"as001234", "AS1234", "192.0.2.10", "198.51.100/24", "--backend=" + name, "--output=" + output, "--slug=fixture"}
					if directFirst {
						args[0], args[2] = args[2], args[0]
					}
					var stdout, stderr bytes.Buffer
					err := executeScanWithResolver(t.Context(), args, &stdout, &stderr, build, func(io.Writer, bool, int) scanView { return &fixtureView{} }, confirm, client.Resolve)
					if err != nil || !confirmed || calls.Load() != 1 {
						t.Fatal(err, stderr.String())
					}
					var data []byte
					if stdoutMode {
						data = stdout.Bytes()
						path = filepath.Join(t.TempDir(), "saved.scan")
						if err := os.WriteFile(path, data, 0600); err != nil {
							t.Fatal(err)
						}
					} else {
						data, err = os.ReadFile(path)
						if err != nil {
							t.Fatal(err)
						}
						if stdout.Len() != 0 {
							t.Fatal(stdout.String())
						}
					}
					wantTarget, wantPrefix := "AS1234", "192.0.2.0/25"
					if directFirst {
						wantTarget, wantPrefix = "192.0.2.10/32", ""
					}
					state, err := scanfile.Replay(bytes.NewReader(data), func(f scanfile.Finding) error {
						if f.Target != wantTarget || f.Prefix != wantPrefix {
							t.Fatal(f)
						}
						return nil
					})
					if err != nil || !state.Complete() || state.Found != 4 || len(p.workers) != 0 {
						t.Fatal(state.Found, err, p.workers)
					}
					// Poison HTTP after scanning: all subsequent work must be offline.
					client.HTTPClient.Transport = fixtureTransport(func(*http.Request) (*http.Response, error) { t.Fatal("offline tool contacted RIPE"); return nil, nil })
					t.Setenv("VULTR_API_TOKEN", "")
					var inspected, diagnostics bytes.Buffer
					if err := execute(t.Context(), []string{"inspect", path}, &inspected, &diagnostics, noScanner(t), nil); err != nil || !strings.Contains(inspected.String(), "AS1234") {
						t.Fatal(err, inspected.String())
					}
					for _, format := range []string{"csv", "jsonl", "list"} {
						dest := filepath.Join(t.TempDir(), "offline."+format)
						if err := execute(t.Context(), []string{"export", path, "--format=" + format, "--output=" + dest}, &inspected, &diagnostics, noScanner(t), nil); err != nil {
							t.Fatal(err)
						}
						out, err := os.ReadFile(dest)
						if err != nil {
							t.Fatal(err)
						}
						if format == "csv" {
							rows, err := csv.NewReader(bytes.NewReader(out)).ReadAll()
							if err != nil || len(rows) != 5 || rows[1][6] != wantTarget || rows[1][7] != wantPrefix {
								t.Fatal(rows, err)
							}
						}
						if format == "jsonl" {
							for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
								var f finding
								if err := json.Unmarshal([]byte(line), &f); err != nil || f.Target != wantTarget || f.Prefix != wantPrefix {
									t.Fatal(f, err)
								}
							}
						}
					}
					if calls.Load() != 1 {
						t.Fatal("unexpected repeated RIPE resolution")
					}
				})
			}
		}
	}
}
