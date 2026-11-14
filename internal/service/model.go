// Package service provides persistent scan management and durable execution.
package service

import (
	"fmt"
	"github.com/google/uuid"
	"github.com/st3fan/masscanr/internal/backend/registry"
	"github.com/st3fan/masscanr/internal/controller"
	"github.com/st3fan/masscanr/internal/scan"
	"github.com/st3fan/masscanr/internal/scanfile"
	"net/netip"
	"time"
)

type Definition struct {
	Targets        []string `json:"targets"`
	Backend        string   `json:"backend"`
	Ports          string   `json:"ports"`
	Shards         int      `json:"shards"`
	Rate           uint64   `json:"rate"`
	Region         string   `json:"region"`
	InstanceType   string   `json:"instance_type"`
	Timeout        string   `json:"timeout"`
	CleanupTimeout string   `json:"cleanup_timeout"`
}

func Defaults() Definition {
	c := scan.Defaults()
	return Definition{Backend: c.Backend, Ports: c.PortSpec(), Shards: c.Shards, Rate: c.Rate, Region: c.Region, InstanceType: c.InstanceType, Timeout: "24h", CleanupTimeout: "5m"}
}
func (d Definition) Config() (scan.Config, controller.Timeouts, error) {
	c := scan.Config{Backend: d.Backend, Shards: d.Shards, Rate: d.Rate, Region: d.Region, InstanceType: d.InstanceType}
	t := controller.DefaultTimeouts()
	var err error
	if c.RequestedTargets, err = scan.ParseRequestedTargets(d.Targets); err != nil {
		return c, t, err
	}
	if c.Ports, err = scan.ParsePorts(d.Ports); err != nil {
		return c, t, err
	}
	if err = c.ValidateOptions(); err != nil {
		return c, t, err
	}
	if c.Shards > scanfile.MaxShards {
		return c, t, fmt.Errorf("too many shards")
	}
	if t.Scan, err = time.ParseDuration(d.Timeout); err != nil || t.Scan <= 0 {
		return c, t, fmt.Errorf("invalid timeout")
	}
	if t.Cleanup, err = time.ParseDuration(d.CleanupTimeout); err != nil || t.Cleanup <= 0 {
		return c, t, fmt.Errorf("invalid cleanup_timeout")
	}
	b, err := registry.Resolve(c.Backend)
	if err != nil {
		return c, t, err
	}
	intent := c.Intent()
	intent.Targets = []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}
	return c, t, b.Validate(intent, c.Shards)
}

type Scan struct {
	ID         string     `json:"id"`
	Revision   int64      `json:"revision"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	Definition Definition `json:"definition"`
}
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

func newID() string { return uuid.NewString() }
