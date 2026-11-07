package ripe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/st3fan/masscanr/internal/scan"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/ris-prefixes-AS3333.json")
	if err != nil {
		t.Fatal(err)
	}
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, data, "", "  "); err != nil {
		t.Fatal(err)
	}
	return formatted.Bytes()
}

func TestResolveVerifiedResponse(t *testing.T) {
	data := fixture(t)
	var calls atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		q := r.URL.Query()
		if r.Method != "GET" || q.Get("resource") != "AS3333" || q.Get("types") != "o" || q.Get("af") != "v4" || q.Get("noise") != "keep" || q.Get("list_prefixes") != "true" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		w.Write(data)
	}))
	defer s.Close()
	c := &Client{HTTPClient: s.Client(), endpoint: s.URL, now: func() time.Time { return time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC) }}
	targets, _ := scan.ParseRequestedTargets([]string{"192.0.2.1", "as003333"})
	resolved, err := c.Resolve(context.Background(), append(targets, targets[1]))
	if err != nil || calls.Load() != 1 {
		t.Fatal(err, calls.Load())
	}
	if len(resolved[1].Prefixes) != 6 || resolved[1].Resolution.SnapshotAt.Format(time.RFC3339) != "2026-11-07T08:00:00Z" {
		t.Fatal(resolved)
	}
	resolved[2].Prefixes[0] = resolved[0].Prefixes[0]
	if resolved[1].Prefixes[0] == resolved[2].Prefixes[0] {
		t.Fatal("shared mutable prefixes")
	}
	if _, err := c.Resolve(context.Background(), targets[:1]); err != nil || calls.Load() != 1 {
		t.Fatal(err, calls.Load())
	}
}

func TestResolveErrors(t *testing.T) {
	base := string(fixture(t))
	for name, body := range map[string]string{
		"api failure":      strings.Replace(base, `"status": "ok"`, `"status": "error"`, 1),
		"wrong AS":         strings.Replace(base, `"resource": "3333"`, `"resource": "1234"`, 1),
		"missing snapshot": strings.Replace(base, `"query_time": "2026-11-07T08:00:00"`, `"query_time": ""`, 1),
		"wrong version":    strings.Replace(base, `"version": "1.0"`, `"version": "2.0"`, 1),
		"transit":          strings.Replace(base, `"o"`, `"t"`, 1),
		"malformed prefix": strings.Replace(base, `193.0.20.0/23`, `bad-prefix`, 1),
		"bad count":        strings.Replace(base, `"originating": 6`, `"originating": 7`, 1),
		"missing count":    strings.Replace(base, `"originating": 6`, `"unknown": 6`, 1),
		"malformed":        `{`,
		"truncated":        base[:len(base)/2],
		"oversized":        strings.Repeat(" ", MaxResponseBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
			defer s.Close()
			c := &Client{HTTPClient: s.Client(), endpoint: s.URL, now: func() time.Time { return time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC) }}
			targets, _ := scan.ParseRequestedTargets([]string{"192.0.2.1", "AS3333"})
			got, err := c.Resolve(context.Background(), targets)
			if err == nil || got != nil {
				t.Fatal("accepted invalid or partial resolution", err)
			}
		})
	}
}

func TestPrefixMembershipAndEmptyScope(t *testing.T) {
	for _, prefixes := range [][]string{{}, {"2001:db8::/32"}, {"192.0.2.1/24", "192.0.2.0/24", "192.0.2.0/25", "2001:db8::/32"}} {
		var payload response
		if err := json.Unmarshal(fixture(t), &payload); err != nil {
			t.Fatal(err)
		}
		payload.Data.Prefixes.V4.Originating = prefixes
		count := len(prefixes)
		payload.Data.Counts.V4.Originating = &count
		body, _ := json.Marshal(payload)
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(body) }))
		c := &Client{HTTPClient: s.Client(), endpoint: s.URL, now: func() time.Time { return time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC) }}
		targets, _ := scan.ParseRequestedTargets([]string{"AS3333"})
		got, err := c.Resolve(context.Background(), targets)
		s.Close()
		if len(prefixes) < 2 {
			if err == nil || got != nil {
				t.Fatal("accepted empty IPv4 scope")
			}
		} else if err != nil || len(got[0].Prefixes) != 2 {
			t.Fatal(got, err)
		}
	}
}

func TestHTTPFailureAndCancellation(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(429) }))
	defer s.Close()
	c := &Client{HTTPClient: s.Client(), endpoint: s.URL, now: func() time.Time { return time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC) }}
	targets, _ := scan.ParseRequestedTargets([]string{"AS3333"})
	if _, err := c.Resolve(context.Background(), targets); err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Resolve(ctx, targets); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer slow.Close()
	c.endpoint = slow.URL
	ctx, cancel = context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := c.Resolve(ctx, targets); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}
