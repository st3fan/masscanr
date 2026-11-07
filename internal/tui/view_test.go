package tui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/st3fan/masscanr/internal/controller"
	"github.com/st3fan/masscanr/internal/remote"
)

func TestRedirectedLogsAndFinalState(t *testing.T) {
	var out bytes.Buffer
	v := New(&out, false, 2)
	v.Start()
	defer v.Stop()
	v.Update(controller.Event{Shard: 1, State: "scanning", Progress: &remote.Progress{Percent: 50, Rate: 500000}})
	v.Update(controller.Event{Shard: 1, State: "scanning", Progress: &remote.Progress{Percent: 90}})
	v.Update(controller.Event{Shard: 1, State: "failed", Detail: "remote\x1b[31m\nerror"})
	v.Update(controller.Event{Shard: 1, State: "shutdown"})
	if strings.Contains(out.String(), "\x1b") || strings.Count(out.String(), "scanning") != 1 {
		t.Fatal(out.String())
	}
	if r := v.rows["1"]; r.State != "failed" || r.Cleanup != "shutdown" || r.Progress.Percent != 90 {
		t.Fatal(r)
	}
}

func TestCompletedWorkerShowsShutdownWhilePeerScans(t *testing.T) {
	var out bytes.Buffer
	v := New(&out, false, 2)
	v.Start()
	defer v.Stop()
	v.Update(controller.Event{Shard: 1, State: "waiting", Progress: &remote.Progress{Percent: 100, Found: 12}})
	v.Update(controller.Event{Shard: 1, State: "completed"})
	v.Update(controller.Event{Shard: 1, State: "deleting"})
	v.Update(controller.Event{Shard: 1, State: "shutdown"})
	v.Update(controller.Event{Shard: 1, State: "shutdown"})
	v.Update(controller.Event{Shard: 2, State: "scanning", Progress: &remote.Progress{Percent: 25, Found: 3}})
	if v.rows["1"].Cleanup != "shutdown" || v.rows["2"].State != "scanning" {
		t.Fatal(v.rows)
	}
	if v.rows["1"].Progress.Percent != 100 || v.rows["1"].Progress.Found != 12 || v.rows["2"].Progress.Percent != 25 {
		t.Fatal(v.rows)
	}
	if strings.Count(out.String(), "shutdown") != 1 {
		t.Fatal("duplicate shutdown log", out.String())
	}
}

func TestShutdownClearsResolvedCleanupErrorAndKeepsScanFailure(t *testing.T) {
	var out bytes.Buffer
	v := New(&out, false, 1)
	v.Update(controller.Event{Shard: 1, State: "failed", Detail: "scan error"})
	v.Update(controller.Event{Shard: 1, State: "cleanup failed", Detail: "temporary deletion error"})
	if r := v.rows["1"]; r.Detail != "scan error" || r.CleanupDetail != "temporary deletion error" {
		t.Fatal(r)
	}
	v.Update(controller.Event{Shard: 1, State: "deleting"})
	if v.rows["1"].CleanupDetail != "temporary deletion error" {
		t.Fatal("cleared unconfirmed cleanup failure")
	}
	v.Update(controller.Event{Shard: 1, State: "shutdown"})
	if r := v.rows["1"]; r.State != "failed" || r.Cleanup != "shutdown" || r.Detail != "scan error" || r.CleanupDetail != "" {
		t.Fatal(r)
	}
}
