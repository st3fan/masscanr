//go:build linux

package integration

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/st3fan/masscanr/internal/alpine"
	"github.com/st3fan/masscanr/internal/scan"
	"golang.org/x/sys/unix"
)

func TestAlpineRuntime(t *testing.T) {
	if os.Getenv("MASSCAN_TEST_ALPINE") != "1" {
		t.Skip("requires the isolated Alpine compatibility image")
	}
	release, err := os.ReadFile("/etc/alpine-release")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := alpine.Release(string(release)); err != nil {
		t.Fatal(err)
	}
	repos, err := os.ReadFile("/etc/apk/repositories")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := alpine.Repositories(string(repos)); err != nil {
		t.Fatal(err)
	}
	data, err := exec.CommandContext(t.Context(), os.Getenv("MASSCAN_TEST_BINARY"), "--version").CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 || !bytes.Contains(data, []byte("Masscan version 1.3.2")) {
		t.Fatal("unrecognized Alpine version behavior", err, string(data))
	}
	packages, err := exec.CommandContext(t.Context(), "apk", "--no-network", "info", "--installed", "-v", "masscan", "coreutils").CombinedOutput()
	if err != nil {
		t.Fatal(err, string(packages))
	}
	t.Logf("Alpine %s; %s", strings.TrimSpace(string(release)), strings.TrimSpace(string(packages)))
}

// A FIFO keeps the native saved-result replay alive after its first finding.
// Receiving that finding before closing the FIFO proves libc line buffering
// works on musl, rather than merely checking that data arrives when masscan exits.
func TestMasscanStreamsBeforeInputEOF(t *testing.T) {
	program, stdbuf := os.Getenv("MASSCAN_TEST_BINARY"), os.Getenv("MASSCAN_TEST_STDBUF")
	if program == "" || stdbuf == "" {
		t.Skip("set MASSCAN_TEST_BINARY and MASSCAN_TEST_STDBUF for native streaming checks")
	}
	path := filepath.Join(t.TempDir(), "saved-results.fifo")
	if err := unix.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	// Open nonblocking before starting the reader, avoiding a leaked writer if
	// the subprocess cannot start or never opens the FIFO.
	input, err := os.OpenFile(path, os.O_RDWR|unix.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	cfg := scan.Defaults()
	cfg.Targets, _ = scan.ParseTargets([]string{"192.0.2.0/24"})
	args, _ := cfg.Args(1, 42)
	args = append(args, "--readscan", path)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, stdbuf, append([]string{"-oL", program}, args...)...)
	var diagnostics bytes.Buffer
	command.Stderr = &diagnostics
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); command.Wait() }()
	header := make([]byte, 99)
	copy(header, "masscan/1.1\ns:1700000000\n")
	record := make([]byte, 15)
	record[0], record[1], record[10], record[14] = 6, 13, 6, 64
	binary.BigEndian.PutUint32(record[2:6], 1700000000)
	copy(record[6:10], []byte{192, 0, 2, 1})
	binary.BigEndian.PutUint16(record[11:13], 80)
	if _, err := input.Write(append(header, record...)); err != nil {
		t.Fatal(err)
	}
	found := make(chan string, 1)
	go func() {
		reader := bufio.NewScanner(stdout)
		for reader.Scan() {
			if strings.HasPrefix(reader.Text(), "open ") {
				found <- reader.Text()
				io.Copy(io.Discard, stdout)
				return
			}
		}
		found <- ""
	}()
	select {
	case line := <-found:
		if line != "open tcp 80 192.0.2.1 1700000000" {
			t.Fatalf("unexpected native output: %q", line)
		}
	case <-ctx.Done():
		t.Fatal("finding did not stream before input EOF")
	}
	input.Close()
	if err := command.Wait(); err != nil {
		t.Fatal(err, diagnostics.String())
	}
}
