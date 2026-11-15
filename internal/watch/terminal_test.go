//go:build linux || darwin

package watch

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/term"
)

// Python supplies a portable local pseudo-terminal only. The viewer uses fake
// snapshots: no actual application, service, provider or scanner is launched.
func TestTerminalRestoredOnDetach(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 supplies the PTY fixture")
	}
	for _, action := range []string{"q", "ctrl-c", "cancel"} {
		t.Run(action, func(t *testing.T) {
			helper := exec.Command(python, "-u", "-c", `import os,pty,sys,threading
m,s=pty.openpty()
print(os.ttyname(s),flush=True)
def drain():
 try:
  while os.read(m,8192): pass
 except OSError: pass
threading.Thread(target=drain,daemon=True).start()
for line in sys.stdin:
 os.write(m,b'\x03' if line.strip()=='ctrl-c' else b'q')
`)
			input, err := helper.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			output, err := helper.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := helper.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { input.Close(); helper.Process.Kill(); helper.Wait() })
			path, err := bufio.NewReader(output).ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			slave, err := os.OpenFile(strings.TrimSpace(path), os.O_RDWR, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer slave.Close()
			before, err := term.GetState(int(slave.Fd()))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			api := &fakeSource{}
			done := make(chan error, 1)
			go func() {
				done <- runTerminal(ctx, cancel, api, options{Interval: time.Hour}, snapshot{Runs: []runSnapshot{fixtureRun("a", false)}}, slave, slave, tea.WithEnvironment([]string{"TERM=xterm-256color"}))
			}()
			deadline := time.Now().Add(3 * time.Second)
			for {
				now, err := term.GetState(int(slave.Fd()))
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(now, before) {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("terminal did not enter raw mode")
				}
				time.Sleep(5 * time.Millisecond)
			}
			if action == "cancel" {
				cancel()
			} else {
				if _, err := io.WriteString(input, action+"\n"); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(4 * time.Second):
				t.Fatal("viewer did not detach")
			}
			after, err := term.GetState(int(slave.Fd()))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatal("terminal state not restored")
			}
			if len(api.stopIDs) != 0 {
				t.Fatal("detach sent stop")
			}
		})
	}
}
