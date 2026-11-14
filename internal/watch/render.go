package watch

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/st3fan/masscanr/internal/runapi"
	"github.com/st3fan/masscanr/internal/tui"
)

func known(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}
func runLines(r runapi.Run, tab, width int) []string {
	status := "active"
	if r.Settled {
		status = "settled"
	}
	lines := []string{
		fmt.Sprintf("Run %s | scan %s | revision %d", r.ID, r.ScanID, r.Plan.Revision),
		fmt.Sprintf("%s | %s | stop requested: %t", status, r.Phase, r.StopRequested),
		fmt.Sprintf("Scan: %s | Cleanup: %s", known(r.Summary.ScanOutcome), known(r.Summary.CleanupOutcome)),
	}
	if r.Error != "" {
		lines = append(lines, "Error: "+r.Error)
	}
	if len(r.Summary.Undeleted) > 0 {
		lines = append(lines, "Undeleted: "+strings.Join(r.Summary.Undeleted, ", "))
	}
	if r.Summary.ReconciliationUnresolved {
		lines = append(lines, "Worker reconciliation unresolved")
	}
	if tab == 1 {
		return append(lines, tui.SnapshotConfigLines(r, max(1, width))...)
	}
	cfg := r.Plan.Config
	lines = append(lines, fmt.Sprintf("Backend %s | shards %d | requested %d pps/worker", cfg.Backend, cfg.Shards, cfg.Rate), "Ports: "+cfg.PortSpec())
	if r.Summary.ScanOutcome != "" {
		lines = append(lines, fmt.Sprintf("Found: %d", r.Summary.Found))
	}
	keys := make([]string, 0, len(r.WorkerSnapshots))
	for k := range r.WorkerSnapshots {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := r.WorkerSnapshots[keys[i]], r.WorkerSnapshots[keys[j]]
		if a.Shard != b.Shard {
			return a.Shard < b.Shard
		}
		return keys[i] < keys[j]
	})
	if len(keys) == 0 {
		lines = append(lines, "Worker telemetry unavailable")
	}
	for _, k := range keys {
		w := r.WorkerSnapshots[k]
		pct, rate, eta, found := "unknown", "unknown", "unknown", "unknown"
		if w.Found != nil {
			found = fmt.Sprint(*w.Found)
		}
		if p := w.Progress; p != nil {
			if !p.PercentUnknown {
				pct = fmt.Sprintf("%.1f%%", p.Percent)
			}
			if !p.RateUnknown {
				rate = fmt.Sprintf("%.0f", p.Rate)
			}
			if !p.ETAUnknown {
				eta = p.ETA.Round(time.Second).String()
			}
		}
		lines = append(lines, fmt.Sprintf("Shard %d | %s | %s | %s | cleanup: %s", w.Shard, known(w.ID), known(w.IP), known(w.State), known(w.Cleanup)), fmt.Sprintf("  Progress %s | rate %s pps | ETA %s | found %s", pct, rate, eta, found))
		if w.ProgressAt != nil {
			lines = append(lines, "  Last telemetry: "+w.ProgressAt.Format(time.RFC3339))
		}
		if w.Detail != "" {
			lines = append(lines, "  "+w.Detail)
		}
		if w.CleanupDetail != "" {
			lines = append(lines, "  Cleanup: "+w.CleanupDetail)
		}
	}
	if r.Artifact != "" {
		lines = append(lines, "Server artifact: "+r.Artifact)
	}
	return lines
}
func renderPlain(server string, s snapshot, stale string) string {
	lines := []string{"masscanr watch | " + server}
	if stale != "" {
		lines = append(lines, stale)
	}
	if len(s.Runs) == 0 {
		message := "No active scans — waiting for new runs"
		if stale != "" {
			message = "Last snapshot had no active scans; current activity unknown"
		}
		lines = append(lines, message)
	}
	for _, r := range s.Runs {
		lines = append(lines, runLines(r, 0, 100)...)
	}
	if s.Last != nil {
		lines = append(lines, "Most recent settled run")
		lines = append(lines, runLines(*s.Last, 0, 100)...)
	}
	for i := range lines {
		lines[i] = clean(lines[i])
	}
	return strings.Join(lines, "\n")
}
