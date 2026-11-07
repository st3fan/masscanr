package controller

import (
	"fmt"
	"slices"

	"github.com/st3fan/masscanr/internal/scan"
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
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := writeString(r.out, fmt.Sprintf("# shard %d\nopen tcp %d %s %d\n", shard, f.Port, f.Address, f.Timestamp)); err != nil {
		return err
	}
	r.count++
	return nil
}
