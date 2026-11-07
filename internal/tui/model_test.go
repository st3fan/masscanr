package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/st3fan/masscanr/internal/controller"
	"github.com/st3fan/masscanr/internal/scan"
)

func fixtureModel(shards int) model {
	cfg := scan.Defaults()
	cfg.Shards = shards
	s := newState(NewRunConfig(cfg, controller.DefaultTimeouts(), "inventory.scan", [2]string{}))
	s.RunID, s.Phase = "fixture", "Scanning"
	m := newModel(s, nil, nil, nil)
	m.now = time.Unix(100, 0)
	m.color = false
	return m
}

func key(m model, code rune, mod tea.KeyMod) (model, tea.Cmd) {
	next, cmd := m.Update(tea.KeyPressMsg{Code: code, Mod: mod})
	return next.(model), cmd
}

func TestOverviewSelectionResizeAndDetails(t *testing.T) {
	m := fixtureModel(30)
	for i := 1; i <= 30; i++ {
		m.state.apply(controller.Event{Shard: i, ID: fmt.Sprintf("instance-%d", i), IP: "203.0.113.10", State: "scanning", Progress: &scan.Progress{Percent: 50, Rate: 500000, Found: 10}}, m.now)
	}
	for range 29 {
		m, _ = key(m, tea.KeyDown, 0)
	}
	if !strings.Contains(m.View().Content, "> 30/30") {
		t.Fatal("selected shard scrolled out of view", m.View().Content)
	}
	m, _ = key(m, tea.KeyEnter, 0)
	if !strings.Contains(m.View().Content, "instance-30") {
		t.Fatal("missing selected shard details", m.View().Content)
	}
	for _, size := range [][2]int{{140, 35}, {80, 24}, {60, 20}, {30, 10}, {8, 3}, {1, 1}} {
		next, _ := m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		view := next.(model).View()
		lines := strings.Split(view.Content, "\n")
		if len(lines) > size[1] || !view.AltScreen {
			t.Fatalf("bad height or screen mode: %v", size)
		}
		for _, line := range lines {
			if ansi.StringWidth(line) > size[0] {
				t.Fatalf("overflow at %v: %q", size, line)
			}
		}
	}
	m, _ = key(m, tea.KeyTab, 0)
	if m.tab != 1 {
		t.Fatal("tab did not switch")
	}
	m, _ = key(m, tea.KeyTab, tea.ModShift)
	if m.tab != 0 {
		t.Fatal("shift-tab did not switch")
	}
	m, _ = key(m, '2', 0)
	if m.tab != 1 {
		t.Fatal("config shortcut")
	}
	m, _ = key(m, '1', 0)
	if m.tab != 0 {
		t.Fatal("overview shortcut")
	}
}

func TestCtrlCRequestsCancellationWithoutQuitting(t *testing.T) {
	m := fixtureModel(1)
	calls := 0
	m.cancel = func() { calls++ }
	for range 2 {
		var cmd tea.Cmd
		m, cmd = key(m, 'c', tea.ModCtrl)
		if cmd != nil {
			t.Fatal("Ctrl+C must wait for controller cleanup, not quit")
		}
	}
	if calls != 1 || !strings.Contains(m.View().Content, "Stopping") {
		t.Fatal("cancellation not latched", calls)
	}
	// A new frame must not erase the stopping state.
	next, _ := m.Update(frameMsg{state: m.state, now: m.now})
	if !next.(model).stopping {
		t.Fatal("frame erased cancellation")
	}
	done := make(chan struct{})
	close(done)
	m.stop = done
	if _, ok := m.Init()().(finishedMsg); !ok {
		t.Fatal("completion did not wake poll")
	}
	_, quit := m.Update(finishedMsg{})
	if _, ok := quit().(tea.QuitMsg); !ok {
		t.Fatal("completion did not quit")
	}
}

func TestLongDiagnosticsAreSanitizedAndInspectable(t *testing.T) {
	m := fixtureModel(1)
	m.details, m.width, m.height = true, 80, 30
	m.state.apply(controller.Event{Shard: 1, ID: "worker", State: "failed", Detail: strings.Repeat("wide界 message ", 100) + "END-DIAGNOSTIC\x1b[31m\n"}, m.now)
	for range 100 {
		m, _ = key(m, tea.KeyPgDown, 0)
	}
	text := m.View().Content
	if !strings.Contains(strings.ReplaceAll(strings.ReplaceAll(text, "\n", ""), " ", ""), "END-DIAGNOSTIC") || strings.Contains(text, "\x1b") {
		t.Fatal("diagnostic lost or unsanitized", text)
	}
}

