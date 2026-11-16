package service

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/st3fan/masscanr/internal/scanfile"
)

func resultFinding(timestamp uint64) scanfile.Finding {
	return scanfile.Finding{Shard: 1, IP: netip.MustParseAddr("192.0.2.1"), Port: 80, Protocol: "tcp", State: "open", Timestamp: timestamp, Target: "192.0.2.1/32"}
}

func TestResultSortOrderAndCursor(t *testing.T) {
	store := testStore(t)
	run := reserveFixture(t, store, "masscan")
	ctx := context.Background()
	for _, row := range []struct {
		ip   string
		port int
	}{{"192.0.2.9", 80}, {"192.0.2.1", 82}, {"192.0.2.5", 81}} {
		_, err := store.Pool.Exec(ctx, `INSERT INTO masscanr.scan_results(run_id,target_id,ip,port,protocol,first_seen,last_seen,observations) VALUES($1,$2,$3::inet,$4,'tcp',1,1,1)`, run.ID, run.TargetID, row.ip, row.port)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		sort string
		want []string
	}{
		{"ip", []string{"192.0.2.1", "192.0.2.5", "192.0.2.9"}},
		{"-ip", []string{"192.0.2.9", "192.0.2.5", "192.0.2.1"}},
		{"port", []string{"192.0.2.9", "192.0.2.5", "192.0.2.1"}},
		{"-port", []string{"192.0.2.1", "192.0.2.5", "192.0.2.9"}},
	} {
		cursor := ""
		for _, want := range tc.want {
			page, err := store.ListScanResults(ctx, ResultFilter{RunID: run.ID, Sort: tc.sort, After: cursor, Limit: 1})
			if err != nil || len(page) != 1 || page[0].IP.String() != want {
				t.Fatalf("sort %s after %q: %+v, %v", tc.sort, cursor, page, err)
			}
			cursor = page[0].ID
		}
	}
}

