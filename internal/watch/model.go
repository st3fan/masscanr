package watch

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type pollMsg struct {
	snapshot snapshot
	err      error
}
type stopMsg struct {
	id  string
	err error
}

type model struct {
	ctx                                  context.Context
	cancel                               context.CancelFunc
	api                                  source
	options                              options
	snapshot                             snapshot
	selected                             string
	width, height, offset, tab, failures int
	stale, notice, confirmID             string
	confirmYes, stopping                 bool
	fatal                                error
}

func newModel(ctx context.Context, cancel context.CancelFunc, api source, o options, s snapshot) model {
	m := model{ctx: ctx, cancel: cancel, api: api, options: o, snapshot: s, width: 80, height: 24}
	m.selectRun()
	return m
}
func (m *model) selectRun() {
	for _, r := range m.snapshot.Runs {
		if r.ID == m.selected {
			return
		}
	}
	m.selected = ""
	m.offset = 0
	if len(m.snapshot.Runs) > 0 {
		m.selected = m.snapshot.Runs[0].ID
	}
}
func (m model) selectedRun() *runSnapshot {
	for _, r := range m.snapshot.Runs {
		if r.ID == m.selected {
			return &r
		}
	}
	return nil
}
func (m model) nextPoll() tea.Cmd {
	if !m.options.All && len(m.snapshot.Runs) == 1 && m.snapshot.Runs[0].Settled {
		return nil
	}
	return func() tea.Msg {
		timer := time.NewTimer(delay(m.options.Interval, m.failures))
		defer timer.Stop()
		select {
		case <-m.ctx.Done():
			return tea.Quit()
		case <-timer.C:
		}
		s, err := poll(m.ctx, m.api, m.options.ID, m.snapshot)
		return pollMsg{s, err}
	}
}
func (m model) Init() tea.Cmd { return m.nextPoll() }
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = max(1, msg.Width), max(1, msg.Height)
	case pollMsg:
		if msg.err != nil {
			if m.ctx.Err() != nil {
				return m, tea.Quit
			}
			if !retryable(msg.err) {
				m.fatal = msg.err
				m.cancel()
				return m, tea.Quit
			}
			m.failures++
			m.stale = fmt.Sprintf("STALE since %s: %s", m.snapshot.Updated.Format(time.RFC3339), msg.err)
		} else {
			m.snapshot = msg.snapshot
			m.failures = 0
			m.stale = ""
			m.selectRun()
		}
		return m, m.nextPoll()
	case stopMsg:
		m.stopping = false
		if msg.err != nil {
			m.notice = "Stop outcome uncertain for " + msg.id + ": " + msg.err.Error() + "; inspect status before retrying"
		} else {
			m.notice = "Stop requested for " + msg.id + "; waiting for cleanup"
		}
	case tea.KeyPressMsg:
		key := msg.String()
		if m.confirmID != "" {
			if (m.width < 40 || m.height < 8) && key != "esc" && key != "ctrl+c" && key != "q" && key != "n" {
				return m, nil
			}
			switch key {
			case "esc", "ctrl+c", "q", "n":
				m.confirmID = ""
				m.confirmYes = false
			case "left", "right", "tab":
				m.confirmYes = !m.confirmYes
			case "enter":
				id, yes := m.confirmID, m.confirmYes
				m.confirmID = ""
				m.confirmYes = false
				if yes {
					m.stopping = true
					return m, func() tea.Msg { _, err := m.api.Stop(m.ctx, id); return stopMsg{id, err} }
				}
			}
			return m, nil
		}
		switch key {
		case "q", "ctrl+c", "ctrl+d":
			m.cancel()
			return m, tea.Quit
		case "s":
			if r := m.selectedRun(); r != nil && r.Active && !m.stopping && m.stale == "" {
				m.confirmID = r.ID
				m.offset = 0
				m.confirmYes = false
			}
		case "tab", "shift+tab":
			m.tab = 1 - m.tab
			m.offset = 0
		case "1":
			m.tab = 0
			m.offset = 0
		case "2":
			m.tab = 1
			m.offset = 0
		case "up":
			m.offset = max(0, m.offset-1)
		case "down":
			m.offset++
		case "pgup":
			m.offset = max(0, m.offset-max(1, m.height/2))
		case "pgdown":
			m.offset += max(1, m.height/2)
		case "home":
			m.offset = 0
		case "[", "]":
			for i, r := range m.snapshot.Runs {
				if r.ID == m.selected {
					step := 1
					if key == "[" {
						step = -1
					}
					m.selected = m.snapshot.Runs[(i+step+len(m.snapshot.Runs))%len(m.snapshot.Runs)].ID
					m.offset = 0
					break
				}
			}
		}
	}
	m.offset = min(m.offset, max(0, len(m.body())-max(1, m.height-4)))
	return m, nil
}
func (m model) body() []string {
	var lines []string
	if m.stale != "" {
		lines = append(lines, m.stale)
	}
	if m.notice != "" {
		lines = append(lines, m.notice)
	}
	if m.confirmID != "" {
		choice := "[Cancel]  Stop"
		if m.confirmYes {
			choice = "Cancel  [Stop]"
		}
		if m.width < 40 || m.height < 8 {
			return []string{ansi.Truncate("Enlarge terminal to confirm; Esc cancels", m.width, "")}
		}
		return strings.Split(ansi.Wrap("Stop run "+clean(m.confirmID)+"?\n"+choice+"\nLeft/right selects; Enter confirms; Esc cancels.", m.width, ""), "\n")
	}
	if m.options.All {
		if len(m.snapshot.Runs) == 0 {
			message := "No active scans — waiting for new runs"
			if m.stale != "" {
				message = "Last snapshot had no active scans; current activity unknown"
			}
			lines = append(lines, message)
		}
		for _, r := range m.snapshot.Runs {
			mark := "  "
			if r.ID == m.selected {
				mark = "> "
			}
			lines = append(lines, mark+r.ID+" | scan "+r.ScanID+" | "+r.Phase)
		}
	}
	r := m.selectedRun()
	if r == nil {
		r = m.snapshot.Last
		if r != nil {
			lines = append(lines, "Most recent settled run")
		}
	}
	if r != nil {
		lines = append(lines, runLines(*r, m.tab, m.width)...)
	}
	var wrapped []string
	for _, line := range lines {
		wrapped = append(wrapped, strings.Split(ansi.Wrap(clean(line), max(1, m.width), ""), "\n")...)
	}
	return wrapped
}
func (m model) View() tea.View {
	header := fmt.Sprintf("masscanr-cli watch | %s | 1 Overview / 2 Config", clean(m.options.Server))
	footer := "q/^C detach | s stop | tab view | ↑↓ scroll | [ ] run"
	body := m.body()
	available := max(0, m.height-2)
	offset := min(m.offset, max(0, len(body)-available))
	lines := []string{ansi.Truncate(header, m.width, "")}
	lines = append(lines, body[offset:min(len(body), offset+available)]...)
	for len(lines) < m.height-1 {
		lines = append(lines, "")
	}
	if m.height > 1 {
		lines = append(lines, ansi.Truncate(footer, m.width, ""))
	}
	lines = lines[:min(len(lines), m.height)]
	v := tea.NewView(strings.Join(lines, "\n"))
	v.AltScreen = true
	return v
}
