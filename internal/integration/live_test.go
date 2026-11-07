package integration

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/st3fan/masscanr/internal/cloud"
	"github.com/st3fan/masscanr/internal/cloud/vultr"
	"github.com/st3fan/masscanr/internal/remote"
	"github.com/st3fan/masscanr/internal/scan"
)

// This opt-in test creates one temporary VM and exercises provider/SSH setup.
// It does not invoke the masscanr app or send probes: masscan only runs --echo.
func TestVultrWorkerSmoke(t *testing.T) {
	if os.Getenv("MASSCANR_LIVE_TEST") != "1" {
		t.Skip("explicitly opt in with MASSCANR_LIVE_TEST=1 and VULTR_API_KEY")
	}
	key := os.Getenv("VULTR_API_KEY")
	if key == "" {
		t.Fatal("VULTR_API_KEY is required")
	}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		t.Fatal(err)
	}
	runID := hex.EncodeToString(id)
	p := vultr.New(key)
	spec := cloud.Spec{RunID: runID, Shard: 1, Region: scan.DefaultRegion, InstanceType: scan.DefaultInstanceType}
	ctx, stop := signal.NotifyContext(t.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 12*time.Minute)
	defer cancel()
	if err := p.Validate(ctx, spec); err != nil {
		t.Fatal(err)
	}
	known := make(map[string]cloud.Worker)
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Minute)
		defer stop()
		var listErr error
		for pass := 0; pass < 3; pass++ {
			workers, err := p.List(cleanup, runID)
			listErr = err
			for _, w := range workers {
				known[w.ID] = w
			}
			for id, w := range known {
				if err := p.Delete(cleanup, w, runID); err == nil {
					delete(known, id)
					t.Log("deleted smoke worker", id)
				} else {
					t.Log("cleanup retry for", id, err)
				}
			}
			if pass < 2 {
				select {
				case <-cleanup.Done():
				case <-time.After(2 * time.Second):
				}
			}
		}
		if len(known) > 0 || listErr != nil {
			t.Errorf("manual cleanup required for run %s; remaining IDs=%v; reconciliation error=%v", runID, workerIDs(known), listErr)
		}
	}()
	createCtx, stopCreate := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
	w, err := p.Create(createCtx, spec)
	stopCreate()
	if w.ID != "" {
		known[w.ID] = w
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Log("created smoke worker", w.ID)
	for {
		current, err := p.Get(ctx, w.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.ID != w.ID || current.Label != cloud.Label || !slices.Contains(current.Tags, "masscanr-run-"+runID) {
			t.Fatal("smoke worker ownership changed before SSH preparation")
		}
		if current.Password == "" {
			current.Password = w.Password
		}
		if current.Ready && current.Password != "" {
			w = current
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}
	runner := remote.NewSSH()
	prepared, err := runner.Prepare(ctx, w)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("root/password SSH ready;", prepared.String())
	cfg := scan.Defaults()
	cfg.Targets, _ = scan.ParseTargets([]string{"192.0.2.0/30"})
	args, _ := cfg.Args(1, 42)
	args = append(args, "--echo")
	var output bytes.Buffer
	if err := runner.Scan(ctx, w, args, &output, nil); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(output.Bytes(), []byte("seed = 42")) {
		t.Fatal(fmt.Errorf("unexpected masscan configuration output"))
	}
}

func workerIDs(workers map[string]cloud.Worker) []string {
	var ids []string
	for id := range workers {
		ids = append(ids, id)
	}
	return ids
}
