package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/st3fan/masscanr/internal/cloud"
	"github.com/st3fan/masscanr/internal/scan"
	"github.com/st3fan/masscanr/internal/scanfile"
)

// ShardLease is one durable, fenced whole-shard attempt.
type ShardLease struct {
	Index      uint64
	Slot       int
	Attempt    int
	InstanceID string
}

// ShardStore persists slot cursors and attempt outcomes. The journal remains
// the replayable evidence used to reconstruct this projection after a crash.
type ShardStore interface {
	InitShardRun(context.Context, string, scan.Manifest, int) (int, int64, error)
	RecordShardWorker(context.Context, string, int64, int, string, string, string) error
	UpdateShardWorker(context.Context, string, int64, string, string, string, string, string) error
	BeginShardAttempt(context.Context, string, int64, int, string) (ShardLease, bool, error)
	FinishShardAttempt(context.Context, string, int64, ShardLease, bool, uint64, string) error
	UpdateShardProgress(context.Context, string, int64, ShardLease, *float64) error
}

// RunSharded executes probe-bounded logical units on stable worker slots.
// A failed unit is retried at most twice from its beginning. A slot receives a
// replacement instance only after owned cleanup of its previous instance.
func (c *Controller) RunSharded(ctx context.Context, cfg scan.Config, out io.Writer, emit func(Event), resume scanfile.State) (summary Summary, err error) {
	cfg = cfg.Clone()
	if len(cfg.RequestedTargets) == 0 {
		cfg.RequestedTargets = scan.DirectTargets(cfg.Targets)
	}
	if err = cfg.Validate(); err != nil {
		return summary, err
	}
	if c.RunID == "" || c.Backend == nil || c.Provider == nil || c.Runner == nil || c.ShardStore == nil || out == nil {
		return summary, fmt.Errorf("durable run ID, backend, provider, runner, shard store and output are required")
	}
	if cfg.Backend != c.Backend.Name() {
		return summary, fmt.Errorf("configured backend differs from selected backend")
	}
	if err = c.Backend.Validate(cfg.Intent(), 1); err != nil {
		return summary, err
	}
	manifest, err := scan.Partition(cfg.Targets, len(cfg.Ports))
	if err != nil {
		return summary, err
	}
	effective := min(cfg.Workers, int(manifest.Count))
	summary.RunID, summary.Identity.Name = c.RunID, c.Backend.Name()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	redact := &redactor{}
	if d, ok := out.(interface{ Sanitize(string) string }); ok {
		redact.external = d.Sanitize
	}
	var journal *scanfile.Writer
	if resume.Header == nil {
		if summary.Seed, err = scan.NewSeedWithin(c.Backend.MaxSeed()); err != nil {
			return summary, err
		}
		journal, err = newShardedJournal(out, cfg, c.Timeouts, c.RunID, summary.Seed, manifest, effective, cancel)
		if err != nil {
			return summary, err
		}
		var actual int
		actual, c.Epoch, err = c.ShardStore.InitShardRun(ctx, c.RunID, manifest, cfg.Workers)
		if err != nil || actual != effective {
			_ = journal.Close()
			return summary, errors.Join(err, fmt.Errorf("unable to initialize shard slots"))
		}
	} else {
		h := resume.Header
		frozenProvider, journalProvider := cfg.Provider, h.Config.Provider
		if frozenProvider == "" {
			frozenProvider = "vultr"
		}
		if journalProvider == "" {
			journalProvider = "vultr"
		}
		if h.Schema != scanfile.ShardedSchema || h.RunID != c.RunID || resume.Truncated || resume.Finished != nil || c.Epoch < 1 || h.Config.RequestedWorkers != cfg.Workers || h.Config.Workers != effective || h.Config.ShardCount != manifest.Count || h.Config.Ports != cfg.PortSpec() || h.Config.Backend != cfg.Backend || journalProvider != frozenProvider || h.Config.Image != cfg.Image || h.Config.SSHAuth != cfg.SSHAuth || h.Config.Rate != cfg.Rate || h.Config.Region != cfg.Region || h.Config.InstanceType != cfg.InstanceType || !slices.Equal(h.Config.Targets, cfg.Targets) || !reflect.DeepEqual(h.Config.RequestedTargets, cfg.RequestedTargets) {
			return summary, fmt.Errorf("recovered journal differs from frozen preflight")
		}
		summary.Seed, err = strconv.ParseUint(h.Config.Seed, 10, 64)
		if err != nil {
			return summary, err
		}
		journal, err = scanfile.ResumeWriter(out, resume, func(error) { cancel() })
		if err != nil {
			return summary, err
		}
	}
	defer func() {
		err = errors.Join(err, journal.Close())
		if err != nil {
			err = sanitizedError{cause: err, message: redact.clean(err.Error())}
		}
	}()
	result := newResults(journal, cfg, c.ResultSink)
	result.count = resume.Found
	result.counts = make(map[int]uint64, len(resume.Counts))
	for shard, count := range resume.Counts {
		result.counts[shard] = count
	}
	var emitMu sync.Mutex
	notify := func(e Event) {
		emitMu.Lock()
		defer emitMu.Unlock()
		e.Detail = redact.clean(e.Detail)
		if emit != nil {
			emit(e)
		}
	}
	notify(Event{State: "validating", RunID: c.RunID, Seed: &summary.Seed, ShardCount: manifest.Count, CompletedShards: resume.CompletedShards()})
	var trackedMu sync.Mutex
	var identityMu sync.Mutex
	expectedIdentity := c.ExpectedIdentity
	checkIdentity := func(prepared scan.Prepared) error {
		identityMu.Lock()
		defer identityMu.Unlock()
		if !prepared.Identity.Valid() || prepared.Identity.Name != c.Backend.Name() || expectedIdentity.Valid() && expectedIdentity != prepared.Identity {
			return fmt.Errorf("worker backend build differs from run")
		}
		expectedIdentity = prepared.Identity
		summary.Identity = prepared.Identity
		return nil
	}
	var trackedWorkers []tracked
	addTracked := func(slot int, w cloud.Worker) {
		trackedMu.Lock()
		trackedWorkers = append(trackedWorkers, tracked{shard: slot, worker: w})
		trackedMu.Unlock()
	}
	markDeleted := func(id string) {
		trackedMu.Lock()
		for i := range trackedWorkers {
			if trackedWorkers[i].worker.ID == id {
				trackedWorkers[i].shutdown = true
			}
		}
		trackedMu.Unlock()
	}
	var ambiguous bool
	var ambiguityMu sync.Mutex
	defer func() {
		// Stop scanner callbacks before publishing the terminal journal record.
		cancel()
		err = errors.Join(err, result.flush(context.Background()), journal.Err())
		summary.ScanOutcome = "complete"
		if err != nil {
			summary.ScanOutcome = failureState(err)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			summary.ScanOutcome = "failed"
		}
		notify(Event{State: "cleanup", Detail: errorString(err)})
		cleanupCtx, stop := context.WithTimeout(context.Background(), c.Timeouts.Cleanup)
		defer stop()
		trackedMu.Lock()
		initial := slices.Clone(trackedWorkers)
		trackedMu.Unlock()
		ambiguityMu.Lock()
		wasAmbiguous := ambiguous
		ambiguityMu.Unlock()
		var cleanupErr error
		summary.Undeleted, summary.ReconciliationUnresolved, cleanupErr = c.cleanup(cleanupCtx, c.RunID, initial, wasAmbiguous, func(e Event) {
			if e.ID != "" && (e.State == "deleting" || e.State == "shutdown" || e.State == "cleanup failed") {
				cleanup := "deleting"
				if e.State == "shutdown" {
					cleanup = "complete"
				} else if e.State == "cleanup failed" {
					cleanup = "incomplete"
				}
				_ = journal.Append(scanfile.Record{Type: "worker", Worker: &scanfile.Worker{Slot: e.Shard, InstanceID: e.ID, IP: e.IP, Phase: e.State, Cleanup: cleanup, Detail: redact.clean(e.Detail)}}, true)
				if e.Shard > 0 {
					_ = c.ShardStore.UpdateShardWorker(context.Background(), c.RunID, c.Epoch, e.ID, e.IP, e.State, cleanup, redact.clean(e.Detail))
				}
			}
			notify(e)
		})
		err = errors.Join(err, cleanupErr)
		summary.Found, summary.CompletedShards, _ = journal.Stats()
		summary.ShardCount = manifest.Count
		summary.CleanupOutcome = "complete"
		if len(summary.Undeleted) > 0 || summary.ReconciliationUnresolved {
			summary.CleanupOutcome = "incomplete"
		}
		err = errors.Join(err, journal.FinishSharded(summary.ScanOutcome, summary.ReconciliationUnresolved, redact.clean(errorString(err))))
	}()
	if err = ctx.Err(); err != nil {
		return summary, err
	}
	for _, prior := range resume.ShardAttempts() {
		if prior.Phase == "failed" && prior.Number >= 3 {
			return summary, fmt.Errorf("shard %d exhausted two retries before restart", prior.Shard)
		}
	}
	spec := cloud.Spec{RunID: c.RunID, Region: cfg.Region, InstanceType: cfg.InstanceType}
	validateCtx, stopValidate := context.WithTimeout(ctx, c.Timeouts.Ready)
	err = c.Provider.Validate(validateCtx, spec)
	stopValidate()
	if err != nil {
		return summary, fmt.Errorf("provider validation: %w", err)
	}
	scanCtx, stopScan := context.WithTimeout(ctx, c.Timeouts.Scan)
	defer stopScan()
	var wg sync.WaitGroup
	var failure error
	var failureMu sync.Mutex
	completedBySlot := make(map[int]uint64)
	for _, a := range resume.ShardAttempts() {
		if a.Phase == "completed" {
			completedBySlot[a.Slot]++
		}
	}
	for slot := 1; slot <= effective; slot++ {
		completed := completedBySlot[slot]
		assigned := (manifest.Count-uint64(slot))/uint64(effective) + 1
		if completed == assigned {
			continue
		}
		wg.Add(1)
		go func(slot int, completed, assigned uint64) {
			defer wg.Done()
			if slotErr := c.runSlot(scanCtx, cfg, manifest, summary.Seed, slot, completed, assigned, result, journal, redact, notify, addTracked, markDeleted, checkIdentity, func() { ambiguityMu.Lock(); ambiguous = true; ambiguityMu.Unlock() }); slotErr != nil {
				failureMu.Lock()
				failure = errors.Join(failure, fmt.Errorf("worker slot %d: %w", slot, slotErr))
				failureMu.Unlock()
				stopScan()
			}
		}(slot, completed, assigned)
	}
	wg.Wait()
	err = errors.Join(failure, result.flush(context.Background()))
	if err == nil && scanCtx.Err() != nil {
		err = scanCtx.Err()
	}
	return summary, err
}

