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
	req, err := http.NewRequestWithContext(t.Context(), "POST", "http://"+ln.Addr().String()+"/v1/targets", strings.NewReader(strings.Repeat("x", (1<<20)+8192)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", jsonAPIMediaType)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var result struct {
		Errors []jsonAPIError `json:"errors"`
	}
	if resp.StatusCode != 400 || json.NewDecoder(resp.Body).Decode(&result) != nil || len(result.Errors) != 1 || result.Errors[0].Detail != "invalid JSON body" {
		t.Fatalf("status=%d, result=%+v", resp.StatusCode, result)
	}
}

// These assertions describe the public router boundary.
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
			if tc.status == 200 && tc.method == http.MethodGet && w.Header().Get("Content-Type") != jsonAPIMediaType {
				t.Fatalf("content type = %q", w.Header().Get("Content-Type"))
			}
		})
	}
}

func TestHTTPContractInvalidJSON(t *testing.T) {
	h := (&API{}).Handler()
	for name, body := range map[string]string{
		"unknown field": `{"data":{"type":"targets","attributes":{"name":"External Network","entries":["192.0.2.1"],"unknown":true}}}`,
		"two objects":   `{"data":{"type":"targets","attributes":{"name":"External Network","entries":["192.0.2.1"]}}}{"data":{"type":"targets"}}`,
		"oversized":     `{"data":{"type":"targets","attributes":{"name":"External Network","entries":["192.0.2.1"]}}}` + strings.Repeat(" ", 1<<20),
	} {
		t.Run(name, func(t *testing.T) {
			w := request(h, "POST", "/v1/targets", body)
			var problem struct {
				Errors []jsonAPIError `json:"errors"`
			}
			if w.Code != 400 || w.Header().Get("Content-Type") != jsonAPIMediaType || json.Unmarshal(w.Body.Bytes(), &problem) != nil || len(problem.Errors) != 1 || problem.Errors[0].Detail == "" {
				t.Fatalf("status=%d type=%q body=%q", w.Code, w.Header().Get("Content-Type"), w.Body)
			}
		})
	}
}

func TestJSONAPIMediaNegotiation(t *testing.T) {
	h := (&API{Catalog: &catalogFixture{}}).Handler()
	for _, tc := range []struct {
		accept string
		status int
	}{
		{jsonAPIMediaType, 200},
		{"application/json", 406},
		{jsonAPIMediaType + "; charset=utf-8", 406},
		{jsonAPIMediaType + ";q=0", 406},
	} {
		req := httptest.NewRequest("GET", "/v1/catalog/regions", nil)
		req.Header.Set("Accept", tc.accept)
		resp, err := h.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.status || resp.Header.Get("Content-Type") != jsonAPIMediaType {
			t.Fatalf("Accept %q: status=%d type=%q", tc.accept, resp.StatusCode, resp.Header.Get("Content-Type"))
		}
	}
	req := httptest.NewRequest("POST", "/v1/targets", strings.NewReader(`{"data":{"type":"targets","attributes":{}}}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 415 || resp.Header.Get("Content-Type") != jsonAPIMediaType {
		t.Fatalf("wrong content type: status=%d type=%q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
}

func TestJSONAPIResourcePreservesLargeIntegers(t *testing.T) {
	resource, err := makeResource("runs", struct {
		ID   string `json:"id"`
		Seed uint64 `json:"seed"`
	}{ID: "run-id", Seed: 18446744073709551615})
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(resource)
	if err != nil || !strings.Contains(string(b), `"seed":18446744073709551615`) {
		t.Fatalf("large integer changed: %s %v", b, err)
	}
}
