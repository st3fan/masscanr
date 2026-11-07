package remote

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/st3fan/masscanr/internal/backend/zmap"
	"github.com/st3fan/masscanr/internal/scan"
	"golang.org/x/crypto/ssh"
)

func TestZMapPrivateScopeAndStreams(t *testing.T) {
	for _, phase := range []string{"success", "sink", "status", "exit"} {
		t.Run(phase, func(t *testing.T) {
			var uploaded bytes.Buffer
			cleaned := false
			runner, w := sshFixture(t, func(command string, ch ssh.Channel) uint32 {
				switch {
				case command == "mktemp -d /tmp/masscanr.XXXXXXXXXX":
					fmt.Fprintln(ch, "/tmp/masscanr.0123456789")
				case strings.HasPrefix(command, "umask 077; cat > "):
					if !strings.Contains(command, "'/tmp/masscanr.0123456789/targets.txt'") {
						t.Error(command)
					}
					io.Copy(&uploaded, ch)
				case strings.HasPrefix(command, "'rm' '-f'"):
					cleaned = true
				case strings.HasPrefix(command, "cd '/tmp/masscanr.0123456789' && exec 'zmap'"):
					if uploaded.String() != "192.0.2.0/30\n" {
						t.Error("missing private scope", uploaded.String())
					}
					fmt.Fprintln(ch, zmap.Fields)
					fmt.Fprintln(ch, "192.0.2.1,443,1700000000,1,synack,0")
					fmt.Fprintln(ch.Stderr(), zmap.StatusHeader)
					if phase == "status" {
						fmt.Fprintln(ch.Stderr(), "2026-11-07 03:00:00,broken")
					} else {
						fmt.Fprintln(ch.Stderr(), "2026-11-07 03:00:00,1,3,25.000000,0.000000,0,4,0,4,1,1,1,1,1,1,0,0,0,0,,0,0")
					}
					if phase == "exit" {
						fmt.Fprintln(ch.Stderr(), "fixture scanner failure")
						return 3
					}
				default:
					t.Error("unexpected command", command)
					return 2
				}
				return 0
			})
			cfg := scan.Defaults()
			cfg.Targets, _ = scan.ParseTargets([]string{"192.0.2.0/30"})
			plan, err := (zmap.Backend{}).Build(cfg.Intent(), scan.Shard{Index: 1, Total: 2}, 42)
			if err != nil {
				t.Fatal(err)
			}
			sentinel := errors.New("fixture sink failed")
			var found []scan.Finding
			var progress []scan.Progress
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			err = runner.Scan(ctx, w, plan, func(f scan.Finding) error {
				if phase == "sink" {
					return sentinel
				}
				found = append(found, f)
				return nil
			}, func(p scan.Progress) { progress = append(progress, p) })
			if (err == nil) != (phase == "success") || !cleaned {
				t.Fatal("scan/cleanup", phase, err, cleaned)
			}
			if phase == "sink" && !errors.Is(err, sentinel) {
				t.Fatal("sink error lost", err)
			}
			if phase == "exit" && !strings.Contains(err.Error(), "fixture scanner failure") {
				t.Fatal(err)
			}
			if phase == "success" && (len(found) != 1 || found[0].Port != 443 || len(progress) != 1 || !progress[0].Waiting) {
				t.Fatal(found, progress)
			}
		})
	}
}

func TestZMapFindingArrivesBeforeRemoteExit(t *testing.T) {
	received := make(chan struct{})
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	runner, w := sshFixture(t, func(_ string, ch ssh.Channel) uint32 {
		fmt.Fprintln(ch, zmap.Fields)
		fmt.Fprintln(ch, "192.0.2.1,443,1700000000,1,synack,0")
		select {
		case <-received:
			return 0
		case <-ctx.Done():
			return 1
		}
	})
	cfg := scan.Defaults()
	cfg.Targets, _ = scan.ParseTargets([]string{"192.0.2.0/24"})
	plan, _ := (zmap.Backend{}).Build(cfg.Intent(), scan.Shard{Index: 1, Total: 1}, 42)
	// This fixture tests the result stream alone; private-file transport has its
	// own success/failure coverage above.
	plan.Files = nil
	err := runner.Scan(ctx, w, plan, func(scan.Finding) error { close(received); return nil }, nil)
	if err != nil {
		t.Fatal("finding waited for remote exit", err)
	}
}
