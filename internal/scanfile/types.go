// Package scanfile implements the versioned, append-only masscanr scan document.
package scanfile

import (
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"time"

	"github.com/st3fan/masscanr/internal/scan"
)

const (
	Format         = "masscanr.scan"
	Schema         = 1
	MaxRecordBytes = 4 << 20
	MaxShards      = 65535
)

type Config struct {
	Targets      []netip.Prefix    `json:"targets"`
	Ports        string            `json:"ports"`
	Backend      string            `json:"backend"`
	Seed         string            `json:"seed"`
	Shards       int               `json:"shards"`
	Rate         uint64            `json:"rate_per_worker"`
	Provider     string            `json:"provider"`
	Region       string            `json:"region"`
	InstanceType string            `json:"instance_type"`
	Timeouts     map[string]string `json:"timeouts"`
	Output       string            `json:"output"`
	Exports      [2]string         `json:"exports"`
}

func (c Config) Validate() error {
	ports, err := scan.ParsePorts(c.Ports)
	if err != nil {
		return err
	}
	cfg := scan.Config{Backend: c.Backend, Targets: c.Targets, Ports: ports, Shards: c.Shards, Rate: c.Rate, Region: c.Region, InstanceType: c.InstanceType}
	if err := cfg.Validate(); err != nil {
		return err
	}
	if cfg.PortSpec() != c.Ports || c.Shards > MaxShards || (c.Backend != "masscan" && c.Backend != "zmap") || c.Provider != "vultr" || c.Output == "" {
		return fmt.Errorf("invalid effective scan configuration")
	}
	values := make([]string, len(c.Targets))
	for i, p := range c.Targets {
		values[i] = p.String()
	}
	normalized, err := scan.ParseTargets(values)
	if err != nil || !slices.Equal(normalized, c.Targets) {
		return fmt.Errorf("targets must be normalized and deduplicated")
	}
	seed, err := strconv.ParseUint(c.Seed, 10, 64)
	if err != nil || strconv.FormatUint(seed, 10) != c.Seed || (c.Backend == "zmap" && seed > 1<<63-1) {
		return fmt.Errorf("invalid backend seed")
	}
	for _, key := range []string{"create", "ready", "prepare", "scan", "cleanup", "delete", "poll"} {
		d, err := time.ParseDuration(c.Timeouts[key])
		if err != nil || d <= 0 {
			return fmt.Errorf("invalid %s timeout", key)
		}
	}
	return nil
}

type Header struct {
	Format            string    `json:"format"`
	Schema            int       `json:"schema"`
	RunID             string    `json:"run_id"`
	CreatedAt         time.Time `json:"created_at"`
	ControllerVersion string    `json:"controller_version"`
	Label             string    `json:"label"`
	OwnershipTag      string    `json:"ownership_tag"`
	Config            Config    `json:"config"`
}

type Worker struct {
	Shard      int    `json:"shard"`
	InstanceID string `json:"instance_id,omitempty"`
	IP         string `json:"ip,omitempty"`
	Phase      string `json:"phase"`
	Cleanup    string `json:"cleanup"`
	Detail     string `json:"detail,omitempty"`
}

type Backend struct {
	Shard    int    `json:"shard"`
	Name     string `json:"name"`
	Version  string `json:"version"`
	Build    string `json:"build"`
	Release  string `json:"release,omitempty"`
	Packages string `json:"packages,omitempty"`
}

type Progress struct {
	Shard      int      `json:"shard"`
	Percent    *float64 `json:"percent"`
	Rate       *float64 `json:"rate"`
	ETASeconds *float64 `json:"eta_seconds"`
	Waiting    bool     `json:"waiting"`
}

type Finding struct {
	Target    string     `json:"target,omitempty"`
	Prefix    string     `json:"prefix,omitempty"`
	Shard     int        `json:"shard"`
	IP        netip.Addr `json:"ip"`
	Port      uint16     `json:"port"`
	Protocol  string     `json:"protocol"`
	State     string     `json:"state"`
	Timestamp uint64     `json:"timestamp"`
}

type Outcome struct {
	Shard int    `json:"shard"`
	Phase string `json:"phase"`
	Found uint64 `json:"found"`
}

type Final struct {
	ScanOutcome              string    `json:"scan_outcome"`
	CleanupOutcome           string    `json:"cleanup_outcome"`
	Found                    uint64    `json:"found"`
	Shards                   []Outcome `json:"shards"`
	Undeleted                []string  `json:"undeleted"`
	ReconciliationUnresolved bool      `json:"reconciliation_unresolved"`
	Error                    string    `json:"error,omitempty"`
}

// A record has exactly one payload. Pointer fields distinguish absent payloads
// from zero-valued ones. Integer fields decode directly without float64 conversion.
type Record struct {
	Type     string    `json:"type"`
	Seq      uint64    `json:"seq"`
	At       time.Time `json:"at"`
	Scan     *Header   `json:"scan,omitempty"`
	Backend  *Backend  `json:"backend,omitempty"`
	Worker   *Worker   `json:"worker,omitempty"`
	Progress *Progress `json:"progress,omitempty"`
	Finding  *Finding  `json:"finding,omitempty"`
	Finished *Final    `json:"finished,omitempty"`
}
