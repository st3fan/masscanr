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
	if text := v.render(); !strings.Contains(text, "failed / shutdown") || !strings.Contains(text, "90.0%") {
		t.Fatal(text)
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
	text := v.render()
	if !strings.Contains(text, "shutdown") || strings.Contains(text, "completed") || !strings.Contains(text, "scanning") {
		t.Fatal(text)
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
	if text := v.render(); !strings.Contains(text, "scan error") || !strings.Contains(text, "temporary deletion error") {
		t.Fatal(text)
	}
	v.Update(controller.Event{Shard: 1, State: "deleting"})
	if !strings.Contains(v.render(), "temporary deletion error") {
		t.Fatal("cleared unconfirmed cleanup failure")
	}
	v.Update(controller.Event{Shard: 1, State: "shutdown"})
	text := v.render()
	if !strings.Contains(text, "failed / shutdown") || !strings.Contains(text, "scan error") || strings.Contains(text, "temporary deletion error") {
		t.Fatal(text)
	}
}
