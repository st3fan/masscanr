package service

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

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
