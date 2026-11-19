package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/st3fan/masscanr/internal/scan"
	"github.com/st3fan/masscanr/internal/scanfile"
)

func validateFrozenConfigSize(cfg scan.Config) error {
	if len(cfg.Exclusions) == 0 {
		return nil // Preserve the existing no-exclusion contract.
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	// Leave room for journal metadata, worker configuration, and future fields.
	if len(data) > scanfile.MaxRecordBytes/2 {
		return fmt.Errorf("frozen scan scope exceeds journal size limit")
	}
	return nil
}

func (s *Store) FreezeExclusions(ctx context.Context, ids []string) ([]scan.ExclusionSnapshot, error) {
	var snapshots []scan.ExclusionSnapshot
	err := s.tx(ctx, func(tx pgx.Tx) error {
		ordered, err := validatePlanExclusions(ctx, tx, ids)
		if err != nil {
			return err
		}
		if len(ordered) > 0 {
			snapshots = make([]scan.ExclusionSnapshot, 0, len(ordered))
		}
		for _, id := range ordered {
			exclusion, err := readExclusion(ctx, tx, id)
			if err != nil {
				return err
			}
			snapshot := scan.ExclusionSnapshot{ID: id, Revision: exclusion.Revision}
			for _, entry := range exclusion.Entries {
				prefix, err := netip.ParsePrefix(entry.Selector)
				if err != nil {
					return fmt.Errorf("invalid stored exclusion selector")
				}
				snapshot.Entries = append(snapshot.Entries, scan.ExclusionSelector{Input: entry.Input, Prefix: prefix})
			}
			snapshots = append(snapshots, snapshot)
		}
		_, err = scan.ExclusionUnion(snapshots)
		return err
	})
	return snapshots, err
}

func validateFrozenExclusions(ctx context.Context, tx pgx.Tx, snapshots []scan.ExclusionSnapshot) error {
	for _, snapshot := range snapshots {
		current, err := readExclusion(ctx, tx, snapshot.ID)
		if err != nil || current.Revision != snapshot.Revision || current.ArchivedAt != nil {
			return ErrConflict
		}
	}
	return nil
}

func snapshotsMatchPlan(ids []string, snapshots []scan.ExclusionSnapshot) bool {
	if len(ids) != len(snapshots) {
		return false
	}
	for i, id := range ids {
		if snapshots[i].ID != id {
			return false
		}
	}
	return true
}

func validatePreflightScope(preflight Plan) error {
	if err := preflight.Config.Validate(); err != nil {
		return err
	}
	if err := validateFrozenConfigSize(preflight.Config); err != nil {
		return err
	}
	requested := scan.TargetUnion(preflight.Config.RequestedTargets)
	if len(preflight.RequestedScope) == 0 && len(preflight.Config.Exclusions) == 0 {
		// Preflights created before Exclusion support carried only Config.
		preflight.RequestedScope = requested
	}
	if !slices.Equal(preflight.RequestedScope, requested) {
		return fmt.Errorf("preflight requested scope differs from frozen Target")
	}
	excluded, err := scan.SubtractPrefixes(requested, preflight.Config.Targets)
	if err != nil || !slices.Equal(preflight.ExcludedScope, excluded) {
		return fmt.Errorf("preflight excluded scope differs from frozen exclusions")
	}
	workload, err := preflight.Config.Intent().Workload()
	if err != nil || workload != preflight.Workload {
		return fmt.Errorf("preflight workload differs from frozen scope")
	}
	manifest, err := scan.Partition(preflight.Config.Targets, len(preflight.Config.Ports))
	if err != nil || manifest.Count != preflight.ShardCount {
		return fmt.Errorf("preflight shard count differs from frozen scope")
	}
	return nil
}
