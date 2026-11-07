package integration

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/st3fan/masscanr/internal/backend/masscan"
	"github.com/st3fan/masscanr/internal/backend/registry"
	"github.com/st3fan/masscanr/internal/backend/zmap"
	"github.com/st3fan/masscanr/internal/cloud/vultr"
	"github.com/st3fan/masscanr/internal/controller"
	"github.com/st3fan/masscanr/internal/remote"
	"github.com/st3fan/masscanr/internal/scan"
	"github.com/st3fan/masscanr/internal/scanfile"
	"golang.org/x/crypto/ssh"
)

// Exercise HTTP provisioning -> root/password SSH -> both streams -> merged
// output -> confirmed deletion with actual protocol clients and no cloud VMs.
func TestHTTPSSHController(t *testing.T) {
	for _, name := range []string{"masscan", "zmap"} {
		for _, phase := range []string{"", "scan", "release", "install", "result", "cancel", "status"} {
			if phase == "status" && name == "masscan" {
				continue
			}
			failScan := phase != ""
			t.Run(fmt.Sprintf("%s/failure=%s", name, phase), func(t *testing.T) {
				var seeds sync.Map
				var uploaded sync.Map
				port := localSSH(t, func(command string, ch ssh.Channel) uint32 {
					switch {
					case command == "id -u":
						fmt.Fprintln(ch, "0")
					case command == "cat /etc/alpine-release":
						if phase == "release" {
							fmt.Fprintln(ch, "3.25.4")
						} else {
							fmt.Fprintln(ch, "3.24.4")
						}
					case command == "cat /etc/apk/repositories":
						fmt.Fprint(ch, "https://dl-cdn.alpinelinux.org/alpine/v3.24/main\nhttps://dl-cdn.alpinelinux.org/alpine/v3.24/community\n")
					case strings.HasPrefix(command, "exec 'apk'"):
						if name == "zmap" && (!strings.Contains(command, "'zmap=4.3.4-r0'") || strings.Contains(command, "masscan") || strings.Contains(command, "coreutils")) {
							t.Error("incorrect ZMap packages", command)
						}
						if phase == "install" {
							fmt.Fprint(ch.Stderr(), "fixture APK failed")
							return 2
						}
					case command == "'apk' '--no-network' 'info' '--installed' '-v' 'masscan' 'coreutils'":
						fmt.Fprint(ch, "masscan-1.3.2-r2\ncoreutils-9.11-r0\n")
					case command == "'apk' '--no-network' 'info' '--installed' '-v' 'zmap'":
						fmt.Fprintln(ch, "zmap-4.3.4-r0")
					case command == "'sha256sum' '/usr/sbin/zmap'":
						fmt.Fprintln(ch, zmap.BinarySHA256+"  /usr/sbin/zmap")
					case command == "'zmap' '--config' '/dev/null' '--disable-syslog' '--version'":
						fmt.Fprintln(ch, "zmap v4.3.4")
					case strings.Contains(command, "'--list-output-fields'"):
						fmt.Fprint(ch, "saddr string: address\nsport int: port\ntimestamp_ts int: epoch\nsuccess bool: success\nclassification string: result\nrepeat bool: duplicate\n")
					case command == "mktemp -d /tmp/masscanr.XXXXXXXXXX":
						fmt.Fprintln(ch, "/tmp/masscanr.0123456789")
					case strings.HasPrefix(command, "umask 077; cat > "):
						data, _ := io.ReadAll(ch)
						uploaded.Store("scope", string(data))
					case strings.HasPrefix(command, "'rm' '-f'"):
						// Only the fixture's private scope file is selected by remote cleanup.
						if !strings.Contains(command, "'/tmp/masscanr.0123456789/targets.txt'") {
							t.Error(command)
						}
					case command == "'sha256sum' '/usr/bin/masscan'":
						fmt.Fprintln(ch, strings.Repeat("a", 64)+"  /usr/bin/masscan")
					case command == "'stdbuf' '-oL' 'masscan' '--version'":
						fmt.Fprintln(ch, "Masscan version 1.3.2 (fixture)")
						return 1
					default:
						shard := regexp.MustCompile(`'--shards' '([12])/2'`).FindStringSubmatch(command)
						if name == "zmap" {
							shard = regexp.MustCompile(`'--shard' '([01])'`).FindStringSubmatch(command)
							if len(shard) == 2 {
								if shard[1] == "0" {
									shard[1] = "1"
								} else {
									shard[1] = "2"
								}
							}
							scope, _ := uploaded.Load("scope")
							if scope != "192.0.2.0/24\n" {
								t.Error("missing exact scope", scope)
							}
						}
						seed := regexp.MustCompile(`'--seed' '(\d+)'`).FindStringSubmatch(command)
						if len(shard) != 2 || len(seed) != 2 {
							t.Error(command)
							return 2
						}
						seeds.Store(shard[1], seed[1])
						address := "192.0.2." + shard[1]
						if phase == "result" {
							address = "198.51.100.1"
						}
						if name == "masscan" {
							fmt.Fprintf(ch, "#masscan\nopen tcp 80 %s 1700000000\n# end\n", address)
							fmt.Fprint(ch.Stderr(), "rate:500.00-kpps, 100.00% done, waiting 0-secs, found=999\r")
						} else {
							fmt.Fprintf(ch, "%s\n%s,80,1700000000,1,synack,0\n%s,443,1700000001,1,synack,0\n", zmap.Fields, address, address)
							fmt.Fprintln(ch.Stderr(), zmap.StatusHeader)
							if phase == "status" {
								fmt.Fprintln(ch.Stderr(), "2026-11-07 03:00:00,malformed")
							} else {
								fmt.Fprintln(ch.Stderr(), "2026-11-07 03:00:00,1,3,25.000000,0.000000,0,100,0,100,999,999,999,999,999,999,0,0,0,0,,0,0")
							}
						}
						if phase == "scan" && shard[1] == "2" {
							return 3
						}
					}
					return 0
				})
				var mu sync.Mutex
				instances := map[string]map[string]any{"existing": {"id": "existing", "main_ip": "144.202.29.87", "label": "production"}}
				created, deleted := 0, 0
				api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					defer mu.Unlock()
					switch {
					case strings.HasPrefix(r.URL.Path, "/regions/"):
						fmt.Fprint(w, `{"available_plans":["vc2-1c-1gb"]}`)
					case r.URL.Path == "/os":
						fmt.Fprint(w, `{"os":[{"id":999,"name":"Alpine Linux x64","arch":"x64","family":"alpinelinux"}]}`)
					case r.URL.Path == "/plans":
						fmt.Fprint(w, `{"plans":[{"id":"vc2-1c-1gb","type":"vc2","disk":25}]}`)
					case r.URL.Path == "/instances" && r.Method == "POST":
						var input map[string]any
						json.NewDecoder(r.Body).Decode(&input)
						created++
						id := fmt.Sprintf("worker-%d", created)
						instance := map[string]any{"id": id, "main_ip": "127.0.0.1", "default_password": "fixture-password", "label": input["label"], "tags": input["tags"], "status": "active", "power_status": "running", "server_status": "ok"}
						instances[id] = instance
						json.NewEncoder(w).Encode(map[string]any{"instance": instance})
					case r.URL.Path == "/instances":
						var owned []map[string]any
						for id, instance := range instances {
							if id != "existing" {
								owned = append(owned, instance)
							}
						}
						json.NewEncoder(w).Encode(map[string]any{"instances": owned})
					case strings.HasPrefix(r.URL.Path, "/instances/"):
						id := strings.TrimPrefix(r.URL.Path, "/instances/")
						instance, ok := instances[id]
						if !ok {
							http.NotFound(w, r)
							return
						}
						if r.Method == "DELETE" {
							if id == "existing" {
								t.Error("attempted unrelated deletion")
							}
							delete(instances, id)
							deleted++
							w.WriteHeader(204)
							return
						}
						json.NewEncoder(w).Encode(map[string]any{"instance": instance})
					default:
						http.NotFound(w, r)
					}
				}))
				defer api.Close()
				provider := vultr.New("fixture-key")
				provider.BaseURL = api.URL
				provider.HTTP = api.Client()
				runner := remote.NewSSH()
				runner.Port = port
				engine, _ := registry.Resolve(name)
				c := controller.Controller{Backend: engine, Provider: provider, Runner: runner, Timeouts: controller.DefaultTimeouts()}
				cfg := scan.Defaults()
				cfg.Backend = name
				cfg.Targets, _ = scan.ParseTargets([]string{"192.0.2.0/24"})
				var output bytes.Buffer
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				ready := 0
				summary, err := c.Run(ctx, cfg, &output, func(event controller.Event) {
					if phase == "cancel" && event.State == "finding" {
						cancel()
					}
					if event.State == "ready" {
						ready++
						if !strings.Contains(event.Detail, "Alpine 3.24.4") || !(strings.Contains(event.Detail, "masscan-1.3.2-r2") || strings.Contains(event.Detail, "zmap-4.3.4-r0")) {
							t.Error("missing observed preparation identity", event.Detail)
						}
					}
					if event.State == "scanning" && ready != 2 {
						t.Error("scanner started before all workers were prepared")
					}
				})
				if (err != nil) != failScan {
					t.Fatal(err)
				}
				if phase == "release" && (!strings.Contains(err.Error(), "unsupported Alpine release") || ready != 0) {
					t.Fatal("unsupported release proceeded", ready, err)
				}
				if phase == "install" && (!strings.Contains(err.Error(), "fixture APK failed") || ready != 0) {
					t.Fatal("APK failure was lost", ready, err)
				}
				if len(instances) != 1 || instances["existing"] == nil || created != 2 || deleted != 2 {
					t.Fatal("lifecycle invariant failed", created, deleted)
				}
				state, replayErr := scanfile.Replay(strings.NewReader(output.String()), nil)
				if replayErr != nil || state.Finished == nil || state.Complete() == failScan || state.Found != summary.Found {
					t.Fatal("invalid run document", replayErr, output.String())
				}
				if !failScan {
					a, _ := seeds.Load("1")
					b, _ := seeds.Load("2")
					expected := uint64(2)
					if name == "zmap" {
						expected = 4
					}
					if !strings.Contains(output.String(), `"backend":"`+name+`"`) || summary.Identity.Name != name {
						t.Error("backend identity missing", summary, output.String())
					}
					if a == nil || a != b || summary.Found != expected || !state.Complete() {
						t.Fatal(output.String(), summary)
					}
				} else if state.Complete() {
					t.Fatal(output.String())
				}
			})
		}
	}
}

