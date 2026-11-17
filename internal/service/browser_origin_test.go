package service

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func TestBrowserOriginBoundary(t *testing.T) {
	app := (&API{BrowserOrigins: []string{"http://127.0.0.1:18080", "https://scanner.internal"}}).Handler()
	for _, tc := range []struct {
		name, host, origin, site, mode string
		forbidden                      bool
	}{
		{"direct browser", "127.0.0.1:18080", "http://127.0.0.1:18080", "same-origin", "cors", false},
		{"private proxy", "127.0.0.1:18080", "https://scanner.internal", "same-origin", "cors", false},
		{"proxy host", "scanner.internal", "https://scanner.internal", "same-origin", "cors", false},
		{"origin fallback", "127.0.0.1:18080", "http://127.0.0.1:18080", "", "", false},
		{"fetch metadata fallback", "127.0.0.1:18080", "", "same-origin", "cors", false},
		{"headerless CLI", "127.0.0.1:18080", "", "", "", false},
		{"headerless unknown host", "evil.example", "", "", "", true},
		{"cross site", "127.0.0.1:18080", "https://evil.example", "cross-site", "cors", true},
		{"same site other origin", "127.0.0.1:18080", "http://127.0.0.1:9999", "same-site", "cors", true},
		{"null origin", "127.0.0.1:18080", "null", "same-origin", "cors", true},
		{"malformed origin", "127.0.0.1:18080", "http://127.0.0.1:18080.evil.example", "same-origin", "cors", true},
		{"forged host", "evil.example", "http://127.0.0.1:18080", "same-origin", "cors", true},
		{"rebinding host", "evil.example", "https://evil.example", "same-origin", "cors", true},
		{"mismatched metadata", "127.0.0.1:18080", "http://127.0.0.1:18080", "cross-site", "cors", true},
		{"missing source", "127.0.0.1:18080", "", "", "cors", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "http://"+tc.host+"/v1/targets", nil)
			req.Host = tc.host
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.site != "" {
				req.Header.Set("Sec-Fetch-Site", tc.site)
			}
			if tc.mode != "" {
				req.Header.Set("Sec-Fetch-Mode", tc.mode)
			}
			resp, err := app.Test(req, fiber.TestConfig{})
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if (resp.StatusCode == 403) != tc.forbidden {
				t.Fatalf("status=%d, forbidden=%t", resp.StatusCode, tc.forbidden)
			}
		})
	}
	read := httptest.NewRequest("GET", "http://evil.example/v1/targets", nil)
	read.Host = "evil.example"
	resp, err := app.Test(read, fiber.TestConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("rebinding read status=%d, want 403", resp.StatusCode)
	}
}

func TestBrowserOriginConfiguration(t *testing.T) {
	for _, tc := range []struct {
		listen, extra string
		want          int
		valid         bool
	}{
		{"127.0.0.1:18080", "", 1, true},
		{"0.0.0.0:18080", "https://scanner.internal", 1, true},
		{"127.0.0.1:18080", "https://scanner.internal", 2, true},
		{"[::]:18080", "", 0, false},
		{"127.0.0.1:18080", "https://scanner.internal/path", 0, false},
		{"127.0.0.1:18080", "null", 0, false},
		{"0.0.0.0:18080", "http://0.0.0.0:18080", 0, false},
	} {
		origins, err := browserOriginsForListener(tc.listen, tc.extra)
		if (err == nil) != tc.valid || tc.valid && len(origins) != tc.want {
			t.Fatalf("listen=%q extra=%q: origins=%v err=%v", tc.listen, tc.extra, origins, err)
		}
	}
}
