package cli

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/st3fan/masscanr/internal/scanfile"
)

type contextReader struct {
	ctx context.Context
	io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}

func documentArgs(fs *flag.FlagSet, args []string) (string, error) {
	ordered, err := interspersed(fs, args)
	if err != nil {
		return "", err
	}
	if err = fs.Parse(ordered); err != nil {
		return "", err
	}
	if fs.NArg() != 1 {
		return "", fmt.Errorf("provide exactly one .scan document")
	}
	return fs.Arg(0), nil
}

func inspectScan(ctx context.Context, args []string, stdout, stderr io.Writer) (err error) {
	fs := flag.NewFlagSet("masscanr inspect", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "Usage: masscanr inspect FILE.scan\n\nInspect saved configuration, results and last recorded state offline.")
	}
	path, err := documentArgs(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	in, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, in.Close()) }()
	s, err := scanfile.Replay(contextReader{ctx, in}, nil)
	if err != nil {
		return fmt.Errorf("inspect scan: %w", err)
	}
	out := bufio.NewWriter(stdout)
	fmt.Fprintf(out, "Run: %q\nFormat: %s (schema %d)\nController: %q\nCreated: %s\n", s.Header.RunID, s.Header.Format, s.Header.Schema, s.Header.ControllerVersion, s.Header.CreatedAt.Format("2006-01-02T15:04:05Z"))
	scanOutcome, cleanup := "unconfirmed (partial results)", "unconfirmed"
	if s.Finished != nil {
		scanOutcome = s.Finished.ScanOutcome
		cleanup = s.Finished.CleanupOutcome
	}
	if s.Truncated {
		scanOutcome = "unconfirmed (truncated document)"
		fmt.Fprintln(stderr, "Partial results: ignored an unterminated final record.")
	}
	fmt.Fprintf(out, "Scan outcome: %s\nCleanup outcome: %s\nAccepted findings: %d\nRecords: %d\n", scanOutcome, cleanup, s.Found, s.Seq)
	config, err := json.MarshalIndent(s.Header.Config, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Configuration:\n%s\nLast recorded workers (historical observations):\n", config)
	workers := make([]scanfile.Worker, 0, len(s.Workers))
	for _, w := range s.Workers {
		workers = append(workers, w)
	}
	sort.Slice(workers, func(i, j int) bool {
		if workers[i].Shard != workers[j].Shard {
			return workers[i].Shard < workers[j].Shard
		}
		return workers[i].InstanceID < workers[j].InstanceID
	})
	for _, w := range workers {
		fmt.Fprintf(out, "  shard %d | instance %q | IP %q | scan %s | cleanup %s | findings %d\n", w.Shard, w.InstanceID, w.IP, w.Phase, w.Cleanup, s.Counts[w.Shard])
		if b, ok := s.Backends[w.Shard]; ok {
			fmt.Fprintf(out, "    backend %s | version %q | build %q | Alpine %q | packages %q\n", b.Name, b.Version, b.Build, b.Release, b.Packages)
		}
		if w.Detail != "" {
			fmt.Fprintf(out, "    detail: %q\n", w.Detail)
		}
	}
	if s.Finished != nil {
		if s.Finished.Error != "" {
			fmt.Fprintf(out, "Error: %q\n", s.Finished.Error)
		}
		if s.Finished.ReconciliationUnresolved {
			fmt.Fprintln(out, "Worker reconciliation remains unresolved.")
		}
	}
	for _, id := range s.Undeleted() {
		fmt.Fprintf(out, "Deletion unconfirmed: %q\n", id)
	}
	return out.Flush()
}

func exportScan(ctx context.Context, args []string, stdout, stderr io.Writer) (err error) {
	fs := flag.NewFlagSet("masscanr export", flag.ContinueOnError)
	fs.SetOutput(stderr)
	format := fs.String("format", "", "finding format: list, csv or jsonl")
	output := fs.String("output", "", "new export file (required; never overwritten)")
	allow := fs.Bool("allow-incomplete", false, "explicitly export partial findings from a failed, cancelled or unfinished document")
	fs.Usage = func() {
		fmt.Fprint(stderr, "Usage: masscanr export FILE.scan --format=list|csv|jsonl --output=PATH [--allow-incomplete]\n\n")
		fs.PrintDefaults()
	}
	path, err := documentArgs(fs, args)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	if (*format != "list" && *format != "csv" && *format != "jsonl") || *output == "" || *output == "-" {
		return fmt.Errorf("provide --format=list|csv|jsonl and --output=PATH")
	}
	in, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, in.Close()) }()
	file, err := os.CreateTemp(filepath.Dir(*output), ".masscanr-export-*")
	if err != nil {
		return err
	}
	defer func() { file.Close(); err = errors.Join(err, os.Remove(file.Name())) }()
	s, err := writeFormat(contextReader{ctx, in}, file, *format, *allow)
	if err != nil {
		return err
	}
	if err = errors.Join(file.Sync(), file.Close()); err != nil {
		return err
	}
	if err = os.Link(file.Name(), *output); err != nil {
		return fmt.Errorf("create export: %w", err)
	}
	if !s.Complete() {
		fmt.Fprintln(stderr, "Partial results exported: scan completion is unconfirmed or the scan failed/cancelled; findings do not establish full coverage.")
	}
	if s.Truncated {
		fmt.Fprintln(stderr, "Ignored an unterminated final record.")
	}
	if s.Finished != nil && s.Finished.CleanupOutcome != "complete" {
		fmt.Fprintln(stderr, "Worker cleanup is incomplete; inspect the .scan document for details.")
	}
	_, err = fmt.Fprintf(stderr, "Export: %s | findings: %d\n", *output, s.Found)
	return err
}

func writeFormat(in io.Reader, out io.Writer, format string, allow bool) (scanfile.State, error) {
	buffer := bufio.NewWriter(out)
	cw := csv.NewWriter(buffer)
	jw := json.NewEncoder(buffer)
	if format == "csv" {
		if err := cw.Write([]string{"ip", "port", "addr", "protocol", "state", "timestamp"}); err != nil {
			return scanfile.State{}, err
		}
	}
	if format == "list" {
		fmt.Fprintln(buffer, "#masscan")
	}
	s, err := scanfile.Replay(in, func(f scanfile.Finding) error {
		row := exportFinding(f)
		switch format {
		case "csv":
			return cw.Write([]string{row.IP, strconv.FormatUint(uint64(row.Port), 10), row.Addr, row.Protocol, row.State, strconv.FormatUint(row.Timestamp, 10)})
		case "jsonl":
			return jw.Encode(row)
		case "list":
			_, err := fmt.Fprintf(buffer, "# shard %d\nopen %s %d %s %d\n", f.Shard, f.Protocol, f.Port, f.IP, f.Timestamp)
			return err
		default:
			return fmt.Errorf("unknown export format %q", format)
		}
	})
	if err != nil {
		return s, err
	}
	if !s.Complete() && !allow {
		return s, fmt.Errorf("scan results are incomplete; use --allow-incomplete explicitly")
	}
	if format == "list" {
		status := "incomplete"
		if s.Complete() {
			status = "complete"
		}
		fmt.Fprintf(buffer, "# masscanr run=%s backend=%s\n# status %s\n", s.Header.RunID, s.Header.Config.Backend, status)
	}
	if format == "csv" {
		cw.Flush()
		if err = cw.Error(); err != nil {
			return s, err
		}
	}
	return s, buffer.Flush()
}
