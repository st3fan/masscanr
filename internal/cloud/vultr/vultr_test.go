package vultr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/st3fan/masscanr/internal/cloud"
)

const testRun = "0123456789abcdef0123456789abcdef"

func fixture(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Error("missing API authentication")
		}
		handler(w, r)
	}))
	t.Cleanup(s.Close)
	c := New("test-secret")
	c.BaseURL = s.URL
	c.HTTP = s.Client()
	return c
}

func TestValidateAndCreate(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/regions/atl/availability":
			fmt.Fprint(w, `{"available_plans":["vc2-1c-1gb"]}`)
		case "/plans":
			fmt.Fprint(w, `{"plans":[{"id":"vc2-1c-1gb","type":"vc2","disk":25}]}`)
		case "/os":
			fmt.Fprint(w, `{"os":[{"id":999,"name":"Alpine Linux x64","arch":"x64","family":"alpinelinux"}]}`)
		case "/instances":
			if r.Method == "GET" {
				fmt.Fprint(w, `{"instances":[]}`)
				return
			}
			if r.Method != "POST" {
				t.Fatal(r.Method)
			}
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
			}
			if req["label"] != "masscanr" || req["user_scheme"] != "root" || req["os_id"] != float64(999) {
				t.Error(req)
			}
			tags := req["tags"].([]any)
			if tags[0] != "masscanr-run-"+testRun {
				t.Error(tags)
			}
			fmt.Fprint(w, `{"instance":{"id":"new","main_ip":"192.0.2.1","default_password":"never-log-me"}}`)
		default:
			t.Error(r.URL)
			http.NotFound(w, r)
		}
	})
	spec := cloud.Spec{RunID: testRun, Shard: 1, Region: "atl", InstanceType: "vc2-1c-1gb"}
	if err := c.Validate(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	worker, err := c.Create(context.Background(), spec)
	if err != nil || worker.ID != "new" || worker.Password != "never-log-me" {
		t.Fatal(worker.ID, err)
	}
	data, _ := json.Marshal(worker)
	if strings.Contains(string(data), "never-log-me") {
		t.Fatal("password serialized")
	}
}

func TestInvalidCredentialsStopBeforeProvisioning(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/instances" || r.Method != "GET" {
			t.Error("proceeded past credential validation", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusUnauthorized)
	})
	err := c.Validate(context.Background(), cloud.Spec{RunID: testRun})
	if err == nil || !strings.Contains(err.Error(), "authentication") {
		t.Fatal(err)
	}
}

func TestDeleteRequiresFreshRunOwnership(t *testing.T) {
	for _, tc := range []struct {
		name, label, tag string
		allowed          bool
	}{
		{"unrelated", "production", "masscanr-run-" + testRun, false},
		{"another-run", "masscanr", "masscanr-run-ffffffffffffffffffffffffffffffff", false},
		{"only-label", "masscanr", "", false},
		{"owned", "masscanr", "masscanr-run-" + testRun, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deleted := false
			c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "DELETE" {
					deleted = true
					w.WriteHeader(204)
					return
				}
				if deleted {
					http.NotFound(w, r)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"instance": map[string]any{"id": "existing", "main_ip": "144.202.29.87", "label": tc.label, "tags": []string{tc.tag}}})
			})
			// Caller-supplied metadata cannot grant permission to delete.
			worker := cloud.Worker{ID: "existing", Label: "masscanr", Tags: []string{"masscanr-run-" + testRun}}
			err := c.Delete(context.Background(), worker, testRun)
			if deleted != tc.allowed || (err == nil) != tc.allowed {
				t.Fatal(deleted, err)
			}
		})
	}
}

func TestListFiltersAndKeepsPaginationOnTrustedEndpoint(t *testing.T) {
	calls := 0
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("tag") != "masscanr-run-"+testRun {
			t.Error(r.URL)
		}
		if calls == 1 {
			fmt.Fprintf(w, `{"instances":[{"id":"owned","label":"masscanr","tags":["masscanr-run-%s"]},{"id":"unrelated","label":"production","tags":["masscanr-run-%s"]}],"meta":{"links":{"next":"worker-next+/=="}}}`, testRun, testRun)
		} else {
			if r.URL.Query().Get("cursor") != "worker-next+/==" || r.URL.Query().Get("per_page") != "100" {
				t.Error(r.URL)
			}
			fmt.Fprint(w, `{"instances":[],"meta":{"links":{"next":""}}}`)
		}
	})
	workers, err := c.List(context.Background(), testRun)
	if err != nil || len(workers) != 1 || workers[0].ID != "owned" || calls != 2 {
		t.Fatal(workers, err, calls)
	}
}

