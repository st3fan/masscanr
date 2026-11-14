// Package runapi defines credential-free run snapshots shared by the service and viewer.
package runapi

import (
	"github.com/st3fan/masscanr/internal/controller"
	"github.com/st3fan/masscanr/internal/scan"
	"time"
)

const SnapshotVersion = 1

type Plan struct {
	ID                   string              `json:"id"`
	ScanID               string              `json:"scan_id"`
	Revision             int64               `json:"revision"`
	ExpiresAt            time.Time           `json:"expires_at"`
	Config               scan.Config         `json:"config"`
	Timeouts             controller.Timeouts `json:"timeouts"`
	Workload             scan.Workload       `json:"workload"`
	RequiresConfirmation bool                `json:"requires_confirmation"`
}
type Run struct {
	SnapshotVersion int               `json:"snapshot_version"`
	Active          bool              `json:"active"`
	Settled         bool              `json:"settled"`
	StopRequested   bool              `json:"stop_requested"`
	WorkerSnapshots map[string]Worker `json:"worker_snapshots"`

	ID        string                   `json:"id"`
	ScanID    string                   `json:"scan_id"`
	Plan      Plan                     `json:"plan"`
	Phase     string                   `json:"phase"`
	CreatedAt time.Time                `json:"created_at"`
	Artifact  string                   `json:"artifact"`
	Summary   controller.Summary       `json:"summary"`
	Error     string                   `json:"error,omitempty"`
	Events    map[int]controller.Event `json:"workers,omitempty"`
}

// Worker retains independent execution and cleanup state. Nil measurements are unknown.
type Worker struct {
	Shard         int            `json:"shard"`
	ID            string         `json:"id"`
	IP            string         `json:"ip"`
	State         string         `json:"state"`
	Detail        string         `json:"detail,omitempty"`
	Cleanup       string         `json:"cleanup"`
	CleanupDetail string         `json:"cleanup_detail,omitempty"`
	Progress      *scan.Progress `json:"progress"`
	ProgressAt    *time.Time     `json:"progress_at"`
	Found         *uint64        `json:"found"`
	Prepared      *scan.Prepared `json:"prepared,omitempty"`
}

type ActiveRuns struct {
	SnapshotVersion int   `json:"snapshot_version"`
	Runs            []Run `json:"runs"`
}
