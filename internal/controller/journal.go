package controller

import (
	"io"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/st3fan/masscanr/internal/scan"
	"github.com/st3fan/masscanr/internal/scanfile"
)

type redactor struct {
	mu       sync.Mutex
	secrets  []string
	external func(string) string
}

type sanitizedError struct {
	cause   error
	message string
}

func (e sanitizedError) Error() string { return e.message }
func (e sanitizedError) Unwrap() error { return e.cause }

func (r *redactor) remember(secret string) {
	if secret != "" {
		r.mu.Lock()
		r.secrets = append(r.secrets, secret)
		r.mu.Unlock()
	}
}
func (r *redactor) clean(s string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.external != nil {
		s = r.external(s)
	}
	for _, secret := range r.secrets {
		s = strings.ReplaceAll(s, secret, "[REDACTED]")
	}
	s = strings.Map(func(c rune) rune {
		if unicode.IsControl(c) && c != '\n' && c != '\t' {
			return -1
		}
		return c
	}, s)
	if len(s) > 4096 {
		s = strings.ToValidUTF8(s[:4096], "") + " [truncated]"
	}
	return s
}

func startJournal(out io.Writer, cfg scan.Config, t Timeouts, run string, seed uint64, cancel func()) (*scanfile.Writer, *redactor, error) {
	output, exports := "(writer)", [2]string{}
	if d, ok := out.(interface{ ScanPaths() (string, [2]string) }); ok {
		output, exports = d.ScanPaths()
	}
	r := &redactor{}
	if d, ok := out.(interface{ Sanitize(string) string }); ok {
		r.external = d.Sanitize
	}
	if len(cfg.RequestedTargets) == 0 {
		cfg.RequestedTargets = scan.DirectTargets(cfg.Targets)
	}
	h := scanfile.Header{Format: scanfile.Format, Schema: scanfile.Schema, RunID: run, CreatedAt: time.Now().UTC(), ControllerVersion: "v9", Label: "masscanr", OwnershipTag: "masscanr-run-" + run, Config: scanfile.Config{Targets: cfg.Targets, RequestedTargets: cfg.RequestedTargets, Ports: cfg.PortSpec(), Backend: cfg.Backend, Seed: strconv.FormatUint(seed, 10), Shards: cfg.Shards, Rate: cfg.Rate, Provider: "vultr", Region: cfg.Region, InstanceType: cfg.InstanceType, Output: output, Exports: exports, Timeouts: map[string]string{"create": t.Create.String(), "ready": t.Ready.String(), "prepare": t.Prepare.String(), "scan": t.Scan.String(), "cleanup": t.Cleanup.String(), "delete": t.Delete.String(), "poll": t.Poll.String()}}}
	w, err := scanfile.NewWriter(out, h, func(error) { cancel() })
	return w, r, err
}

// Persist lifecycle independently of UI rendering. Calls are serialized both for
// deterministic attribution and because UI callbacks may not be concurrency safe.
func journalEvents(w *scanfile.Writer, r *redactor, emit func(Event)) func(Event) {
	var mu sync.Mutex
	phases := make(map[int]string)
	lastProgress := make(map[int]time.Time)
	return func(e Event) {
		mu.Lock()
		defer mu.Unlock()
		if e.Shard > 0 || e.ID != "" {
			detail := r.clean(e.Detail)
			if e.Prepared != nil {
				p := e.Prepared
				_ = w.Append(scanfile.Record{Type: "backend", Backend: &scanfile.Backend{Shard: e.Shard, Name: p.Identity.Name, Version: p.Identity.Version, Build: p.Identity.Build, Release: p.Release, Packages: r.clean(p.Packages)}}, true)
			}
			switch e.State {
			case "deleting":
				_ = w.Worker(e.Shard, e.ID, e.IP, "", "deleting", detail)
			case "shutdown":
				_ = w.Worker(e.Shard, e.ID, e.IP, "", "complete", detail)
			case "cleanup failed":
				_ = w.Worker(e.Shard, e.ID, e.IP, "", "incomplete", detail)
			case "finding": // The finding sink already persisted the observation.
			default:
				boundary := phases[e.Shard] != e.State
				if boundary {
					_ = w.Worker(e.Shard, e.ID, e.IP, e.State, "", detail)
					phases[e.Shard] = e.State
				}
				if e.Progress != nil && (boundary || time.Since(lastProgress[e.Shard]) >= time.Second) {
					p := e.Progress
					stored := scanfile.Progress{Shard: e.Shard, Waiting: p.Waiting}
					if !p.PercentUnknown {
						v := p.Percent
						stored.Percent = &v
					}
					if !p.RateUnknown {
						v := p.Rate
						stored.Rate = &v
					}
					if !p.ETAUnknown && p.ETA >= 0 {
						v := p.ETA.Seconds()
						stored.ETASeconds = &v
					}
					_ = w.Append(scanfile.Record{Type: "progress", Progress: &stored}, false)
					lastProgress[e.Shard] = time.Now()
				}
			}
		}
		e.Detail = r.clean(e.Detail)
		if emit != nil {
			emit(e)
		}
	}
}
