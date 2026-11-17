package digitalocean

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/st3fan/masscanr/internal/cloud"
)

type droplet struct {
	ID       int64    `json:"id"`
	Name     string   `json:"name"`
	Status   string   `json:"status"`
	Tags     []string `json:"tags"`
	Networks struct {
		V4 []struct {
			IP   string `json:"ip_address"`
			Type string `json:"type"`
		} `json:"v4"`
	} `json:"networks"`
}

func (d droplet) worker(key *cloud.SSHKey) cloud.Worker {
	w := cloud.Worker{ID: strconv.FormatInt(d.ID, 10), Key: key, Platform: "fedora44", Label: d.Name, Tags: d.Tags}
	for _, n := range d.Networks.V4 {
		if n.Type != "public" {
			continue
		}
		ip, err := netip.ParseAddr(n.IP)
		if err == nil && ip.Is4() && !ip.IsUnspecified() {
			w.IP = n.IP
			break
		}
	}
	w.Ready = d.Status == "active" && w.IP != ""
	return w
}

func workerName(runID string, slot int) string {
	// Droplet names may not contain UUID underscores; run IDs are hex/UUID.
	return fmt.Sprintf("masscanr-%s-%d", strings.ReplaceAll(runID, "-", ""), slot)
}

func owned(w cloud.Worker, tag string) bool {
	return slices.Contains(w.Tags, cloud.Label) && slices.Contains(w.Tags, tag) && strings.HasPrefix(w.Label, "masscanr-")
}

func (c *Client) Validate(ctx context.Context, spec cloud.Spec) error {
	c.mu.Lock()
	c.ready = false
	c.validated = cloud.Spec{}
	c.mu.Unlock()
	if _, err := c.derive(spec.RunID); err != nil {
		return err
	}
	if !validSlug(spec.Region) || !validSlug(spec.InstanceType) {
		return fmt.Errorf("invalid DigitalOcean region or size")
	}
	// A run-tagged listing checks the same authentication and permission needed
	// for cleanup before creating a billable Droplet.
	if _, err := c.List(ctx, spec.RunID); err != nil {
		return fmt.Errorf("DigitalOcean authentication: %w", err)
	}
	regions, err := c.Regions(ctx)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(regions, func(r cloud.Region) bool { return r.ID == spec.Region }) {
		return fmt.Errorf("DigitalOcean region unavailable")
	}
	sizes, err := c.InstanceTypes(ctx, spec.Region)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(sizes, func(t cloud.InstanceType) bool { return t.ID == spec.InstanceType }) {
		return fmt.Errorf("DigitalOcean size unavailable in region")
	}
	ok, err := c.imageAvailable(ctx, spec.Region)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("Fedora 44 image unavailable in DigitalOcean region")
	}
	c.mu.Lock()
	c.ready = true
	c.validated = cloud.Spec{RunID: spec.RunID, Region: spec.Region, InstanceType: spec.InstanceType}
	c.mu.Unlock()
	return nil
}

func (c *Client) Create(ctx context.Context, spec cloud.Spec) (cloud.Worker, error) {
	tag, err := runTag(spec.RunID)
	if err != nil {
		return cloud.Worker{}, err
	}
	c.mu.Lock()
	ready := c.ready
	validated := c.validated
	c.mu.Unlock()
	if !ready || validated.RunID != spec.RunID || validated.Region != spec.Region || validated.InstanceType != spec.InstanceType {
		return cloud.Worker{}, fmt.Errorf("provider must be validated before creation")
	}
	key, private, err := c.ensureKey(ctx, spec.RunID)
	if err != nil {
		return cloud.Worker{}, err
	}
	name := workerName(spec.RunID, spec.Shard)
	// A restarted workflow can reach Create after a prior POST succeeded but
	// before its worker identity was persisted. Reuse that exact owned slot.
	prior, err := c.List(ctx, spec.RunID)
	if err != nil {
		return cloud.Worker{}, err
	}
	var existing []cloud.Worker
	for _, w := range prior {
		if w.Label == name {
			existing = append(existing, w)
		}
	}
	if len(existing) > 1 {
		return cloud.Worker{}, fmt.Errorf("multiple owned Droplets for run slot")
	}
	if len(existing) == 1 {
		return existing[0], nil
	}
	request := struct {
		Name    string   `json:"name"`
		Region  string   `json:"region"`
		Size    string   `json:"size"`
		Image   string   `json:"image"`
		SSHKeys []int64  `json:"ssh_keys"`
		Tags    []string `json:"tags"`
		Backups bool     `json:"backups"`
	}{name, spec.Region, spec.InstanceType, fedoraImage, []int64{key.ID}, []string{cloud.Label, tag}, false}
	var response struct {
		Droplet droplet `json:"droplet"`
	}
	err = c.request(ctx, "POST", "/droplets", request, &response)
	if err != nil || response.Droplet.ID == 0 {
		// An ambiguous create can have succeeded. Reconcile by unique run+slot
		// name and never send another POST for this slot.
		found, listErr := c.List(ctx, spec.RunID)
		if listErr != nil {
			return cloud.Worker{}, fmt.Errorf("DigitalOcean create ambiguous; reconcile run %s: %w", spec.RunID, listErr)
		}
		var matches []cloud.Worker
		for _, w := range found {
			if w.Label == name {
				matches = append(matches, w)
			}
		}
		if len(matches) == 1 {
			return matches[0], nil
		}
		if err != nil {
			return cloud.Worker{}, fmt.Errorf("DigitalOcean create for run %s slot %d failed; found %d matching Droplets: %w", spec.RunID, spec.Shard, len(matches), err)
		}
		return cloud.Worker{}, fmt.Errorf("DigitalOcean create ambiguous for run %s slot %d; found %d matching Droplets", spec.RunID, spec.Shard, len(matches))
	}
	w := response.Droplet.worker(private)
	if !owned(w, tag) || w.Label != name {
		return cloud.Worker{}, fmt.Errorf("DigitalOcean create response lacks worker ownership")
	}
	return w, nil
}

