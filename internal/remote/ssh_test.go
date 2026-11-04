package remote

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/st3fan/masscanr/internal/cloud"
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

func TestRootPasswordPrepareAndSeparateStreams(t *testing.T) {
	runner, worker := sshFixture(t, func(command string, ch ssh.Channel) uint32 {
		switch {
		case command == "id -u":
			fmt.Fprint(ch, "0\n")
		case strings.Contains(command, "apt-get"):
			fmt.Fprint(ch, "Masscan version 1.3.2 (fixture)\n")
		case strings.HasPrefix(command, "exec stdbuf -oL masscan "):
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
	version, err := runner.Prepare(ctx, worker)
	if err != nil || version != "1.3.2" {
		t.Fatal(version, err)
	}
	var out bytes.Buffer
	var updates []Progress
	err = runner.Scan(ctx, worker, []string{"--shards", "1/2"}, &out, func(p Progress) { updates = append(updates, p) })
	if err != nil {
		t.Fatal(err)
	}
	if out.String() != "open tcp 80 192.0.2.1 1700000000\n" || len(updates) != 2 || !updates[1].Waiting || updates[0].Rate != 500000 {
		t.Fatal(out.String(), updates)
	}
}

func TestRemoteExitAndCancellation(t *testing.T) {
	t.Run("exit", func(t *testing.T) {
		r, w := sshFixture(t, func(_ string, ch ssh.Channel) uint32 { fmt.Fprint(ch.Stderr(), "fixture error\n"); return 3 })
		err := r.Scan(t.Context(), w, nil, io.Discard, nil)
		if err == nil || !strings.Contains(err.Error(), "fixture error") {
			t.Fatal(err)
		}
	})
	t.Run("cancel", func(t *testing.T) {
		r, w := sshFixture(t, func(_ string, _ ssh.Channel) uint32 { <-t.Context().Done(); return 1 })
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()
		start := time.Now()
		err := r.Scan(ctx, w, nil, io.Discard, nil)
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
	err := Records(strings.NewReader("one\rtwo\r\nthree"), func(line string) { lines = append(lines, line) })
	if err != nil || strings.Join(lines, ",") != "one,two,three" {
		t.Fatal(lines, err)
	}
	if _, ok := ParseProgress("rate: nan-kpps, 10% done"); ok {
		t.Fatal("accepted invalid status")
	}
	if got := QuoteArgs([]string{"x'y", "$(reboot)"}); got != "'x'\\''y' '$(reboot)'" {
		t.Fatal(got)
	}
}
