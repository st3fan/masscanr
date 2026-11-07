package tui

import (
	"fmt"
	"sort"
	"time"

	"github.com/st3fan/masscanr/internal/controller"
	"github.com/st3fan/masscanr/internal/scan"
)

// RunConfig is a credential-free snapshot of the effective run settings.
// Construct it with NewRunConfig so later caller mutations cannot change the UI.
type RunConfig struct {
	Scan                        scan.Config
	ScanTimeout, CleanupTimeout time.Duration
	Output                      string
	Exports                     [2]string
}

func NewRunConfig(cfg scan.Config, timeouts controller.Timeouts, output string, exports [2]string) RunConfig {
	if cfg.Backend == "" {
		cfg.Backend = "masscan"
	}
	cfg = cfg.Clone()
	return RunConfig{Scan: cfg, ScanTimeout: timeouts.Scan, CleanupTimeout: timeouts.Cleanup, Output: output, Exports: exports}
}

const staleAfter = 5 * time.Second

type shardState struct {
	Shard                          int
	ID, IP, State, Cleanup, Detail string
	CleanupDetail                  string
	Progress                       scan.Progress
	Updated                        time.Time
	Known                          bool
}

func (r shardState) stale(now time.Time) bool {
	return r.Known && now.Sub(r.Updated) >= staleAfter && (r.State == "scanning" || r.State == "waiting")
}

func (r shardState) active(now time.Time) bool {
	return r.Known && !r.stale(now) && r.State == "scanning" && !r.Progress.Waiting && r.Cleanup == ""
}

// state is updated under View's lock and copied before rendering. It has no
// terminal or framework dependencies, and keeps scan and cleanup outcomes apart.
type state struct {
	Identity  scan.Identity
	Config    RunConfig
	RunID     string
	Seed      uint64
	SeedKnown bool
	Phase     string
	Final     *completion
	rows      map[string]shardState
}

// completion is immutable once published; render snapshots may safely share it.
type completion struct {
	Status, Detail string
	Found          uint64
}

func newState(cfg RunConfig) state {
	s := state{Config: cfg, Phase: "Starting", rows: make(map[string]shardState)}
	for i := 1; i <= cfg.Scan.Shards; i++ {
		s.rows[fmt.Sprint(i)] = shardState{Shard: i, State: "pending"}
	}
	return s
}

// apply returns whether the lifecycle state changed (for plain output).
func (s *state) apply(e controller.Event, now time.Time) bool {
	key := fmt.Sprint(e.Shard)
	if e.Shard == 0 {
		if e.ID == "" {
			return false
		}
		key = "instance:" + e.ID // Workers discovered during reconciliation.
	}
	r := s.rows[key]
	r.Shard = e.Shard
	if e.ID != "" {
		r.ID = clean(e.ID)
	}
	if e.IP != "" {
		r.IP = clean(e.IP)
	}
	if e.Found != nil {
		r.Progress.Found = max(r.Progress.Found, *e.Found)
	}
	if e.State == "finding" {
		s.rows[key] = r
		return false
	}
	changed := false
	switch e.State {
	case "deleting", "shutdown", "cleanup failed":
		changed = r.Cleanup != e.State
		r.Cleanup = e.State
		if e.State == "shutdown" {
			r.CleanupDetail = ""
		} else if e.Detail != "" {
			r.CleanupDetail = clean(e.Detail)
		}
	default:
		changed = r.State != e.State
		r.State = clean(e.State)
		if e.Detail != "" {
			r.Detail = clean(e.Detail)
		}
	}
	if e.Progress != nil {
		priorFound := r.Progress.Found
		r.Progress = *e.Progress // Callbacks may reuse their progress value.
		r.Progress.Found = max(priorFound, r.Progress.Found)
		r.Known, r.Updated = true, now
	}
	if e.State == "completed" {
		r.Progress.Percent = 100
		r.Progress.PercentUnknown = false
		r.Progress.Rate = 0
		r.Known = true
	}
	s.rows[key] = r
	return changed
}

func (s state) copy() state {
	clone := s
	clone.rows = make(map[string]shardState, len(s.rows))
	for key, row := range s.rows {
		clone.rows[key] = row
	}
	return clone
}

func (s state) sorted() []shardState {
	rows := make([]shardState, 0, len(s.rows))
	for _, r := range s.rows {
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Shard != rows[j].Shard {
			// Reconciled workers follow the configured shards.
			if rows[i].Shard == 0 {
				return false
			}
			if rows[j].Shard == 0 {
				return true
			}
			return rows[i].Shard < rows[j].Shard
		}
		return rows[i].ID < rows[j].ID
	})
	return rows
}

type totals struct {
	Percent                     float64
	Rate                        float64
	Found                       uint64
	Complete, VPSs              int
	ETA                         time.Duration
	ETAKnown                    bool
	Stale                       bool
	PercentUnknown, RateUnknown bool
}

func (s state) totals(now time.Time) totals {
	t := totals{ETAKnown: s.Config.Scan.Shards > 0}
	for _, r := range s.rows {
		if r.ID != "" && r.Cleanup != "shutdown" {
			t.VPSs++
		}
		if r.Shard < 1 || r.Shard > s.Config.Scan.Shards {
			continue
		}
		t.Found += r.Progress.Found
		if r.State == "completed" {
			t.Percent += 100 / float64(s.Config.Scan.Shards)
			t.Complete++
			continue
		}
		if r.Known {
			// Approximate equal weighting of native shards. Never infer sending
			// percentage from ZMap's estimate of elapsed time plus cooldown.
			if r.Progress.PercentUnknown {
				t.PercentUnknown = true
			} else {
				t.Percent += min(100, max(0, r.Progress.Percent)) / float64(s.Config.Scan.Shards)
			}
		} else if r.State == "scanning" || r.State == "waiting" {
			t.PercentUnknown = true
			t.RateUnknown = true
		}

		t.Stale = t.Stale || r.stale(now)
		if r.active(now) && r.Progress.RateUnknown {
			t.RateUnknown = true
		}
		if r.active(now) && !r.Progress.RateUnknown {
			t.Rate += r.Progress.Rate
		}
		if (r.active(now) || (r.State == "waiting" && r.Known && !r.stale(now))) && !r.Progress.ETAUnknown && r.Progress.ETA >= 0 {
			t.ETA = max(t.ETA, r.Progress.ETA)
		} else {
			t.ETAKnown = false
		}
	}
	if s.Final != nil {
		t.Rate, t.Found, t.ETAKnown, t.Stale = 0, s.Final.Found, false, false
		t.RateUnknown = false
		if s.Final.Status == "Complete" {
			t.Percent = 100
			t.PercentUnknown = false
		}
	}
	return t
}
