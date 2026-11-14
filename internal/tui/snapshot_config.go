package tui

import "strings"
import "github.com/st3fan/masscanr/internal/runapi"

// SnapshotConfigLines reuses the existing configuration layout without wiring
// the viewer to the controller-owned TUI's cancel/stop lifecycle.
func SnapshotConfigLines(r runapi.Run, width int) []string {
	exports := [2]string{}
	if r.Artifact != "" {
		base := strings.TrimSuffix(r.Artifact, ".scan")
		exports = [2]string{base + ".csv", base + ".jsonl"}
	}
	s := newState(NewRunConfig(r.Plan.Config, r.Plan.Timeouts, r.Artifact, exports))
	s.Identity = r.Summary.Identity
	s.RunID = r.ID
	s.Seed = r.Summary.Seed
	s.SeedKnown = r.Summary.RunID != ""
	return (model{state: s, width: width}).configLines()
}
