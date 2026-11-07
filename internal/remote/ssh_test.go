package remote

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/st3fan/masscanr/internal/backend"
	"github.com/st3fan/masscanr/internal/backend/masscan"
	"github.com/st3fan/masscanr/internal/cloud"
	"github.com/st3fan/masscanr/internal/scan"
	"golang.org/x/crypto/ssh"
)

func sshFixture(t *testing.T, handle func(string, ssh.Channel) uint32) (*SSH, cloud.Worker) {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	config := &ssh.ServerConfig{PasswordCallback: func(meta ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
		if meta.User() != "root" || string(password) != "test-password" {
			return nil, fmt.Errorf("authentication failed")
		}
		return nil, nil
	}}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var conns sync.Map
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conns.Store(conn, true)
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer conns.Delete(conn)
				defer conn.Close()
				server, channels, requests, err := ssh.NewServerConn(conn, config)
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
					channel, requests, err := incoming.Accept()
					if err != nil {
						return
					}
					for request := range requests {
						if request.Type != "exec" {
							request.Reply(false, nil)
							continue
						}
						var command struct{ Command string }
						if err := ssh.Unmarshal(request.Payload, &command); err != nil {
							t.Error(err)
							return
						}
						request.Reply(true, nil)
						status := handle(command.Command, channel)
						channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
						channel.Close()
						break
					}
				}
			}()
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		conns.Range(func(k, v any) bool { k.(net.Conn).Close(); return true })
		wg.Wait()
	})
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	runner := NewSSH()
	runner.Port = port
	runner.RetryDelay = time.Millisecond
	return runner, cloud.Worker{ID: "fixture", IP: "127.0.0.1", Password: "test-password"}
}

func alpinePreparationFixture(command string, ch ssh.Channel) (bool, uint32) {
	switch {
	case command == "id -u":
		fmt.Fprintln(ch, "0")
	case command == "cat /etc/alpine-release":
		fmt.Fprintln(ch, "3.24.4")
	case command == "cat /etc/apk/repositories":
		fmt.Fprintln(ch, "https://dl-cdn.alpinelinux.org/alpine/v3.24/main")
	case strings.HasPrefix(command, "exec 'apk' '--repositories-file' '/dev/null' '--no-cache'"):
		if !strings.HasSuffix(command, "'add' 'masscan~1.3.2' 'coreutils' >&2") || !strings.Contains(command, "https://dl-cdn.alpinelinux.org/alpine/v3.24/community") {
			return false, 1
		}
	case command == "'sha256sum' '/usr/bin/masscan'":
		fmt.Fprintln(ch, strings.Repeat("a", 64)+"  /usr/bin/masscan")
	case command == "'stdbuf' '-oL' 'masscan' '--version'":
		fmt.Fprintln(ch, "Masscan version 1.3.2 (fixture)")
		return true, 1
	case command == "'apk' '--no-network' 'info' '--installed' '-v' 'masscan' 'coreutils'":
		fmt.Fprint(ch, "masscan-1.3.2-r2\ncoreutils-9.11-r0\n")
	default:
		return false, 0
	}
	return true, 0
}

func TestRootPasswordPrepareAndSeparateStreams(t *testing.T) {
	runner, worker := sshFixture(t, func(command string, ch ssh.Channel) uint32 {
		if handled, status := alpinePreparationFixture(command, ch); handled {
			return status
		}
		switch {
		case strings.HasPrefix(command, "exec 'stdbuf' '-oL' 'masscan' "):
			if !strings.Contains(command, "'1/2'") {
				t.Error(command)
			}
			fmt.Fprint(ch, "open tcp 80 192.0.2.1 1700000000\n")
			fmt.Fprint(ch.Stderr(), "rate:500.00-kpps, 50.00% done, 0:00:01 remaining, found=1\r")
			fmt.Fprint(ch.Stderr(), "rate:0.00-kpps, 100.00% done, waiting 10-secs, found=1\r")
		default:
			t.Error(command)
			return 1
		}
		return 0
	})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	version, err := runner.Prepare(ctx, worker, (masscan.Backend{}).Preparation())
	if err != nil || version.Identity.Version != "1.3.2" || version.Release != "3.24.4" || version.Packages != "masscan-1.3.2-r2, coreutils-9.11-r0" {
		t.Fatal(version, err)
	}
	var out bytes.Buffer
	var updates []scan.Progress
	plan := fixturePlan()
	plan.Command.Args = append(plan.Command.Args, "--shards", "1/2")
	err = runner.Scan(ctx, worker, plan, func(f scan.Finding) error {
		_, err := fmt.Fprintf(&out, "open tcp %d %s %d\n", f.Port, f.Address, f.Timestamp)
		return err
	}, func(p scan.Progress) { updates = append(updates, p) })
	if err != nil {
		t.Fatal(err)
	}
	if out.String() != "open tcp 80 192.0.2.1 1700000000\n" || len(updates) != 2 || !updates[1].Waiting || updates[0].Rate != 500000 {
		t.Fatal(out.String(), updates)
	}
}

