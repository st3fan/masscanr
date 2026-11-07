package remote

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/st3fan/masscanr/internal/alpine"
	"github.com/st3fan/masscanr/internal/backend"
	"github.com/st3fan/masscanr/internal/cloud"
	"github.com/st3fan/masscanr/internal/scan"
	"golang.org/x/crypto/ssh"
)

type Runner interface {
	Prepare(context.Context, cloud.Worker, backend.Preparation) (scan.Prepared, error)
	Scan(context.Context, cloud.Worker, backend.Plan, func(scan.Finding) error, func(scan.Progress)) error
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
	return s.commandInput(ctx, w, command, nil, out, diagnostics)
}

func (s *SSH) commandInput(ctx context.Context, w cloud.Worker, command string, input io.Reader, out, diagnostics io.Writer) error {
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
	session.Stdin = input
	session.Stdout = out
	session.Stderr = diagnostics
	err = session.Run(command)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func (s *SSH) Prepare(ctx context.Context, w cloud.Worker, preparation backend.Preparation) (scan.Prepared, error) {
	var prepared scan.Prepared
	if preparation.Verify == nil || len(preparation.Packages) == 0 || len(preparation.Probes) == 0 {
		return prepared, fmt.Errorf("incomplete backend preparation")
	}
	for _, pkg := range preparation.Packages {
		if len(strings.FieldsFunc(pkg, func(r rune) bool { return r == '~' || r == '=' || r == '<' || r == '>' })) == 0 {
			return prepared, fmt.Errorf("invalid backend package constraint")
		}
	}
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
	args = append(append(args, "add"), preparation.Packages...)
	var diagnostics limitedBuffer
	if err := s.command(ctx, w, "exec "+QuoteArgs(args)+" >&2", io.Discard, &diagnostics); err != nil {
		return prepared, fmt.Errorf("Alpine %s package installation failed: %w (%s)", prepared.Release, err, strings.TrimSpace(diagnostics.String()))
	}
	outputs := make([]string, len(preparation.Probes))
	for i, probe := range preparation.Probes {
		var out textBuffer
		diagnostics = limitedBuffer{}
		err = s.command(ctx, w, QuoteArgs(probe.Command.Argv()), &out, &diagnostics)
		code := 0
		if err != nil {
			var exit *ssh.ExitError
			if !errors.As(err, &exit) {
				return prepared, fmt.Errorf("backend probe failed: %w (%s)", err, strings.TrimSpace(diagnostics.String()))
			}
			code = exit.ExitStatus()
		}
		if !slices.Contains(probe.ExitCodes, code) {
			return prepared, fmt.Errorf("backend probe exit %d: %w (%s)", code, err, strings.TrimSpace(diagnostics.String()))
		}
		if out.overflow {
			return prepared, fmt.Errorf("backend probe exceeds 64 KiB")
		}
		outputs[i] = out.String()
	}
	names := make([]string, len(preparation.Packages))
	for i, pkg := range preparation.Packages {
		names[i] = strings.FieldsFunc(pkg, func(r rune) bool { return r == '~' || r == '=' || r == '<' || r == '>' })[0]
	}
	info, err := s.readText(ctx, w, QuoteArgs(append([]string{"apk", "--no-network", "info", "--installed", "-v"}, names...)))
	if err != nil {
		return prepared, fmt.Errorf("read installed Alpine packages: %w", err)
	}
	fields := strings.Fields(info)
	if len(fields) != len(names) {
		return prepared, fmt.Errorf("installed package identities were not recognized")
	}
	ordered := make([]string, len(names))
	for i, name := range names {
		for _, field := range fields {
			if strings.HasPrefix(field, name+"-") {
				if ordered[i] != "" {
					return prepared, fmt.Errorf("duplicate installed package identity")
				}
				ordered[i] = field
			}
		}
		if ordered[i] == "" {
			return prepared, fmt.Errorf("missing installed package identity for %s", name)
		}
	}
	prepared.Packages = strings.Join(ordered, ", ")
	prepared.Identity, err = preparation.Verify(outputs, prepared.Packages)
	if err != nil {
		return prepared, err
	}
	if !prepared.Identity.Valid() {
		return prepared, fmt.Errorf("backend returned unknown identity")
	}

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

func (s *SSH) Scan(ctx context.Context, w cloud.Worker, plan backend.Plan, sink func(scan.Finding) error, progress func(scan.Progress)) (err error) {
	if plan.NewDecoder == nil || sink == nil || plan.Command.Executable == "" {
		return fmt.Errorf("incomplete backend execution plan")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	command := "exec " + QuoteArgs(plan.Command.Argv())
	if len(plan.Files) > 0 {
		directory, createErr := s.readText(ctx, w, "mktemp -d /tmp/masscanr.XXXXXXXXXX")
		if createErr != nil {
			return createErr
		}
		directory = strings.TrimSpace(directory)
		if !validDirectory(directory) {
			return fmt.Errorf("invalid remote workspace")
		}
		defer func() {
			cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
			defer stop()
			args := []string{"rm", "-f"}
			for _, file := range plan.Files {
				if validFile(file.Name) {
					args = append(args, directory+"/"+file.Name)
				}
			}
			cleanupCommand := QuoteArgs(args) + "; " + QuoteArgs([]string{"rmdir", directory})
			err = errors.Join(err, s.command(cleanup, w, cleanupCommand, io.Discard, io.Discard))
		}()
		for _, file := range plan.Files {
			if !validFile(file.Name) {
				return fmt.Errorf("invalid backend file name")
			}
			if err := s.commandInput(ctx, w, "umask 077; cat > "+QuoteArgs([]string{directory + "/" + file.Name}), strings.NewReader(file.Content), io.Discard, io.Discard); err != nil {
				return fmt.Errorf("write backend file: %w", err)
			}
		}
		command = "cd " + QuoteArgs([]string{directory}) + " && " + command
	}
	decoder := plan.NewDecoder()
	stdout, outWriter := io.Pipe()
	stderr, errWriter := io.Pipe()
	defer stdout.Close()
	defer stderr.Close()
	resultDone, statusDone := make(chan error, 1), make(chan error, 1)
	var tail limitedBuffer
	go func() {
		err := decoder.Results(stdout, sink)
		stdout.CloseWithError(err)
		if err != nil {
			cancel()
		}
		resultDone <- err
	}()
	go func() {
		err := backend.Records(stderr, func(line string) error {
			p, ok, err := decoder.Status(line)
			if err != nil {
				return err
			}
			if ok {
				if progress != nil {
					progress(p)
				}
			} else {
				tail.Write([]byte(line + "\n"))
			}
			return nil
		})
		stderr.CloseWithError(err)
		if err != nil {
			cancel()
		}
		statusDone <- err
	}()
	runErr := s.command(ctx, w, command, outWriter, errWriter)
	outWriter.Close()
	errWriter.Close()
	resultErr, statusErr := <-resultDone, <-statusDone
	if resultErr != nil || statusErr != nil {
		return errors.Join(resultErr, statusErr)
	}
	if runErr != nil {
		return fmt.Errorf("remote scanner: %w (%s)", runErr, strings.TrimSpace(tail.String()))
	}
	return nil
}

func validFile(name string) bool {
	return name != "" && name != "." && name != ".." && path.Base(name) == name && !strings.ContainsAny(name, "/\\\x00\r\n")
}
func validDirectory(value string) bool {
	const prefix = "/tmp/masscanr."
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+10 {
		return false
	}
	for _, r := range strings.TrimPrefix(value, prefix) {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
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
