// Package controller orchestrates one ephemeral distributed scan.
package controller

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"

	"github.com/st3fan/masscanr/internal/backend"
	"github.com/st3fan/masscanr/internal/cloud"
	"github.com/st3fan/masscanr/internal/remote"
	"github.com/st3fan/masscanr/internal/scan"
)

type Event struct {
	Shard                 int
	ID, IP, State, Detail string
	Progress              *scan.Progress
	RunID                 string
	Seed                  *uint64 // Present only on run-start metadata events.
}

type Summary struct {
	RunID     string
	Seed      uint64
	Found     uint64
	Undeleted []string
}

type Timeouts struct {
	Create, Ready, Prepare, Scan, Cleanup, Delete, Poll time.Duration
}

func DefaultTimeouts() Timeouts {
	return Timeouts{Create: 45 * time.Second, Ready: 5 * time.Minute, Prepare: 10 * time.Minute, Scan: 24 * time.Hour, Cleanup: 5 * time.Minute, Delete: time.Minute, Poll: 2 * time.Second}
}

type Controller struct {
	Backend  backend.Backend
	Provider cloud.Provider
	Runner   remote.Runner
	Timeouts Timeouts
}

type tracked struct {
	shard    int
	worker   cloud.Worker
	shutdown bool
}

func (c *Controller) Run(ctx context.Context, cfg scan.Config, out io.Writer, emit func(Event)) (summary Summary, err error) {
	if err = cfg.Validate(); err != nil {
		return summary, err
	}
	if ctx.Err() != nil {
		return summary, ctx.Err()
	}
	if c.Backend == nil || c.Provider == nil || c.Runner == nil || out == nil {
		return summary, fmt.Errorf("backend, provider, runner and output are required")
	}
	if err = c.Backend.Validate(cfg.Intent(), cfg.Shards); err != nil {
		return summary, err
	}
	for _, d := range []time.Duration{c.Timeouts.Create, c.Timeouts.Ready, c.Timeouts.Prepare, c.Timeouts.Scan, c.Timeouts.Cleanup, c.Timeouts.Delete, c.Timeouts.Poll} {
		if d <= 0 {
			return summary, fmt.Errorf("controller timeouts must be positive")
		}
	}
	id := make([]byte, 16)
	if _, err = rand.Read(id); err != nil {
		return summary, err
	}
	summary.RunID = hex.EncodeToString(id)
	if summary.Seed, err = scan.NewSeed(); err != nil {
		return summary, err
	}
	var events sync.Mutex
	notify := func(e Event) {
		if emit != nil {
			events.Lock()
			defer events.Unlock()
			emit(e)
		}
	}
	notify(Event{State: "validating", RunID: summary.RunID, Seed: &summary.Seed})
	result := &results{out: out, cfg: cfg}
	if err = result.line(fmt.Sprintf("#masscan\n# masscanr run=%s seed=%d targets=%v ports=%s rate=%d shards=%d", summary.RunID, summary.Seed, cfg.Targets, cfg.PortSpec(), cfg.Rate, cfg.Shards)); err != nil {
		return summary, err
	}
	var workers []tracked
	attempted, ambiguous := false, false
	defer func() {
		cleanup := Event{State: "cleanup"}
		if err != nil {
			cleanup.Detail = err.Error()
		}
		notify(cleanup)
		if attempted {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), c.Timeouts.Cleanup)
			defer cancel()
			var cleanupErr error
			summary.Undeleted, cleanupErr = c.cleanup(cleanupCtx, summary.RunID, workers, ambiguous, notify)
			err = errors.Join(err, cleanupErr)
		}
		summary.Found = result.count
		status := "complete"
		if err != nil {
			status = "incomplete"
		}
		err = errors.Join(err, result.line("# status "+status))
	}()
	spec := cloud.Spec{RunID: summary.RunID, Region: cfg.Region, InstanceType: cfg.InstanceType}
	validateCtx, stop := context.WithTimeout(ctx, c.Timeouts.Ready)
	err = c.Provider.Validate(validateCtx, spec)
	stop()
	if err != nil {
		return summary, fmt.Errorf("provider validation: %w", err)
	}
	for shard := 1; shard <= cfg.Shards; shard++ {
		if ctx.Err() != nil {
			return summary, ctx.Err()
		}
		notify(Event{Shard: shard, State: "provisioning"})
		spec.Shard = shard
		attempted = true
		// Let an in-flight creation settle even after Ctrl-C so a successful
		// response's ID is retained. The API request is still time-bounded.
		createCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.Timeouts.Create)
		w, createErr := c.Provider.Create(createCtx, spec)
		cancel()
		if w.ID != "" {
			workers = append(workers, tracked{shard: shard, worker: w})
			notify(Event{Shard: shard, ID: w.ID, IP: w.IP, State: "booting"})
		}
		if createErr != nil || w.ID == "" {
			ambiguous = true
			if createErr == nil {
				createErr = fmt.Errorf("provider returned no worker ID")
			}
			notify(Event{Shard: shard, State: "failed", Detail: createErr.Error()})
			return summary, fmt.Errorf("create shard %d: %w", shard, createErr)
		}
	}
	// Prepare all workers before scanning, so incompatible backend builds
	// cannot silently use different shard permutations.
	if err = c.prepare(ctx, workers, summary.RunID, notify); err != nil {
		return summary, err
	}
	scanCtx, cancel := context.WithTimeout(ctx, c.Timeouts.Scan)
	defer cancel()
	var wg sync.WaitGroup
	var failure error
	var mu sync.Mutex
	for index := range workers {
		item := &workers[index]
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := item.worker
			notify(Event{Shard: item.shard, ID: w.ID, IP: w.IP, State: "scanning"})
			plan, planErr := c.Backend.Build(cfg.Intent(), scan.Shard{Index: item.shard, Total: cfg.Shards}, summary.Seed)
			runErr := planErr
			if runErr == nil {
				runErr = c.Runner.Scan(scanCtx, w, plan, func(f scan.Finding) error { return result.accept(item.shard, f) }, func(p scan.Progress) {
					state := "scanning"
					if p.Waiting {
						state = "waiting"
					}
					notify(Event{Shard: item.shard, ID: w.ID, IP: w.IP, State: state, Progress: &p})
				})
			}

			if runErr == nil {
				notify(Event{Shard: item.shard, ID: w.ID, IP: w.IP, State: "collecting"})
			}
			runErr = errors.Join(runErr, scanCtx.Err())
			if runErr != nil {
				mu.Lock()
				failure = errors.Join(failure, fmt.Errorf("shard %d: %w", item.shard, runErr))
				mu.Unlock()
				notify(Event{Shard: item.shard, ID: w.ID, IP: w.IP, State: failureState(runErr), Detail: runErr.Error()})
				cancel()
			} else {
				notify(Event{Shard: item.shard, ID: w.ID, IP: w.IP, State: "completed"})
			}
			// Scanner exit includes drained output and the response-wait phase.
			// Retire this worker now, independently of peers still scanning and
			// independently of scan cancellation. Final cleanup retries failures.
			if c.shutdown(context.WithoutCancel(scanCtx), *item, summary.RunID, notify) == nil {
				item.shutdown = true
			}
		}()
	}
	wg.Wait()
	// Shutdown has its own context. Cancellation after all scanners exited
	// must not retroactively invalidate an otherwise completed scan.
	return summary, failure
}