func localSSH(t *testing.T, handle func(string, ssh.Channel) uint32) string {
	t.Helper()
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(key)
	cfg := &ssh.ServerConfig{PasswordCallback: func(meta ssh.ConnMetadata, p []byte) (*ssh.Permissions, error) {
		if meta.User() != "root" || string(p) != "fixture-password" {
			return nil, fmt.Errorf("auth failed")
		}
		return nil, nil
	}}
	cfg.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var connections sync.Map
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			connections.Store(conn, true)
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer conn.Close()
				defer connections.Delete(conn)
				server, channels, requests, err := ssh.NewServerConn(conn, cfg)
				if err != nil {
					return
				}
				defer server.Close()
				go ssh.DiscardRequests(requests)
				for incoming := range channels {
					if incoming.ChannelType() != "session" {
						incoming.Reject(ssh.UnknownChannelType, "session only")
						continue
					}
					ch, requests, err := incoming.Accept()
					if err != nil {
						return
					}
					for req := range requests {
						if req.Type != "exec" {
							req.Reply(false, nil)
							continue
						}
						var payload struct{ Command string }
						ssh.Unmarshal(req.Payload, &payload)
						req.Reply(true, nil)
						status := handle(payload.Command, ch)
						ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
						ch.Close()
						break
					}
				}
			}()
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		connections.Range(func(k, v any) bool { k.(net.Conn).Close(); return true })
		wg.Wait()
	})
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	return port
}

