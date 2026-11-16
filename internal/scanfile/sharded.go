package scanfile

import (
	"fmt"
	"math"
	"net/netip"
	"slices"
	"strconv"
	"time"

	"github.com/st3fan/masscanr/internal/scan"
)

// ValidateSharded checks the compact, immutable schema-3 execution manifest.
func (c Config) ValidateSharded() (scan.Manifest, error) {
	ports, err := scan.ParsePorts(c.Ports)
	if err != nil {
		return scan.Manifest{}, err
	}
	cfg := scan.Config{Backend: c.Backend, Targets: c.Targets, Ports: ports, Shards: 1, Rate: c.Rate, Region: c.Region, InstanceType: c.InstanceType}
	if err = cfg.Validate(); err != nil {
		return scan.Manifest{}, err
	}
	if c.Shards != 0 || cfg.PortSpec() != c.Ports || c.Provider != "vultr" || c.Output == "" || (c.Backend != "masscan" && c.Backend != "zmap") {
		return scan.Manifest{}, fmt.Errorf("invalid sharded scan configuration")
	}
	values := make([]string, len(c.Targets))
	for i, p := range c.Targets {
		values[i] = p.String()
	}
	normalized, err := scan.ParseTargets(values)
	if err != nil || !slices.Equal(normalized, c.Targets) {
		return scan.Manifest{}, fmt.Errorf("targets must be normalized and deduplicated")
	}
	if c.PartitionVersion != scan.PartitionVersion || c.MaxProbesPerUnit != scan.MaxProbesPerShard {
		return scan.Manifest{}, fmt.Errorf("unsupported shard partition")
	}
	m, err := scan.Partition(c.Targets, len(ports))
	if err != nil {
		return scan.Manifest{}, err
	}
	if c.ShardCount != m.Count || c.RequestedWorkers < 1 || c.Workers != min(c.RequestedWorkers, int(m.Count)) {
		return scan.Manifest{}, fmt.Errorf("shard or worker count disagrees with frozen scope")
	}
	seed, err := strconv.ParseUint(c.Seed, 10, 64)
	if err != nil || strconv.FormatUint(seed, 10) != c.Seed || c.Backend == "zmap" && seed > math.MaxInt64 {
		return scan.Manifest{}, fmt.Errorf("invalid backend seed")
	}
	for _, key := range []string{"create", "ready", "prepare", "scan", "cleanup", "delete", "poll"} {
		d, e := time.ParseDuration(c.Timeouts[key])
		if e != nil || d <= 0 {
			return scan.Manifest{}, fmt.Errorf("invalid %s timeout", key)
		}
	}
	return m, nil
}

type shardedState struct {
	manifest   scan.Manifest
	attempts   map[int]Attempt
	history    []Attempt
	completed  uint64
	scopes     map[int][]netip.Prefix
	workerSlot map[string]int
	current    map[int]string
	next       []uint64
	activeSlot map[int]int
}

// ShardAttempts returns the validated attempt history in journal order.
func (s State) ShardAttempts() []Attempt {
	if s.v3 == nil {
		return nil
	}
	return slices.Clone(s.v3.history)
}

// ActiveShardAttempts identifies attempts that lost their terminal event.
func (s State) ActiveShardAttempts() []Attempt {
	if s.v3 == nil {
		return nil
	}
	var active []Attempt
	for _, a := range s.v3.attempts {
		if a.Phase == "started" {
			active = append(active, a)
		}
	}
	slices.SortFunc(active, func(a, b Attempt) int { return a.Shard - b.Shard })
	return active
}

func (s State) SlotInstance(slot int) string {
	if s.v3 == nil {
		return ""
	}
	return s.v3.current[slot]
}

// CompletedShards is a compact progress count for schema 3.
func (s State) CompletedShards() uint64 {
	if s.v3 != nil {
		return s.v3.completed
	}
	var count uint64
	for _, outcome := range s.Outcomes() {
		if outcome.Phase == "completed" {
			count++
		}
	}
	return count
}

