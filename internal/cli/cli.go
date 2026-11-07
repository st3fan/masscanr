// Package cli validates user input before invoking the scan controller.
package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/st3fan/masscanr/internal/cloud"
	"github.com/st3fan/masscanr/internal/cloud/vultr"
	"github.com/st3fan/masscanr/internal/controller"
	"github.com/st3fan/masscanr/internal/remote"
	"github.com/st3fan/masscanr/internal/scan"
	"github.com/st3fan/masscanr/internal/tui"
	"golang.org/x/term"
)

type runFunc func(context.Context, scan.Config, io.Writer, func(controller.Event)) (controller.Summary, error)
type factory func(string, controller.Timeouts) runFunc

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	return execute(ctx, args, stdout, stderr, func(key string, timeouts controller.Timeouts) runFunc {
		c := &controller.Controller{Provider: vultr.New(key), Runner: remote.NewSSH(), Timeouts: timeouts}
		return c.Run
	}, vultr.New(""))
}

func execute(ctx context.Context, args []string, stdout, stderr io.Writer, build factory, catalog cloud.Catalog) error {
	if len(args) == 0 {
		return rootHelp(stdout)
	}
	if args[0] == "help" {
		if len(args) == 1 {
			return rootHelp(stdout)
		}
		if len(args) != 2 {
			return fmt.Errorf("usage: masscanr help [command]")
		}
		switch args[1] {
		case "scan", "list-regions", "list-instance-types":
			args = []string{args[1], "--help"}
		default:
			return fmt.Errorf("unknown command %q", args[1])
		}
	}
	switch args[0] {
	case "--help", "-h":
		if len(args) != 1 {
			return fmt.Errorf("usage: masscanr --help")
		}
		return rootHelp(stdout)
	case "--version":
		if len(args) != 1 {
			return fmt.Errorf("usage: masscanr --version")
		}
		_, err := fmt.Fprintln(stdout, "masscanr v2")
		return err
	case "scan":
		return executeScan(ctx, args[1:], stdout, stderr, build)
	case "list-regions":
		return listRegions(ctx, args[1:], stdout, stderr, catalog)
	case "list-instance-types":
		return listInstanceTypes(ctx, args[1:], stdout, stderr, catalog)
	default:
		return fmt.Errorf("unknown command %q; run 'masscanr --help' for usage", args[0])
	}
}

func rootHelp(out io.Writer) error {
	_, err := fmt.Fprint(out, `Usage: masscanr <command> [options]

Commands:
  scan                  Scan IPv4 CIDRs using temporary Vultr workers
  list-regions          Show region IDs, names, countries and continents
  list-instance-types   Show plans available in a region (default: atl)

Use 'masscanr <command> --help' for command options.
Use 'masscanr --version' to print the version.
`)
	return err
}

type scanView interface {
	Configure(tui.RunConfig, func())
	Start()
	Stop() error
	Update(controller.Event)
}

type viewFactory func(io.Writer, bool, int) scanView

func executeScan(ctx context.Context, args []string, stdout, stderr io.Writer, build factory) error {
	return executeScanWithView(ctx, args, stdout, stderr, build, func(out io.Writer, terminal bool, shards int) scanView { return tui.New(out, terminal, shards) })
}

