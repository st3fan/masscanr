package controller

import (
	"fmt"
	"slices"

	"github.com/st3fan/masscanr/internal/scan"
	"github.com/st3fan/masscanr/internal/scanfile"
)

// accept is the sole authoritative finding path for every backend. Preserve
// accepted order and duplicates, and attribute each normalized record to a shard.
func (r *results) accept(shard int, f scan.Finding) error {
	if shard < 1 || shard > r.cfg.Shards {
		return fmt.Errorf("invalid finding shard %d", shard)
	}
	if f.Protocol != "tcp" || f.State != "open" {
		return fmt.Errorf("unexpected finding state/protocol from shard %d", shard)
	}
	if !f.Address.Is4() {
		return fmt.Errorf("invalid result address from shard %d", shard)
	}
	if !slices.Contains(r.cfg.Ports, f.Port) {
		return fmt.Errorf("unexpected result port from shard %d", shard)
	}
	inScope := false
	for _, p := range r.cfg.Targets {
		if p.Contains(f.Address) {
			inScope = true
			break
		}
	}
	if !inScope {
		return fmt.Errorf("out-of-scope result from shard %d", shard)
	}
	target, prefix, matched := r.attribution.Lookup(f.Address)
	if !matched {
		return fmt.Errorf("finding has no requested target from shard %d", shard)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.journal.Append(scanfile.Record{Type: "finding", Finding: &scanfile.Finding{Shard: shard, IP: f.Address, Port: f.Port, Protocol: f.Protocol, State: f.State, Timestamp: f.Timestamp, Target: target, Prefix: prefix}}, false); err != nil {
		return err
	}

	r.count++
	if r.counts == nil {
		r.counts = make(map[int]uint64)
	}
	r.counts[shard]++
	return nil
}
