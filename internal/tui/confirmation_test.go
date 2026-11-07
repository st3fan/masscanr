package tui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/st3fan/masscanr/internal/scan"
)

func confirmationFixture() (scan.Config, scan.Workload) {
	cfg := scan.Defaults()
	cfg.Targets = []netip.Prefix{netip.MustParsePrefix("192.0.0.0/16")}
	w, _ := cfg.Intent().Workload()
	return cfg, w
}

func TestConfirmationDecisions(t *testing.T) {
	cfg, work := confirmationFixture()
	for _, tc := range []struct {
		name string
		keys []tea.KeyPressMsg
		want bool
	}{
		{"default cancel", []tea.KeyPressMsg{{Code: tea.KeyEnter}}, false},
		{"explicit continue", []tea.KeyPressMsg{{Code: tea.KeyTab}, {Code: tea.KeyEnter}}, true},
		{"back to cancel", []tea.KeyPressMsg{{Code: tea.KeyRight}, {Code: tea.KeyLeft}, {Code: tea.KeyEnter}}, false},
		{"escape", []tea.KeyPressMsg{{Code: tea.KeyTab}, {Code: tea.KeyEscape}}, false},
		{"interrupt", []tea.KeyPressMsg{{Code: tea.KeyTab}, {Code: 'c', Mod: tea.ModCtrl}}, false},
		{"EOF key", []tea.KeyPressMsg{{Code: tea.KeyTab}, {Code: 'd', Mod: tea.ModCtrl}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := confirmationModel{cfg: cfg, work: work, width: 80, height: 24}
			var cmd tea.Cmd
			for _, k := range tc.keys {
				next, c := m.Update(k)
				m, cmd = next.(confirmationModel), c
			}
			if m.accepted != tc.want || cmd == nil {
				t.Fatal("wrong decision", m.accepted, tc.want)
			}
		})
	}
}

func TestConfirmationLayoutAndScopeScrolling(t *testing.T) {
	cfg, work := confirmationFixture()
	for i := range 40 {
		cfg.Targets = append(cfg.Targets, netip.PrefixFrom(netip.AddrFrom4([4]byte{198, 51, 100, byte(i)}), 32))
	}
	for _, size := range [][2]int{{80, 24}, {32, 12}, {24, 10}} {
		m := confirmationModel{cfg: cfg, work: work, width: size[0], height: size[1]}
		view := m.View()
		if !view.AltScreen || !strings.Contains(view.Content, "6,553,600") || !strings.Contains(view.Content, "[Cancel]") {
			t.Fatal(view.Content)
		}
		seen := ""
		for range 200 {
			seen += m.View().Content
			next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
			m = next.(confirmationModel)
		}
		for _, p := range cfg.Targets {
			if !strings.Contains(seen, p.String()) {
				t.Fatal("scope unavailable by scrolling", size, p)
			}
		}
		for _, line := range strings.Split(m.View().Content, "\n") {
			if ansi.StringWidth(line) > size[0] {
				t.Fatal("line overflows terminal", size, line)
			}
		}
		if len(strings.Split(m.View().Content, "\n")) > size[1] {
			t.Fatal("view overflows terminal height", size, m.View().Content)
		}
	}
	tooSmall := confirmationModel{cfg: cfg, work: work, width: 12, height: 4, proceed: true}
	if !strings.Contains(tooSmall.View().Content, "Resize") {
		t.Fatal("missing resize instruction")
	}
	next, _ := tooSmall.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if next.(confirmationModel).accepted {
		t.Fatal("accepted without enough room to review")
	}
}

func TestPlainConfirmationFailsClosed(t *testing.T) {
	cfg, work := confirmationFixture()
	for _, answer := range []string{"yes\n", "YES\n", "\n", "no\n", "y\n", "\x1b", "\x03", "\x04", "", "yes", strings.Repeat("y", 8192) + "\n"} {
		var out bytes.Buffer
		err := confirmPlain(t.Context(), cfg, work, strings.NewReader(answer), &out, 32)
		want := answer == "yes\n" || answer == "YES\n"
		if (err == nil) != want {
			t.Fatalf("answer=%q: %v", answer, err)
		}
		text := out.String()
		if !strings.HasPrefix(text, "6,553,600 planned probes") || !strings.Contains(text, "192.0.0.0/16") || !strings.Contains(text, "default: Cancel") {
			t.Fatal(text)
		}
	}
	if err := confirmPlain(t.Context(), cfg, work, brokenInput{}, io.Discard, 80); err == nil {
		t.Fatal("accepted input failure")
	}
	if err := confirmPlain(t.Context(), cfg, work, strings.NewReader("yes\n"), confirmationBrokenOutput{}, 80); err == nil {
		t.Fatal("accepted without displaying prompt")
	}
}

type confirmationBrokenOutput struct{}

func (confirmationBrokenOutput) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestConfirmationEOFAndInputErrors(t *testing.T) {
	cfg, work := confirmationFixture()
	for _, input := range []io.Reader{strings.NewReader(""), brokenInput{}} {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		err := runConfirmation(ctx, cfg, work, input, io.Discard, tea.WithoutRenderer())
		cancel()
		if err == nil || errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("did not fail immediately on input error/EOF", err)
		}
	}
}

func TestPlainConfirmationCancellationInterruptsRead(t *testing.T) {
	input, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	defer writer.Close()
	cfg, work := confirmationFixture()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if err := confirmPlain(ctx, cfg, work, input, io.Discard, 80); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}