func TestUnknownStaleAndCleanupFailureVisible(t *testing.T) {
	m := fixtureModel(2)
	if strings.Count(m.View().Content, "0.0%") != 1 {
		t.Fatal("invented worker progress")
	}
	m.state.apply(controller.Event{Shard: 1, State: "scanning", Progress: &scan.Progress{Percent: 90, Rate: 500000}}, m.now.Add(-staleAfter))
	m.state.apply(controller.Event{Shard: 2, State: "completed"}, m.now)
	m.state.apply(controller.Event{Shard: 2, State: "cleanup failed", Detail: "provider unavailable"}, m.now)
	text := m.View().Content
	for _, want := range []string{"95.0%", "Stale telemetry", "cleanup failed", "Scan ETA —"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q:\n%s", want, text)
		}
	}
}

func TestDiagnosticScrollDoesNotOvershootAndNarrowFooterShowsStop(t *testing.T) {
	m := fixtureModel(1)
	m.details = true
	m.state.apply(controller.Event{Shard: 1, State: "failed", Detail: strings.Repeat("diagnostic ", 300)}, m.now)
	for range 100 {
		m, _ = key(m, tea.KeyPgDown, 0)
	}
	end := m.detailOffset
	m, _ = key(m, tea.KeyPgUp, 0)
	if m.detailOffset >= end {
		t.Fatal("cannot scroll back from diagnostic end")
	}
	m.width = 40
	if !strings.Contains(strings.Split(m.View().Content, "\n")[m.height-2], "^C stop") {
		t.Fatal("stop hint clipped in narrow terminal")
	}
}

func TestCompletedRunStaysNavigableUntilExplicitQuit(t *testing.T) {
	for _, outcome := range []string{"Complete", "Failed", "Cancelled"} {
		t.Run(outcome, func(t *testing.T) {
			m := fixtureModel(2)
			m.stopping = true
			m.state.Final = &completion{Status: outcome, Detail: "final diagnostic", Found: 123}
			next, cmd := m.Update(frameMsg{state: m.state, now: m.now})
			m = next.(model)
			if cmd == nil {
				t.Fatal("final snapshot must keep polling, not exit")
			}
			for _, want := range []string{outcome, "Found 123", "q quit", "Run error"} {
				if !strings.Contains(m.View().Content, want) {
					t.Fatalf("missing %q:\n%s", want, m.View().Content)
				}
			}
			m, _ = key(m, tea.KeyTab, 0)
			if m.tab != 1 {
				t.Fatal("config inaccessible after completion")
			}
			m, _ = key(m, '1', 0)
			m, _ = key(m, tea.KeyDown, 0)
			m, _ = key(m, tea.KeyEnter, 0)
			if m.selected != 1 || !m.details {
				t.Fatal("shard details inaccessible after completion")
			}
			for _, k := range []tea.KeyPressMsg{{Code: 'q'}, {Code: 'c', Mod: tea.ModCtrl}} {
				_, quit := m.Update(k)
				if quit == nil {
					t.Fatal("explicit quit ignored")
				}
				if _, ok := quit().(tea.QuitMsg); !ok {
					t.Fatal("explicit quit did not quit")
				}
			}
		})
	}
	m := fixtureModel(1)
	_, cmd := key(m, 'q', 0)
	if cmd != nil {
		t.Fatal("q must not bypass active cleanup")
	}
}

func TestASCIIBarsAndGreenOnBlackTheme(t *testing.T) {
	for _, tc := range []struct {
		percent float64
		want    string
	}{
		{0, "[>.......]"}, {50, "[====>...]"}, {100, "[========]"},
	} {
		if got := progressBar(tc.percent, 8); got != tc.want {
			t.Fatalf("%g%%: %q", tc.percent, got)
		}
	}
	m := fixtureModel(1)
	m.color = true
	v := m.View()
	if v.ForegroundColor == nil || v.BackgroundColor == nil {
		t.Fatal("theme colors missing")
	}
	r, g, b, _ := v.ForegroundColor.RGBA()
	br, bg, bb, _ := v.BackgroundColor.RGBA()
	if r != 0 || g != 65535 || b != 0 || br != 0 || bg != 0 || bb != 0 {
		t.Fatal("expected bright green on black")
	}
	m.color = false
	plain := m.View()
	if plain.ForegroundColor != nil || plain.BackgroundColor != nil || strings.Contains(plain.Content, "\x1b") {
		t.Fatal("plain mode includes color controls")
	}
}
