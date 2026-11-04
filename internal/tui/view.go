// Package tui renders worker state without sharing the result stream.
package tui

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/st3fan/masscanr/internal/controller"
	"github.com/st3fan/masscanr/internal/remote"
	"golang.org/x/term"
)

type row struct {
	Shard                          int
	ID, IP, State, Cleanup, Detail string
	Progress                       remote.Progress
}

type View struct {
	mu         sync.Mutex
	out        io.Writer
	terminal   bool
	rows       map[string]row
	stop, done chan struct{}
}

func New(out io.Writer, terminal bool, shards int) *View {
	v := &View{out: out, terminal: terminal, rows: make(map[string]row), stop: make(chan struct{}), done: make(chan struct{})}
	for i := 1; i <= shards; i++ {
		v.rows[fmt.Sprint(i)] = row{Shard: i, State: "pending"}
	}
	return v
}

func (v *View) Start() {
	if !v.terminal {
		close(v.done)
		return
	}
	fmt.Fprint(v.out, "\x1b[?25l")
	go func() {
		defer close(v.done)
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		lines := 0
		redraw := func() {
			v.mu.Lock()
			if lines > 0 {
				fmt.Fprintf(v.out, "\x1b[%dA", lines)
			}
			text := v.render()
			fmt.Fprint(v.out, "\r\x1b[J")
			for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
				fmt.Fprint(v.out, "\r\x1b[2K", line, "\n")
			}
			lines = strings.Count(text, "\n")
			v.mu.Unlock()
		}
		redraw()
		for {
			select {
			case <-v.stop:
				redraw()
				return
			case <-ticker.C:
				redraw()
			}
		}
	}()
}

func (v *View) Stop() {
	if v.terminal {
		close(v.stop)
		<-v.done
		v.mu.Lock()
		defer v.mu.Unlock()
		// The final state is emitted by the CLI summary. Always restore cursor.
		fmt.Fprint(v.out, "\x1b[?25h")
	}
}

func (v *View) Update(e controller.Event) {
	v.mu.Lock()
	defer v.mu.Unlock()
	key := fmt.Sprint(e.Shard)
	if e.Shard == 0 {
		key = e.ID
	}
	r := v.rows[key]
	r.Shard = e.Shard
	if e.ID != "" {
		r.ID = e.ID
	}
	if e.IP != "" {
		r.IP = e.IP
	}
	cleanup := e.State == "deleting" || e.State == "deleted" || e.State == "cleanup failed"
	changed := false
	if cleanup {
		changed = r.Cleanup != e.State
		r.Cleanup = e.State
	} else {
		changed = r.State != e.State
		r.State = e.State
	}
	if e.Progress != nil {
		r.Progress = *e.Progress
	}
	if e.Detail != "" {
		r.Detail = clean(e.Detail)
	}
	v.rows[key] = r
	if !v.terminal && changed {
		fmt.Fprintf(v.out, "shard %d %-14s id=%s ip=%s", e.Shard, e.State, clean(r.ID), clean(r.IP))
		if e.Detail != "" {
			fmt.Fprintf(v.out, " %s", clean(e.Detail))
		}
		fmt.Fprintln(v.out)
	}
}

func (v *View) render() string {
	var b strings.Builder
	width := 80
	if f, ok := v.out.(interface{ Fd() uintptr }); ok {
		if columns, _, err := term.GetSize(int(f.Fd())); err == nil {
			width = columns
		}
	}
	compact := width < 130
	fmt.Fprintln(&b, "masscanr  |  distributed IPv4 scan  |  Ctrl-C stops scans and deletes workers")
	if compact {
		fmt.Fprintln(&b, "SHARD  INSTANCE       ADDRESS          STATE")
	} else {
		fmt.Fprintln(&b, "SHARD  INSTANCE       ADDRESS          STATE                      PROGRESS           RATE       ETA       FOUND")
	}
	rows := make([]row, 0, len(v.rows))
	for _, r := range v.rows {
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Shard != rows[j].Shard {
			return rows[i].Shard < rows[j].Shard
		}
		return rows[i].ID < rows[j].ID
	})
	for _, r := range rows {
		state := r.State
		if r.Cleanup != "" {
			state += " / " + r.Cleanup
		}
		filled := min(10, max(0, int(r.Progress.Percent/10)))
		bar := strings.Repeat("=", filled) + strings.Repeat(".", 10-filled)
		id := clean(r.ID)
		if len(id) > 12 {
			id = id[:12]
		}
		fmt.Fprintf(&b, "%5d  %-12s   %-15s  %-25s", r.Shard, id, clean(r.IP), state)
		if compact {
			fmt.Fprint(&b, "\n       ")
		}
		fmt.Fprintf(&b, "  [%s] %5.1f%%  %7.1fk pps  ETA %-8s  found %d\n", bar, r.Progress.Percent, r.Progress.Rate/1000, r.Progress.ETA.Round(time.Second), r.Progress.Found)
		if r.Detail != "" {
			fmt.Fprintf(&b, "       %s\n", r.Detail)
		}
	}
	// Prevent line wrapping from invalidating cursor positioning on narrow
	// terminals. Wider views use one row; normal terminals use two.
	var clipped strings.Builder
	for _, line := range strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n") {
		runes := []rune(line)
		if len(runes) > width && width > 0 {
			runes = runes[:width]
		}
		clipped.WriteString(string(runes))
		clipped.WriteByte('\n')
	}
	return clipped.String()
}

// Remote diagnostics must not inject terminal escape sequences or new rows.
func clean(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsPrint(r) {
			b.WriteRune(r)
		} else {
			b.WriteByte(' ')
		}
		if b.Len() >= 200 {
			break
		}
	}
	return b.String()
}