func (c *Client) Get(ctx context.Context, id string) (cloud.Worker, error) {
	if _, err := strconv.ParseInt(id, 10, 64); err != nil {
		return cloud.Worker{}, fmt.Errorf("invalid DigitalOcean Droplet ID")
	}
	var response struct {
		Droplet droplet `json:"droplet"`
	}
	if err := c.request(ctx, "GET", "/droplets/"+url.PathEscape(id), nil, &response); err != nil {
		return cloud.Worker{}, err
	}
	if response.Droplet.ID == 0 {
		return cloud.Worker{}, fmt.Errorf("DigitalOcean Droplet response lacks ID")
	}
	var private *cloud.SSHKey
	if c.runID != "" {
		tag, _ := runTag(c.runID)
		w := response.Droplet.worker(nil)
		if owned(w, tag) {
			derived, err := c.derive(c.runID)
			if err != nil {
				return cloud.Worker{}, err
			}
			private = derived.private
		}
	}
	return response.Droplet.worker(private), nil
}

func (c *Client) List(ctx context.Context, runID string) ([]cloud.Worker, error) {
	tag, err := runTag(runID)
	if err != nil {
		return nil, err
	}
	derived, err := c.derive(runID)
	if err != nil {
		return nil, err
	}
	path := "/droplets?tag_name=" + url.QueryEscape(tag) + "&per_page=200"
	var out []cloud.Worker
	for page := 0; page < 100; page++ {
		var response struct {
			Droplets []droplet `json:"droplets"`
			pages
		}
		if err := c.request(ctx, "GET", path, nil, &response); err != nil {
			return nil, err
		}
		for _, d := range response.Droplets {
			w := d.worker(derived.private)
			if owned(w, tag) {
				out = append(out, w)
			}
		}
		path, err = c.nextPath(response.Links.Pages.Next)
		if err != nil {
			return nil, err
		}
		if path == "" {
			return out, nil
		}
	}
	return nil, fmt.Errorf("DigitalOcean Droplet pagination limit exceeded")
}

func (c *Client) Delete(ctx context.Context, worker cloud.Worker, runID string) error {
	tag, err := runTag(runID)
	if err != nil {
		return err
	}
	if worker.ID == "" {
		return fmt.Errorf("refusing deletion with empty Droplet ID")
	}
	current, err := c.Get(ctx, worker.ID)
	if errors.Is(err, cloud.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if current.ID != worker.ID || !owned(current, tag) {
		return fmt.Errorf("refusing deletion of unowned Droplet %s", worker.ID)
	}
	if err := c.request(ctx, "DELETE", "/droplets/"+url.PathEscape(worker.ID), nil, nil); err != nil && !errors.Is(err, cloud.ErrNotFound) {
		return err
	}
	for {
		_, err = c.Get(ctx, worker.ID)
		if errors.Is(err, cloud.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := pause(ctx, time.Second); err != nil {
			return fmt.Errorf("Droplet deletion unconfirmed for %s: %w", worker.ID, err)
		}
	}
}
