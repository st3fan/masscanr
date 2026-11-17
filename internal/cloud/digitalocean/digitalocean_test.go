package digitalocean

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/st3fan/masscanr/internal/cloud"
	"golang.org/x/crypto/ssh"
)

const testRun = "11111111-2222-3333-4444-555555555555"

type fixture struct {
	mu              sync.Mutex
	key             accountKey
	droplets        map[int64]droplet
	gpuDroplets     map[int64]droplet
	posts           int
	deletes         int
	keyDeletes      int
	token           string
	createAmbiguous bool
	createDenied    bool
	omitSize        bool
	omitImage       bool
	paginate        bool
	gpuSize         bool
}

func (f *fixture) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+f.token {
		w.WriteHeader(401)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	path := strings.TrimPrefix(r.URL.Path, "/v2")
	switch {
	case r.Method == "GET" && strings.HasPrefix(path, "/account/keys/"):
		if f.key.ID == 0 {
			w.WriteHeader(404)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"ssh_key": f.key})
	case r.Method == "POST" && path == "/account/keys":
		f.posts++
		var in struct {
			Name      string `json:"name"`
			PublicKey string `json:"public_key"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		f.key = accountKey{ID: 17, Name: in.Name, PublicKey: in.PublicKey}
		// Test stores the fingerprint supplied by deterministic derivation.
		pub, err := parsePublic(in.PublicKey)
		if err != nil {
			w.WriteHeader(400)
			return
		}
		f.key.Fingerprint = pub
		json.NewEncoder(w).Encode(map[string]any{"ssh_key": f.key})
	case r.Method == "DELETE" && strings.HasPrefix(path, "/account/keys/"):
		f.keyDeletes++
		f.key = accountKey{}
		w.WriteHeader(204)
	case r.Method == "GET" && path == "/regions":
		json.NewEncoder(w).Encode(map[string]any{"regions": []map[string]any{{"slug": "tor1", "name": "Toronto", "available": true}}})
	case r.Method == "GET" && path == "/sizes":
		if f.omitSize {
			json.NewEncoder(w).Encode(map[string]any{"sizes": []any{}})
			return
		}
		sizes := []map[string]any{{"slug": "s-1vcpu-1gb", "vcpus": 1, "memory": 1024, "disk": 25, "available": true, "regions": []string{"tor1"}}}
		if f.gpuSize {
			sizes = append(sizes, map[string]any{"slug": "gpu-test", "vcpus": 8, "memory": 32768, "disk": 100, "available": true, "regions": []string{"tor1"}, "gpu_info": map[string]any{"count": 1}})
		}
		json.NewEncoder(w).Encode(map[string]any{"sizes": sizes})
	case r.Method == "GET" && path == "/images":
		if f.omitImage {
			json.NewEncoder(w).Encode(map[string]any{"images": []any{}})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"images": []map[string]any{{"slug": fedoraImage, "status": "available", "regions": []string{"tor1"}}}})
	case r.Method == "GET" && path == "/droplets":
		if f.paginate && r.URL.Query().Get("page") == "" {
			json.NewEncoder(w).Encode(map[string]any{"droplets": []any{}, "links": map[string]any{"pages": map[string]any{"next": "http://" + r.Host + "/v2/droplets?page=2"}}})
			return
		}
		var ds []droplet
		selected := f.droplets
		if r.URL.Query().Get("type") == "gpus" {
			selected = f.gpuDroplets
		}
		for _, d := range selected {
			ds = append(ds, d)
		}
		json.NewEncoder(w).Encode(map[string]any{"droplets": ds})
	case r.Method == "POST" && path == "/droplets":
		f.posts++
		if f.createDenied {
			w.WriteHeader(403)
			return
		}
		var in struct {
			Name    string   `json:"name"`
			Tags    []string `json:"tags"`
			SSHKeys []int64  `json:"ssh_keys"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		if len(in.SSHKeys) != 1 || in.SSHKeys[0] != 17 {
			w.WriteHeader(400)
			return
		}
		d := droplet{ID: 42, Name: in.Name, Status: "active", Tags: in.Tags}
		d.Networks.V4 = append(d.Networks.V4, struct {
			IP   string `json:"ip_address"`
			Type string `json:"type"`
		}{"203.0.113.42", "public"})
		f.droplets[42] = d
		if f.createAmbiguous {
			w.WriteHeader(500)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"droplet": d})
	case r.Method == "GET" && strings.HasPrefix(path, "/droplets/"):
		var id int64
		fmt.Sscan(strings.TrimPrefix(path, "/droplets/"), &id)
		d, ok := f.droplets[id]
		if !ok {
			w.WriteHeader(404)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"droplet": d})
	case r.Method == "DELETE" && strings.HasPrefix(path, "/droplets/"):
		f.deletes++
		var id int64
		fmt.Sscan(strings.TrimPrefix(path, "/droplets/"), &id)
		delete(f.droplets, id)
		w.WriteHeader(204)
	default:
		w.WriteHeader(404)
	}
}

