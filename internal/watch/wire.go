package watch

import (
	"strconv"
	"strings"
	"time"
)

// These types describe only the fields watch reads from the server's JSON API.
// Keeping them here prevents the client binary from importing scan execution code.
const snapshotVersion = 2
const maxShards = 65535

type runSnapshot struct {
	SnapshotVersion int                       `json:"snapshot_version"`
	Active          bool                      `json:"active"`
	Settled         bool                      `json:"settled"`
	StopRequested   bool                      `json:"stop_requested"`
	WorkerSnapshots map[string]workerSnapshot `json:"worker_snapshots"`
	ID              string                    `json:"id"`
	TargetID        string                    `json:"target_id"`
	ScanPlanID      string                    `json:"scan_plan_id"`
	Plan            planSnapshot              `json:"preflight"`
	Phase           string                    `json:"phase"`
	CreatedAt       time.Time                 `json:"created_at"`
	Artifact        string                    `json:"artifact"`
	Summary         summarySnapshot           `json:"summary"`
	Error           string                    `json:"error,omitempty"`
}

type planSnapshot struct {
	ScanPlanRevision int64           `json:"scan_plan_revision"`
	Config           configSnapshot  `json:"config"`
	Timeouts         timeoutSnapshot `json:"timeouts"`
}

type timeoutSnapshot struct {
	Scan    time.Duration `json:"Scan"`
	Cleanup time.Duration `json:"Cleanup"`
}

type configSnapshot struct {
	Backend          string            `json:"Backend"`
	Targets          []string          `json:"Targets"`
	RequestedTargets []requestedTarget `json:"RequestedTargets"`
	Ports            []uint16          `json:"Ports"`
	Shards           int               `json:"Shards"`
	Rate             uint64            `json:"Rate"`
	Region           string            `json:"Region"`
	InstanceType     string            `json:"InstanceType"`
}

type requestedTarget struct {
	Input      string            `json:"input"`
	Target     string            `json:"target"`
	Prefixes   []string          `json:"prefixes"`
	Resolution *targetResolution `json:"resolution,omitempty"`
}

type targetResolution struct {
	Source     string    `json:"source"`
	FetchedAt  time.Time `json:"fetched_at"`
	SnapshotAt time.Time `json:"snapshot_at"`
}

func (c configSnapshot) PortSpec() string {
	var parts []string
	for i := 0; i < len(c.Ports); i++ {
		first, last := c.Ports[i], c.Ports[i]
		for i+1 < len(c.Ports) && uint32(c.Ports[i+1]) == uint32(last)+1 {
			i++
			last = c.Ports[i]
		}
		part := strconv.Itoa(int(first))
		if last != first {
			part += "-" + strconv.Itoa(int(last))
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, ",")
}

type summarySnapshot struct {
	Identity                 identitySnapshot `json:"Identity"`
	RunID                    string           `json:"RunID"`
	Seed                     uint64           `json:"Seed"`
	Found                    uint64           `json:"Found"`
	Undeleted                []string         `json:"Undeleted"`
	ScanOutcome              string           `json:"ScanOutcome"`
	CleanupOutcome           string           `json:"CleanupOutcome"`
	ReconciliationUnresolved bool             `json:"ReconciliationUnresolved"`
}

type identitySnapshot struct {
	Name    string `json:"Name"`
	Version string `json:"Version"`
	Build   string `json:"Build"`
}

type workerSnapshot struct {
	Shard         int               `json:"shard"`
	ID            string            `json:"id"`
	IP            string            `json:"ip"`
	State         string            `json:"state"`
	Detail        string            `json:"detail,omitempty"`
	Cleanup       string            `json:"cleanup"`
	CleanupDetail string            `json:"cleanup_detail,omitempty"`
	Progress      *progressSnapshot `json:"progress"`
	ProgressAt    *time.Time        `json:"progress_at"`
	Found         *uint64           `json:"found"`
}

type progressSnapshot struct {
	Percent        float64       `json:"Percent"`
	Rate           float64       `json:"Rate"`
	ETA            time.Duration `json:"ETA"`
	PercentUnknown bool          `json:"PercentUnknown"`
	RateUnknown    bool          `json:"RateUnknown"`
	ETAUnknown     bool          `json:"ETAUnknown"`
}
