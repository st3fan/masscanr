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
	v.Update(controller.Event{Shard: 1, State: "deleted"})
	if strings.Contains(out.String(), "\x1b") || strings.Count(out.String(), "scanning") != 1 {
		t.Fatal(out.String())
	}
	if text := v.render(); !strings.Contains(text, "failed / deleted") || !strings.Contains(text, "90.0%") {
		t.Fatal(text)
	}
}
