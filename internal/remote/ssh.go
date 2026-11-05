package remote

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/st3fan/masscanr/internal/cloud"
	"golang.org/x/crypto/ssh"
)

type Runner interface {
	Prepare(context.Context, cloud.Worker) (string, error)
	Scan(context.Context, cloud.Worker, []string, io.Writer, func(Progress)) error
}

type SSH struct {
	Port       string
	RetryDelay time.Duration
	mu         sync.Mutex
	keys       map[string][]byte
}

func NewSSH() *SSH {
	return &SSH{Port: "22", RetryDelay: 2 * time.Second, keys: make(map[string][]byte)}
}

func (s *SSH) dial(ctx context.Context, w cloud.Worker) (*ssh.Client, error) {
	if _, err := netip.ParseAddr(w.IP); err != nil {
		return nil, fmt.Errorf("worker has invalid IP")
	}
	if w.Password == "" || w.ID == "" {
		return nil, fmt.Errorf("worker has no root password or ID")
	}
	dialer := net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(w.IP, s.Port))
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	config := &ssh.ClientConfig{User: "root", Auth: []ssh.AuthMethod{ssh.Password(w.Password)}, HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		previous, exists := s.keys[w.ID]
		if exists && !bytes.Equal(previous, key.Marshal()) {
			return fmt.Errorf("SSH host key changed for worker %s", w.ID)
		}
		if !exists {
			s.keys[w.ID] = append([]byte(nil), key.Marshal()...)
		}
		return nil
	}}
	cc, chans, requests, err := ssh.NewClientConn(conn, net.JoinHostPort(w.IP, s.Port), config)
	if err != nil {
		conn.Close()
		return nil, err
	}
	conn.SetDeadline(time.Time{})
	return ssh.NewClient(cc, chans, requests), nil
}

func (s *SSH) command(ctx context.Context, w cloud.Worker, command string, out, diagnostics io.Writer) error {
	client, err := s.dial(ctx, w)
	if err != nil {
		return err
	}
	defer client.Close()
	stop := context.AfterFunc(ctx, func() { client.Close() })
	defer stop()
	session, err := client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()
	session.Stdout = out
	session.Stderr = diagnostics
	err = session.Run(command)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

var versionPattern = regexp.MustCompile(`Masscan version ([^\s]+)`)

func (s *SSH) Prepare(ctx context.Context, w cloud.Worker) (string, error) {
	// A provisioned VM may still be booting or generating its host key.
	for {
		var uid limitedBuffer
		err := s.command(ctx, w, "id -u", &uid, io.Discard)
		if err == nil {
			if strings.TrimSpace(uid.String()) != "0" {
				return "", fmt.Errorf("worker login is not root")
			}
			break
		}
		timer := time.NewTimer(s.RetryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", fmt.Errorf("SSH readiness: %w", ctx.Err())
		case <-timer.C:
		}
	}
	var out, diagnostics limitedBuffer
	const bootstrap = "export DEBIAN_FRONTEND=noninteractive; apt-get -o DPkg::Lock::Timeout=120 update >&2 && apt-get -o DPkg::Lock::Timeout=120 install -y --no-install-recommends masscan >&2"
	if err := s.command(ctx, w, bootstrap, &out, &diagnostics); err != nil {
		return "", fmt.Errorf("masscan installation failed: %w (%s)", err, strings.TrimSpace(diagnostics.String()))
	}
	out = limitedBuffer{}
	diagnostics = limitedBuffer{}
	err := s.command(ctx, w, "masscan --version", &out, &diagnostics)
	var exit *ssh.ExitError
	// Debian masscan 1.3.2 deliberately exits 1 after printing its version.
	// Accept only that known status and still require recognized version text;
	// installation failures and other version-command failures remain errors.
	if err != nil && !(errors.As(err, &exit) && exit.ExitStatus() == 1) {
		return "", fmt.Errorf("masscan version command failed: %w", err)
	}
	m := versionPattern.FindStringSubmatch(out.String())
	if m == nil {
		return "", fmt.Errorf("masscan version not recognized")
	}
	return m[1], nil
}

// QuoteArgs prevents shell interpretation even if future flags accept text.
func QuoteArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
	}
	return strings.Join(quoted, " ")
}

func (s *SSH) Scan(ctx context.Context, w cloud.Worker, args []string, out io.Writer, progress func(Progress)) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	output := &cancelWriter{writer: out, cancel: cancel}
	reader, writer := io.Pipe()
	parsed := make(chan error, 1)
	var tail limitedBuffer
	go func() {
		err := Records(reader, func(line string) {
			if p, ok := ParseProgress(line); ok {
				if progress != nil {
					progress(p)
				}
			} else {
				tail.Write([]byte(line + "\n"))
			}
		})
		reader.CloseWithError(err)
		if err != nil {
			cancel()
		}
		parsed <- err
	}()
	// Debian's coreutils stdbuf makes list output arrive line by line over
	// SSH instead of waiting for libc's redirected-stdout buffer to fill.
	err := s.command(ctx, w, "exec stdbuf -oL masscan "+QuoteArgs(args), output, writer)
	writer.Close()
	parseErr := <-parsed
	if output.err != nil {
		return fmt.Errorf("write scan results: %w", output.err)
	}
	if parseErr != nil {
		return fmt.Errorf("masscan progress stream: %w", parseErr)
	}
	if err != nil {
		return fmt.Errorf("remote masscan: %w (%s)", err, strings.TrimSpace(tail.String()))
	}
	return nil
}

type cancelWriter struct {
	writer io.Writer
	cancel context.CancelFunc
	err    error
}

func (w *cancelWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	if err != nil {
		w.err = err
		w.cancel()
	}
	return n, err
}

// Keep only a bounded diagnostic tail while continuing to drain the stream.
type limitedBuffer struct{ data []byte }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if len(p) >= 4096 {
		b.data = append(b.data[:0], p[len(p)-4096:]...)
	} else {
		if len(b.data)+len(p) > 4096 {
			b.data = append([]byte(nil), b.data[len(b.data)+len(p)-4096:]...)
		}
		b.data = append(b.data, p...)
	}
	return n, nil
}
func (b *limitedBuffer) String() string { return string(b.data) }
