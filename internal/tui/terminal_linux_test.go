//go:build linux

package tui

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/st3fan/masscanr/internal/controller"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// Exercise the real renderer and raw-mode lifecycle on a local pseudo-terminal.
// This runs synthetic UI events only: no app/controller/provider or scan traffic.
func TestTerminalRestoredAfterCancellationAndCleanup(t *testing.T) {
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK, 0)
	if err != nil {
		t.Skipf("pseudo-terminal unavailable: %v", err)
	}
	defer master.Close()
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
	defer slave.Close()
	if err := unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, &unix.Winsize{Col: 100, Row: 30}); err != nil {
		t.Fatal(err)
	}
	before, err := term.GetState(int(slave.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	var capture struct {
		sync.Mutex
		bytes.Buffer
	}
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buf := make([]byte, 8192)
		for {
			n, err := master.Read(buf)
			capture.Lock()
			capture.Write(buf[:n])
			capture.Unlock()
			if err != nil {
				return
			}
		}
	}()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	v := New(slave, true, 1)
	v.cancel = cancel
	v.start(tea.WithInput(slave), tea.WithEnvironment([]string{"TERM=xterm-256color", "NO_COLOR=1"}))
	defer v.Stop()
	deadline := time.Now().Add(3 * time.Second)
	for {
		current, err := term.GetState(int(slave.Fd()))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, current) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("terminal never entered raw mode")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := io.WriteString(master, "\x03"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("terminal Ctrl+C was not received")
	}
	select {
	case <-v.done:
		t.Fatal("UI quit before worker cleanup")
	default:
	}
	v.Update(controller.Event{Shard: 1, ID: "fixture-worker", State: "deleting"})
	v.Update(controller.Event{Shard: 1, State: "shutdown"})
	if err := v.Stop(); err != nil {
		t.Fatal(err)
	}
	after, err := term.GetState(int(slave.Fd()))
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("terminal attributes not restored", err)
	}
	slave.Close()
	select {
	case <-readDone:
	case <-time.After(time.Second):
		master.Close()
		<-readDone
	}
	capture.Lock()
	text := capture.String()
	capture.Unlock()
	if !strings.Contains(text, "\x1b[?1049h") || !strings.Contains(text, "\x1b[?1049l") {
		t.Fatal("alternate screen was not entered and restored")
	}
}