func TestPrepareRejectsUnexpectedVersionFailure(t *testing.T) {
	var versionChecked atomic.Bool
	runner, worker := sshFixture(t, func(command string, ch ssh.Channel) uint32 {
		if command == "'stdbuf' '-oL' 'masscan' '--version'" {
			versionChecked.Store(true)
			fmt.Fprintln(ch, "Masscan version 1.3.2")
			return 2
		}
		if handled, status := alpinePreparationFixture(command, ch); handled {
			return status
		}
		t.Error(command)
		return 1
	})
	if _, err := runner.Prepare(t.Context(), worker, (masscan.Backend{}).Preparation()); err == nil || !versionChecked.Load() {
		t.Fatal("unexpected version-command exit status not checked", err)
	}
}

func TestRemoteExitAndCancellation(t *testing.T) {
	t.Run("exit", func(t *testing.T) {
		r, w := sshFixture(t, func(_ string, ch ssh.Channel) uint32 { fmt.Fprint(ch.Stderr(), "fixture error\n"); return 3 })
		err := r.Scan(t.Context(), w, fixturePlan(), func(scan.Finding) error { return nil }, nil)
		if err == nil || !strings.Contains(err.Error(), "fixture error") {
			t.Fatal(err)
		}
	})
	t.Run("cancel", func(t *testing.T) {
		r, w := sshFixture(t, func(_ string, _ ssh.Channel) uint32 { <-t.Context().Done(); return 1 })
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()
		start := time.Now()
		err := r.Scan(ctx, w, fixturePlan(), func(scan.Finding) error { return nil }, nil)
		if err == nil || time.Since(start) > time.Second {
			t.Fatal(err, time.Since(start))
		}
	})
}

func TestHostKeyPinning(t *testing.T) {
	r, w := sshFixture(t, func(_ string, ch ssh.Channel) uint32 { fmt.Fprint(ch, "0"); return 0 })
	if err := r.command(t.Context(), w, "id -u", io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	r.keys[w.ID] = []byte("different-key")
	r.mu.Unlock()
	err := r.command(t.Context(), w, "id -u", io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "host key changed") {
		t.Fatal(err)
	}
}

func TestProgressRecordsAndShellQuoting(t *testing.T) {
	var lines []string
	err := backend.Records(strings.NewReader("one\rtwo\r\nthree"), func(line string) error { lines = append(lines, line); return nil })
	if err != nil || strings.Join(lines, ",") != "one,two,three" {
		t.Fatal(lines, err)
	}
	if _, ok := masscan.ParseProgress("rate: nan-kpps, 10% done"); ok {
		t.Fatal("accepted invalid status")
	}
	if got := QuoteArgs([]string{"x'y", "$(reboot)"}); got != "'x'\\''y' '$(reboot)'" {
		t.Fatal(got)
	}
}

func TestPrepareRejectsPlatformDriftBeforeInstalling(t *testing.T) {
	const stable = "https://dl-cdn.alpinelinux.org/alpine/v3.24/main\n"
	for _, tc := range []struct{ name, command, response, want string }{
		{"earlier-patch", "cat /etc/alpine-release", "3.24.3\n", "unsupported Alpine release"},
		{"other-series", "cat /etc/alpine-release", "3.25.4\n", "unsupported Alpine release"},
		{"non-Alpine", "cat /etc/alpine-release", "Debian 13\n", "unsupported Alpine release"},
		{"edge", "cat /etc/apk/repositories", stable + "https://dl-cdn.alpinelinux.org/alpine/edge/community\n", "outside stable"},
		{"missing-main", "cat /etc/apk/repositories", "", "main repository is required"},
		{"credential", "cat /etc/apk/repositories", "https://user:private-token@mirror.example/alpine/v3.24/main\n", "credential-free"},
		{"oversized-policy", "cat /etc/apk/repositories", "https://mirror.example/alpine/edge/main\n" + strings.Repeat("# comment\n", 8000) + stable, "exceeds 64 KiB"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var installed atomic.Bool
			r, w := sshFixture(t, func(command string, ch ssh.Channel) uint32 {
				if command == tc.command {
					fmt.Fprint(ch, tc.response)
					return 0
				}
				if strings.HasPrefix(command, "exec 'apk'") {
					installed.Store(true)
				}
				if handled, status := alpinePreparationFixture(command, ch); handled {
					return status
				}
				t.Error(command)
				return 1
			})
			_, err := r.Prepare(t.Context(), w, (masscan.Backend{}).Preparation())
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "private-token") || installed.Load() {
				t.Fatal("policy not enforced before installation", err, installed.Load())
			}
		})
	}
}

