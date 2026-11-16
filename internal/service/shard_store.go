package service

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/st3fan/masscanr/internal/controller"
	"github.com/st3fan/masscanr/internal/scan"
	"github.com/st3fan/masscanr/internal/scanfile"
)

// ShardLease is the durable identity of one whole-shard attempt.
type ShardLease = controller.ShardLease

type WorkerSlotState struct {
	Slot            int
	AssignedShards  uint64
	CompletedShards uint64
	NextShard       uint64
	InstanceID      string
	State           string
}

// RunWorker is one physical instance in a stable worker slot.
type RunWorker struct {
	ID        string    `json:"id"`
	RunID     string    `json:"run_id"`
	Slot      int       `json:"slot"`
	IP        string    `json:"ip"`
	State     string    `json:"state"`
	Cleanup   string    `json:"cleanup"`
	Detail    string    `json:"detail,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (s *Store) ListShardWorkers(ctx context.Context, runID, after string, limit int) ([]RunWorker, error) {
	if limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("limit must be 1..1000")
	}
	rows, err := s.Pool.Query(ctx, `SELECT instance_id,run_id,slot,ip,state,cleanup,detail,created_at,updated_at FROM masscanr.run_workers WHERE run_id=$1 AND instance_id>$2 ORDER BY instance_id LIMIT $3`, runID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RunWorker{}
	for rows.Next() {
		var w RunWorker
		if err = rows.Scan(&w.ID, &w.RunID, &w.Slot, &w.IP, &w.State, &w.Cleanup, &w.Detail, &w.CreatedAt, &w.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// InitShardRun persists only slot cursors. Unstarted shard scopes remain
// derivable from the frozen manifest instead of preallocating thousands of rows.
func (s *Store) InitShardRun(ctx context.Context, runID string, manifest scan.Manifest, requested int) (int, int64, error) {
	if manifest.Version != scan.PartitionVersion || manifest.Count == 0 || requested < 1 {
		return 0, 0, fmt.Errorf("invalid shard manifest or worker request")
	}
	effective := min(requested, int(manifest.Count))
	var epoch int64
	err := s.tx(ctx, func(tx pgx.Tx) error {
		var oldCount, oldEpoch int64
		if e := tx.QueryRow(ctx, `SELECT shard_count,execution_epoch FROM masscanr.runs WHERE id=$1 AND active AND NOT stop FOR UPDATE`, runID).Scan(&oldCount, &oldEpoch); e != nil {
			return e
		}
		if oldEpoch != 0 || oldCount != 0 && oldCount != int64(manifest.Count) {
			return ErrConflict
		}
		if e := tx.QueryRow(ctx, `UPDATE masscanr.runs SET shard_count=$2,requested_workers=$3,effective_workers=$4,execution_epoch=1 WHERE id=$1 RETURNING execution_epoch`, runID, int64(manifest.Count), requested, effective).Scan(&epoch); e != nil {
			return e
		}
		for slot := 1; slot <= effective; slot++ {
			assigned := (manifest.Count-uint64(slot))/uint64(effective) + 1
			if _, e := tx.Exec(ctx, `INSERT INTO masscanr.run_worker_slots(run_id,slot,assigned_shards,next_shard) VALUES($1,$2,$3,$4)`, runID, slot, int64(assigned), int64(slot)); e != nil {
				return e
			}
		}
		return nil
	})
	return effective, epoch, err
}

// AdvanceShardEpoch fences callbacks from an earlier controller after the
// caller has reconciled current-run workers and validated journal evidence.
func (s *Store) AdvanceShardEpoch(ctx context.Context, runID string) (int64, error) {
	var epoch int64
	err := s.tx(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `UPDATE masscanr.runs SET execution_epoch=execution_epoch+1 WHERE id=$1 AND active AND shard_count>0 RETURNING execution_epoch`, runID).Scan(&epoch)
	})
	return epoch, err
}

// RestoreShardRun rebuilds the execution projection from a validated journal.
// Journal events, not interrupted process callbacks, determine completed work.
// The new epoch fences any callback retained by the previous process.
func (s *Store) RestoreShardRun(ctx context.Context, runID string, state scanfile.State) (int64, error) {
	if state.Header == nil || state.Header.Schema != scanfile.ShardedSchema || state.Header.RunID != runID || state.Truncated || state.Finished != nil || len(state.ActiveShardAttempts()) != 0 {
		return 0, fmt.Errorf("journal is not safe for shard continuation")
	}
	workers := state.Header.Config.Workers
	count := state.Header.Config.ShardCount
	if workers < 1 || count == 0 || uint64(workers) > count {
		return 0, fmt.Errorf("invalid journal shard manifest")
	}
	var epoch int64
	err := s.tx(ctx, func(tx pgx.Tx) error {
		var active, stopped bool
		if e := tx.QueryRow(ctx, `SELECT active,stop FROM masscanr.runs WHERE id=$1 FOR UPDATE`, runID).Scan(&active, &stopped); e != nil {
			return e
		}
		if !active || stopped {
			return ErrConflict
		}
		for _, table := range []string{"run_shard_attempts", "run_shards", "run_workers", "run_worker_slots"} {
			if _, e := tx.Exec(ctx, `DELETE FROM masscanr.`+table+` WHERE run_id=$1`, runID); e != nil {
				return e
			}
		}
		if e := tx.QueryRow(ctx, `UPDATE masscanr.runs SET shard_count=$2,requested_workers=$3,effective_workers=$4,completed_shards=0,execution_epoch=execution_epoch+1 WHERE id=$1 RETURNING execution_epoch`, runID, int64(count), state.Header.Config.RequestedWorkers, workers).Scan(&epoch); e != nil {
			return e
		}
		for slot := 1; slot <= workers; slot++ {
			assigned := (count-uint64(slot))/uint64(workers) + 1
			if _, e := tx.Exec(ctx, `INSERT INTO masscanr.run_worker_slots(run_id,slot,assigned_shards,next_shard) VALUES($1,$2,$3,$4)`, runID, slot, int64(assigned), slot); e != nil {
				return e
			}
		}
		for _, worker := range state.Workers {
			if worker.Slot == 0 && worker.Cleanup == "complete" {
				continue // An ambiguously created worker has no assigned slot.
			}
			if worker.Slot < 1 || worker.Slot > workers {
				return fmt.Errorf("journal worker slot is invalid")
			}
			if _, e := tx.Exec(ctx, `INSERT INTO masscanr.run_workers(run_id,instance_id,slot,ip,state,cleanup,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,now(),now())`, runID, worker.InstanceID, worker.Slot, worker.IP, worker.Phase, worker.Cleanup); e != nil {
				return e
			}
		}
		for slot := 1; slot <= workers; slot++ {
			if id := state.SlotInstance(slot); id != "" {
				if _, e := tx.Exec(ctx, `UPDATE masscanr.run_worker_slots SET current_instance_id=$3,state='pending' WHERE run_id=$1 AND slot=$2`, runID, slot, id); e != nil {
					return e
				}
			}
		}
		for _, attempt := range state.ShardAttempts() {
			status := "running"
			if attempt.Phase == "failed" || attempt.Phase == "completed" {
				status = attempt.Phase
			}
			if attempt.Phase == "started" {
				_, e := tx.Exec(ctx, `INSERT INTO masscanr.run_shards(run_id,shard_index,slot,state,attempts,current_instance_id,updated_at) VALUES($1,$2,$3,'running',$4,$5,now()) ON CONFLICT(run_id,shard_index) DO UPDATE SET state='running',attempts=$4,current_instance_id=$5,updated_at=now()`, runID, attempt.Shard, attempt.Slot, attempt.Number, attempt.InstanceID)
				if e != nil {
					return e
				}
				if _, e = tx.Exec(ctx, `INSERT INTO masscanr.run_shard_attempts(run_id,shard_index,attempt,instance_id,state,started_at) VALUES($1,$2,$3,$4,'running',now())`, runID, attempt.Shard, attempt.Number, attempt.InstanceID); e != nil {
					return e
				}
				continue
			}
			if _, e := tx.Exec(ctx, `UPDATE masscanr.run_shard_attempts SET state=$4,detail=$5,finished_at=now() WHERE run_id=$1 AND shard_index=$2 AND attempt=$3`, runID, attempt.Shard, attempt.Number, status, attempt.Detail); e != nil {
				return e
			}
			if _, e := tx.Exec(ctx, `UPDATE masscanr.run_shards SET state=$3,updated_at=now() WHERE run_id=$1 AND shard_index=$2`, runID, attempt.Shard, status); e != nil {
				return e
			}
			if status == "completed" {
				if _, e := tx.Exec(ctx, `UPDATE masscanr.run_worker_slots SET completed_shards=completed_shards+1,next_shard=next_shard+$3 WHERE run_id=$1 AND slot=$2`, runID, attempt.Slot, workers); e != nil {
					return e
				}
				if _, e := tx.Exec(ctx, `UPDATE masscanr.runs SET completed_shards=completed_shards+1 WHERE id=$1`, runID); e != nil {
					return e
				}
			}
		}
		return nil
	})
	return epoch, err
}

// RecordShardWorker assigns a physical instance to a stable slot. A previous
// instance must have completed owned cleanup before replacement.
func (s *Store) RecordShardWorker(ctx context.Context, runID string, epoch int64, slot int, instanceID, ip, state string) error {
	if instanceID == "" || slot < 1 || state == "" {
		return fmt.Errorf("invalid worker identity")
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		var current string
		if e := tx.QueryRow(ctx, `SELECT COALESCE(current_instance_id,'') FROM masscanr.run_worker_slots WHERE run_id=$1 AND slot=$2 FOR UPDATE`, runID, slot).Scan(&current); e != nil {
			return e
		}
		if e := checkShardEpoch(ctx, tx, runID, epoch); e != nil {
			return e
		}
		if current != "" && current != instanceID {
			var cleanup string
			if e := tx.QueryRow(ctx, `SELECT cleanup FROM masscanr.run_workers WHERE run_id=$1 AND instance_id=$2`, runID, current).Scan(&cleanup); e != nil {
				return e
			}
			if cleanup != "complete" {
				return ErrConflict
			}
		}
		now := time.Now().UTC()
		result, e := tx.Exec(ctx, `INSERT INTO masscanr.run_workers(run_id,instance_id,slot,ip,state,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$6)
ON CONFLICT(run_id,instance_id) DO UPDATE SET ip=EXCLUDED.ip,state=EXCLUDED.state,updated_at=EXCLUDED.updated_at
WHERE masscanr.run_workers.slot=EXCLUDED.slot AND masscanr.run_workers.cleanup<>'complete'`, runID, instanceID, slot, ip, state, now)
		if e != nil {
			return e
		}
		if result.RowsAffected() != 1 {
			return ErrConflict
		}
		_, e = tx.Exec(ctx, `UPDATE masscanr.run_worker_slots SET current_instance_id=$3,state=$4 WHERE run_id=$1 AND slot=$2`, runID, slot, instanceID, state)
		return e
	})
}

func (s *Store) UpdateShardWorker(ctx context.Context, runID string, epoch int64, instanceID, state, cleanup, detail string) error {
	return s.tx(ctx, func(tx pgx.Tx) error {
		if e := checkShardEpoch(ctx, tx, runID, epoch); e != nil {
			return e
		}
		result, e := tx.Exec(ctx, `UPDATE masscanr.run_workers SET state=$3,cleanup=$4,detail=$5,updated_at=$6 WHERE run_id=$1 AND instance_id=$2 AND cleanup<>'complete'`, runID, instanceID, state, cleanup, detail, time.Now().UTC())
		if e != nil {
			return e
		}
		if result.RowsAffected() != 1 {
			return ErrConflict
		}
		return nil
	})
}

// BeginShardAttempt advances neither a slot nor a retry budget until its
// previous attempt has a durable terminal state.
func (s *Store) BeginShardAttempt(ctx context.Context, runID string, epoch int64, slot int, instanceID string) (ShardLease, bool, error) {
	var lease ShardLease
	var done bool
	err := s.tx(ctx, func(tx pgx.Tx) error {
		if e := checkShardEpoch(ctx, tx, runID, epoch); e != nil {
			return e
		}
		var count int64
		var workers int
		var stopped bool
		if e := tx.QueryRow(ctx, `SELECT shard_count,effective_workers,stop FROM masscanr.runs WHERE id=$1`, runID).Scan(&count, &workers, &stopped); e != nil {
			return e
		}
		if stopped {
			return ErrConflict
		}
		var next int64
		var current string
		if e := tx.QueryRow(ctx, `SELECT next_shard,COALESCE(current_instance_id,'') FROM masscanr.run_worker_slots WHERE run_id=$1 AND slot=$2 FOR UPDATE`, runID, slot).Scan(&next, &current); e != nil {
			return e
		}
		if slot < 1 || slot > workers || current != instanceID || instanceID == "" {
			return ErrConflict
		}
		if next > count {
			done = true
			return nil
		}
		var priorState string
		var priorAttempts int
		e := tx.QueryRow(ctx, `SELECT state,attempts FROM masscanr.run_shards WHERE run_id=$1 AND shard_index=$2 FOR UPDATE`, runID, next).Scan(&priorState, &priorAttempts)
		if e != nil && e != pgx.ErrNoRows {
			return e
		}
		attempt := 1
		now := time.Now().UTC()
		if e == pgx.ErrNoRows {
			if _, e = tx.Exec(ctx, `INSERT INTO masscanr.run_shards(run_id,shard_index,slot,state,attempts,current_instance_id,updated_at) VALUES($1,$2,$3,'running',1,$4,$5)`, runID, next, slot, instanceID, now); e != nil {
				return e
			}
		} else {
			if priorState != "failed" || priorAttempts >= 3 {
				return ErrConflict
			}
			attempt = priorAttempts + 1
			if _, e = tx.Exec(ctx, `UPDATE masscanr.run_shards SET state='running',attempts=$3,current_instance_id=$4,progress_percent=NULL,progress_at=NULL,updated_at=$5 WHERE run_id=$1 AND shard_index=$2`, runID, next, attempt, instanceID, now); e != nil {
				return e
			}
		}
		if _, e = tx.Exec(ctx, `INSERT INTO masscanr.run_shard_attempts(run_id,shard_index,attempt,instance_id,state,started_at) VALUES($1,$2,$3,$4,'running',$5)`, runID, next, attempt, instanceID, now); e != nil {
			return e
		}
		lease = ShardLease{Index: uint64(next), Slot: slot, Attempt: attempt, InstanceID: instanceID}
		return nil
	})
	return lease, done, err
}

// FinishShardAttempt requires the journal/result checkpoint of the terminal
// event to be durable before completion is published.
func (s *Store) FinishShardAttempt(ctx context.Context, runID string, epoch int64, lease ShardLease, success bool, journalSeq uint64, detail string) error {
	if journalSeq == 0 || journalSeq > uint64(^uint64(0)>>1) {
		return fmt.Errorf("invalid journal sequence")
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		if e := checkShardEpoch(ctx, tx, runID, epoch); e != nil {
			return e
		}
		var state, instanceID string
		var attempt, slot int
		if e := tx.QueryRow(ctx, `SELECT state,attempts,slot,current_instance_id FROM masscanr.run_shards WHERE run_id=$1 AND shard_index=$2 FOR UPDATE`, runID, int64(lease.Index)).Scan(&state, &attempt, &slot, &instanceID); e != nil {
			return e
		}
		if state != "running" || attempt != lease.Attempt || slot != lease.Slot || instanceID != lease.InstanceID {
			return ErrConflict
		}
		status := "failed"
		if success {
			status = "completed"
		}
		now := time.Now().UTC()
		result, e := tx.Exec(ctx, `UPDATE masscanr.run_shard_attempts SET state=$4,detail=$5,finished_at=$6,journal_seq=$7 WHERE run_id=$1 AND shard_index=$2 AND attempt=$3 AND state='running'`, runID, int64(lease.Index), lease.Attempt, status, detail, now, int64(journalSeq))
		if e != nil {
			return e
		}
		if result.RowsAffected() != 1 {
			return ErrConflict
		}
		if _, e := tx.Exec(ctx, `UPDATE masscanr.run_shards SET state=$3,updated_at=$4 WHERE run_id=$1 AND shard_index=$2`, runID, int64(lease.Index), status, now); e != nil {
			return e
		}
		if success {
			var workers int
			if e := tx.QueryRow(ctx, `SELECT effective_workers FROM masscanr.runs WHERE id=$1`, runID).Scan(&workers); e != nil {
				return e
			}
			result, e := tx.Exec(ctx, `UPDATE masscanr.run_worker_slots SET completed_shards=completed_shards+1,next_shard=next_shard+$3 WHERE run_id=$1 AND slot=$2 AND next_shard=$4`, runID, lease.Slot, workers, int64(lease.Index))
			if e != nil {
				return e
			}
			if result.RowsAffected() != 1 {
				return ErrConflict
			}
			if _, e := tx.Exec(ctx, `UPDATE masscanr.runs SET completed_shards=completed_shards+1 WHERE id=$1`, runID); e != nil {
				return e
			}
		}
		return nil
	})
}

func (s *Store) UpdateShardProgress(ctx context.Context, runID string, epoch int64, lease ShardLease, percent *float64) error {
	if percent != nil && (*percent < 0 || *percent > 100) {
		return fmt.Errorf("invalid shard percent")
	}
	return s.tx(ctx, func(tx pgx.Tx) error {
		if e := checkShardEpoch(ctx, tx, runID, epoch); e != nil {
			return e
		}
		result, e := tx.Exec(ctx, `UPDATE masscanr.run_shards SET progress_percent=$5,progress_at=$6,updated_at=$6 WHERE run_id=$1 AND shard_index=$2 AND attempts=$3 AND current_instance_id=$4 AND state='running'`, runID, int64(lease.Index), lease.Attempt, lease.InstanceID, percent, time.Now().UTC())
		if e != nil {
			return e
		}
		if result.RowsAffected() != 1 {
			return ErrConflict
		}
		return nil
	})
}

func (s *Store) WorkerSlots(ctx context.Context, runID string) ([]WorkerSlotState, error) {
	rows, err := s.Pool.Query(ctx, `SELECT slot,assigned_shards,completed_shards,next_shard,COALESCE(current_instance_id,''),state FROM masscanr.run_worker_slots WHERE run_id=$1 ORDER BY slot`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorkerSlotState
	for rows.Next() {
		var slot WorkerSlotState
		var assigned, completed, next int64
		if err = rows.Scan(&slot.Slot, &assigned, &completed, &next, &slot.InstanceID, &slot.State); err != nil {
			return nil, err
		}
		slot.AssignedShards, slot.CompletedShards, slot.NextShard = uint64(assigned), uint64(completed), uint64(next)
		out = append(out, slot)
	}
	return out, rows.Err()
}

func checkShardEpoch(ctx context.Context, tx pgx.Tx, runID string, epoch int64) error {
	var current int64
	var active bool
	if err := tx.QueryRow(ctx, `SELECT execution_epoch,active FROM masscanr.runs WHERE id=$1`, runID).Scan(&current, &active); err != nil {
		return err
	}
	if !active || current != epoch || epoch < 1 {
		return ErrConflict
	}
	return nil
}
