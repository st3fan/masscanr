package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/st3fan/masscanr/internal/scanfile"
)

type postgresResultSink struct {
	store    *Store
	runID    string
	targetID string
}

func (s *Store) ResultSink(runID, targetID string) postgresResultSink {
	return postgresResultSink{store: s, runID: runID, targetID: targetID}
}

func (sink postgresResultSink) WriteBatch(ctx context.Context, seq uint64, findings []scanfile.Finding) error {
	if len(findings) == 0 {
		return nil
	}
	if seq == 0 || seq > math.MaxInt64 {
		return fmt.Errorf("invalid journal checkpoint")
	}
	return sink.store.tx(ctx, func(tx pgx.Tx) error {
		var checkpoint int64
		err := tx.QueryRow(ctx, `SELECT result_checkpoint FROM masscanr.runs WHERE id=$1 AND target_id=$2 FOR UPDATE`, sink.runID, sink.targetID).Scan(&checkpoint)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if seq <= uint64(checkpoint) {
			return nil
		} // An ambiguous prior commit is safe to retry.
		for _, finding := range findings {
			if err = upsertResult(ctx, tx, sink.runID, sink.targetID, finding); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `UPDATE masscanr.runs SET result_checkpoint=$2,result_state='live' WHERE id=$1`, sink.runID, int64(seq))
		return err
	})
}

func upsertResult(ctx context.Context, tx pgx.Tx, runID, targetID string, finding scanfile.Finding) error {
	if !finding.IP.Is4() || finding.Protocol != "tcp" || finding.State != "open" || finding.Target == "" {
		return fmt.Errorf("invalid persisted finding")
	}
	timestamp := strconv.FormatUint(finding.Timestamp, 10)
	result, err := tx.Exec(ctx, `INSERT INTO masscanr.scan_results(run_id,target_id,ip,port,protocol,first_seen,last_seen,observations,selector,origin_prefix)
VALUES($1,$2,$3::inet,$4,$5,$6::numeric,$6::numeric,1,$7,$8)
ON CONFLICT(run_id,ip,port,protocol) DO UPDATE SET
first_seen=LEAST(masscanr.scan_results.first_seen,EXCLUDED.first_seen),
last_seen=GREATEST(masscanr.scan_results.last_seen,EXCLUDED.last_seen),
observations=masscanr.scan_results.observations+1
WHERE masscanr.scan_results.target_id=EXCLUDED.target_id AND masscanr.scan_results.selector=EXCLUDED.selector AND masscanr.scan_results.origin_prefix=EXCLUDED.origin_prefix`,
		runID, targetID, finding.IP.String(), int(finding.Port), finding.Protocol, timestamp, finding.Target, finding.Prefix)
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Store) resultObservationCount(ctx context.Context, runID string) (uint64, error) {
	var value string
	err := s.Pool.QueryRow(ctx, `SELECT COALESCE(sum(observations),0)::text FROM masscanr.scan_results WHERE run_id=$1`, runID).Scan(&value)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(value, 10, 64)
}

func (s *Store) markResultError(ctx context.Context, runID string) {
	markCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, _ = s.Pool.Exec(markCtx, `UPDATE masscanr.runs SET result_state='error' WHERE id=$1`, runID)
}

// FinalizeRunResults validates the journal before reporting complete coverage.
// A missing live batch is repaired from evidence, including interrupted runs.
func (s *Store) FinalizeRunResults(ctx context.Context, runID, path string) error {
	file, err := openArtifact(path)
	if err != nil {
		s.markResultError(ctx, runID)
		return err
	}
	state, err := scanfile.Replay(contextReader{Context: ctx, Reader: file}, nil)
	_ = file.Close()
	if err != nil || state.Header == nil || state.Header.RunID != runID {
		s.markResultError(ctx, runID)
		if err != nil {
			return err
		}
		return fmt.Errorf("journal identity mismatch")
	}
	count, err := s.resultObservationCount(ctx, runID)
	if err != nil {
		s.markResultError(ctx, runID)
		return err
	}
	if count != state.Found {
		return s.RebuildRunResults(ctx, runID, path)
	}
	resultState := "partial"
	if state.Complete() {
		resultState = "complete"
	}
	if state.Seq > math.MaxInt64 {
		s.markResultError(ctx, runID)
		return fmt.Errorf("journal sequence exceeds database range")
	}
	_, err = s.Pool.Exec(ctx, `UPDATE masscanr.runs SET result_checkpoint=$2,result_state=$3 WHERE id=$1`, runID, int64(state.Seq), resultState)
	if err != nil {
		s.markResultError(ctx, runID)
	}
	return err
}

// RebuildRunResults uses one transaction so provisional replay callbacks are
// never published if a later record is invalid. Memory stays bounded.
func (s *Store) RebuildRunResults(ctx context.Context, runID, path string) error {
	file, err := openArtifact(path)
	if err != nil {
		s.markResultError(ctx, runID)
		return err
	}
	defer file.Close()
	err = s.tx(ctx, func(tx pgx.Tx) error {
		var targetID string
		if e := tx.QueryRow(ctx, `SELECT target_id FROM masscanr.runs WHERE id=$1 FOR UPDATE`, runID).Scan(&targetID); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, `DELETE FROM masscanr.scan_results WHERE run_id=$1`, runID); e != nil {
			return e
		}
		state, e := scanfile.Replay(contextReader{Context: ctx, Reader: file}, func(finding scanfile.Finding) error {
			return upsertResult(ctx, tx, runID, targetID, finding)
		})
		if e != nil {
			return e
		}
		if state.Header == nil || state.Header.RunID != runID {
			return fmt.Errorf("journal identity mismatch")
		}
		if state.Seq > math.MaxInt64 {
			return fmt.Errorf("journal sequence exceeds database range")
		}
		resultState := "partial"
		if state.Complete() {
			resultState = "complete"
		}
		_, e = tx.Exec(ctx, `UPDATE masscanr.runs SET result_checkpoint=$2,result_state=$3 WHERE id=$1`, runID, int64(state.Seq), resultState)
		return e
	})
	if err != nil {
		s.markResultError(ctx, runID)
	}
	return err
}
