package tui

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/cancelreader"
	"github.com/st3fan/masscanr/internal/scan"
	"golang.org/x/term"
)

var ErrScanDeclined = errors.New("scan not started: confirmation cancelled")

// Count formats exact integer counts for the confirmation and its diagnostics.
func Count(n uint64) string {
	s := strconv.FormatUint(n, 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// ConfirmScan returns only after a decision and terminal restoration. Callers
// must check for interactive terminals before entering this preflight dialog.
func ConfirmScan(ctx context.Context, cfg scan.Config, work scan.Workload, input *os.File, out *os.File, plain bool) error {
	if plain {
		width, _, err := term.GetSize(int(out.Fd()))
		if err != nil {
			width = 80
		}
		err = confirmPlain(ctx, cfg, work, input, out, width)
		if err != nil {
			_, writeErr := fmt.Fprintln(out)
			err = errors.Join(err, writeErr)
		}
		return err
	}
	return runConfirmation(ctx, cfg, work, input, out)
}

func warningLines(cfg scan.Config, work scan.Workload, width int) []string {
	text := fmt.Sprintf("WARNING: Scanning %s TCP ports on %s addresses means %s planned probes. This exceeds the %s-probe confirmation threshold.\n\nTargets:\n",
		Count(work.Ports), Count(work.Addresses), Count(work.Probes), Count(scan.ConfirmationProbeThreshold))
	var b strings.Builder
	b.WriteString(text)
	for _, t := range cfg.RequestedTargets {
		b.WriteString(t.Target)
		if t.Resolution != nil {
			b.WriteString(" (RIPE snapshot " + t.Resolution.SnapshotAt.Format("2006-01-02T15:04:05Z") + ")")
		}
		b.WriteByte('\n')
	}
	b.WriteString("Effective IPv4 scope:\n")
	for _, p := range cfg.Targets {
		b.WriteString(p.String())
		b.WriteByte('\n')
	}
	b.WriteString("\nAre you sure you want to continue?")
	return strings.Split(ansi.Wrap(b.String(), max(1, width), " /"), "\n")
}

func confirmPlain(ctx context.Context, cfg scan.Config, work scan.Workload, input io.Reader, out io.Writer, width int) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	warning := Count(work.Probes) + " planned probes\n\n" + strings.Join(warningLines(cfg, work, width), "\n") + "\nType yes and press Enter to continue [default: Cancel]: "
	if _, err := io.WriteString(out, warning); err != nil {
		return fmt.Errorf("display scan confirmation: %w", err)
	}
	if file, ok := input.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		state, err := term.MakeRaw(int(file.Fd()))
		if err != nil {
			return fmt.Errorf("prepare scan confirmation input: %w", err)
		}
		defer func() { err = errors.Join(err, term.Restore(int(file.Fd()), state)) }()
	}
	r, err := cancelreader.NewReader(input)
	if err != nil {
		return fmt.Errorf("read scan confirmation: %w", err)
	}
	type answer struct {
		line string
		err  error
	}
	answers := make(chan answer, 1)
	go func() {
		line, err := readPlainAnswer(r, out)
		answers <- answer{line, err}
	}()
	defer r.Close()
	select {
	case <-ctx.Done():
		if r.Cancel() {
			<-answers // Do not leave a terminal reader behind on cancellation.
		}
		return ctx.Err()
	case a := <-answers:
		if err := ctx.Err(); err != nil {
			return err
		}
		if a.err != nil {
			return fmt.Errorf("read scan confirmation: %w", a.err)
		}
		if !strings.EqualFold(strings.TrimSpace(a.line), "yes") {
			return ErrScanDeclined
		}
		return nil
	}
}

func readPlainAnswer(input io.Reader, out io.Writer) (string, error) {
	r := bufio.NewReader(input)
	line := make([]byte, 0, 16)
	for {
		c, err := r.ReadByte()
		if err != nil {
			return "", err // EOF, even after 'yes', is not approval.
		}
		switch c {
		case '\r', '\n':
			_, err := io.WriteString(out, "\r\n")
			return string(line), err
		case '\x1b', '\x03', '\x04': // Escape, Ctrl-C, Ctrl-D.
			return "", ErrScanDeclined
		case '\x7f', '\b':
			if len(line) > 0 {
				line = line[:len(line)-1]
				if _, err := io.WriteString(out, "\b \b"); err != nil {
					return "", err
				}
			}
		default:
			if c < ' ' || c > '~' || len(line) == 64 {
				return "", ErrScanDeclined
			}
			line = append(line, c)
			if _, err := out.Write([]byte{c}); err != nil {
				return "", err
			}
		}
	}
}