func (s *State) applySharded(r Record) error {
	if s.Finished != nil || r.Seq != s.Seq+1 || r.At.IsZero() {
		return fmt.Errorf("invalid sequence or record after finish")
	}
	payloads := 0
	for _, present := range []bool{r.Scan != nil, r.Backend != nil, r.Worker != nil, r.Attempt != nil, r.Progress != nil, r.Finding != nil, r.Finished != nil} {
		if present {
			payloads++
		}
	}
	if payloads != 1 {
		return fmt.Errorf("record must have exactly one payload")
	}
	if s.Header == nil {
		h := r.Scan
		if r.Type != "scan" || h == nil || r.Seq != 1 || h.Format != Format || h.Schema != ShardedSchema || h.RunID == "" || h.CreatedAt.IsZero() || h.ControllerVersion == "" || h.Label != "masscanr" || h.OwnershipTag != "masscanr-run-"+h.RunID {
			return fmt.Errorf("invalid sharded scan header")
		}
		manifest, err := h.Config.ValidateSharded()
		if err != nil {
			return err
		}
		if err = scan.ValidateTargets(h.Config.RequestedTargets, h.Config.Targets); err != nil {
			return err
		}
		s.Header = h
		v3 := &shardedState{manifest: manifest, attempts: map[int]Attempt{}, scopes: map[int][]netip.Prefix{}, workerSlot: map[string]int{}, current: map[int]string{}, next: make([]uint64, h.Config.Workers+1), activeSlot: map[int]int{}}
		for slot := 1; slot <= h.Config.Workers; slot++ {
			v3.next[slot] = uint64(slot)
		}
		s.v3 = v3
		s.Workers = map[string]Worker{}
		s.Counts = map[int]uint64{}
		s.attribution = scan.NewAttributionIndex(h.Config.RequestedTargets)
		s.ports, _ = scan.ParsePorts(h.Config.Ports)
		s.Seq = r.Seq
		return nil
	}
	if s.v3 == nil {
		return fmt.Errorf("missing sharded state")
	}
	switch r.Type {
	case "worker":
		w := r.Worker
		if w == nil || w.InstanceID == "" || w.Slot < 1 || w.Slot > s.Header.Config.Workers || w.Phase == "" || !slices.Contains([]string{"pending", "deleting", "incomplete", "complete"}, w.Cleanup) || w.Shard != 0 {
			return fmt.Errorf("invalid physical worker")
		}
		if w.Cleanup == "complete" && s.v3.activeSlot[w.Slot] != 0 {
			return fmt.Errorf("worker cleanup during active attempt")
		}
		if prior, ok := s.v3.workerSlot[w.InstanceID]; ok && prior != w.Slot {
			return fmt.Errorf("worker moved between slots")
		}
		for id, slot := range s.v3.workerSlot {
			if id != w.InstanceID && slot == w.Slot && s.Workers["instance:"+id].Cleanup != "complete" {
				return fmt.Errorf("slot replaced before prior worker cleanup")
			}
		}
		if prior, ok := s.Workers["instance:"+w.InstanceID]; ok && prior.Cleanup == "complete" {
			return fmt.Errorf("worker changed after cleanup")
		}
		s.v3.workerSlot[w.InstanceID] = w.Slot
		s.v3.current[w.Slot] = w.InstanceID
		s.Workers["instance:"+w.InstanceID] = *w
	case "attempt":
		a := r.Attempt
		if a == nil || a.Shard < 1 || uint64(a.Shard) > s.v3.manifest.Count || a.Slot != (a.Shard-1)%s.Header.Config.Workers+1 || a.InstanceID == "" || s.v3.workerSlot[a.InstanceID] != a.Slot || a.Number < 1 || a.Number > 3 {
			return fmt.Errorf("invalid shard attempt")
		}
		prior, exists := s.v3.attempts[a.Shard]
		if a.Phase == "started" {
			if a.Shard != int(s.v3.next[a.Slot]) || s.v3.activeSlot[a.Slot] != 0 || s.Workers["instance:"+a.InstanceID].Cleanup == "complete" || exists && (prior.Phase != "failed" || a.Number != prior.Number+1) || !exists && a.Number != 1 {
				return fmt.Errorf("attempt starts outside retry sequence")
			}
			unit, err := s.v3.manifest.At(uint64(a.Shard))
			if err != nil {
				return err
			}
			s.v3.scopes[a.Shard] = unit.Targets
			s.v3.activeSlot[a.Slot] = a.Shard
		} else if a.Phase == "failed" || a.Phase == "completed" {
			if !exists || prior.Phase != "started" || prior.Number != a.Number || prior.InstanceID != a.InstanceID || prior.Slot != a.Slot {
				return fmt.Errorf("attempt has no matching active start")
			}
			delete(s.v3.scopes, a.Shard)
			delete(s.v3.activeSlot, a.Slot)
			if a.Phase == "completed" {
				s.v3.completed++
				s.v3.next[a.Slot] += uint64(s.Header.Config.Workers)
			}
		} else {
			return fmt.Errorf("invalid attempt phase")
		}
		s.v3.attempts[a.Shard] = *a
		s.v3.history = append(s.v3.history, *a)
	case "progress":
		p := r.Progress
		if p == nil || p.Shard < 1 || p.Attempt < 1 {
			return fmt.Errorf("invalid shard progress")
		}
		a := s.v3.attempts[p.Shard]
		if a.Phase != "started" || a.Number != p.Attempt {
			return fmt.Errorf("progress outside active attempt")
		}
		for i, v := range []*float64{p.Percent, p.Rate, p.ETASeconds} {
			if v != nil && (math.IsNaN(*v) || math.IsInf(*v, 0) || *v < 0 || (i == 0 && *v > 100)) {
				return fmt.Errorf("invalid progress measurement")
			}
		}
	case "finding":
		f := r.Finding
		if f == nil || f.Shard < 1 || f.Attempt < 1 || !f.IP.Is4() || f.Protocol != "tcp" || f.State != "open" || !slices.Contains(s.ports, f.Port) {
			return fmt.Errorf("invalid shard finding")
		}
		a := s.v3.attempts[f.Shard]
		if a.Phase != "started" || a.Number != f.Attempt {
			return fmt.Errorf("finding outside active attempt")
		}
		inShard := false
		for _, prefix := range s.v3.scopes[f.Shard] {
			if prefix.Contains(f.IP) {
				inShard = true
				break
			}
		}
		target, prefix, ok := s.attribution.Lookup(f.IP)
		if !inShard || !ok || target != f.Target || prefix != f.Prefix {
			return fmt.Errorf("finding outside frozen shard scope or provenance")
		}
		s.Found++
		s.Counts[f.Shard]++
	case "finished":
		f := r.Finished
		if f == nil || f.Found != s.Found || f.ShardCount != s.v3.manifest.Count || f.CompletedShards != s.v3.completed || !slices.Equal(f.Undeleted, s.Undeleted()) {
			return fmt.Errorf("sharded finish disagrees with evidence")
		}
		if f.ScanOutcome != "complete" && f.ScanOutcome != "failed" && f.ScanOutcome != "cancelled" && f.ScanOutcome != "interrupted" {
			return fmt.Errorf("invalid scan outcome")
		}
		if f.CleanupOutcome != "complete" && f.CleanupOutcome != "incomplete" {
			return fmt.Errorf("invalid cleanup outcome")
		}
		if f.ScanOutcome == "complete" && s.v3.completed != s.v3.manifest.Count {
			return fmt.Errorf("complete run has unfinished shards")
		}
		if len(s.v3.activeSlot) != 0 {
			return fmt.Errorf("finished run has active attempts")
		}
		if (f.CleanupOutcome == "complete") != (len(f.Undeleted) == 0 && !f.ReconciliationUnresolved) {
			return fmt.Errorf("cleanup summary disagrees with worker state")
		}
		s.Finished = f
	default:
		return fmt.Errorf("unknown sharded record %q", r.Type)
	}
	s.Seq = r.Seq
	return nil
}
