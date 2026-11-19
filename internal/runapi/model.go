// Package runapi defines credential-free run snapshots shared by the service and viewer.
package runapi

import (
	"github.com/st3fan/masscanr/internal/controller"
	"github.com/st3fan/masscanr/internal/scan"
	"net/netip"
	"time"
)

const SnapshotVersion = 3

type Preflight struct {
	ID                   string              `json:"id"`
	TargetID             string              `json:"target_id,omitempty"`
	TargetRevision       int64               `json:"target_revision,omitempty"`
	ScanPlanID           string              `json:"scan_plan_id,omitempty"`
	ScanPlanRevision     int64               `json:"scan_plan_revision,omitempty"`
	RequestedScope       []netip.Prefix      `json:"requested_scope,omitempty"`
	ExcludedScope        []netip.Prefix      `json:"excluded_scope,omitempty"`
	ExpiresAt            time.Time           `json:"expires_at"`
	Config               scan.Config         `json:"config"`
	Timeouts             controller.Timeouts `json:"timeouts"`
	Workload             scan.Workload       `json:"workload"`
	ShardCount           uint64              `json:"shard_count"`
	EffectiveWorkers     int                 `json:"effective_workers"`
	MaxProbesPerShard    uint64              `json:"max_probes_per_shard"`
	RequiresConfirmation bool                `json:"requires_confirmation"`
}
type Plan = Preflight

type Run struct {
	SnapshotVersion int               `json:"snapshot_version"`
	Active          bool              `json:"active"`
	Settled         bool              `json:"settled"`
	StopRequested   bool              `json:"stop_requested"`
	WorkerSnapshots map[string]Worker `json:"worker_snapshots"`

	ID         string                   `json:"id"`
	TargetID   string                   `json:"target_id,omitempty"`
	ScanPlanID string                   `json:"scan_plan_id,omitempty"`
	Plan       Preflight                `json:"preflight"`
	Phase      string                   `json:"phase"`
	CreatedAt  time.Time                `json:"created_at"`
	Artifact   string                   `json:"artifact"`
	Summary    controller.Summary       `json:"summary"`
	Error      string                   `json:"error,omitempty"`
	Events     map[int]controller.Event `json:"workers,omitempty"`
}

// Worker retains independent execution and cleanup state. Nil measurements are unknown.
type Worker struct {
	Shard           int            `json:"shard"`
	Slot            int            `json:"slot,omitempty"`
	CurrentShard    int            `json:"current_shard,omitempty"`
	Attempt         int            `json:"attempt,omitempty"`
	AssignedShards  uint64         `json:"assigned_shards,omitempty"`
	CompletedShards uint64         `json:"completed_shards,omitempty"`
	InstanceCount   int            `json:"instance_count,omitempty"`
	ID              string         `json:"id"`
	IP              string         `json:"ip"`
	State           string         `json:"state"`
	Detail          string         `json:"detail,omitempty"`
	Cleanup         string         `json:"cleanup"`
	CleanupDetail   string         `json:"cleanup_detail,omitempty"`
	Progress        *scan.Progress `json:"progress"`
	ProgressAt      *time.Time     `json:"progress_at"`
	Found           *uint64        `json:"found"`
	Prepared        *scan.Prepared `json:"prepared,omitempty"`
}

type ActiveRuns struct {
	SnapshotVersion int   `json:"snapshot_version"`
	Runs            []Run `json:"runs"`
}
