package service

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/st3fan/masscanr/internal/scanfile"
)

const artifactRunID = "00000000-0000-4000-8000-000000000001"

func artifactFixture(t *testing.T, finished bool) string {
	t.Helper()
	var out bytes.Buffer
	h := scanfile.Header{Format: scanfile.Format, Schema: scanfile.LegacySchema, RunID: artifactRunID, CreatedAt: time.Now().UTC(), ControllerVersion: "fixture", Label: "masscanr", OwnershipTag: "masscanr-run-" + artifactRunID, Config: scanfile.Config{Targets: []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}, Ports: "80", Backend: "masscan", Seed: "7", Shards: 1, Rate: 1, Provider: "vultr", Region: "atl", InstanceType: "vc2-1c-1gb", Output: artifactRunID + ".scan", Timeouts: map[string]string{"create": "1s", "ready": "1s", "prepare": "1s", "scan": "1s", "cleanup": "1s", "delete": "1s", "poll": "1s"}}}
	w, err := scanfile.NewWriter(&out, h, nil)
	if err != nil {
		t.Fatal(err)
	}
	step := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	step(w.Worker(1, "", "", "provisioning", "", ""))
	step(w.Worker(1, "worker", "", "booting", "", ""))
	step(w.Worker(1, "worker", "", "preparing", "", ""))
	step(w.Append(scanfile.Record{Type: "backend", Backend: &scanfile.Backend{Shard: 1, Name: "masscan", Version: "fixture", Build: "fixture"}}, true))
	step(w.Worker(1, "worker", "", "ready", "", ""))
	step(w.Worker(1, "worker", "", "scanning", "", ""))
	f := scanfile.Finding{Shard: 1, IP: netip.MustParseAddr("192.0.2.1"), Port: 80, Protocol: "tcp", State: "open", Timestamp: 1}
	step(w.Append(scanfile.Record{Type: "finding", Finding: &f}, false))
	if finished {
		step(w.Worker(1, "worker", "", "collecting", "", ""))
		step(w.Worker(1, "worker", "", "completed", "", ""))
		step(w.Worker(1, "worker", "", "", "deleting", ""))
		step(w.Worker(1, "worker", "", "", "complete", ""))
		step(w.Finish("complete", true, "cleanup reconciliation unresolved"))
	}
	step(w.Close())
	return out.String()
}

func artifactHandler(dir string) http.Handler {
	api := artifactAPI{dir: dir, getRun: func(_ context.Context, id string) (Run, error) {
		if id != artifactRunID {
			return Run{}, ErrNotFound
		}
		return Run{ID: id, Artifact: filepath.Join(dir, id+".scan")}, nil
	}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/runs/{id}/inspection", api.inspect)
	mux.HandleFunc("GET /v1/runs/{id}/exports/{format}", api.export)
	return mux
}

func TestArtifactInspectionAndDownloads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, artifactRunID+".scan")
	if err := os.WriteFile(path, []byte(artifactFixture(t, true)), 0600); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"csv": "ip,port\n192.0.2.1,80\n", "jsonl": "{\"ip\":\"192.0.2.1\"}\n"} {
		if err := os.WriteFile(filepath.Join(dir, artifactRunID+"."+name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	h := artifactHandler(dir)
	w := request(h, "GET", "/v1/runs/"+artifactRunID+"/inspection", "")
	var v inspection
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil || w.Code != 200 || !v.Complete || v.AcceptedFindings != 1 || v.CleanupOutcome != "incomplete" || len(v.Workers) != 1 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("inspection: status=%d body=%s error=%v", w.Code, w.Body, err)
	}
	for format, content := range map[string]string{"csv": "ip,port\n192.0.2.1,80\n", "jsonl": "{\"ip\":\"192.0.2.1\"}\n"} {
		w = request(h, "GET", "/v1/runs/"+artifactRunID+"/exports/"+format, "")
		if w.Code != 200 || w.Body.String() != content || !strings.Contains(w.Header().Get("Content-Disposition"), artifactRunID+"."+format) || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: status=%d body=%s headers=%v", format, w.Code, w.Body, w.Header())
		}
	}
	if err := os.Remove(filepath.Join(dir, artifactRunID+".csv")); err != nil {
		t.Fatal(err)
	}
	if w := request(h, "GET", "/v1/runs/"+artifactRunID+"/exports/csv", ""); w.Code != 409 || !strings.Contains(w.Body.String(), "saved export missing") {
		t.Fatal(w.Code, w.Body)
	}
}

func TestArtifactPartialCorruptAndPathSafety(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, artifactRunID+".scan")
	partial := artifactFixture(t, false) + `{"type":`
	if err := os.WriteFile(path, []byte(partial), 0600); err != nil {
		t.Fatal(err)
	}
	h := artifactHandler(dir)
	w := request(h, "GET", "/v1/runs/"+artifactRunID+"/inspection", "")
	var v inspection
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil || w.Code != 200 || v.Complete || !v.Truncated || v.ScanOutcome != "unconfirmed" || v.AcceptedFindings != 1 {
		t.Fatalf("partial inspection: status=%d body=%s error=%v", w.Code, w.Body, err)
	}
	if w := request(h, "GET", "/v1/runs/"+artifactRunID+"/exports/csv", ""); w.Code != 409 || !strings.Contains(w.Body.String(), "incomplete") {
		t.Fatal(w.Code, w.Body)
	}
	if w := request(h, "GET", "/v1/runs/"+artifactRunID+"/exports/list", ""); w.Code != 400 {
		t.Fatal(w.Code, w.Body)
	}
	if w := request(h, "GET", "/v1/runs/missing/inspection", ""); w.Code != 404 {
		t.Fatal(w.Code, w.Body)
	}
	if err := os.WriteFile(path, []byte("corrupt\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if w := request(h, "GET", "/v1/runs/"+artifactRunID+"/inspection", ""); w.Code != 500 || strings.Contains(w.Body.String(), "corrupt\n") {
		t.Fatal(w.Code, w.Body)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "secret"), path); err != nil {
		t.Fatal(err)
	}
	if w := request(h, "GET", "/v1/runs/"+artifactRunID+"/inspection", ""); w.Code != 500 {
		t.Fatal(w.Code, w.Body)
	}
}
