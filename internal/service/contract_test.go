package service

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/st3fan/masscanr/internal/cloud"
)

type waitingCatalog struct{ started chan struct{} }

func (f waitingCatalog) Regions(ctx context.Context) ([]cloud.Region, error) {
	close(f.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

func (f waitingCatalog) InstanceTypes(context.Context, string) ([]cloud.InstanceType, error) {
	return nil, nil
}

func TestHTTPContractShutdownCancelsHandler(t *testing.T) {
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	app := (&API{Base: base, Catalog: waitingCatalog{started: started}}).Handler()
	done := make(chan *http.Response, 1)
	errors := make(chan error, 1)
	go func() {
		resp, err := app.Test(httptest.NewRequest("GET", "/v1/catalog/regions", nil), fiber.TestConfig{Timeout: 5 * time.Second, FailOnTimeout: true})
		if err != nil {
			errors <- err
			return
		}
		done <- resp
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not start")
	}
	cancel()
	select {
	case resp := <-done:
		defer resp.Body.Close()
		if resp.StatusCode != 502 {
			t.Fatalf("status=%d, want 502", resp.StatusCode)
		}
	case err := <-errors:
		t.Fatal(err)
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not stop after shutdown")
	}
}

func TestHTTPContractRejectsBodyOverJSONLimit(t *testing.T) {
	app := (&API{}).Handler()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = app.Listener(ln) }()
	t.Cleanup(func() { _ = app.Shutdown() })
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(t.Context(), "POST", "http://"+ln.Addr().String()+"/v1/scans", strings.NewReader(strings.Repeat("x", (1<<20)+8192)))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var result struct {
		Error string `json:"error"`
	}
	if resp.StatusCode != 400 || json.NewDecoder(resp.Body).Decode(&result) != nil || result.Error != "invalid JSON body" {
		t.Fatalf("status=%d, result=%+v", resp.StatusCode, result)
	}
}

// These assertions describe the public boundary before the router is changed.
// They intentionally use the same API entry point as the existing service tests.
func TestHTTPContractRoutes(t *testing.T) {
	h := (&API{Catalog: &catalogFixture{}}).Handler()
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/v1/catalog/regions", 200},
		{"HEAD", "/v1/catalog/regions", 200},
		{"POST", "/v1/catalog/regions", 405},
		{"GET", "/v1/catalog/regions/", 404},
		{"GET", "/v1/catalog/REGIONS", 404},
		{"GET", "/v1/catalog/%72egions", 200},
		{"GET", "/v1/catalog/regions%2Fextra", 404},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			w := request(h, tc.method, tc.path, "")
			if w.Code != tc.status {
				t.Fatalf("status=%d, want %d, body=%q", w.Code, tc.status, w.Body)
			}
			if tc.status == 200 && tc.method == http.MethodGet && w.Header().Get("Content-Type") != "application/json" {
				t.Fatalf("content type = %q", w.Header().Get("Content-Type"))
			}
		})
	}
}

func TestHTTPContractInvalidJSON(t *testing.T) {
	h := (&API{}).Handler()
	for name, body := range map[string]string{
		"unknown field": `{"targets":["192.0.2.1"],"unknown":true}`,
		"two objects":   `{"targets":["192.0.2.1"]}{"targets":["192.0.2.2"]}`,
		"oversized":     `{"targets":["192.0.2.1"]}` + strings.Repeat(" ", 1<<20),
	} {
		t.Run(name, func(t *testing.T) {
			w := request(h, "POST", "/v1/scans", body)
			var problem struct {
				Error string `json:"error"`
			}
			if w.Code != 400 || w.Header().Get("Content-Type") != "application/json" || json.Unmarshal(w.Body.Bytes(), &problem) != nil || problem.Error == "" {
				t.Fatalf("status=%d type=%q body=%q", w.Code, w.Header().Get("Content-Type"), w.Body)
			}
		})
	}
}
