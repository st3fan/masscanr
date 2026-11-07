package controller

import (
	"github.com/st3fan/masscanr/internal/scanfile"
	"sync"

	"github.com/st3fan/masscanr/internal/scan"
)

type results struct {
	mu      sync.Mutex
	journal *scanfile.Writer
	cfg     scan.Config
	count   uint64
	counts  map[int]uint64
}

func (r *results) countFor(shard int) uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.counts[shard]
}