func newShardedJournal(out io.Writer, cfg scan.Config, t Timeouts, runID string, seed uint64, manifest scan.Manifest, workers int, cancel func()) (*scanfile.Writer, error) {
	output, exports := "(writer)", [2]string{}
	if d, ok := out.(interface{ ScanPaths() (string, [2]string) }); ok {
		output, exports = d.ScanPaths()
	}
	provider := cfg.Provider
	if provider == "" {
		provider = "vultr"
	}
	h := scanfile.Header{Format: scanfile.Format, Schema: scanfile.ShardedSchema, RunID: runID, CreatedAt: time.Now().UTC(), ControllerVersion: "v10", Label: "masscanr", OwnershipTag: "masscanr-run-" + runID, Config: scanfile.Config{Targets: cfg.Targets, RequestedTargets: cfg.RequestedTargets, Ports: cfg.PortSpec(), Backend: cfg.Backend, Seed: strconv.FormatUint(seed, 10), RequestedWorkers: cfg.Workers, Workers: workers, ShardCount: manifest.Count, PartitionVersion: manifest.Version, MaxProbesPerUnit: scan.MaxProbesPerShard, Rate: cfg.Rate, Provider: provider, Image: cfg.Image, SSHAuth: cfg.SSHAuth, Region: cfg.Region, InstanceType: cfg.InstanceType, Output: output, Exports: exports, Timeouts: map[string]string{"create": t.Create.String(), "ready": t.Ready.String(), "prepare": t.Prepare.String(), "scan": t.Scan.String(), "cleanup": t.Cleanup.String(), "delete": t.Delete.String(), "poll": t.Poll.String()}}}
	return scanfile.NewWriter(out, h, func(error) { cancel() })
}

