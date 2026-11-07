package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type frameMsg struct {
	state state
	now   time.Time
}
type finishedMsg struct{}

type model struct {
	state                                     state
	source                                    func() state
	stop                                      <-chan struct{}
	cancel                                    func()
	now                                       time.Time
	width, height                             int
	tab, selected, configOffset, detailOffset int
	details, stopping                         bool
	color                                     bool
}

func newModel(s state, source func() state, stop <-chan struct{}, cancel func()) model {
	return model{state: s, source: source, stop: stop, cancel: cancel, now: time.Now(), width: 80, height: 24, color: os.Getenv("NO_COLOR") == ""}
}

func (m model) poll() tea.Cmd {
	return func() tea.Msg {
		timer := time.NewTimer(100 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-m.stop:
			return finishedMsg{}
		case now := <-timer.C:
			s := m.state
			if m.source != nil {
				s = m.source()
			}
			return frameMsg{state: s, now: now}
		}
	}
}

func (m model) Init() tea.Cmd { return m.poll() }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case finishedMsg:
		return m, tea.Quit
	case frameMsg:
		m.state, m.now = msg.state, msg.now
		return m, m.poll()
	case tea.WindowSizeMsg:
		m.width, m.height = max(1, msg.Width), max(1, msg.Height)
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c":
			if !m.stopping {
				m.stopping = true
				if m.cancel != nil {
					m.cancel()
				}
			}
		case "tab", "shift+tab":
			m.tab = 1 - m.tab
		case "1":
			m.tab = 0
		case "2":
			m.tab = 1
		case "enter":
			if m.tab == 0 {
				m.details = !m.details
				m.detailOffset = 0
			}
		case "up", "down":
			delta := 1
			if msg.String() == "up" {
				delta = -1
			}
			if m.tab == 1 {
				m.configOffset = max(0, m.configOffset+delta)
			} else {
				m.selected = max(0, min(len(m.state.rows)-1, m.selected+delta))
				m.detailOffset = 0
			}
		case "pgup", "pgdown":
			delta := max(1, m.height/3)
			if msg.String() == "pgup" {
				delta = -delta
			}
			if m.tab == 1 {
				m.configOffset = max(0, m.configOffset+delta)
			} else if m.details {
				m.detailOffset = max(0, m.detailOffset+delta)
			} else {
				m.selected = max(0, min(len(m.state.rows)-1, m.selected+delta))
			}
		}
	}
	if m.details && len(m.state.rows) > 0 {
		headerHeight := 6
		if m.state.totals(m.now).Stale {
			headerHeight++
		}
		detailHeight := m.detailHeight(max(0, m.height-headerHeight-2))
		rows := m.state.sorted()
		selected := min(m.selected, len(rows)-1)
		m.detailOffset = min(m.detailOffset, max(0, len(m.detailLines(rows[selected]))-max(1, detailHeight-1)))
	}
	return m, nil
}

