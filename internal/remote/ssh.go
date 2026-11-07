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

	"github.com/st3fan/masscanr/internal/alpine"
	"github.com/st3fan/masscanr/internal/cloud"
	"golang.org/x/crypto/ssh"
)

type Runner interface {
	Prepare(context.Context, cloud.Worker) (Prepared, error)
	Scan(context.Context, cloud.Worker, []string, io.Writer, func(Progress)) error
}

// Prepared records the observed platform and scanner identity without secrets.
type Prepared struct {
	Version, Release, Packages string
}

func (p Prepared) String() string {
	parts := []string{"masscan " + p.Version}
	if p.Release != "" {
		parts = append([]string{"Alpine " + p.Release}, parts...)
	}
	if p.Packages != "" {
		parts = append(parts, p.Packages)
	}
	return strings.Join(parts, "; ")
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

func (s *SSH) Prepare(ctx context.Context, w cloud.Worker) (Prepared, error) {
	var prepared Prepared
	// A provisioned VM may still be booting or generating its host key.
	for {
		var uid limitedBuffer
		err := s.command(ctx, w, "id -u", &uid, io.Discard)
		if err == nil {
			if strings.TrimSpace(uid.String()) != "0" {
				return prepared, fmt.Errorf("worker login is not root")
			}
			break
		}
		timer := time.NewTimer(s.RetryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return prepared, fmt.Errorf("SSH readiness: %w", ctx.Err())
		case <-timer.C:
		}
	}
	release, err := s.readText(ctx, w, "cat /etc/alpine-release")
	if err != nil {
		return prepared, fmt.Errorf("read Alpine release: %w", err)
	}
	if prepared.Release, err = alpine.Release(release); err != nil {
		return prepared, err
	}
	repositories, err := s.readText(ctx, w, "cat /etc/apk/repositories")
	if err != nil {
		return prepared, fmt.Errorf("read APK repositories: %w", err)
	}
	repositories, err = alpine.Repositories(repositories)
	if err != nil {
		return prepared, err
	}
	// Explicit sources exclude ambient repository snippets. The image's files
	// stay untouched, and APK retains its normal package-signature verification.
	args := []string{"apk", "--repositories-file", "/dev/null", "--no-cache"}
	for _, repository := range strings.Fields(repositories) {
		args = append(args, "--repository", repository)
	}
	args = append(args, "add", "masscan~1.3.2", "coreutils")
	var diagnostics limitedBuffer
	if err := s.command(ctx, w, "exec "+QuoteArgs(args)+" >&2", io.Discard, &diagnostics); err != nil {
		return prepared, fmt.Errorf("Alpine %s package installation failed: %w (%s)", prepared.Release, err, strings.TrimSpace(diagnostics.String()))
	}
	var out limitedBuffer
	diagnostics = limitedBuffer{}
	err = s.command(ctx, w, "stdbuf -oL masscan --version", &out, &diagnostics)
	var exit *ssh.ExitError
	// Packaged masscan 1.3.2 exits 1 after printing its version on Alpine too.
	// Accept only that known status and still require recognized version text;
	// installation failures and other version-command failures remain errors.
	if err != nil && !(errors.As(err, &exit) && exit.ExitStatus() == 1) {
		return prepared, fmt.Errorf("masscan version command failed: %w (%s)", err, strings.TrimSpace(diagnostics.String()))
	}
	m := versionPattern.FindStringSubmatch(out.String())
	if m == nil {
		return prepared, fmt.Errorf("masscan version not recognized")
	}
	if m[1] != "1.3.2" {
		return prepared, fmt.Errorf("unsupported masscan version %q on Alpine %s; require 1.3.2", m[1], prepared.Release)
	}
	prepared.Version = m[1]
	packages, err := s.readText(ctx, w, "apk --no-network info --installed -v masscan coreutils")
	if err != nil {
		return prepared, fmt.Errorf("read installed Alpine packages: %w", err)
	}
	var masscanPackage, coreutilsPackage string
	fields := strings.Fields(packages)
	for _, field := range fields {
		if strings.HasPrefix(field, "masscan-1.3.2-r") {
			masscanPackage = field
		}
		if strings.HasPrefix(field, "coreutils-") {
			coreutilsPackage = field
		}
	}
	if len(fields) != 2 || masscanPackage == "" || coreutilsPackage == "" {
		return prepared, fmt.Errorf("installed masscan/coreutils package identities were not recognized")
	}
	prepared.Packages = masscanPackage + ", " + coreutilsPackage
	return prepared, nil
}

// Preparation policy must inspect complete input, never a truncated diagnostic
// tail that could hide an earlier edge/mixed-release repository entry.
func (s *SSH) readText(ctx context.Context, w cloud.Worker, command string) (string, error) {
	var out textBuffer
	var diagnostics limitedBuffer
	if err := s.command(ctx, w, command, &out, &diagnostics); err != nil {
		return "", err
	}
	if out.overflow {
		return "", fmt.Errorf("worker configuration exceeds 64 KiB")
	}
	return out.String(), nil
}

type textBuffer struct {
	buffer   bytes.Buffer
	overflow bool
}

func (b *textBuffer) String() string { return b.buffer.String() }

func (b *textBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := max(0, (64<<10)-b.buffer.Len())
	if n > remaining {
		b.overflow = true
		p = p[:remaining]
	}
	b.buffer.Write(p)
	return n, nil // Drain the complete SSH stream with bounded memory.
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
	// GNU coreutils stdbuf is tested on Alpine/musl: findings must arrive before
	// scanner exit rather than waiting for redirected-stdout buffers to flush.
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