func (c *Controller) runSlot(ctx context.Context, cfg scan.Config, manifest scan.Manifest, seed uint64, slot int, completed, assigned uint64, result *results, journal *scanfile.Writer, redact *redactor, notify func(Event), addTracked func(int, cloud.Worker), markDeleted func(string), checkIdentity func(scan.Prepared) error, markAmbiguous func()) error {
	var worker cloud.Worker
	setupFailures := 0
	for completed < assigned {
		if err := ctx.Err(); err != nil {
			return err
		}
		if worker.ID == "" {
			notify(Event{Slot: slot, State: "provisioning", AssignedShards: assigned, CompletedForSlot: completed})
			createCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), c.Timeouts.Create)
			w, err := c.Provider.Create(createCtx, cloud.Spec{RunID: c.RunID, Shard: slot, Region: cfg.Region, InstanceType: cfg.InstanceType})
			stop()
			redact.remember(w.Password)
			if w.ID != "" {
				addTracked(slot, w)
				if recordErr := c.recordShardedWorker(journal, slot, w, "booting", "pending", "", notify); recordErr != nil {
					return recordErr
				}
			}
			if err != nil || w.ID == "" {
				markAmbiguous()
				if err == nil {
					err = fmt.Errorf("provider returned no worker ID")
				}
				return err
			}
			readyCtx, readyStop := context.WithTimeout(ctx, c.Timeouts.Ready)
			worker, err = c.ready(readyCtx, w, c.RunID)
			readyStop()
			if err != nil {
				if ctx.Err() != nil || setupFailures >= 2 {
					return err
				}
				if retireErr := c.retireShardedWorker(context.Background(), journal, slot, worker, notify); retireErr != nil {
					return errors.Join(err, retireErr)
				}
				markDeleted(worker.ID)
				worker = cloud.Worker{}
				setupFailures++
				continue
			}
			redact.remember(worker.Password)
			if err = c.recordShardedWorker(journal, slot, worker, "preparing", "pending", "", notify); err != nil {
				return err
			}
			prepCtx, prepStop := context.WithTimeout(ctx, c.Timeouts.Prepare)
			preparation, prepErr := preparationFor(c.Backend, worker)
			var prepared scan.Prepared
			if prepErr == nil {
				prepared, prepErr = c.Runner.Prepare(prepCtx, worker, preparation)
			}
			prepStop()
			if prepErr != nil {
				if ctx.Err() != nil || setupFailures >= 2 {
					return prepErr
				}
				if retireErr := c.retireShardedWorker(context.Background(), journal, slot, worker, notify); retireErr != nil {
					return errors.Join(prepErr, retireErr)
				}
				markDeleted(worker.ID)
				worker = cloud.Worker{}
				setupFailures++
				continue
			}
			if err = checkIdentity(prepared); err != nil {
				return err
			}
			notify(Event{State: "prepared", Identity: &prepared.Identity})
			if err = c.recordShardedWorker(journal, slot, worker, "ready", "pending", prepared.String(), notify); err != nil {
				return err
			}
			setupFailures = 0
		}
		lease, done, err := c.ShardStore.BeginShardAttempt(ctx, c.RunID, c.Epoch, slot, worker.ID)
		if err != nil {
			return err
		}
		if done {
			break
		}
		unit, err := manifest.At(lease.Index)
		if err != nil {
			return err
		}
		attempt := scanfile.Attempt{Shard: int(lease.Index), Slot: slot, Number: lease.Attempt, InstanceID: worker.ID, Phase: "started"}
		if err = journal.Append(scanfile.Record{Type: "attempt", Attempt: &attempt}, true); err != nil {
			return err
		}
		notify(Event{Slot: slot, Shard: int(lease.Index), Attempt: lease.Attempt, ID: worker.ID, IP: worker.IP, State: "scanning", AssignedShards: assigned, CompletedForSlot: completed})
		intent := cfg.Intent()
		intent.Targets = unit.Targets
		plan, err := c.Backend.Build(intent, scan.Shard{Index: 1, Total: 1}, seed)
		if err == nil {
			var lastProgress time.Time
			var lastFinding time.Time
			err = c.Runner.Scan(ctx, worker, plan, func(f scan.Finding) error {
				if acceptErr := result.acceptSharded(unit, lease.Attempt, f); acceptErr != nil {
					return acceptErr
				}
				if time.Since(lastFinding) >= time.Second {
					lastFinding = time.Now()
					found := result.countFor(int(unit.Index))
					notify(Event{Slot: slot, Shard: int(unit.Index), Attempt: lease.Attempt, ID: worker.ID, IP: worker.IP, State: "finding", Found: &found, AssignedShards: assigned, CompletedForSlot: completed})
				}
				return nil
			}, func(p scan.Progress) {
				if time.Since(lastProgress) < time.Second {
					return
				}
				lastProgress = time.Now()
				stored := scanfile.Progress{Shard: int(unit.Index), Attempt: lease.Attempt, Waiting: p.Waiting}
				if !p.PercentUnknown {
					stored.Percent = &p.Percent
				}
				if !p.RateUnknown {
					stored.Rate = &p.Rate
				}
				if !p.ETAUnknown && p.ETA >= 0 {
					v := p.ETA.Seconds()
					stored.ETASeconds = &v
				}
				if appendErr := journal.Append(scanfile.Record{Type: "progress", Progress: &stored}, false); appendErr != nil {
					return
				}
				_ = c.ShardStore.UpdateShardProgress(context.Background(), c.RunID, c.Epoch, lease, stored.Percent)
				notify(Event{Slot: slot, Shard: int(unit.Index), Attempt: lease.Attempt, ID: worker.ID, IP: worker.IP, State: "scanning", Progress: &p, AssignedShards: assigned, CompletedForSlot: completed})
			})
		}
		if flushErr := result.flush(context.Background()); flushErr != nil {
			err = errors.Join(err, flushErr)
		}
		if ctx.Err() != nil {
			err = errors.Join(err, ctx.Err())
		}
		attempt.Phase = "completed"
		if err != nil {
			attempt.Phase = "failed"
			attempt.Detail = redact.clean(err.Error())
		}
		if journalErr := journal.Append(scanfile.Record{Type: "attempt", Attempt: &attempt}, true); journalErr != nil {
			return errors.Join(err, journalErr)
		}
		seq, flushErr := journal.Flush()
		if flushErr != nil {
			return errors.Join(err, flushErr)
		}
		if storeErr := c.ShardStore.FinishShardAttempt(context.Background(), c.RunID, c.Epoch, lease, err == nil, seq, attempt.Detail); storeErr != nil {
			return errors.Join(err, storeErr)
		}
		if err == nil {
			completed++
			_, totalDone, _ := journal.Stats()
			notify(Event{Slot: slot, Shard: int(unit.Index), Attempt: lease.Attempt, ID: worker.ID, IP: worker.IP, State: "completed", AssignedShards: assigned, CompletedForSlot: completed, CompletedShards: totalDone, ShardCount: manifest.Count})
			continue
		}
		notify(Event{Slot: slot, Shard: int(unit.Index), Attempt: lease.Attempt, ID: worker.ID, IP: worker.IP, State: "retrying", Detail: err.Error(), AssignedShards: assigned, CompletedForSlot: completed})
		if ctx.Err() != nil || lease.Attempt >= 3 {
			return err
		}
		if retireErr := c.retireShardedWorker(context.Background(), journal, slot, worker, notify); retireErr != nil {
			return errors.Join(err, retireErr)
		}
		markDeleted(worker.ID)
		worker = cloud.Worker{}
	}
	if worker.ID != "" {
		if err := c.retireShardedWorker(context.Background(), journal, slot, worker, notify); err != nil {
			return err
		}
		markDeleted(worker.ID)
	}
	return nil
}

