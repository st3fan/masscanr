package service

import (
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/st3fan/masscanr/internal/controller"
	"github.com/st3fan/masscanr/internal/scan"
)

// Target is a reusable, revisioned set of authorized network selectors.
type Target struct {
	ID         string        `json:"id"`
	Name       string        `json:"name"`
	Revision   int64         `json:"revision"`
	CreatedAt  time.Time     `json:"created_at"`
	UpdatedAt  time.Time     `json:"updated_at"`
	ArchivedAt *time.Time    `json:"archived_at,omitempty"`
	Entries    []TargetEntry `json:"entries"`
}

// TargetEntry order is significant for overlapping selector attribution.
type TargetEntry struct {
	ID       string `json:"id"`
	Position int    `json:"position"`
	Kind     string `json:"kind"` // ip, cidr, or as
	Selector string `json:"selector"`
	Input    string `json:"input"`
}

// NormalizeTargetEntries keeps original input while rejecting duplicate scope.
func NormalizeTargetEntries(inputs []string) ([]TargetEntry, error) {
	if len(inputs) == 0 || len(inputs) > scan.MaxRequestedTargets {
		return nil, fmt.Errorf("target requires 1..%d entries", scan.MaxRequestedTargets)
	}
	seen := make(map[string]bool, len(inputs))
	entries := make([]TargetEntry, 0, len(inputs))
	for i, input := range inputs {
		parsed, err := scan.ParseRequestedTargets([]string{input})
		if err != nil {
			return nil, err
		}
		selector := parsed[0].Target
		if seen[selector] {
			return nil, fmt.Errorf("duplicate target selector %q", selector)
		}
		seen[selector] = true
		kind := "ip"
		if parsed[0].IsAS() {
			kind = "as"
		} else if strings.Contains(input, "/") {
			kind = "cidr"
		}
		entries = append(entries, TargetEntry{ID: newID(), Position: i, Kind: kind, Selector: selector, Input: input})
	}
	return entries, nil
}

func (t Target) Inputs() []string {
	inputs := make([]string, len(t.Entries))
	for i, entry := range t.Entries {
		inputs[i] = entry.Input
	}
	return inputs
}

// ScanPlanSettings contains the reusable execution choices, separate from scope.
type ScanPlanSettings struct {
	Backend        string `json:"backend"`
	Ports          string `json:"ports"`
	Provider       string `json:"provider"`
	Workers        int    `json:"workers"`
	Rate           uint64 `json:"rate"`
	Region         string `json:"region"`
	InstanceType   string `json:"instance_type"`
	Timeout        string `json:"timeout"`
	CleanupTimeout string `json:"cleanup_timeout"`
}

func DefaultScanPlanSettings() ScanPlanSettings {
	d := Defaults()
	return ScanPlanSettings{Backend: d.Backend, Ports: d.Ports, Provider: "vultr", Workers: d.Workers, Rate: d.Rate, Region: d.Region, InstanceType: d.InstanceType, Timeout: d.Timeout, CleanupTimeout: d.CleanupTimeout}
}

func (s ScanPlanSettings) Config(targetInputs []string) (scan.Config, controller.Timeouts, error) {
	if s.Provider != "vultr" && s.Provider != "digitalocean" {
		return scan.Config{}, controller.Timeouts{}, fmt.Errorf("unsupported provider %q", s.Provider)
	}
	d := Definition{Targets: targetInputs, Backend: s.Backend, Ports: s.Ports, Workers: s.Workers, Rate: s.Rate, Region: s.Region, InstanceType: s.InstanceType, Timeout: s.Timeout, CleanupTimeout: s.CleanupTimeout}
	c, timeouts, err := d.Config()
	c.Provider = s.Provider
	return c, timeouts, err
}

type ScanPlan struct {
	ID        string           `json:"id"`
	TargetID  string           `json:"target_id"`
	Name      string           `json:"name"`
	Revision  int64            `json:"revision"`
	CreatedAt time.Time        `json:"created_at"`
	UpdatedAt time.Time        `json:"updated_at"`
	Settings  ScanPlanSettings `json:"settings"`
}

type ScanResult struct {
	ID           string     `json:"id"`
	RunID        string     `json:"run_id"`
	TargetID     string     `json:"target_id"`
	IP           netip.Addr `json:"ip"`
	Port         uint16     `json:"port"`
	Protocol     string     `json:"protocol"`
	FirstSeen    uint64     `json:"first_seen"`
	LastSeen     uint64     `json:"last_seen"`
	Observations uint64     `json:"observations"`
	Selector     string     `json:"selector,omitempty"`
	OriginPrefix string     `json:"origin_prefix,omitempty"`
	Coverage     string     `json:"coverage"`
}