func parsePublic(encoded string) (string, error) {
	key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(encoded))
	if err != nil {
		return "", err
	}
	return ssh.FingerprintLegacyMD5(key), nil
}

func newFixture(t *testing.T) (*Client, *fixture, func()) {
	t.Helper()
	f := &fixture{droplets: map[int64]droplet{}, gpuDroplets: map[int64]droplet{}, token: "fixture-token"}
	s := httptest.NewServer(http.HandlerFunc(f.handler))
	c := New(f.token, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)), testRun)
	c.BaseURL = s.URL + "/v2"
	return c, f, s.Close
}

func TestRunLifecycleAndRecovery(t *testing.T) {
	c, f, done := newFixture(t)
	defer done()
	ctx := context.Background()
	spec := cloud.Spec{RunID: testRun, Shard: 1, Region: "tor1", InstanceType: "s-1vcpu-1gb"}
	if err := c.Validate(ctx, spec); err != nil {
		t.Fatal(err)
	}
	w, err := c.Create(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	if !w.Ready || w.Key == nil || w.Platform != "fedora44" {
		t.Fatalf("unexpected worker: %+v", w)
	}
	if err := c.FinalizeRun(ctx, testRun); err == nil {
		t.Fatal("removed key while Droplet existed")
	}
	// Reconstructed client derives the same key and adopts the existing slot.
	restarted := New(f.token, c.seed, testRun)
	restarted.BaseURL = c.BaseURL
	if err := restarted.Validate(ctx, spec); err != nil {
		t.Fatal(err)
	}
	again, err := restarted.Create(ctx, spec)
	if err != nil || again.ID != w.ID {
		t.Fatalf("recovery: %v %+v", err, again)
	}
	if f.posts != 2 {
		t.Fatalf("unexpected POST count %d", f.posts)
	}
	if err := restarted.Delete(ctx, again, testRun); err != nil {
		t.Fatal(err)
	}
	if err := restarted.FinalizeRun(ctx, testRun); err != nil {
		t.Fatal(err)
	}
	if f.deletes != 1 || f.keyDeletes != 1 {
		t.Fatalf("cleanup counts: %d %d", f.deletes, f.keyDeletes)
	}
}

func TestRefusesUnownedDroplet(t *testing.T) {
	c, f, done := newFixture(t)
	defer done()
	f.droplets[99] = droplet{ID: 99, Name: "someone-else", Tags: []string{cloud.Label}}
	if err := c.Delete(t.Context(), cloud.Worker{ID: "99"}, testRun); err == nil {
		t.Fatal("deleted unowned Droplet")
	}
	if f.deletes != 0 {
		t.Fatal("issued delete for unowned Droplet")
	}
}

func TestCredentialsRedactedAndPaginationBounded(t *testing.T) {
	c, _, done := newFixture(t)
	defer done()
	if _, err := c.nextPath("https://outside.example/droplets"); err == nil {
		t.Fatal("accepted foreign pagination host")
	}
	c.token = "invalid-secret"
	err := c.Validate(t.Context(), cloud.Spec{RunID: testRun, Region: "tor1", InstanceType: "s-1vcpu-1gb"})
	if err == nil || strings.Contains(err.Error(), c.token) {
		t.Fatalf("credential leak: %v", err)
	}
}

func TestAmbiguousCreateReconcilesWithoutSecondPost(t *testing.T) {
	client, fixture, done := newFixture(t)
	defer done()
	fixture.createAmbiguous = true
	spec := cloud.Spec{RunID: testRun, Shard: 1, Region: "tor1", InstanceType: "s-1vcpu-1gb"}
	if err := client.Validate(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	worker, err := client.Create(t.Context(), spec)
	if err != nil || worker.ID != "42" || fixture.posts != 2 {
		t.Fatalf("create reconciliation: worker=%+v error=%v posts=%d", worker, err, fixture.posts)
	}
}

func TestValidationRejectsMissingSizeOrFedoraImage(t *testing.T) {
	for _, missing := range []string{"size", "image"} {
		t.Run(missing, func(t *testing.T) {
			client, fixture, done := newFixture(t)
			defer done()
			fixture.omitSize = missing == "size"
			fixture.omitImage = missing == "image"
			spec := cloud.Spec{RunID: testRun, Shard: 1, Region: "tor1", InstanceType: "s-1vcpu-1gb"}
			if err := client.Validate(t.Context(), spec); err == nil {
				t.Fatal("accepted unavailable worker image or size")
			}
			if _, err := client.Create(t.Context(), spec); err == nil || fixture.posts != 0 {
				t.Fatalf("created worker after validation failure: %v posts=%d", err, fixture.posts)
			}
		})
	}
}

func TestRunListingPaginatesAndFiltersOwnership(t *testing.T) {
	client, fixture, done := newFixture(t)
	defer done()
	fixture.paginate = true
	fixture.droplets[1] = droplet{ID: 1, Name: workerName(testRun, 1), Tags: []string{cloud.Label, "masscanr-run-" + testRun}}
	fixture.droplets[2] = droplet{ID: 2, Name: "unrelated", Tags: []string{"masscanr-run-" + testRun}}
	workers, err := client.List(t.Context(), testRun)
	if err != nil || len(workers) != 1 || workers[0].ID != "1" {
		t.Fatalf("paginated owned workers: %+v %v", workers, err)
	}
}

func TestGPUCollectionBlocksKeyRetirement(t *testing.T) {
	client, fixture, done := newFixture(t)
	defer done()
	fixture.gpuDroplets[99] = droplet{ID: 99, Name: workerName(testRun, 1), Tags: []string{cloud.Label, "masscanr-run-" + testRun}}
	if err := client.FinalizeRun(t.Context(), testRun); err == nil {
		t.Fatal("retired run key while an owned GPU Droplet remained")
	}
	workers, err := client.List(t.Context(), testRun)
	if err != nil || len(workers) != 1 || workers[0].ID != "99" {
		t.Fatalf("GPU Droplet omitted from owned-worker list: %+v %v", workers, err)
	}
}

func TestGPUSizeExcludedFromCatalog(t *testing.T) {
	client, fixture, done := newFixture(t)
	defer done()
	fixture.gpuSize = true
	sizes, err := client.InstanceTypes(t.Context(), "tor1")
	if err != nil || len(sizes) != 1 || sizes[0].ID != "s-1vcpu-1gb" {
		t.Fatalf("GPU size offered for scanner worker: %+v %v", sizes, err)
	}
}

func TestCreateKeepsProviderStatusWhenReconciliationFindsNothing(t *testing.T) {
	client, fixture, done := newFixture(t)
	defer done()
	fixture.createDenied = true
	spec := cloud.Spec{RunID: testRun, Shard: 1, Region: "tor1", InstanceType: "s-1vcpu-1gb"}
	if err := client.Validate(t.Context(), spec); err != nil {
		t.Fatal(err)
	}
	_, err := client.Create(t.Context(), spec)
	if err == nil || !strings.Contains(err.Error(), "HTTP 403") || fixture.posts != 2 {
		t.Fatalf("lost provider denial: %v posts=%d", err, fixture.posts)
	}
}