func (c *Controller) prepare(ctx context.Context, workers []tracked, runID string, notify func(Event)) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	var mu sync.Mutex
	var failure error
	versions := make([]scan.Identity, len(workers))
	for index := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			item := &workers[index]
			readyCtx, stop := context.WithTimeout(ctx, c.Timeouts.Ready)
			w, err := c.ready(readyCtx, item.worker, runID)
			stop()
			if err == nil {
				item.worker = w
				notify(Event{Shard: item.shard, ID: w.ID, IP: w.IP, State: "preparing"})
				prepCtx, stop := context.WithTimeout(ctx, c.Timeouts.Prepare)
				var prepared scan.Prepared
				prepared, err = c.Runner.Prepare(prepCtx, w, c.Backend.Preparation())
				versions[index] = prepared.Identity
				stop()
				if err == nil {
					notify(Event{Shard: item.shard, ID: w.ID, IP: w.IP, State: "ready", Detail: prepared.String()})
				}
			}
			if err != nil {
				mu.Lock()
				failure = errors.Join(failure, fmt.Errorf("prepare shard %d: %w", item.shard, err))
				mu.Unlock()
				notify(Event{Shard: item.shard, ID: item.worker.ID, State: failureState(err), Detail: err.Error()})
				cancel()
			}
		}()
	}
	wg.Wait()
	if failure != nil {
		return failure
	}
	for _, version := range versions {
		if !version.Valid() || version.Name != c.Backend.Name() || version != versions[0] {
			return fmt.Errorf("workers have different or unknown backend builds")
		}
	}
	return ctx.Err()
}

