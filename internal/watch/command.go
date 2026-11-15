package watch

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/term"
)

type options struct {
	ID, Server string
	All, Plain bool
	Interval   time.Duration
}

func parse(args []string, out io.Writer) (options, error) {
	var o options
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.StringVar(&o.Server, "server", "http://127.0.0.1:8080", "service URL")
	fs.DurationVar(&o.Interval, "interval", 2*time.Second, "poll interval (100ms..1m)")
	fs.BoolVar(&o.All, "all", false, "continuously watch all active runs, including while idle")
	fs.BoolVar(&o.Plain, "plain", false, "plain status output (automatic when redirected)")
	fs.Usage = func() {
		fmt.Fprintln(out, "Usage: masscanr-cli watch <run-id> | --all [--server URL] [--interval 2s] [--plain]")
		fs.PrintDefaults()
	}
	// Accept options on either side of the positional ID.
	var flags, positionals []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") {
			positionals = append(positionals, arg)
			continue
		}
		flags = append(flags, arg)
		if arg == "--server" || arg == "-server" || arg == "--interval" || arg == "-interval" {
			if i+1 >= len(args) {
				return o, fmt.Errorf("%s requires a value", arg)
			}
			i++
			flags = append(flags, args[i])
		}
	}
	if err := fs.Parse(append(flags, positionals...)); err != nil {
		return o, err
	}
	if o.All && fs.NArg() != 0 || !o.All && fs.NArg() != 1 {
		return o, fmt.Errorf("provide exactly one run ID or --all")
	}
	if !o.All {
		o.ID = fs.Arg(0)
		if !validID(o.ID) {
			return o, fmt.Errorf("invalid run ID")
		}
	}
	if o.Interval < 100*time.Millisecond || o.Interval > time.Minute {
		return o, fmt.Errorf("interval must be between 100ms and 1m")
	}
	return o, nil
}

// Run never contacts the database, cloud provider, or worker machines.
func Run(ctx context.Context, args []string, in io.Reader, out io.Writer) error {
	o, err := parse(args, out)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	client, err := NewClient(o.Server)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	initial, err := poll(ctx, client, o.ID, snapshot{})
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	input, okIn := in.(*os.File)
	output, okOut := out.(*os.File)
	terminal := okIn && okOut && term.IsTerminal(int(input.Fd())) && term.IsTerminal(int(output.Fd()))
	if o.Plain || !terminal {
		return plain(ctx, client, o, initial, out)
	}
	return runTerminal(ctx, cancel, client, o, initial, in, out)
}

// Bubble Tea otherwise treats EOF as input exhaustion without leaving the UI.
type inputReader struct{ io.Reader }

func (r inputReader) Read(p []byte) (int, error) {
	n, e := r.Reader.Read(p)
	if errors.Is(e, io.EOF) {
		e = io.ErrUnexpectedEOF
	}
	return n, e
}

type inputFile struct{ *os.File }

func (f inputFile) Read(p []byte) (int, error) { return (inputReader{f.File}).Read(p) }

func runTerminal(ctx context.Context, cancel context.CancelFunc, api source, o options, s snapshot, in io.Reader, out io.Writer, opts ...tea.ProgramOption) error {
	var reader io.Reader = inputReader{in}
	if f, ok := in.(*os.File); ok {
		reader = inputFile{f}
	}
	m := newModel(ctx, cancel, api, o, s)
	opts = append([]tea.ProgramOption{tea.WithInput(reader), tea.WithOutput(out), tea.WithoutSignalHandler()}, opts...)
	p := tea.NewProgram(m, opts...)
	finished, joined := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(joined)
		select {
		case <-ctx.Done():
			p.Quit()
		case <-finished:
		}
	}()
	final, err := p.Run()
	close(finished)
	<-joined
	cancel() // Cancel outstanding HTTP requests even on EOF or renderer failure.
	if result, ok := final.(model); ok && result.fatal != nil {
		return result.fatal
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return nil
	}
	return err
}

func plain(ctx context.Context, api source, o options, s snapshot, out io.Writer) error {
	last := ""
	failures := 0
	stale := ""
	for {
		text := renderPlain(o.Server, s, stale)
		if text != last {
			if _, err := fmt.Fprintln(out, text); err != nil {
				return err
			}
			last = text
		}
		if !o.All && len(s.Runs) == 1 && s.Runs[0].Settled {
			return nil
		}
		timer := time.NewTimer(delay(o.Interval, failures))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		next, err := poll(ctx, api, o.ID, s)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			if !retryable(err) {
				return err
			}
			failures++
			stale = fmt.Sprintf("STALE since %s: %s", s.Updated.Format(time.RFC3339), err)
		} else {
			s = next
			failures = 0
			stale = ""
		}
	}
}
