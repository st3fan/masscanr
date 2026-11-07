package controller

import (
	"github.com/st3fan/masscanr/internal/scanfile"
	"sync"

	"github.com/st3fan/masscanr/internal/scan"
)

type results struct {
	mu          sync.Mutex
	journal     *scanfile.Writer
	cfg         scan.Config
	attribution *scan.AttributionIndex
	count       uint64
	counts      map[int]uint64
}

func (r *results) countFor(shard int) uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.counts[shard]
}

func newResults(journal *scanfile.Writer, cfg scan.Config) *results {
	if len(cfg.RequestedTargets) == 0 {
		cfg.RequestedTargets = scan.DirectTargets(cfg.Targets)
	}
	return &results{journal: journal, cfg: cfg, attribution: scan.NewAttributionIndex(cfg.RequestedTargets)}
}
