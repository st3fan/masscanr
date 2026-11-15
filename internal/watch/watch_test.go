package watch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/st3fan/masscanr/internal/controller"
	"github.com/st3fan/masscanr/internal/runapi"
	"github.com/st3fan/masscanr/internal/scan"
)

type activeRunsFixture struct {
	SnapshotVersion int           `json:"snapshot_version"`
	Runs            []runSnapshot `json:"runs"`
}

func fixtureRun(id string, active bool) runSnapshot {
	return runSnapshot{SnapshotVersion: 1, ID: id, ScanID: "scan", Active: active, Settled: !active, Phase: "scanning", CreatedAt: time.Now(), Plan: planSnapshot{Revision: 1, Config: configSnapshot{Backend: "masscan", Shards: 2, Ports: []uint16{80, 443}}}}
}

func TestServerSnapshotWireCompatibility(t *testing.T) {
	cfg := scan.Defaults()
	cfg.Targets, _ = scan.ParseTargets([]string{"192.0.2.0/24"})
	r := runapi.Run{SnapshotVersion: runapi.SnapshotVersion, Active: true, ID: "run-a", ScanID: "scan-a", Phase: "scanning", Plan: runapi.Plan{Revision: 2, Config: cfg, Timeouts: controller.DefaultTimeouts()}, Summary: controller.Summary{RunID: "run-a", Seed: 17, Found: 3, ScanOutcome: "running"}, WorkerSnapshots: map[string]runapi.Worker{"1": {Shard: 1, ID: "worker-a", Progress: &scan.Progress{PercentUnknown: true}}}}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeRun(b)
	if err != nil || got.Plan.Config.PortSpec() != cfg.PortSpec() || got.Plan.Config.Targets[0] != "192.0.2.0/24" || got.Summary.Seed != 17 || got.WorkerSnapshots["1"].Progress == nil || !got.WorkerSnapshots["1"].Progress.PercentUnknown {
		t.Fatal(got, err)
	}
}
func fixtureClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := NewClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func TestArgumentsAndURLs(t *testing.T) {
	for _, args := range [][]string{{}, {"a", "--all"}, {"a", "b"}, {"a", "--interval", "0s"}, {"../bad"}, {"--all", "--interval", "2m"}} {
		if _, err := parse(args, io.Discard); err == nil {
			t.Fatal(args)
		}
	}
	for _, args := range [][]string{{"a", "--interval", "1s"}, {"--all"}, {"--server", "http://localhost:8080", "a"}} {
		if _, err := parse(args, io.Discard); err != nil {
			t.Fatal(args, err)
		}
	}
	for _, u := range []string{"file:///tmp/a", "http://user:secret@host", "http://host?token=x", "http://host/#x", ":bad"} {
		if _, err := NewClient(u); err == nil {
			t.Fatal(u)
		}
	}
}
func TestClientProtocolValidation(t *testing.T) {
	good := fixtureRun("a", true)
	b, _ := json.Marshal(good)
	for name, body := range map[string]string{"invalid": "{", "oversize": strings.Repeat(" ", maxResponse+1), "old-service": `{"id":"a"}`, "wrong-run": strings.Replace(string(b), `"id":"a"`, `"id":"b"`, 1), "missing-active": strings.Replace(string(b), `"active":true,`, "", 1), "null-active": strings.Replace(string(b), `"active":true`, `"active":null`, 1)} {
		t.Run(name, func(t *testing.T) {
			c := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) })
			if _, err := c.Get(t.Context(), "a"); err == nil {
				t.Fatal("accepted invalid response")
			}
		})
	}
	c := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "http://localhost/secret")
		w.WriteHeader(302)
	})
	if _, err := c.Get(t.Context(), "a"); err == nil || retryable(err) {
		t.Fatal(err)
	}
	c = fixtureClient(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := c.Get(ctx, "a"); err == nil {
		t.Fatal("ignored cancellation")
	}
}
func TestDiscoveryTransitionsAndStopRequest(t *testing.T) {
	var stage atomic.Int32
	var stops atomic.Int32
	a, b := fixtureRun("a", true), fixtureRun("b", true)
	c := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/runs":
			if r.Method != "GET" || r.URL.RawQuery != "active=true" {
				t.Error(r.Method, r.URL)
			}
			runs := []runSnapshot{}
			if stage.Load() == 1 {
				runs = append(runs, a)
			}
			if stage.Load() == 3 {
				runs = append(runs, b)
			}
			if stage.Load() == 4 {
				w.WriteHeader(503)
				return
			}
			json.NewEncoder(w).Encode(activeRunsFixture{SnapshotVersion: 1, Runs: runs})
		case "/v1/runs/a":
			v := a
			v.Active = false
			v.Settled = true
			v.Summary.ScanOutcome = "failed"
			v.Summary.CleanupOutcome = "incomplete"
			json.NewEncoder(w).Encode(v)
		case "/v1/runs/a/stop":
			if r.Method != "POST" {
				t.Error(r.Method)
			}
			stops.Add(1)
			w.WriteHeader(202)
			json.NewEncoder(w).Encode(a)
		default:
			t.Error(r.URL)
			w.WriteHeader(404)
		}
	})
	s, err := poll(t.Context(), c, "", snapshot{})
	if err != nil || len(s.Runs) != 0 {
		t.Fatal(s, err)
	}
	stage.Store(1)
	s, err = poll(t.Context(), c, "", s)
	if err != nil || len(s.Runs) != 1 {
		t.Fatal(s, err)
	}
	again, err := poll(t.Context(), c, "", s)
	if err != nil || len(again.Runs) != 1 {
		t.Fatal(again, err)
	}
	stage.Store(2)
	s, err = poll(t.Context(), c, "", s)
	if err != nil || len(s.Runs) != 0 || s.Last == nil || s.Last.Summary.CleanupOutcome != "incomplete" {
		t.Fatal(s, err)
	}
	stage.Store(3)
	s, err = poll(t.Context(), c, "", s)
	if err != nil || s.Last != nil || s.Runs[0].ID != "b" {
		t.Fatal(s, err)
	}
	stage.Store(4)
	stale, err := poll(t.Context(), c, "", s)
	if !retryable(err) || stale.Runs[0].ID != "b" {
		t.Fatal(stale, err)
	}
	if stops.Load() != 0 {
		t.Fatal("polling mutated service")
	}
	if _, err := c.Stop(t.Context(), "a"); err != nil || stops.Load() != 1 {
		t.Fatal(err, stops.Load())
	}
}