func (c *Controller) ready(ctx context.Context, initial cloud.Worker, runID string) (cloud.Worker, error) {
	for {
		if ctx.Err() != nil {
			return initial, ctx.Err()
		}
		w, err := c.Provider.Get(ctx, initial.ID)
		if err != nil {
			return initial, err
		}
		if w.ID != initial.ID || w.Label != cloud.Label || !slices.Contains(w.Tags, "masscanr-run-"+runID) {
			return initial, fmt.Errorf("worker %s is not owned by this run", initial.ID)
		}
		if w.Password == "" {
			w.Password = initial.Password
		}
		if w.Ready && w.Password != "" {
			return w, nil
		}
		if err = pause(ctx, c.Timeouts.Poll); err != nil {
			return initial, err
		}
	}
}

func (c *Controller) cleanup(ctx context.Context, runID string, initial []tracked, ambiguous bool, notify func(Event)) ([]string, error) {
	known := make(map[string]tracked)
	shutdown := make(map[string]bool)
	for _, item := range initial {
		if item.shutdown {
			shutdown[item.worker.ID] = true
		} else {
			known[item.worker.ID] = item
		}
	}
	var reconciliation error
	// Multiple reconciliation passes catch instances created despite an
	// ambiguous POST response. Do not retry the creation itself.
	passes := 1
	if ambiguous {
		passes = 3
	}
	var failures map[string]error
	for pass := 0; pass < passes; pass++ {
		found, err := c.Provider.List(ctx, runID)
		if err != nil {
			reconciliation = fmt.Errorf("reconcile run %s (check its tagged instances manually): %w", runID, err)
		} else {
			reconciliation = nil
			for _, w := range found {
				// A stale list response must not resurrect a confirmed shutdown.
				if shutdown[w.ID] {
					continue
				}
				if _, exists := known[w.ID]; !exists {
					known[w.ID] = tracked{worker: w}
				}
			}
		}
		failures = make(map[string]error)
		var wg sync.WaitGroup
		var mu sync.Mutex
		limit := make(chan struct{}, 4)
		for id, item := range known {
			wg.Add(1)
			go func() {
				defer wg.Done()
				select {
				case limit <- struct{}{}:
				case <-ctx.Done():
					mu.Lock()
					failures[id] = ctx.Err()
					mu.Unlock()
					return
				}
				defer func() { <-limit }()
				err := c.shutdown(ctx, item, runID, notify)
				if err != nil {
					mu.Lock()
					failures[id] = err
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		for id := range known {
			if _, failed := failures[id]; !failed {
				delete(known, id)
			}
		}
		if pass+1 < passes {
			if err = pause(ctx, c.Timeouts.Poll); err != nil {
				break
			}
		}
	}
	var remaining []string
	var failure error
	for id, err := range failures {
		remaining = append(remaining, id)
		failure = errors.Join(failure, fmt.Errorf("undeleted instance %s: %w", id, err))
	}
	slices.Sort(remaining)
	return remaining, errors.Join(failure, reconciliation)
}

func (c *Controller) shutdown(ctx context.Context, item tracked, runID string, notify func(Event)) error {
	w := item.worker
	notify(Event{Shard: item.shard, ID: w.ID, IP: w.IP, State: "deleting"})
	deleteCtx, cancel := context.WithTimeout(ctx, c.Timeouts.Delete)
	defer cancel()
	if err := c.Provider.Delete(deleteCtx, w, runID); err != nil {
		notify(Event{Shard: item.shard, ID: w.ID, State: "cleanup failed", Detail: err.Error()})
		return err
	}
	// Provider.Delete confirms deletion, rather than just acknowledging it.
	notify(Event{Shard: item.shard, ID: w.ID, State: "shutdown"})
	return nil
}

func pause(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func failureState(err error) string {
	if onlyCancelled(err) {
		return "cancelled"
	}
	return "failed"
}

// A joined scan/output error must not disappear behind a concurrent cancellation.
func onlyCancelled(err error) bool {
	if err == context.Canceled {
		return true
	}
	switch wrapped := err.(type) {
	case interface{ Unwrap() []error }:
		causes := wrapped.Unwrap()
		for _, cause := range causes {
			if !onlyCancelled(cause) {
				return false
			}
		}
		return len(causes) > 0
	case interface{ Unwrap() error }:
		return onlyCancelled(wrapped.Unwrap())
	}
	return false
}