func TestLiveResultsDeduplicateAndExposeCoverage(t *testing.T) {
	store := testStore(t)
	run := reserveFixture(t, store, "masscan")
	ctx := context.Background()
	sink := store.ResultSink(run.ID, run.TargetID)
	first, last := resultFinding(1), resultFinding(math.MaxUint64)
	if err := sink.WriteBatch(ctx, 10, []scanfile.Finding{first, last}); err != nil {
		t.Fatal(err)
	}
	if err := sink.WriteBatch(ctx, 10, []scanfile.Finding{first, last}); err != nil {
		t.Fatal(err)
	}
	if err := sink.WriteBatch(ctx, 20, []scanfile.Finding{resultFinding(7)}); err != nil {
		t.Fatal(err)
	}
	results, err := store.ListScanResults(ctx, ResultFilter{RunID: run.ID, TargetID: run.TargetID, IP: first.IP, Port: &first.Port, Limit: 100})
	if err != nil || len(results) != 1 || results[0].Observations != 3 || results[0].FirstSeen != 1 || results[0].LastSeen != math.MaxUint64 || results[0].Coverage != "live" {
		t.Fatalf("results = %+v, %v", results, err)
	}
	coverage, err := store.GetResultCoverage(ctx, run.ID)
	if err != nil || coverage.State != "live" || coverage.Checkpoint != 20 {
		t.Fatal(coverage, err)
	}
	app := (&API{Store: store}).Handler()
	response := request(app, "GET", "/v1/scan-results?filter[run_id]="+run.ID+"&filter[port]=80", "")
	var listed []ScanResult
	if response.Code != 200 || decodeTestList(response.Body.Bytes(), &listed) != nil || len(listed) != 1 || listed[0].Observations != 3 || !strings.Contains(response.Body.String(), `"coverage"`) {
		t.Fatalf("query: %d %s", response.Code, response.Body)
	}
	if _, err := store.Pool.Exec(ctx, `UPDATE masscanr.runs SET active=false WHERE id=$1`, run.ID); err != nil {
		t.Fatal(err)
	}
	second, err := store.ReserveScanPlan(ctx, run.ScanPlanID, run.Plan.ID, "second-result-run", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ResultSink(second.ID, second.TargetID).WriteBatch(ctx, 10, []scanfile.Finding{first}); err != nil {
		t.Fatal(err)
	}
	page := request(app, "GET", "/v1/scan-results?filter[target_id]="+run.TargetID+"&limit=1", "")
	var pageDoc struct {
		Links map[string]string `json:"links"`
		Data  []json.RawMessage `json:"data"`
	}
	if page.Code != 200 || json.Unmarshal(page.Body.Bytes(), &pageDoc) != nil || len(pageDoc.Data) != 1 || pageDoc.Links["next"] == "" {
		t.Fatal(page.Code, page.Body)
	}
	next := request(app, "GET", pageDoc.Links["next"], "")
	var nextResults []ScanResult
	var firstResults []ScanResult
	if err := decodeTestList(page.Body.Bytes(), &firstResults); err != nil {
		t.Fatal(err)
	}
	if next.Code != 200 || decodeTestList(next.Body.Bytes(), &nextResults) != nil || len(nextResults) != 1 || len(firstResults) != 1 || nextResults[0].RunID == firstResults[0].RunID {
		t.Fatal(next.Code, next.Body)
	}
	if response = request(app, "GET", "/v1/scan-results?filter[ip]=2001:db8::1", ""); response.Code != 400 {
		t.Fatal(response.Code, response.Body)
	}
	if response = request(app, "GET", "/v1/scan-results?filter[run_id]=missing", ""); response.Code != 404 {
		t.Fatal(response.Code, response.Body)
	}
	for _, sortKey := range []string{"ip", "-ip", "port", "-port"} {
		w := request(app, "GET", "/v1/scan-results?sort="+sortKey+"&limit=1", "")
		var sorted struct {
			Links map[string]string `json:"links"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &sorted) != nil || sorted.Links["next"] == "" {
			t.Fatal(sortKey, w.Code, w.Body)
		}
		w = request(app, "GET", sorted.Links["next"], "")
		var next []ScanResult
		if w.Code != 200 || decodeTestList(w.Body.Bytes(), &next) != nil || len(next) != 1 {
			t.Fatal(sortKey, w.Code, w.Body)
		}
	}
	if w := request(app, "GET", "/v1/scan-results?sort=unknown", ""); w.Code != 400 {
		t.Fatal(w.Code, w.Body)
	}
}

func journalForRun(t *testing.T, run Run) []byte {
	t.Helper()
	cfg := run.Plan.Config
	var out bytes.Buffer
	timeouts := map[string]string{"create": "1s", "ready": "1s", "prepare": "1s", "scan": "1s", "cleanup": "1s", "delete": "1s", "poll": "1s"}
	header := scanfile.Header{Format: scanfile.Format, Schema: scanfile.Schema, RunID: run.ID, CreatedAt: time.Now().UTC(), ControllerVersion: "fixture", Label: "masscanr", OwnershipTag: "masscanr-run-" + run.ID, Config: scanfile.Config{RequestedTargets: cfg.RequestedTargets, Targets: cfg.Targets, Ports: cfg.PortSpec(), Backend: cfg.Backend, Seed: "7", Shards: cfg.Workers, Rate: cfg.Rate, Provider: "vultr", Region: cfg.Region, InstanceType: cfg.InstanceType, Timeouts: timeouts, Output: run.ID + ".scan"}}
	writer, err := scanfile.NewWriter(&out, header, nil)
	if err != nil {
		t.Fatal(err)
	}
	step := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	step(writer.Worker(1, "", "", "provisioning", "", ""))
	step(writer.Worker(1, "worker", "", "booting", "", ""))
	step(writer.Worker(1, "worker", "", "preparing", "", ""))
	step(writer.Append(scanfile.Record{Type: "backend", Backend: &scanfile.Backend{Shard: 1, Name: cfg.Backend, Version: "fixture", Build: "fixture"}}, true))
	step(writer.Worker(1, "worker", "", "ready", "", ""))
	step(writer.Worker(1, "worker", "", "scanning", "", ""))
	first, second := resultFinding(3), resultFinding(8)
	step(writer.Append(scanfile.Record{Type: "finding", Finding: &first}, false))
	step(writer.Append(scanfile.Record{Type: "finding", Finding: &second}, false))
	step(writer.Worker(1, "worker", "", "collecting", "", ""))
	step(writer.Worker(1, "worker", "", "completed", "", ""))
	step(writer.Worker(1, "worker", "", "", "deleting", ""))
	step(writer.Worker(1, "worker", "", "", "complete", ""))
	step(writer.Finish("complete", false, ""))
	step(writer.Close())
	return out.Bytes()
}

func TestResultRebuildIsAtomicAndEvidenceAware(t *testing.T) {
	store := testStore(t)
	run := reserveFixture(t, store, "masscan")
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), run.ID+".scan")
	valid := journalForRun(t, run)
	if err := os.WriteFile(path, valid, 0600); err != nil {
		t.Fatal(err)
	}
	if err := store.RebuildRunResults(ctx, run.ID, path); err != nil {
		t.Fatal(err)
	}
	results, err := store.ListScanResults(ctx, ResultFilter{RunID: run.ID, Limit: 100})
	if err != nil || len(results) != 1 || results[0].Observations != 2 || results[0].FirstSeen != 3 || results[0].LastSeen != 8 || results[0].Coverage != "complete" {
		t.Fatal(results, err)
	}
	if err := os.WriteFile(path, append(valid, []byte(`{"type":"invalid"}`+"\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	if err := store.RebuildRunResults(ctx, run.ID, path); err == nil {
		t.Fatal("accepted corrupt journal")
	}
	results, err = store.ListScanResults(ctx, ResultFilter{RunID: run.ID, Limit: 100})
	if err != nil || len(results) != 1 || results[0].Observations != 2 || results[0].Coverage != "error" {
		t.Fatal(results, err)
	}
}

func TestPartialRebuildAndMissingEvidence(t *testing.T) {
	store := testStore(t)
	run := reserveFixture(t, store, "masscan")
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), run.ID+".scan")
	valid := journalForRun(t, run)
	lastRecord := bytes.LastIndex(valid[:len(valid)-1], []byte("\n")) + 1
	if err := os.WriteFile(path, valid[:lastRecord], 0600); err != nil {
		t.Fatal(err)
	}
	if err := store.RebuildRunResults(ctx, run.ID, path); err != nil {
		t.Fatal(err)
	}
	results, err := store.ListScanResults(ctx, ResultFilter{RunID: run.ID, Limit: 100})
	if err != nil || len(results) != 1 || results[0].Coverage != "partial" || results[0].Observations != 2 {
		t.Fatal(results, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := store.FinalizeRunResults(ctx, run.ID, path); err == nil {
		t.Fatal("missing evidence appeared valid")
	}
	coverage, err := store.GetResultCoverage(ctx, run.ID)
	if err != nil || coverage.State != "error" {
		t.Fatal(coverage, err)
	}
}

func BenchmarkPostgresResultBatch(b *testing.B) {
	store := testStore(b)
	ctx := context.Background()
	target, err := store.CreateTarget(ctx, "Benchmark network", []string{"192.0.2.1"})
	if err != nil {
		b.Fatal(err)
	}
	plan, err := store.CreateScanPlan(ctx, "Benchmark plan", target.ID, DefaultScanPlanSettings())
	if err != nil {
		b.Fatal(err)
	}
	runID := newID()
	if _, err := store.Pool.Exec(ctx, `INSERT INTO masscanr.runs(id,target_id,scan_plan_id,request_key,data) VALUES($1,$2,$3,$4,'{}')`, runID, target.ID, plan.ID, newID()); err != nil {
		b.Fatal(err)
	}
	batch := make([]scanfile.Finding, 128)
	for i := range batch {
		batch[i] = resultFinding(uint64(i))
	}
	sink := store.ResultSink(runID, target.ID)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := sink.WriteBatch(ctx, uint64(i+1), batch); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(b.N*len(batch))/b.Elapsed().Seconds(), "findings/s")
}