// Bubble Tea treats normal EOF as the end of input without quitting the model.
// During confirmation it must instead fail closed. Preserve file descriptors
// so the program can still enter and restore raw terminal mode.
type confirmationReader struct{ io.Reader }

func (r confirmationReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if errors.Is(err, io.EOF) {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}

type confirmationFile struct{ *os.File }

func (f confirmationFile) Read(p []byte) (int, error) {
	return (confirmationReader{f.File}).Read(p)
}

func runConfirmation(ctx context.Context, cfg scan.Config, work scan.Workload, input io.Reader, out io.Writer, options ...tea.ProgramOption) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var reader io.Reader = confirmationReader{input}
	if file, ok := input.(*os.File); ok {
		reader = confirmationFile{file}
	}
	// Do not permit approval until the terminal size is known and reviewable.
	m := confirmationModel{cfg: cfg, work: work, width: 1, height: 1}
	options = append([]tea.ProgramOption{tea.WithInput(reader), tea.WithOutput(out), tea.WithoutSignalHandler()}, options...)
	p := tea.NewProgram(m, options...)
	finished, watcherDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(watcherDone)
		select {
		case <-ctx.Done():
			// Graceful quit joins the terminal reader before closing it.
			// WithContext uses forced shutdown, which skips that join.
			p.Quit()
		case <-finished:
		}
	}()
	final, err := p.Run()
	close(finished)
	<-watcherDone
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return fmt.Errorf("scan confirmation: %w", err)
	}
	if !final.(confirmationModel).accepted {
		return ErrScanDeclined
	}
	return nil
}

type confirmationModel struct {
	cfg               scan.Config
	work              scan.Workload
	width, height     int
	offset            int
	proceed, accepted bool
}

func (m confirmationModel) Init() tea.Cmd { return nil }

func (m confirmationModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = max(1, msg.Width), max(1, msg.Height)
	case tea.KeyPressMsg:
		switch msg.String() {
		case "esc", "ctrl+c", "ctrl+d", "q", "n":
			return m, tea.Quit
		case "tab", "shift+tab", "left", "right":
			m.proceed = !m.proceed
		case "enter":
			m.accepted = m.proceed && m.width >= 24 && m.height >= 10
			return m, tea.Quit
		case "up":
			m.offset--
		case "down":
			m.offset++
		case "pgup":
			m.offset -= max(1, m.height/2)
		case "pgdown":
			m.offset += max(1, m.height/2)
		}
	}
	m.offset = max(0, min(m.offset, max(0, len(warningLines(m.cfg, m.work, m.width))-m.bodyHeight())))
	return m, nil
}

func (m confirmationModel) bodyHeight() int {
	title := ansi.Wrap(Count(m.work.Probes)+" planned probes", m.width, " ")
	footer := ansi.Wrap("Tab selects; Enter decides. Up/Down scroll; Esc cancels.", m.width, " ")
	return max(1, m.height-(strings.Count(title, "\n")+1)-(strings.Count(footer, "\n")+1)-3)
}

func (m confirmationModel) View() tea.View {
	var text string
	if m.width < 24 || m.height < 10 {
		text = ansi.Wrap("Resize terminal to at least 24 columns and 10 rows to review this scan. Esc cancels.", m.width, " ")
		lines := strings.Split(text, "\n")
		text = strings.Join(lines[:min(len(lines), m.height)], "\n")
	} else {
		lines := warningLines(m.cfg, m.work, m.width)
		offset := min(m.offset, max(0, len(lines)-m.bodyHeight()))
		buttons := "[Cancel]  Continue"
		if m.proceed {
			buttons = " Cancel  [Continue]"
		}
		text = ansi.Wrap(Count(m.work.Probes)+" planned probes", m.width, " ") + "\n\n" + strings.Join(lines[offset:min(len(lines), offset+m.bodyHeight())], "\n") +
			"\n\n" + buttons + "\n" + ansi.Wrap("Tab selects; Enter decides. Up/Down scroll; Esc cancels.", m.width, " ")
	}
	v := tea.NewView(text)
	v.AltScreen = true
	return v
}