func executeScanWithView(ctx context.Context, args []string, stdout, stderr io.Writer, build factory, newView viewFactory) (err error) {
	cfg := scan.Defaults()
	fs := flag.NewFlagSet("masscanr scan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.IntVar(&cfg.Shards, "shards", cfg.Shards, "number of temporary VPS workers")
	fs.Uint64Var(&cfg.Rate, "rate", cfg.Rate, "packets/second per worker")
	fs.StringVar(&cfg.Region, "region", cfg.Region, "Vultr region identifier (Atlanta: atl)")
	fs.StringVar(&cfg.InstanceType, "instance-type", cfg.InstanceType, "Vultr instance plan")
	var ports, output, envFile, slug string
	fs.StringVar(&ports, "ports", scan.TopPorts, "TCP ports/ranges, e.g. 22,80,443,8000-8100 (default: Nmap top 100)")
	fs.StringVar(&ports, "p", scan.TopPorts, "alias for --ports")
	fs.StringVar(&output, "output", "", "new local list file plus CSV/JSONL exports (default: timestamped masscanr-*); - streams list to stdout")
	fs.StringVar(&slug, "slug", "", "text in generated filenames (with --output=-, names the CSV/JSONL exports)")
	fs.StringVar(&envFile, "env-file", ".env", "dotenv file; VULTR_API_KEY in the environment takes precedence")
	timeouts := controller.DefaultTimeouts()
	fs.DurationVar(&timeouts.Scan, "timeout", timeouts.Scan, "maximum scanning time, followed by cleanup")
	fs.DurationVar(&timeouts.Cleanup, "cleanup-timeout", timeouts.Cleanup, "independent deadline for deleting workers")
	var noTUI, version bool
	fs.BoolVar(&noTUI, "no-tui", false, "print lifecycle logs instead of a live table")
	fs.BoolVar(&version, "version", false, "print version and exit")
	fs.Usage = func() {
		fmt.Fprint(stderr, "Usage: masscanr scan [options] IPv4-CIDR [IPv4-CIDR ...]\n\nTemporary Vultr workers run root masscan. The local controller needs no root.\nDefault ports: Nmap 7.95 top 100 TCP. Rate is per worker.\n\n")
		fs.PrintDefaults()
	}
	ordered, orderErr := interspersed(fs, args)
	if orderErr != nil {
		return orderErr
	}
	if err = fs.Parse(ordered); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if version {
		fmt.Fprintln(stdout, "masscanr v2")
		return nil
	}
	if strings.ContainsAny(slug, "/\\") || strings.IndexFunc(slug, unicode.IsControl) >= 0 {
		return fmt.Errorf("--slug must not contain path separators or control characters")
	}
	if cfg.Targets, err = scan.ParseTargets(fs.Args()); err != nil {
		return err
	}
	if cfg.Ports, err = scan.ParsePorts(ports); err != nil {
		return err
	}
	if err = cfg.Validate(); err != nil {
		return err
	}
	if timeouts.Scan <= 0 || timeouts.Cleanup <= 0 {
		return fmt.Errorf("timeouts must be positive")
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	key, err := apiKey(envFile)
	if err != nil {
		return err
	}
	var file *os.File
	exportOutput := output
	if output != "-" {
		if output == "" {
			output = outputFilename(slug, time.Now())
		}
		file, err = os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return fmt.Errorf("create output: %w", err)
		}
		exportOutput = output
	} else {
		// Keep stdout streaming while retaining a private source for conversion.
		exportOutput = outputFilename(slug, time.Now())
		file, err = os.CreateTemp("", "masscanr-results-*.list")
		if err != nil {
			return fmt.Errorf("create temporary export source: %w", err)
		}
		temporary := file.Name()
		defer func() { err = errors.Join(err, os.Remove(temporary)) }()
	}
	defer func() {
		if file != nil {
			err = errors.Join(err, file.Sync(), file.Close())
		}
	}()
	var destination io.Writer = file
	if output == "-" {
		destination = io.MultiWriter(stdout, file)
	}
	terminal := false
	if f, ok := stderr.(*os.File); ok {
		terminal = term.IsTerminal(int(f.Fd()))
	}
	runCtx, cancelScan := context.WithCancel(ctx)
	defer cancelScan()
	view := newView(stderr, terminal && !noTUI, cfg.Shards)
	paths := exportPaths(exportOutput)
	view.Configure(tui.NewRunConfig(cfg, timeouts, output, paths), cancelScan)
	fmt.Fprintf(stderr, "Targets: %v | workers: %d | rate: %d pps/worker | output: %s\n", cfg.Targets, cfg.Shards, cfg.Rate, output)
	view.Start()
	stopped := false
	stopView := func() error {
		if stopped {
			return nil
		}
		stopped = true
		return view.Stop()
	}
	defer func() { err = errors.Join(err, stopView()) }()
	summary, scanErr := build(key, timeouts)(runCtx, cfg, destination, view.Update)
	source := file.Name()
	scanErr = errors.Join(scanErr, file.Sync(), file.Close())
	file = nil
	if len(summary.Undeleted) > 0 && scanErr == nil {
		scanErr = fmt.Errorf("worker deletion unconfirmed")
	}
	var exportErr error
	if scanErr == nil {
		view.Update(controller.Event{State: "exporting"})
		if exportErr = exportResults(source, exportOutput); exportErr != nil {
			exportErr = fmt.Errorf("scan completed but CSV/JSONL export failed (native list output: %s): %w", output, exportErr)
		}
	}
	uiErr := stopView()
	if uiErr != nil {
		uiErr = fmt.Errorf("terminal UI: %w", uiErr)
	}
	err = errors.Join(scanErr, exportErr, uiErr)
	if scanErr != nil {
		fmt.Fprintf(stderr, "Incomplete scan. Partial results: %s | run: %s | found: %d\n", output, summary.RunID, summary.Found)
	} else {
		fmt.Fprintf(stderr, "Scan complete. Results: %s | run: %s | found: %d | workers deleted\n", output, summary.RunID, summary.Found)
		if exportErr == nil {
			fmt.Fprintf(stderr, "Exports: %s | %s\n", paths[0], paths[1])
		}
	}
	if len(summary.Undeleted) > 0 {
		fmt.Fprintf(stderr, "VPS deletion unconfirmed: %s\n", strings.Join(summary.Undeleted, ", "))
	}
	if err != nil {
		fmt.Fprintln(stderr, "Run failed; see error details.")
	} else {
		fmt.Fprintln(stderr, "Run complete.")
	}
	return err
}

func outputFilename(slug string, when time.Time) string {
	prefix := "masscanr"
	if slug != "" {
		prefix += "-" + slug
	}
	return prefix + "-" + when.UTC().Format("20060102T150405.000000000Z") + ".list"
}

// flag normally stops at the first CIDR; keep masscan-style options usable
// before or after targets, while letting flag handle values and errors.
func interspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var options, targets []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			targets = append(targets, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			targets = append(targets, arg)
			continue
		}
		options = append(options, arg)
		name, _, hasValue := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		f := fs.Lookup(name)
		if f == nil || hasValue {
			continue
		}
		if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
			continue
		}
		if i+1 >= len(args) {
			return nil, fmt.Errorf("option %s needs a value", arg)
		}
		i++
		options = append(options, args[i])
	}
	return append(append(options, "--"), targets...), nil
}

