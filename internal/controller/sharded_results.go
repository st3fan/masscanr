package controller

import (
	"context"
	"fmt"
	"slices"

	"github.com/st3fan/masscanr/internal/scan"
	"github.com/st3fan/masscanr/internal/scanfile"
)

// acceptSharded validates the exact retryable unit before recording an
// observation. Retries retain duplicate journal evidence; the DB deduplicates.
func (r *results) acceptSharded(unit scan.WorkShard, attempt int, f scan.Finding) error {
	if unit.Index == 0 || unit.Index > uint64(^uint(0)>>1) || attempt < 1 || attempt > 3 || f.Protocol != "tcp" || f.State != "open" || !f.Address.Is4() || !slices.Contains(r.cfg.Ports, f.Port) {
		return fmt.Errorf("invalid finding for shard %d", unit.Index)
	}
	inUnit := false
	for _, p := range unit.Targets {
		if p.Contains(f.Address) {
			inUnit = true
			break
		}
	}
	target, prefix, matched := r.attribution.Lookup(f.Address)
	if !inUnit || !matched {
		return fmt.Errorf("finding outside frozen shard %d", unit.Index)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	finding := scanfile.Finding{Shard: int(unit.Index), Attempt: attempt, IP: f.Address, Port: f.Port, Protocol: f.Protocol, State: f.State, Timestamp: f.Timestamp, Target: target, Prefix: prefix}
	if err := r.journal.Append(scanfile.Record{Type: "finding", Finding: &finding}, false); err != nil {
		return err
	}
	r.count++
	if r.persist != nil {
		r.pending = append(r.pending, finding)
		if len(r.pending) >= resultBatchSize {
			if err := r.flushLocked(context.Background()); err != nil {
				return err
			}
		}
	}
	if r.counts == nil {
		r.counts = make(map[int]uint64)
	}
	r.counts[int(unit.Index)]++
	return nil
}
