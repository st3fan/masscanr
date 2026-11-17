//go:build digitalocean_live

package digitalocean

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/st3fan/masscanr/internal/cloud"
)

// These tests are excluded from ordinary go test runs. Each live operation
// also requires its own explicit environment gate. Never run them in CI.
func liveClient(t *testing.T, gate string) (*Client, cloud.Spec) {
	t.Helper()
	if os.Getenv("MASSCANR_DO_LIVE") != "1" || os.Getenv(gate) != "1" {
		t.Skip("DigitalOcean live test requires explicit environment gates")
	}
	token := os.Getenv("DIGITALOCEAN_API_TOKEN")
	if token == "" {
		t.Fatal("DIGITALOCEAN_API_TOKEN is required")
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	runID := hex.EncodeToString(id[:])
	region := os.Getenv("MASSCANR_DO_LIVE_REGION")
	if region == "" {
		region = "tor1"
	}
	size := os.Getenv("MASSCANR_DO_LIVE_SIZE")
	if size == "" {
		size = "s-1vcpu-1gb"
	}
	t.Logf("DigitalOcean test run %s, region %s, size %s", runID, region, size)
	return New(token, os.Getenv("DIGITALOCEAN_SSH_SEED"), runID), cloud.Spec{RunID: runID, Shard: 1, Region: region, InstanceType: size}
}

func TestLiveDigitalOceanCatalog(t *testing.T) {
	c, spec := liveClient(t, "MASSCANR_DO_LIVE_CATALOG")
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	if err := c.Validate(ctx, spec); err != nil {
		t.Fatal(err)
	}
	t.Log("catalog, image, size, and owned-worker listing succeeded")
}

func TestLiveDigitalOceanKeyLifecycle(t *testing.T) {
	c, spec := liveClient(t, "MASSCANR_DO_LIVE_KEYS")
	liveCleanup(t, c, spec.RunID, nil)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	if err := c.Validate(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.ensureKey(ctx, spec.RunID); err != nil {
		t.Fatal(err)
	}
	t.Log("owned SSH key registered; confirming deletion")
	if err := c.FinalizeRun(ctx, spec.RunID); err != nil {
		t.Fatal(err)
	}
	t.Log("owned SSH key created and confirmed deleted")
}

func TestLiveDigitalOceanDropletLifecycle(t *testing.T) {
	c, spec := liveClient(t, "MASSCANR_DO_LIVE_DROPLET")
	possibleCreate := false
	liveCleanup(t, c, spec.RunID, &possibleCreate)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	if err := c.Validate(ctx, spec); err != nil {
		t.Fatal(err)
	}
	possibleCreate = true
	w, err := c.Create(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	if w.ID == "" || w.Key == nil || w.Platform != "fedora44" {
		t.Fatal("created Droplet lacks expected worker contract")
	}
	if err := c.Delete(ctx, w, spec.RunID); err != nil {
		t.Fatal(err)
	}
	possibleCreate = false
	if err := c.FinalizeRun(ctx, spec.RunID); err != nil {
		t.Fatal(err)
	}
	t.Log("owned Droplet and SSH key confirmed deleted; no scan was run")
}

// Cleanup has a fresh context so a test timeout does not strand a test-owned
// Droplet. The provider re-reads exact ownership tags before every deletion.
func liveCleanup(t *testing.T, c *Client, runID string, possibleCreate *bool) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		if possibleCreate != nil && *possibleCreate {
			// A failed POST may still create a Droplet after the first listing.
			// Keep reconciling the unique run tag before retiring its SSH key.
			deadline := time.Now().Add(2 * time.Minute)
			for time.Now().Before(deadline) {
				workers, err := c.List(ctx, runID)
				if err != nil {
					t.Errorf("run %s ambiguous-create listing failed: %v", runID, err)
					return
				}
				for _, w := range workers {
					if err := c.Delete(ctx, w, runID); err != nil {
						t.Errorf("run %s cleanup of Droplet %s failed: %v", runID, w.ID, err)
						return
					}
				}
				if err := pause(ctx, 5*time.Second); err != nil {
					t.Errorf("run %s ambiguous-create reconciliation failed: %v", runID, err)
					return
				}
			}
		}
		workers, err := c.List(ctx, runID)
		if err != nil {
			t.Errorf("run %s cleanup listing failed: %v", runID, err)
			return
		}
		for _, w := range workers {
			if err := c.Delete(ctx, w, runID); err != nil {
				t.Errorf("run %s cleanup of Droplet %s failed: %v", runID, w.ID, err)
			}
		}
		if err := c.FinalizeRun(ctx, runID); err != nil {
			t.Errorf("run %s SSH key cleanup failed: %v", runID, err)
			return
		}
		remaining, err := c.List(ctx, runID)
		if err != nil || len(remaining) != 0 {
			t.Errorf("run %s cleanup unconfirmed: %d workers remain; listing error: %v", runID, len(remaining), err)
		}
	})
}
