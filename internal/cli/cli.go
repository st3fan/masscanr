// Package cli validates user input before invoking the scan controller.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/netip"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/st3fan/masscanr/internal/backend"
	"github.com/st3fan/masscanr/internal/backend/registry"
	"github.com/st3fan/masscanr/internal/cloud"
	"github.com/st3fan/masscanr/internal/cloud/vultr"
	"github.com/st3fan/masscanr/internal/controller"
	"github.com/st3fan/masscanr/internal/remote"
	"github.com/st3fan/masscanr/internal/ripe"
	"github.com/st3fan/masscanr/internal/scan"
	"github.com/st3fan/masscanr/internal/scanfile"
	"github.com/st3fan/masscanr/internal/tui"
	"golang.org/x/term"
)

type runFunc func(context.Context, scan.Config, io.Writer, func(controller.Event)) (controller.Summary, error)
type factory func(string, controller.Timeouts, backend.Backend) runFunc

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) > 0 && args[0] == "scan" {
		return fmt.Errorf("interactive scans have been retired; use masscanr serve and the REST API")
	}
	return execute(ctx, args, stdout, stderr, func(key string, timeouts controller.Timeouts, engine backend.Backend) runFunc {
		c := &controller.Controller{Backend: engine, Provider: vultr.New(key), Runner: remote.NewSSH(), Timeouts: timeouts}
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
		case "scan", "inspect", "export", "list-regions", "list-instance-types":
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
		_, err := fmt.Fprintln(stdout, "masscanr v9")
		return err
	case "inspect":
		return inspectScan(ctx, args[1:], stdout, stderr)
	case "export":
		return exportScan(ctx, args[1:], stdout, stderr)
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
  serve                 Start the REST service (see masscanr serve --help)
  inspect               Inspect a saved .scan document offline
  export                Export .scan findings as list, CSV or JSONL
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
	Finish(context.Context, controller.Summary, error)
	Stop() error
	Update(controller.Event)
}

type viewFactory func(io.Writer, bool, int) scanView

func executeScan(ctx context.Context, args []string, stdout, stderr io.Writer, build factory) error {
	return executeScanWithView(ctx, args, stdout, stderr, build, func(out io.Writer, terminal bool, shards int) scanView { return tui.New(out, terminal, shards) })
}

func executeScanWithView(ctx context.Context, args []string, stdout, stderr io.Writer, build factory, newView viewFactory) (err error) {
	return executeScanWithConfirmation(ctx, args, stdout, stderr, build, newView, confirmLargeScan)
}

type confirmationFunc func(context.Context, scan.Config, scan.Workload, io.Writer, bool) error

func executeScanWithConfirmation(ctx context.Context, args []string, stdout, stderr io.Writer, build factory, newView viewFactory, confirm confirmationFunc) (err error) {
	return executeScanWithResolver(ctx, args, stdout, stderr, build, newView, confirm, ripe.New().Resolve)
}

type targetResolver func(context.Context, []scan.Target) ([]scan.Target, error)

func executeScanWithResolver(ctx context.Context, args []string, stdout, stderr io.Writer, build factory, newView viewFactory, confirm confirmationFunc, resolve targetResolver) (err error) {
	cfg := scan.Defaults()
	fs := flag.NewFlagSet("masscanr scan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&cfg.Backend, "backend", cfg.Backend, "scanning backend: masscan or zmap")
	fs.IntVar(&cfg.Shards, "shards", cfg.Shards, "number of temporary VPS workers")
	fs.Uint64Var(&cfg.Rate, "rate", cfg.Rate, "packets/second per worker")
	fs.StringVar(&cfg.Region, "region", cfg.Region, "Vultr region identifier (Atlanta: atl)")
	fs.StringVar(&cfg.InstanceType, "instance-type", cfg.InstanceType, "Vultr instance plan")
	var ports, output, slug string
	fs.StringVar(&ports, "ports", scan.TopPorts, "TCP ports/ranges, e.g. 22,80,443,8000-8100 (default: Nmap top 100)")
	fs.StringVar(&ports, "p", scan.TopPorts, "alias for --ports")
	fs.StringVar(&output, "output", "", "new .scan document plus CSV/JSONL exports (default: timestamped masscanr-*.scan); - streams scan JSON Lines to stdout")
	fs.StringVar(&slug, "slug", "", "text in generated filenames (with --output=-, names the CSV/JSONL exports)")
	timeouts := controller.DefaultTimeouts()
	fs.DurationVar(&timeouts.Scan, "timeout", timeouts.Scan, "maximum scanning time, followed by cleanup")
	fs.DurationVar(&timeouts.Cleanup, "cleanup-timeout", timeouts.Cleanup, "independent deadline for deleting workers")
	var noTUI, version bool
	fs.BoolVar(&noTUI, "no-tui", false, "print lifecycle logs instead of a live table")
	fs.BoolVar(&version, "version", false, "print version and exit")
	fs.Usage = func() {
		fmt.Fprint(stderr, "Usage: masscanr scan [options] TARGET [TARGET ...]\n\nTemporary Vultr workers run the selected scanner as root. The local controller needs no root.\nTargets: IPv4 CIDR (10.0.0.0/8 or 10/8), IPv4 address, or AS1234.\nAS targets resolve through RIPE to IPv4 origin prefixes before confirmation.\nDefault ports: Nmap 7.95 top 100 TCP. Rate is per worker.\nCredentials: VULTR_API_TOKEN must be set in the environment.\nMore than 1,000,000 planned probes (unique addresses x TCP ports) requires interactive confirmation.\nNo unattended bypass; stdin and stderr must be terminals for large scans.\n\n")
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
		fmt.Fprintln(stdout, "masscanr v9")
		return nil
	}
	if strings.ContainsAny(slug, "/\\") || strings.IndexFunc(slug, unicode.IsControl) >= 0 {
		return fmt.Errorf("--slug must not contain path separators or control characters")
	}
	requested, err := scan.ParseRequestedTargets(fs.Args())
	if err != nil {
		return err
	}
	if cfg.Ports, err = scan.ParsePorts(ports); err != nil {
		return err
	}
	if cfg.Shards > scanfile.MaxShards {
		return fmt.Errorf("scan documents support at most %d shards", scanfile.MaxShards)
	}
	if err = cfg.ValidateOptions(); err != nil {
		return err
	}
	engine, err := registry.Resolve(cfg.Backend)
	if err != nil {
		return err
	}
	// A full IPv4 scope checks backend option limits without treating a small
	// unresolved AS as empty. Actual scope constraints are checked after lookup.
	settings := cfg.Intent()
	settings.Targets = []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}
	if err = engine.Validate(settings, cfg.Shards); err != nil {
		return err
	}
	if timeouts.Scan <= 0 || timeouts.Cleanup <= 0 {
		return fmt.Errorf("timeouts must be positive")
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if cfg.RequestedTargets, err = resolve(ctx, requested); err != nil {
		return err
	}
	cfg = cfg.Clone()
	cfg.Targets = scan.TargetUnion(cfg.RequestedTargets)
	if err = cfg.Validate(); err != nil {
		return err
	}
	if err = engine.Validate(cfg.Intent(), cfg.Shards); err != nil {
		return err
	}
	work, err := cfg.Intent().Workload()
	if err != nil {
		return err
	}
	if work.RequiresConfirmation() {
		if err := describeTargets(stderr, cfg, work); err != nil {
			return err
		}
		if err := confirm(ctx, cfg.Clone(), work, stderr, noTUI); err != nil {
			return err
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	token, err := apiToken()
	if err != nil {
		return err
	}
	if !work.RequiresConfirmation() {
		if err := describeTargets(stderr, cfg, work); err != nil {
			return err
		}
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
		file, err = os.CreateTemp("", "masscanr-results-*.scan")
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
	fmt.Fprintf(stderr, "Backend: %s | Targets: %v | workers: %d | rate: %d pps/worker | output: %s\n", engine.Name(), cfg.Targets, cfg.Shards, cfg.Rate, output)
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
	persisted := privateDestination{Destination: scanfile.Destination{Writer: destination, SyncFunc: file.Sync, Output: output, Exports: paths}, token: token}
	summary, scanErr := build(token, timeouts, engine)(runCtx, cfg, persisted, view.Update)
	if summary.Identity.Name == "" {
		summary.Identity.Name = engine.Name()
	}
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
			exportErr = fmt.Errorf("scan completed but CSV/JSONL export failed (scan document: %s): %w", output, exportErr)
		}
	}
	view.Finish(ctx, summary, errors.Join(scanErr, exportErr))
	uiErr := stopView()
	if uiErr != nil {
		uiErr = fmt.Errorf("terminal UI: %w", uiErr)
	}
	err = errors.Join(scanErr, exportErr, uiErr)
	if scanErr != nil && summary.ScanOutcome == "complete" {
		fmt.Fprintf(stderr, "Backend: %s\n", summary.Identity.String())
		fmt.Fprintf(stderr, "Scan complete. Results: %s | run: %s | found: %d | cleanup incomplete or document persistence failed\n", output, summary.RunID, summary.Found)
	} else if scanErr != nil {
		fmt.Fprintf(stderr, "Backend: %s\n", summary.Identity.String())
		fmt.Fprintf(stderr, "Incomplete scan. Partial results: %s | run: %s | found: %d\n", output, summary.RunID, summary.Found)
	} else {
		fmt.Fprintf(stderr, "Backend: %s\n", summary.Identity.String())
		fmt.Fprintf(stderr, "Scan complete. Results: %s | run: %s | found: %d | workers deleted\n", output, summary.RunID, summary.Found)
		if exportErr == nil {
			fmt.Fprintf(stderr, "Exports: %s | %s\n", paths[0], paths[1])
		}
	}
	fmt.Fprintf(stderr, "Requested targets: %s | effective IPv4 prefixes: %d | addresses: %s | planned probes: %s\n", targetNames(cfg.RequestedTargets), len(cfg.Targets), tui.Count(work.Addresses), tui.Count(work.Probes))
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
	return prefix + "-" + when.UTC().Format("20060102T150405.000000000Z") + ".scan"
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

// apiToken reads credentials only from the controller environment.
func apiToken() (string, error) {
	token := os.Getenv("VULTR_API_TOKEN")
	if strings.TrimSpace(token) == "" {
		return "", fmt.Errorf("VULTR_API_TOKEN is required in the environment")
	}
	return token, nil
}

// Keep credentials out of persisted diagnostics while retaining destination sync.
type privateDestination struct {
	scanfile.Destination
	token string
}

func (d privateDestination) Sanitize(s string) string {
	return strings.ReplaceAll(s, d.token, "[REDACTED]")
}
