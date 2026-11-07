//go:build linux

package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// Only the confirmation UI runs on this PTY: no app, controller or provider.
func TestConfirmationRestoresTerminal(t *testing.T) {
	for _, tc := range []struct {
		name, keys string
		plain      bool
		accept     bool
	}{
		{"dialog accept", "\t\r", false, true},
		{"dialog cancel", "\x03", false, false},
		{"plain accept", "yes\r", true, true},
		{"plain escape", "\x1b", true, false},
		{"dialog context cancellation", "", false, false},
		{"plain context cancellation", "", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			master, slave := confirmationPTY(t)
			before, err := term.GetState(int(slave.Fd()))
			if err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			var capture bytes.Buffer
			readDone := make(chan struct{})
			go func() {
				defer close(readDone)
				buf := make([]byte, 8192)
				for {
					n, err := master.Read(buf)
					mu.Lock()
					capture.Write(buf[:n])
					mu.Unlock()
					if err != nil {
						return
					}
				}
			}()
			defer func() {
				slave.Close()
				master.Close()
				<-readDone
			}()
			cfg, work := confirmationFixture()
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			result := make(chan error, 1)
			runDone := make(chan struct{})
			go func() {
				defer close(runDone)
				result <- ConfirmScan(ctx, cfg, work, slave, slave, tc.plain)
			}()
			defer func() {
				cancel()
				<-runDone // Restore the terminal before closing its descriptors.
			}()
			// Wait for raw mode in both modes. In the dialog also wait for
			// a rendered warning so the decision follows a visible prompt.
			deadline := time.Now().Add(2 * time.Second)
			for {
				state, err := term.GetState(int(slave.Fd()))
				mu.Lock()
				visible := strings.Contains(capture.String(), "6,553,600")
				mu.Unlock()
				if err == nil && !reflect.DeepEqual(state, before) && visible {
					break
				}
				if time.Now().After(deadline) {
					cancel()
					<-result
					t.Fatal("confirmation never became ready", err)
				}
				time.Sleep(5 * time.Millisecond)
			}
			if tc.keys == "" {
				cancel()
			} else {
				if _, err := io.WriteString(master, tc.keys); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-result:
				if (err == nil) != tc.accept || errors.Is(err, context.DeadlineExceeded) || (tc.keys == "" && !errors.Is(err, context.Canceled)) {
					t.Fatal("unexpected confirmation outcome", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("confirmation did not handle decision")
			}
			after, err := term.GetState(int(slave.Fd()))
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("terminal attributes not restored", err)
			}
			if !tc.plain {
				// The program has returned after flushing its restoration.
				// Drain capture before checking escape sequences below.
				slave.Close()
				select {
				case <-readDone:
				case <-time.After(time.Second):
					master.Close()
					<-readDone
				}
				mu.Lock()
				text := capture.String()
				mu.Unlock()
				if !strings.Contains(text, "\x1b[?1049h") || !strings.Contains(text, "\x1b[?1049l") {
					t.Fatal("alternate screen not restored")
				}
			}
		})
	}
}

func confirmationPTY(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK, 0)
	if err != nil {
		t.Skipf("pseudo-terminal unavailable: %v", err)
	}
	t.Cleanup(func() { master.Close() })
	fd := int(master.Fd())
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	number, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { slave.Close() })
	if err := unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, &unix.Winsize{Col: 80, Row: 24}); err != nil {
		t.Fatal(err)
	}
	return master, slave
}