func TestPrepareReportsAPKAndScannerFailures(t *testing.T) {
	for _, phase := range []string{"install", "version", "package-info"} {
		t.Run(phase, func(t *testing.T) {
			r, w := sshFixture(t, func(command string, ch ssh.Channel) uint32 {
				if phase == "install" && strings.HasPrefix(command, "exec 'apk'") {
					fmt.Fprint(ch.Stderr(), "fixture package unavailable")
					return 2
				}
				if phase == "version" && command == "'stdbuf' '-oL' 'masscan' '--version'" {
					fmt.Fprintln(ch, "Masscan version 1.3.3")
					return 1
				}
				if phase == "package-info" && command == "'apk' '--no-network' 'info' '--installed' '-v' 'masscan' 'coreutils'" {
					return 0
				}
				if handled, status := alpinePreparationFixture(command, ch); handled {
					return status
				}
				t.Error(command)
				return 1
			})
			_, err := r.Prepare(t.Context(), w, (masscan.Backend{}).Preparation())
			if err == nil {
				t.Fatal("accepted invalid preparation")
			}
			if phase == "install" && !strings.Contains(err.Error(), "fixture package unavailable") {
				t.Fatal(err)
			}
			if phase == "version" && !strings.Contains(err.Error(), "unsupported masscan version") {
				t.Fatal(err)
			}
		})
	}
}

func TestPreparationCancellationDuringAPK(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	r, w := sshFixture(t, func(command string, ch ssh.Channel) uint32 {
		if strings.HasPrefix(command, "exec 'apk'") {
			close(entered)
			<-release
			return 1
		}
		if handled, status := alpinePreparationFixture(command, ch); handled {
			return status
		}
		t.Error(command)
		return 1
	})
	done := make(chan error, 1)
	go func() { _, err := r.Prepare(ctx, w, (masscan.Backend{}).Preparation()); done <- err }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("APK was not started")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("preparation did not cancel promptly")
	}
}

func fixturePlan() backend.Plan {
	cfg := scan.Defaults()
	cfg.Targets, _ = scan.ParseTargets([]string{"192.0.2.0/24"})
	plan, _ := (masscan.Backend{}).Build(cfg.Intent(), scan.Shard{Index: 1, Total: 2}, 42)
	return plan
}

func TestDecoderAndSinkFailureCancelRemote(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		t.Run(fmt.Sprint(malformed), func(t *testing.T) {
			release := make(chan struct{})
			defer close(release)
			r, w := sshFixture(t, func(_ string, ch ssh.Channel) uint32 {
				if malformed {
					fmt.Fprintln(ch, "invalid result")
				} else {
					fmt.Fprintln(ch, "open tcp 80 192.0.2.1 1")
				}
				<-release
				return 0
			})
			sentinel := errors.New("fixture sink error")
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			err := r.Scan(ctx, w, fixturePlan(), func(scan.Finding) error { return sentinel }, nil)
			if err == nil || (!malformed && !errors.Is(err, sentinel)) || errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("lost decoder/sink failure", err)
			}
		})
	}
}

type eofErrorDecoder struct{ backend.Decoder }

var fixtureDecodeError = errors.New("fixture decoder error")

func (d eofErrorDecoder) Results(r io.Reader, _ func(scan.Finding) error) error {
	io.Copy(io.Discard, r)
	return fixtureDecodeError
}

func TestDecoderFailurePreservesRemoteExitDiagnostics(t *testing.T) {
	r, w := sshFixture(t, func(_ string, ch ssh.Channel) uint32 { fmt.Fprintln(ch.Stderr(), "fixture root cause"); return 3 })
	plan := fixturePlan()
	base := plan.NewDecoder
	plan.NewDecoder = func() backend.Decoder { return eofErrorDecoder{base()} }
	err := r.Scan(t.Context(), w, plan, func(scan.Finding) error { return nil }, nil)
	if !errors.Is(err, fixtureDecodeError) || !strings.Contains(err.Error(), "fixture root cause") || !strings.Contains(err.Error(), "status 3") {
		t.Fatal("lost remote cause", err)
	}
}