// Only read the key needed by this app. No shell expansion/evaluation occurs.
func apiKey(path string) (string, error) {
	if key, exists := os.LookupEnv("VULTR_API_KEY"); exists {
		if strings.TrimSpace(key) == "" {
			return "", fmt.Errorf("VULTR_API_KEY is empty")
		}
		return key, nil
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("VULTR_API_KEY is required in the environment or %s", path)
	}
	if err != nil {
		return "", fmt.Errorf("read dotenv: %w", err)
	}
	defer file.Close()
	s := bufio.NewScanner(file)
	for s.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s.Text()), "export "))
		name, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(name) != "VULTR_API_KEY" {
			continue
		}
		value = strings.TrimSpace(value)
		if strings.HasPrefix(value, "\"") {
			// Locate the closing quote before an optional trailing comment.
			end := 1
			escaped := false
			for ; end < len(value); end++ {
				if value[end] == '"' && !escaped {
					break
				}
				if value[end] == '\\' && !escaped {
					escaped = true
				} else {
					escaped = false
				}
			}
			if end >= len(value) {
				return "", fmt.Errorf("invalid quoted VULTR_API_KEY in dotenv")
			}
			value, err = strconv.Unquote(value[:end+1])
			if err != nil {
				return "", fmt.Errorf("invalid quoted VULTR_API_KEY in dotenv")
			}
		} else if strings.HasPrefix(value, "'") {
			end := strings.Index(value[1:], "'")
			if end < 0 {
				return "", fmt.Errorf("invalid quoted VULTR_API_KEY in dotenv")
			}
			value = value[1 : end+1]
		} else {
			value, _, _ = strings.Cut(value, " #")
			value = strings.TrimSpace(value)
		}
		if value == "" {
			return "", fmt.Errorf("VULTR_API_KEY is empty")
		}
		return value, nil
	}
	if err := s.Err(); err != nil {
		return "", fmt.Errorf("read dotenv: %w", err)
	}
	return "", fmt.Errorf("VULTR_API_KEY is required")
}