func TestCreateNeverRetriesAndErrorsDoNotEchoSecrets(t *testing.T) {
	calls := 0
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(503)
		fmt.Fprint(w, "test-secret")
	})
	c.osID = 999
	_, err := c.Create(context.Background(), cloud.Spec{RunID: testRun})
	if err == nil || strings.Contains(err.Error(), "test-secret") || calls != 1 {
		t.Fatal(err, calls)
	}
}

func TestMissingWorkerAndInvalidRun(t *testing.T) {
	c := fixture(t, func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	if err := c.Delete(context.Background(), cloud.Worker{ID: "gone"}, testRun); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(context.Background(), "gone"); !errors.Is(err, cloud.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := c.List(context.Background(), ""); err == nil {
		t.Fatal("accepted missing ownership")
	}
}

func TestAlpineImageSelectionRejectsFallbackAndAmbiguity(t *testing.T) {
	for _, tc := range []struct {
		name, catalog, wantError string
		wantID                   int
	}{
		{"generic-label", `[{"id":3141,"name":"Alpine Linux x64","family":"alpinelinux","arch":"x64"}]`, "", 3141},
		{"debian-only", `[{"id":999,"name":"Debian 13 x64","family":"debian","arch":"x64"}]`, "unavailable", 0},
		{"wrong-architecture", `[{"id":999,"name":"Alpine Linux","family":"alpinelinux","arch":"arm64"}]`, "unavailable", 0},
		{"ambiguous", `[{"id":1,"family":"alpinelinux","arch":"x64"},{"id":2,"family":"alpinelinux","arch":"x64"}]`, "ambiguous", 0},
		{"invalid-ID", `[{"id":0,"family":"alpinelinux","arch":"x64"}]`, "invalid", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Error("provisioned before valid preflight")
					http.Error(w, "unexpected create", 400)
					return
				}
				switch r.URL.Path {
				case "/instances":
					fmt.Fprint(w, `{"instances":[]}`)
				case "/regions/atl/availability":
					fmt.Fprint(w, `{"available_plans":["vc2-1c-1gb"]}`)
				case "/plans":
					fmt.Fprint(w, `{"plans":[{"id":"vc2-1c-1gb","disk":25}]}`)
				case "/os":
					fmt.Fprintf(w, `{"os":%s}`, tc.catalog)
				default:
					t.Error(r.URL)
					http.NotFound(w, r)
				}
			})
			c.osID = 123 // A failed new validation must invalidate old selection.
			spec := cloud.Spec{RunID: testRun, Region: "atl", InstanceType: "vc2-1c-1gb"}
			err := c.Validate(t.Context(), spec)
			if c.osID != tc.wantID || (err != nil) != (tc.wantError != "") || err != nil && !strings.Contains(err.Error(), tc.wantError) {
				t.Fatal(c.osID, err)
			}
			if err != nil {
				if _, createErr := c.Create(t.Context(), spec); createErr == nil {
					t.Fatal("stale OS selection survived failed validation")
				}
			}
		})
	}
}

func TestAlpineImageSelectionReadsAllCatalogPages(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		t.Run(fmt.Sprintf("duplicate=%v", duplicate), func(t *testing.T) {
			calls := 0
			c := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/os" || r.URL.Query().Get("per_page") != "500" {
					t.Error(r.URL)
				}
				if calls == 1 {
					fmt.Fprint(w, `{"os":[{"id":7,"family":"alpinelinux","arch":"x64"}],"meta":{"links":{"next":"os-next+/=="}}}`)
				} else {
					if r.URL.Query().Get("cursor") != "os-next+/==" {
						t.Error(r.URL)
					}
					id := 8
					if duplicate {
						id = 7
					}
					fmt.Fprintf(w, `{"os":[{"id":%d,"family":"alpinelinux","arch":"x64"}]}`, id)
				}
			})
			id, err := c.alpineImage(t.Context())
			if calls != 2 || duplicate && (err != nil || id != 7) || !duplicate && (err == nil || id != 0) {
				t.Fatal(calls, id, err)
			}
		})
	}
}