func (m model) paint(text, color string) string {
	if !m.color {
		return text
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(text)
}

func (m model) View() tea.View {
	w := max(1, m.width)
	run := m.state.RunID
	if run == "" {
		run = "pending"
	}
	phase := m.state.Phase
	if m.stopping {
		phase = "Stopping; cleaning up"
	}
	header := []string{fmt.Sprintf("masscanr  |  Run: %s  |  %s", run, phase)}
	tabs := []string{"1 Overview", "2 Config"}
	tabs[m.tab] = m.paint("["+tabs[m.tab]+"]", "6")
	header = append(header, strings.Join(tabs, "   "), strings.Repeat("─", w))
	footer := "tab tabs  ↑↓ shard  enter details  pgup/pgdown scroll  ctrl+c stop"
	if m.tab == 1 {
		footer = "tab tabs  ↑↓ / pgup/pgdown scroll  ctrl+c stop"
	}
	if w < 76 {
		footer = "^C stop · tab tabs · ↑↓ · enter"
		if m.tab == 1 {
			footer = "^C stop · tab tabs · ↑↓ scroll"
		}
	}
	var body []string
	if m.tab == 0 {
		total := m.state.totals(m.now)
		eta := "—"
		if total.ETAKnown {
			eta = total.ETA.Round(time.Second).String()
		}
		header = append(header,
			fmt.Sprintf("Scan progress  %s  %.1f%%", progressBar(total.Percent, min(28, max(4, w-28))), total.Percent),
			fmt.Sprintf("Rate %s pps  |  Found %d  |  Scan ETA %s", rate(total.Rate), total.Found, eta),
			fmt.Sprintf("Shards %d/%d complete  |  VPSs remaining %d", total.Complete, m.state.Config.Scan.Shards, total.VPSs))
		if total.Stale {
			header = append(header, m.paint("Stale telemetry: last progress retained; stale rates excluded", "3"))
		}
		body = m.overview(max(0, m.height-len(header)-2))
	} else {
		body = m.configLines()
		available := max(0, m.height-len(header)-2)
		offset := min(m.configOffset, max(0, len(body)-available))
		body = body[offset:min(len(body), offset+available)]
	}
	lines := append(header, body...)
	// Keep navigation visible even on unusually short terminals.
	lines = lines[:min(len(lines), max(0, m.height-1))]
	for len(lines) < m.height-1 {
		lines = append(lines, "")
	}
	lines = append(lines, footer)
	for i, line := range lines {
		lines[i] = ansi.Truncate(line, w, "")
	}
	v := tea.NewView(strings.Join(lines, "\n"))
	v.AltScreen = true
	return v
}

func (m model) overview(height int) []string {
	if height <= 0 {
		return nil
	}
	rows := m.state.sorted()
	wide, narrow := m.width >= 110, m.width < 76
	heading := "  SHARD  VPS ADDRESS      STATE                      PROGRESS            RATE"
	if wide {
		heading += "       ETA       FOUND"
	}
	if narrow {
		heading = "  SHARD  VPS ADDRESS      STATE"
	}
	lines := []string{heading}
	rowHeight := 1
	if narrow {
		rowHeight = 2
	}
	detailHeight := m.detailHeight(height)
	capacity := max(0, (height-1-detailHeight)/rowHeight)
	selected := min(m.selected, max(0, len(rows)-1))
	start := max(0, selected-capacity+1)
	end := min(len(rows), start+capacity)
	for i := start; i < end; i++ {
		r := rows[i]
		marker := " "
		if i == selected {
			marker = ">"
		}
		ip := r.IP
		if ip == "" {
			ip = "—"
		}
		state := shardLabel(r)
		if r.stale(m.now) {
			state += " (stale)"
		}
		pct, observed, eta := "—", "—", "—"
		if r.Known {
			pct = fmt.Sprintf("%s %5.1f%%", progressBar(r.Progress.Percent, 8), r.Progress.Percent)
		}
		if r.active(m.now) {
			observed = rate(r.Progress.Rate)
		}
		if r.Known && !r.stale(m.now) && (r.State == "scanning" || r.State == "waiting") {
			eta = r.Progress.ETA.Round(time.Second).String()
		}
		shard := fmt.Sprintf("%d/%d", r.Shard, m.state.Config.Scan.Shards)
		if r.Shard == 0 {
			shard = "?"
		}
		line := fmt.Sprintf("%s %-6s %-15s  %-26s %-18s %7s", marker, shard, ip, state, pct, observed)
		if wide {
			line += fmt.Sprintf("  %-8s %6d", eta, r.Progress.Found)
		}
		if narrow {
			line = fmt.Sprintf("%s %-6s %-15s  %s", marker, shard, ip, state)
		}
		color := ""
		switch {
		case r.State == "failed" || r.Cleanup == "cleanup failed":
			color = "1"
		case r.State == "waiting" || r.Cleanup == "deleting":
			color = "3"
		case r.State == "completed" || r.Cleanup == "shutdown":
			color = "2"
		case i == selected:
			color = "6"
		}
		if color != "" {
			line = m.paint(line, color)
		}
		lines = append(lines, line)
		if narrow {
			lines = append(lines, fmt.Sprintf("         %s  %s pps  ETA %s  found %d", pct, observed, eta, r.Progress.Found))
		}
	}
	if detailHeight > 0 && len(rows) > 0 {
		wrapped := m.detailLines(rows[selected])
		offset := min(m.detailOffset, max(0, len(wrapped)-detailHeight+1))
		lines = append(lines, fmt.Sprintf("── Details (%d/%d; pgup/pgdown) ──", offset+1, len(wrapped)))
		lines = append(lines, wrapped[offset:min(len(wrapped), offset+detailHeight-1)]...)
	}
	return lines
}

func (m model) detailHeight(available int) int {
	if !m.details || available < 6 {
		return 0
	}
	return min(8, max(3, available/3))
}

func (m model) detailLines(r shardState) []string {
	detail := fmt.Sprintf("Shard %d | Instance: %s | Address: %s\nScan: %s | Cleanup: %s\nETA: %s | Found: %d", r.Shard, r.ID, r.IP, r.State, r.Cleanup, shardETA(r, m.now), r.Progress.Found)
	if r.Detail != "" {
		detail += "\n" + r.Detail
	}
	if r.CleanupDetail != "" {
		detail += "\nCleanup: " + r.CleanupDetail
	}
	return strings.Split(ansi.Wrap(detail, max(1, m.width), ""), "\n")
}

func shardLabel(r shardState) string {
	label := r.State
	if label == "" {
		label = "reconciled"
	}
	if r.Cleanup != "" {
		label += " / " + r.Cleanup
	}
	return label
}

func shardETA(r shardState, now time.Time) string {
	if !r.Known || r.stale(now) || (r.State != "scanning" && r.State != "waiting") {
		return "—"
	}
	return r.Progress.ETA.Round(time.Second).String()
}

func progressBar(percent float64, width int) string {
	n := min(width, max(0, int(percent*float64(width)/100)))
	return strings.Repeat("█", n) + strings.Repeat("░", width-n)
}

func rate(value float64) string {
	if value >= 1000000 {
		return fmt.Sprintf("%.2fM", value/1000000)
	}
	if value >= 1000 {
		return fmt.Sprintf("%.1fk", value/1000)
	}
	return fmt.Sprintf("%.0f", value)
}

// The Config layer supplies the full effective configuration view.
func (m model) configLines() []string { return []string{"Configuration"} }