type fakeSource struct{ stopIDs []string }

func (*fakeSource) Get(context.Context, string) (runSnapshot, error) {
	return runSnapshot{}, fmt.Errorf("unexpected get")
}
func (*fakeSource) Active(context.Context) ([]runSnapshot, error) {
	return nil, fmt.Errorf("unexpected discovery")
}
func (s *fakeSource) Stop(_ context.Context, id string) (runSnapshot, error) {
	s.stopIDs = append(s.stopIDs, id)
	return fixtureRun(id, true), nil
}
func key(text string) tea.KeyPressMsg {
	switch text {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	}
	return tea.KeyPressMsg{Code: rune(text[0]), Text: text}
}
func update(m model, msg tea.Msg) (model, tea.Cmd) { v, c := m.Update(msg); return v.(model), c }
func TestDetachAndImmutableConfirmation(t *testing.T) {
	for _, exit := range []string{"q", "ctrl+c"} {
		ctx, cancel := context.WithCancel(t.Context())
		api := &fakeSource{}
		m := newModel(ctx, cancel, api, options{All: true}, snapshot{Runs: []runSnapshot{fixtureRun("a", true)}})
		_, cmd := update(m, key(exit))
		if cmd == nil || ctx.Err() == nil || len(api.stopIDs) > 0 {
			t.Fatal(exit)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	api := &fakeSource{}
	m := newModel(ctx, cancel, api, options{All: true}, snapshot{Runs: []runSnapshot{fixtureRun("a", true)}})
	m, _ = update(m, key("s"))
	m, cmd := update(m, key("enter"))
	if cmd != nil || len(api.stopIDs) > 0 {
		t.Fatal("default was not cancel")
	}
	m, _ = update(m, key("s"))
	m, _ = update(m, key("ctrl+c"))
	if ctx.Err() != nil || m.confirmID != "" {
		t.Fatal("confirmation Ctrl-C did not dismiss")
	}
	m, _ = update(m, key("s"))
	m, _ = update(m, pollMsg{snapshot: snapshot{Runs: []runSnapshot{fixtureRun("b", true)}}})
	if m.selected != "b" || m.confirmID != "a" {
		t.Fatal(m.selected, m.confirmID)
	}
	m, _ = update(m, key("right"))
	m, cmd = update(m, key("enter"))
	if cmd == nil {
		t.Fatal("no stop")
	}
	m, _ = update(m, cmd())
	if len(api.stopIDs) != 1 || api.stopIDs[0] != "a" {
		t.Fatal(api.stopIDs)
	}
}
func TestRenderUnknownNarrowAndPlain(t *testing.T) {
	for _, backend := range []string{"masscan", "zmap"} {
		r := fixtureRun("a", true)
		r.Plan.Config.Backend = backend
		r.Error = "bad\x1b[31m\nrow"
		r.WorkerSnapshots = map[string]workerSnapshot{"1": {Shard: 1, ID: "w", State: "booting"}}
		s := snapshot{Runs: []runSnapshot{r}}
		text := renderPlain("http://server", s, "")
		if strings.Contains(text, "\x1b") || !strings.Contains(text, "Progress unknown") || !strings.Contains(text, backend) {
			t.Fatal(text)
		}
		ctx, cancel := context.WithCancel(t.Context())
		m := newModel(ctx, cancel, &fakeSource{}, options{}, s)
		for _, size := range [][2]int{{1, 1}, {20, 5}, {80, 24}} {
			m.width, m.height = size[0], size[1]
			v := m.View()
			lines := strings.Split(v.Content, "\n")
			if len(lines) > m.height {
				t.Fatal("too tall")
			}
			for _, line := range lines {
				if ansi.StringWidth(line) > m.width {
					t.Fatalf("too wide: %q", line)
				}
			}
		}
		cancel()
	}
}
func TestPlainAllIdleAndDedup(t *testing.T) {
	var gets, mutations atomic.Int32
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	c := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			mutations.Add(1)
		}
		gets.Add(1)
		json.NewEncoder(w).Encode(activeRunsFixture{SnapshotVersion: 1, Runs: []runSnapshot{}})
	})
	s, err := poll(ctx, c, "", snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- plain(ctx, c, options{All: true, Interval: 10 * time.Millisecond}, s, &out) }()
	deadline := time.After(time.Second)
	for gets.Load() < 4 {
		select {
		case <-deadline:
			t.Fatal("did not keep polling idle")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "No active scans") != 1 || mutations.Load() != 0 {
		t.Fatal(out.String(), mutations.Load())
	}
}
func TestTerminalEOFFromFixture(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	err := runTerminal(ctx, cancel, &fakeSource{}, options{Interval: time.Hour}, snapshot{Runs: []runSnapshot{fixtureRun("a", false)}}, strings.NewReader(""), io.Discard, tea.WithoutRenderer(), tea.WithEnvironment([]string{"TERM=dumb"}))
	if err != nil {
		t.Fatal(err)
	}
}

