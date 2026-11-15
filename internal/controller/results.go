package controller

import (
	"context"
	"github.com/st3fan/masscanr/internal/scanfile"
	"sync"
	"time"

	"github.com/st3fan/masscanr/internal/scan"
)

// ResultSink writes bounded, journal-synced finding batches to the primary store.
type ResultSink interface {
	WriteBatch(context.Context, uint64, []scanfile.Finding) error
}

const resultBatchSize = 128

type results struct {
	mu          sync.Mutex
	journal     *scanfile.Writer
	cfg         scan.Config
	attribution *scan.AttributionIndex
	count       uint64
	counts      map[int]uint64
	persist     ResultSink
	pending     []scanfile.Finding
}

func (r *results) countFor(shard int) uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.counts[shard]
}

func newResults(journal *scanfile.Writer, cfg scan.Config, sink ...ResultSink) *results {
	if len(cfg.RequestedTargets) == 0 {
		cfg.RequestedTargets = scan.DirectTargets(cfg.Targets)
	}
	r := &results{journal: journal, cfg: cfg, attribution: scan.NewAttributionIndex(cfg.RequestedTargets)}
	if len(sink) > 0 {
		r.persist = sink[0]
	}
	return r
}

func (r *results) flush(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.flushLocked(ctx)
}

func (r *results) flushLocked(ctx context.Context) error {
	if r.persist == nil || len(r.pending) == 0 {
		return nil
	}
	seq, err := r.journal.Flush()
	if err != nil {
		return err
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err = r.persist.WriteBatch(writeCtx, seq, r.pending); err != nil {
		return err
	}
	r.pending = r.pending[:0]
	return nil
}
