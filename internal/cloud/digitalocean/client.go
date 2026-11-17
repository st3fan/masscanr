// Package digitalocean implements temporary Fedora workers through the
// DigitalOcean API. It never deletes a Droplet without fresh ownership proof.
package digitalocean

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/st3fan/masscanr/internal/cloud"
)

const apiURL = "https://api.digitalocean.com/v2"
const maxResponse = 4 << 20

type Client struct {
	HTTP      *http.Client
	BaseURL   string
	token     string
	seed      string
	runID     string
	ready     bool
	validated cloud.Spec
	mu        sync.Mutex
}

func New(token, seed, runID string) *Client {
	return &Client{HTTP: &http.Client{Timeout: 30 * time.Second}, BaseURL: apiURL, token: token, seed: seed, runID: runID}
}

type HTTPError struct {
	Status int
	Path   string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("DigitalOcean %s: HTTP %d", e.Path, e.Status) }

func (c *Client) request(ctx context.Context, method, path string, payload, dest any) error {
	if strings.TrimSpace(c.token) == "" {
		return fmt.Errorf("DIGITALOCEAN_API_TOKEN is required")
	}
	var data []byte
	var err error
	if payload != nil {
		data, err = json.Marshal(payload)
		if err != nil {
			return err
		}
	}
	for attempt := 0; attempt < 4; attempt++ {
		request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, bytes.NewReader(data))
		if err != nil {
			return fmt.Errorf("invalid DigitalOcean API request")
		}
		request.Header.Set("Authorization", "Bearer "+c.token)
		request.Header.Set("Content-Type", "application/json")
		response, err := c.HTTP.Do(request)
		if err != nil {
			return fmt.Errorf("DigitalOcean %s %s request failed", method, request.URL.EscapedPath())
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, maxResponse+1))
		response.Body.Close()
		if readErr != nil || len(body) > maxResponse {
			return fmt.Errorf("DigitalOcean %s response could not be read within limit", request.URL.EscapedPath())
		}
		if method == http.MethodGet && attempt < 3 && (response.StatusCode == 429 || response.StatusCode >= 500) {
			if err := pause(ctx, time.Duration(1<<attempt)*time.Second); err != nil {
				return err
			}
			continue
		}
		if response.StatusCode == http.StatusNotFound {
			return cloud.ErrNotFound
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return &HTTPError{Status: response.StatusCode, Path: request.URL.EscapedPath()}
		}
		if dest != nil && len(body) != 0 {
			if err := json.Unmarshal(body, dest); err != nil {
				return fmt.Errorf("invalid DigitalOcean %s response", request.URL.EscapedPath())
			}
		}
		return nil
	}
	return fmt.Errorf("DigitalOcean request retry limit exceeded")
}

func pause(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// nextPath accepts only pagination links from the configured API origin and
// version path. No bearer token is ever sent to a foreign host.
func (c *Client) nextPath(next string) (string, error) {
	if next == "" {
		return "", nil
	}
	base, err := url.Parse(c.BaseURL)
	if err != nil {
		return "", fmt.Errorf("invalid DigitalOcean API base URL")
	}
	u, err := url.Parse(next)
	if err != nil {
		return "", fmt.Errorf("invalid DigitalOcean pagination link")
	}
	u = base.ResolveReference(u)
	if u.Scheme != base.Scheme || u.Host != base.Host || !strings.HasPrefix(u.Path, strings.TrimRight(base.Path, "/")+"/") || u.User != nil || u.Fragment != "" {
		return "", fmt.Errorf("DigitalOcean pagination link leaves API origin")
	}
	return strings.TrimPrefix(u.Path, strings.TrimRight(base.Path, "/")) + func() string {
		if u.RawQuery == "" {
			return ""
		}
		return "?" + u.RawQuery
	}(), nil
}

func statusIs(err error, code int) bool {
	var apiErr *HTTPError
	return errors.As(err, &apiErr) && apiErr.Status == code
}
