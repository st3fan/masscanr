// Package vultr implements the worker lifecycle using Vultr API v2.
package vultr

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/st3fan/masscanr/internal/cloud"
)

type Client struct {
	HTTP    *http.Client
	BaseURL string
	key     string
	osID    int
}

func New(key string) *Client {
	return &Client{HTTP: &http.Client{Timeout: 30 * time.Second}, BaseURL: "https://api.vultr.com/v2", key: key}
}

type instance struct {
	ID       string   `json:"id"`
	IP       string   `json:"main_ip"`
	Password string   `json:"default_password"`
	Label    string   `json:"label"`
	Tags     []string `json:"tags"`
	Status   string   `json:"status"`
	Power    string   `json:"power_status"`
	Server   string   `json:"server_status"`
}

func (i instance) worker() cloud.Worker {
	ip, err := netip.ParseAddr(i.IP)
	return cloud.Worker{ID: i.ID, IP: i.IP, Password: i.Password, Label: i.Label, Tags: i.Tags,
		Ready: err == nil && ip.Is4() && !ip.IsUnspecified() && i.Status == "active" && i.Power == "running" && i.Server == "ok"}
}

func runTag(runID string) (string, error) {
	b, err := hex.DecodeString(runID)
	if err != nil || len(b) != 16 {
		return "", fmt.Errorf("invalid run identifier")
	}
	return "masscanr-run-" + runID, nil
}

func owned(w cloud.Worker, tag string) bool {
	return w.Label == cloud.Label && slices.Contains(w.Tags, tag)
}

// Validate selects one explicit Debian release and verifies regional plan
// availability before any instance is created. It is called once per run.
func (c *Client) Validate(ctx context.Context, spec cloud.Spec) error {
	if strings.TrimSpace(c.key) == "" {
		return fmt.Errorf("VULTR_API_KEY is required")
	}
	if _, err := runTag(spec.RunID); err != nil {
		return err
	}
	// OS and region endpoints are public: they do not validate the API key.
	// Check an authenticated read before attempting any paid provisioning.
	if err := c.request(ctx, "GET", "/account", nil, nil); err != nil {
		return fmt.Errorf("Vultr authentication: %w", err)
	}
	var availability struct {
		Plans []string `json:"available_plans"`
	}
	if err := c.request(ctx, "GET", "/regions/"+url.PathEscape(spec.Region)+"/availability?type=vc2", nil, &availability); err != nil {
		return err
	}
	if !slices.Contains(availability.Plans, spec.InstanceType) {
		return fmt.Errorf("instance type %s is unavailable in region %s", spec.InstanceType, spec.Region)
	}
	var systems struct {
		OS []struct {
			ID     int    `json:"id"`
			Name   string `json:"name"`
			Arch   string `json:"arch"`
			Family string `json:"family"`
		} `json:"os"`
	}
	if err := c.request(ctx, "GET", "/os?per_page=500", nil, &systems); err != nil {
		return err
	}
	for _, os := range systems.OS {
		if os.Family == "debian" && os.Arch == "x64" && strings.HasPrefix(os.Name, "Debian 13 ") {
			c.osID = os.ID
			return nil
		}
	}
	return fmt.Errorf("Debian 13 x64 image is unavailable")
}

func (c *Client) Create(ctx context.Context, spec cloud.Spec) (cloud.Worker, error) {
	tag, err := runTag(spec.RunID)
	if err != nil {
		return cloud.Worker{}, err
	}
	if c.osID == 0 {
		return cloud.Worker{}, fmt.Errorf("provider must be validated before creation")
	}
	req := struct {
		Region     string   `json:"region"`
		Plan       string   `json:"plan"`
		OS         int      `json:"os_id"`
		Label      string   `json:"label"`
		Hostname   string   `json:"hostname"`
		Tags       []string `json:"tags"`
		Backups    string   `json:"backups"`
		UserScheme string   `json:"user_scheme"`
	}{spec.Region, spec.InstanceType, c.osID, cloud.Label, fmt.Sprintf("masscanr-%d", spec.Shard), []string{tag}, "disabled", "root"}
	var result struct {
		Instance instance `json:"instance"`
	}
	// Never retry a POST: a timeout may mean creation succeeded remotely.
	err = c.request(ctx, "POST", "/instances", req, &result)
	w := result.Instance.worker()
	if err == nil && w.ID == "" {
		err = fmt.Errorf("Vultr create response has no instance ID; reconcile run %s", spec.RunID)
	}
	return w, err
}