func (c *Controller) recordShardedWorker(journal *scanfile.Writer, slot int, w cloud.Worker, phase, cleanup, detail string, notify func(Event)) error {
	if err := journal.Append(scanfile.Record{Type: "worker", Worker: &scanfile.Worker{Slot: slot, InstanceID: w.ID, IP: w.IP, Phase: phase, Cleanup: cleanup, Detail: detail}}, true); err != nil {
		return err
	}
	if phase == "booting" {
		if err := c.ShardStore.RecordShardWorker(context.Background(), c.RunID, c.Epoch, slot, w.ID, w.IP, phase); err != nil {
			return err
		}
	} else if err := c.ShardStore.UpdateShardWorker(context.Background(), c.RunID, c.Epoch, w.ID, w.IP, phase, cleanup, detail); err != nil {
		return err
	}
	notify(Event{Slot: slot, ID: w.ID, IP: w.IP, State: phase, Detail: detail})
	return nil
}

func (c *Controller) retireShardedWorker(ctx context.Context, journal *scanfile.Writer, slot int, w cloud.Worker, notify func(Event)) error {
	if err := c.recordShardedWorker(journal, slot, w, "deleting", "deleting", "", notify); err != nil {
		return err
	}
	deleteCtx, stop := context.WithTimeout(ctx, c.Timeouts.Delete)
	err := c.Provider.Delete(deleteCtx, w, c.RunID)
	stop()
	if err != nil {
		_ = c.recordShardedWorker(journal, slot, w, "cleanup failed", "incomplete", err.Error(), notify)
		return err
	}
	return c.recordShardedWorker(journal, slot, w, "shutdown", "complete", "", notify)
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