func TestOldServiceExplainsDiscovery404(t *testing.T) {
	c := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	_, err := c.Active(t.Context())
	if err == nil || !strings.Contains(err.Error(), "update and restart masscanr-server") || retryable(err) {
		t.Fatal(err)
	}
}

func TestStopCannotBeConfirmedInUnreadableTerminal(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	api := &fakeSource{}
	m := newModel(ctx, cancel, api, options{}, snapshot{Runs: []runSnapshot{fixtureRun("a", true)}})
	m, _ = update(m, key("s"))
	m.width = 10
	m.height = 2
	m, _ = update(m, key("right"))
	m, cmd := update(m, key("enter"))
	if cmd != nil || m.confirmYes || len(api.stopIDs) != 0 {
		t.Fatal("confirmed without readable scope")
	}
	m, _ = update(m, key("esc"))
	if m.confirmID != "" {
		t.Fatal("cannot cancel")
	}
}

func TestStaleIdleDoesNotClaimCurrentInactivity(t *testing.T) {
	text := renderPlain("http://server", snapshot{}, "STALE: connection lost")
	if strings.Contains(text, "No active scans — waiting") || !strings.Contains(text, "current activity unknown") {
		t.Fatal(text)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	m := newModel(ctx, cancel, &fakeSource{}, options{All: true}, snapshot{})
	m.stale = "STALE: connection lost"
	if text := strings.Join(m.body(), " "); !strings.Contains(text, "current activity unknown") {
		t.Fatal(text)
	}
}