func (c *Client) Get(ctx context.Context, id string) (cloud.Worker, error) {
	var result struct {
		Instance instance `json:"instance"`
	}
	err := c.request(ctx, "GET", "/instances/"+url.PathEscape(id), nil, &result)
	return result.Instance.worker(), err
}

func (c *Client) List(ctx context.Context, runID string) ([]cloud.Worker, error) {
	tag, err := runTag(runID)
	if err != nil {
		return nil, err
	}
	path := "/instances?per_page=100&tag=" + url.QueryEscape(tag)
	var workers []cloud.Worker
	for pages := 0; pages < 1000; pages++ {
		var result struct {
			Instances []instance `json:"instances"`
			Meta      struct {
				Links struct {
					Next string `json:"next"`
				} `json:"links"`
			} `json:"meta"`
		}
		if err := c.request(ctx, "GET", path, nil, &result); err != nil {
			return nil, err
		}
		for _, i := range result.Instances {
			w := i.worker()
			if owned(w, tag) {
				workers = append(workers, w)
			}
		}
		if result.Meta.Links.Next == "" {
			return workers, nil
		}
		// Vultr next links contain a cursor. Never send credentials to a URL
		// supplied by the response; retain our endpoint and tag filter.
		next, err := url.Parse(result.Meta.Links.Next)
		if err != nil || next.Query().Get("cursor") == "" {
			return nil, fmt.Errorf("invalid Vultr pagination cursor")
		}
		path = "/instances?per_page=100&tag=" + url.QueryEscape(tag) + "&cursor=" + url.QueryEscape(next.Query().Get("cursor"))
	}
	return nil, fmt.Errorf("Vultr pagination limit exceeded")
}

func (c *Client) Delete(ctx context.Context, worker cloud.Worker, runID string) error {
	tag, err := runTag(runID)
	if err != nil {
		return err
	}
	if worker.ID == "" {
		return fmt.Errorf("refusing deletion with empty instance ID")
	}
	// Fresh verification protects existing VMs and workers owned by other runs.
	current, err := c.Get(ctx, worker.ID)
	if errors.Is(err, cloud.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if current.ID != worker.ID || !owned(current, tag) {
		return fmt.Errorf("refusing deletion of unowned instance %s", worker.ID)
	}
	if err := c.request(ctx, "DELETE", "/instances/"+url.PathEscape(worker.ID), nil, nil); err != nil && !errors.Is(err, cloud.ErrNotFound) {
		return err
	}
	// A DELETE acknowledgement is not proof of deletion. Wait for GET 404.
	for {
		_, err = c.Get(ctx, worker.ID)
		if errors.Is(err, cloud.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if err = wait(ctx, time.Second); err != nil {
			return fmt.Errorf("deletion unconfirmed for %s: %w", worker.ID, err)
		}
	}
}

func wait(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c *Client) request(ctx context.Context, method, path string, payload, dest any) error {
	var data []byte
	var err error
	if payload != nil {
		data, err = json.Marshal(payload)
		if err != nil {
			return err
		}
	}
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bytes.NewReader(data))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+c.key)
		req.Header.Set("Content-Type", "application/json")
		res, err := c.HTTP.Do(req)
		if err != nil {
			return fmt.Errorf("Vultr %s request failed: %w", method, err)
		}
		if method != "POST" && attempt < 3 && (res.StatusCode == 429 || res.StatusCode >= 500) {
			res.Body.Close()
			if err = wait(ctx, time.Duration(1<<attempt)*time.Second); err != nil {
				return err
			}
			continue
		}
		defer res.Body.Close()
		if res.StatusCode == 404 {
			return cloud.ErrNotFound
		}
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			return fmt.Errorf("Vultr %s returned HTTP %d", method, res.StatusCode)
		}
		if dest != nil {
			if err = json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(dest); err != nil {
				return fmt.Errorf("decode Vultr response: %w", err)
			}
		}
		return nil
	}
}
