package tui

import (
	"github.com/st3fan/masscanr/internal/controller"
)

// runEvent consumes metadata and phases, never creating a fictitious shard 0.
// The caller holds View.mu. No credentials are carried on these events.
func (v *View) runEvent(e controller.Event) {
	if e.RunID != "" {
		v.RunID = clean(e.RunID)
	}
	if e.Seed != nil {
		v.Seed, v.SeedKnown = *e.Seed, true
	}
	switch e.State {
	case "validating":
		v.Phase = "Validating"
	case "cleanup":
		v.Phase = "Cleanup"
		if e.Detail != "" {
			for key, r := range v.rows {
				if r.State != "completed" && r.State != "failed" && r.State != "cancelled" {
					r.State = "cancelled"
					if r.Detail == "" {
						r.Detail = "Run stopped before shard completion"
					}
					v.rows[key] = r
				}
			}
		}
	case "exporting":
		v.Phase = "Exporting"
	}
}

func runPhase(s state) string {
	phase := "Provisioning"
	completed := 0
	for _, r := range s.rows {
		if r.State == "failed" || r.State == "cancelled" {
			return "Stopping; cleaning up"
		}
		if r.Shard > 0 && r.State == "completed" {
			completed++
		}
	}
	for _, r := range s.rows {
		switch r.State {
		case "scanning":
			return "Scanning"
		case "waiting":
			phase = "Waiting for responses"
		case "collecting":
			if phase != "Waiting for responses" {
				phase = "Collecting results"
			}
		case "preparing", "ready":
			if phase == "Provisioning" {
				phase = "Preparing"
			}
		}
	}
	if completed == s.Config.Scan.Shards {
		return "Cleaning up"
	}
	return phase
}
