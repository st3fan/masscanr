// Package watch implements an HTTP-only viewer for service-owned scan runs.
package watch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/st3fan/masscanr/internal/runapi"
	"github.com/st3fan/masscanr/internal/scanfile"
)

const maxResponse = 8 << 20

type Client struct {
	base string
	http *http.Client
}
type clientError struct {
	message string
	retry   bool
}

func (e *clientError) Error() string { return e.message }
func retryable(err error) bool       { var e *clientError; return errors.As(err, &e) && e.retry }

func NewClient(server string) (*Client, error) {
	u, err := url.Parse(server)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return nil, fmt.Errorf("server must be an http(s) URL without credentials, query, or fragment")
	}
	return &Client{base: strings.TrimRight(u.String(), "/"), http: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func validID(id string) bool {
	if len(id) < 1 || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
			return false
		}
	}
	return true
}
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return ' '
	}, s)
}
func (c *Client) request(ctx context.Context, method, path string, status int) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, nil)
	if err != nil {
		return nil, &clientError{message: "invalid API request"}
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// Avoid echoing URLs or remote bodies which could contain sensitive text.
		return nil, &clientError{message: method + " " + path + ": connection failed or timed out", retry: true}
	}
	defer resp.Body.Close()
	if resp.StatusCode != status {
		return nil, &clientError{message: fmt.Sprintf("%s %s: HTTP %d %s", method, path, resp.StatusCode, http.StatusText(resp.StatusCode)), retry: resp.StatusCode == 408 || resp.StatusCode == 429 || resp.StatusCode >= 500}
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return nil, &clientError{message: "reading API response failed", retry: true}
	}
	if len(b) > maxResponse {
		return nil, &clientError{message: "API response exceeds 8 MiB"}
	}
	return b, nil
}
func decodeRun(b []byte) (runapi.Run, error) {
	var r runapi.Run
	var fields map[string]json.RawMessage
	if json.Unmarshal(b, &r) != nil || json.Unmarshal(b, &fields) != nil {
		return r, &clientError{message: "invalid run JSON"}
	}
	for _, key := range []string{"active", "settled", "stop_requested"} {
		var v bool
		if len(fields[key]) == 0 || string(fields[key]) == "null" || json.Unmarshal(fields[key], &v) != nil {
			return r, &clientError{message: "incompatible run snapshot: missing " + key}
		}
	}
	if r.SnapshotVersion != runapi.SnapshotVersion || r.Active == r.Settled || !validID(r.ID) || !validID(r.ScanID) || r.Plan.Config.Shards < 1 || r.Plan.Config.Shards > scanfile.MaxShards || r.Plan.Revision < 1 || r.Phase == "" {
		return r, &clientError{message: "incompatible run snapshot; upgrade the service to V10"}
	}
	return r, nil
}
func (c *Client) Get(ctx context.Context, id string) (runapi.Run, error) {
	return c.runRequest(ctx, http.MethodGet, id, "", 200)
}
func (c *Client) Stop(ctx context.Context, id string) (runapi.Run, error) {
	return c.runRequest(ctx, http.MethodPost, id, "/stop", 202)
}
func (c *Client) runRequest(ctx context.Context, method, id, suffix string, status int) (runapi.Run, error) {
	if !validID(id) {
		return runapi.Run{}, &clientError{message: "invalid run ID"}
	}
	b, err := c.request(ctx, method, "/v1/runs/"+id+suffix, status)
	if err != nil {
		return runapi.Run{}, err
	}
	r, err := decodeRun(b)
	if err == nil && r.ID != id {
		err = &clientError{message: "API returned a different run ID"}
	}
	return r, err
}
func (c *Client) Active(ctx context.Context) ([]runapi.Run, error) {
	b, err := c.request(ctx, http.MethodGet, "/v1/runs?active=true", 200)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		SnapshotVersion int               `json:"snapshot_version"`
		Runs            []json.RawMessage `json:"runs"`
	}
	if json.Unmarshal(b, &envelope) != nil || envelope.SnapshotVersion != runapi.SnapshotVersion || envelope.Runs == nil {
		return nil, &clientError{message: "incompatible active-run response; upgrade the service to V10"}
	}
	runs := make([]runapi.Run, 0, len(envelope.Runs))
	seen := map[string]bool{}
	for _, raw := range envelope.Runs {
		r, err := decodeRun(raw)
		if err != nil {
			return nil, err
		}
		if !r.Active || seen[r.ID] {
			return nil, &clientError{message: "invalid active-run list"}
		}
		seen[r.ID] = true
		runs = append(runs, r)
	}
	return runs, nil
}