func TestMasscanConfiguration(t *testing.T) {
	binary := os.Getenv("MASSCAN_TEST_BINARY")
	if binary == "" {
		t.Skip("set MASSCAN_TEST_BINARY to validate against native masscan --echo")
	}
	cfg := scan.Defaults()
	cfg.Shards = 3
	cfg.Ports, _ = scan.ParsePorts("22,80,443,8000-8100")
	cfg.Targets, _ = scan.ParseTargets([]string{"192.0.2.0/24"})
	for shard := 1; shard <= cfg.Shards; shard++ {
		args, _ := masscan.Arguments(cfg.Intent(), scan.Shard{Index: shard, Total: cfg.Shards}, 42)
		args = append(args, "--echo")
		data, err := exec.CommandContext(t.Context(), binary, args...).CombinedOutput()
		if err != nil {
			t.Fatal(err, string(data))
		}
		for _, field := range []string{fmt.Sprintf("shard = %d/3", shard), "seed = 42", "rate = 500000.", "ports = 22,80,443,8000-8100", "range = 192.0.2.0/24", "output-format = list"} {
			if !strings.Contains(string(data), field) {
				t.Error("missing", field, string(data))
			}
		}
	}
}

func TestMasscanListOutput(t *testing.T) {
	program := os.Getenv("MASSCAN_TEST_BINARY")
	if program == "" {
		t.Skip("set MASSCAN_TEST_BINARY for a non-network native result round trip")
	}
	// masscan 1.3.2 out-binary.c/out-record.h: 99-byte header, type-6
	// IPv4 open record (13-byte payload), then a 99-byte trailer.
	// Replaying this saved record exercises real stdout formatting without
	// transmitting packets or requiring privileges/cloud infrastructure.
	header := make([]byte, 99)
	copy(header, "masscan/1.1\ns:1700000000\n")
	record := make([]byte, 15)
	record[0] = 6
	record[1] = 13
	binary.BigEndian.PutUint32(record[2:6], 1700000000)
	copy(record[6:10], []byte{192, 0, 2, 1})
	record[10] = 6
	binary.BigEndian.PutUint16(record[11:13], 80)
	record[14] = 64
	trailer := make([]byte, 99)
	copy(trailer, "masscan/1.1")
	path := filepath.Join(t.TempDir(), "fixture.scan")
	if err := os.WriteFile(path, append(append(header, record...), trailer...), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := scan.Defaults()
	cfg.Targets, _ = scan.ParseTargets([]string{"192.0.2.0/24"})
	args, _ := masscan.Arguments(cfg.Intent(), scan.Shard{Index: 1, Total: cfg.Shards}, 42)
	args = append(args, "--readscan", path)
	var output, diagnostics bytes.Buffer
	command := exec.CommandContext(t.Context(), program, args...)
	command.Stdout = &output
	command.Stderr = &diagnostics
	if err := command.Run(); err != nil {
		t.Fatal(err, diagnostics.String())
	}
	var records []string
	for _, line := range strings.Split(output.String(), "\n") {
		if line != "" && !strings.HasPrefix(line, "#") {
			records = append(records, line)
		}
	}
	if len(records) != 1 || records[0] != "open tcp 80 192.0.2.1 1700000000" {
		t.Fatalf("stdout is not pure masscan list output: %q", output.String())
	}
}
