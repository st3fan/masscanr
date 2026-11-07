// Package ripe resolves AS targets through RIPEstat without sending scan traffic.
package ripe

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/st3fan/masscanr/internal/scan"
)

const (
	LookupTimeout    = 20 * time.Second
	MaxResponseBytes = 4 << 20
)

type Client struct {
	HTTPClient *http.Client
	endpoint   string
	now        func() time.Time
}

func New() *Client {
	return &Client{HTTPClient: &http.Client{
		Timeout: LookupTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return fmt.Errorf("RIPE redirects are not supported")
		},
	}}
}

// Resolve returns an owned, complete mapping or an error. No caller-visible
// partial scope survives failure. Each canonical ASN is queried only once.
func (c *Client) Resolve(ctx context.Context, targets []scan.Target) ([]scan.Target, error) {
	if len(targets) == 0 || len(targets) > scan.MaxRequestedTargets {
		return nil, fmt.Errorf("invalid requested target count")
	}
	result := make([]scan.Target, len(targets))
	cache := make(map[string]scan.Target)
	total := 0
	for i, t := range targets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		parsed, err := scan.ParseRequestedTargets([]string{t.Input})
		if err != nil || parsed[0].Target != t.Target {
			return nil, fmt.Errorf("invalid requested target identity")
		}
		t = parsed[0]
		if t.IsAS() {
			if prior, ok := cache[t.Target]; ok {
				t.Prefixes = slices.Clone(prior.Prefixes)
				r := *prior.Resolution
				t.Resolution = &r
			} else {
				t, err = c.lookup(ctx, t)
				if err != nil {
					return nil, fmt.Errorf("resolve %s: %w", t.Target, err)
				}
				cache[t.Target] = t
			}
		}
		total += len(t.Prefixes)
		if total > scan.MaxResolvedPrefixes {
			return nil, fmt.Errorf("resolved scope exceeds %d prefix memberships", scan.MaxResolvedPrefixes)
		}
		result[i] = t
	}
	return result, nil
}

type response struct {
	Status       string `json:"status"`
	StatusCode   int    `json:"status_code"`
	DataCallName string `json:"data_call_name"`
	Version      string `json:"version"`
	Data         struct {
		Resource     string   `json:"resource"`
		QueryTime    string   `json:"query_time"`
		ListPrefixes bool     `json:"list_prefixes"`
		Types        []string `json:"types"`
		AF           []string `json:"af"`
		Counts       struct {
			V4 struct {
				Originating *int `json:"originating"`
			} `json:"v4"`
		} `json:"counts"`
		Prefixes struct {
			V4 struct {
				Originating []string `json:"originating"`
			} `json:"v4"`
		} `json:"prefixes"`
	} `json:"data"`
}

func (c *Client) lookup(ctx context.Context, t scan.Target) (scan.Target, error) {
	ctx, cancel := context.WithTimeout(ctx, LookupTimeout)
	defer cancel()
	endpoint := c.endpoint
	if endpoint == "" {
		endpoint = scan.RIPEEndpoint
	}
	query := url.Values{"resource": {t.Target}, "list_prefixes": {"true"}, "types": {"o"}, "af": {"v4"}, "noise": {"keep"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return t, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "masscanr/8")
	client := c.HTTPClient
	if client == nil {
		client = New().HTTPClient
	}
	res, err := client.Do(req)
	if err != nil {
		return t, fmt.Errorf("RIPE request failed: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return t, fmt.Errorf("RIPE HTTP status %d", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, MaxResponseBytes+1))
	if err != nil {
		return t, fmt.Errorf("read RIPE response: %w", err)
	}
	if len(body) > MaxResponseBytes {
		return t, fmt.Errorf("RIPE response exceeds %d bytes", MaxResponseBytes)
	}
	var payload response
	if err := json.Unmarshal(body, &payload); err != nil {
		return t, fmt.Errorf("invalid RIPE JSON response: %w", err)
	}
	d := payload.Data
	resource := d.Resource
	if !strings.HasPrefix(strings.ToUpper(resource), "AS") {
		resource = "AS" + resource
	}
	identity, parseErr := scan.ParseRequestedTargets([]string{resource})
	if payload.Status != "ok" || payload.StatusCode != 200 || payload.DataCallName != "ris-prefixes" || payload.Version != "1.0" || parseErr != nil || identity[0].Target != t.Target || !d.ListPrefixes || !slices.Equal(d.Types, []string{"o"}) || !slices.Contains(d.AF, "v4") {
		return t, fmt.Errorf("invalid RIPE status, resource, or origin-prefix response schema")
	}
	prefixes := d.Prefixes.V4.Originating
	count := d.Counts.V4.Originating
	if count == nil || *count != len(prefixes) {
		return t, fmt.Errorf("RIPE IPv4 origin-prefix count disagrees with response")
	}
	if len(prefixes) > scan.MaxResolvedPrefixes {
		return t, fmt.Errorf("RIPE response exceeds %d prefixes", scan.MaxResolvedPrefixes)
	}
	snapshot, err := time.Parse(time.RFC3339Nano, d.QueryTime)
	if err != nil {
		// RIPE's documented response uses an offset-free UTC snapshot string.
		snapshot, err = time.Parse("2006-01-02T15:04:05", d.QueryTime)
	}
	if err != nil {
		return t, fmt.Errorf("invalid RIPE snapshot time")
	}
	fetched := time.Now().UTC()
	if c.now != nil {
		fetched = c.now().UTC()
	}
	if snapshot.IsZero() || snapshot.After(fetched) {
		return t, fmt.Errorf("RIPE snapshot time is missing or in the future")
	}
	for _, value := range prefixes {
		p, err := netip.ParsePrefix(value)
		if err != nil {
			return t, fmt.Errorf("RIPE response contains an invalid prefix")
		}
		if p.Addr().Is4() {
			t.Prefixes = append(t.Prefixes, p.Masked())
		}
	}
	t.Prefixes = scan.SortPrefixes(t.Prefixes)
	if len(t.Prefixes) == 0 {
		return t, fmt.Errorf("RIPE returned no IPv4 origin prefixes")
	}
	t.Resolution = &scan.Resolution{Source: scan.RIPEEndpoint, FetchedAt: fetched, SnapshotAt: snapshot.UTC()}
	return t, nil
}
