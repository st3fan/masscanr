package vultr

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/st3fan/masscanr/internal/cloud"
)

func publicCatalog(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("Authorization") != "" {
			t.Error("catalog must use anonymous GET requests", r.Method)
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	client := New("")
	client.BaseURL = server.URL
	client.HTTP = server.Client()
	return client
}

func TestRegionsPaginationAndNames(t *testing.T) {
	calls := 0
	client := publicCatalog(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/regions" || r.URL.Query().Get("per_page") != "100" {
			t.Error(r.URL)
		}
		if calls == 1 {
			fmt.Fprint(w, `{"regions":[{"id":"ewr","city":"New Jersey","country":"US","continent":"North America"}],"meta":{"links":{"next":"bmV4dF9f+/=="}}}`)
		} else {
			if r.URL.Query().Get("cursor") != "bmV4dF9f+/==" {
				t.Error(r.URL)
			}
			fmt.Fprint(w, `{"regions":[{"id":"atl","city":"Atlanta","country":"US","continent":"North America"}]}`)
		}
	})
	regions, err := client.Regions(t.Context())
	if err != nil || calls != 2 || len(regions) != 2 {
		t.Fatal(regions, err, calls)
	}
	if regions[0].ID != "atl" || regions[0].Name != "Atlanta" || regions[0].Country != "US" || regions[1].ID != "ewr" {
		t.Fatal(regions)
	}
}

func TestInstanceTypesUseAllFamiliesAndRegionAvailability(t *testing.T) {
	pages := 0
	client := publicCatalog(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/regions/atl/availability":
			if r.URL.Query().Get("type") != "" {
				t.Error("unexpected plan-family restriction")
			}
			fmt.Fprint(w, `{"available_plans":["vc2-1c-1gb","vhp-1c-1gb-amd","vx1-g-2c-8g"]}`)
		case "/plans":
			pages++
			if pages == 1 {
				fmt.Fprint(w, `{"plans":[{"id":"vc2-1c-1gb","type":"vc2","vcpu_count":1,"ram":1024,"disk":25,"disk_count":1,"storage_type":"local","disk_type":"ssd"},{"id":"unavailable","type":"vc2","vcpu_count":2}],"meta":{"links":{"next":"plans-next+/=="}}}`)
			} else {
				if r.URL.Query().Get("cursor") != "plans-next+/==" {
					t.Error(r.URL)
				}
				fmt.Fprint(w, `{"plans":[{"id":"vx1-g-2c-8g","type":"vx1","vcpu_count":2,"ram":8192,"disk":0,"storage_type":"block","vcpu_type":"dedicated"},{"id":"vhp-1c-1gb-amd","type":"vhp","vcpu_count":1,"ram":1024,"disk":25,"disk_count":1,"disk_type":"nvme","cpu_vendor":"amd","vcpu_type":"shared"}]}`)
			}
		default:
			t.Error("unexpected endpoint", r.URL)
			http.NotFound(w, r)
		}
	})
	plans, err := client.InstanceTypes(t.Context(), "atl")
	if err != nil || len(plans) != 3 || pages != 2 {
		t.Fatal(plans, err, pages)
	}
	if plans[0].ID != "vc2-1c-1gb" || plans[1].Family != "vhp" || plans[1].CPUVendor != "amd" || plans[2].StorageType != "block" || plans[2].VCPUs != 2 {
		t.Fatal(plans)
	}
}

func TestCatalogRejectsInvalidRegionAndPropagatesFailures(t *testing.T) {
	client := publicCatalog(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/regions/missing/availability" {
			t.Error("unexpected request", r.URL)
		}
		http.NotFound(w, r)
	})
	for _, id := range []string{"", "../atl", "ATL", "atl?type=vc2", "missing"} {
		if _, err := client.InstanceTypes(t.Context(), id); err == nil {
			t.Error("accepted", id)
		} else if id == "missing" && !strings.Contains(err.Error(), "unknown region") {
			t.Error(err)
		}
	}
	client = publicCatalog(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"regions":[],"meta":{"links":{"next":"same-token"}}}`)
	})
	if _, err := client.Regions(t.Context()); err == nil || !strings.Contains(err.Error(), "did not advance") {
		t.Fatal("did not detect repeated pagination cursor", err)
	}
	client = publicCatalog(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(400) })
	if _, err := client.Regions(t.Context()); err == nil {
		t.Fatal("ignored API failure")
	}
}

func TestScanValidationAcceptsAvailableNonVC2Family(t *testing.T) {
	client := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/instances":
			fmt.Fprint(w, `{"instances":[]}`)
		case "/regions/atl/availability":
			if r.URL.Query().Get("type") != "" {
				t.Error("restricted scan availability to one family")
			}
			fmt.Fprint(w, `{"available_plans":["vhp-1c-1gb-amd"]}`)
		case "/os":
			fmt.Fprint(w, `{"os":[{"id":999,"name":"Debian 13 x64 (trixie)","arch":"x64","family":"debian"}]}`)
		case "/plans":
			fmt.Fprint(w, `{"plans":[{"id":"vhp-1c-1gb-amd","type":"vhp","disk":25}]}`)
		default:
			t.Error(r.URL)
			http.NotFound(w, r)
		}
	})
	if err := client.Validate(context.Background(), cloud.Spec{RunID: testRun, Region: "atl", InstanceType: "vhp-1c-1gb-amd"}); err != nil {
		t.Fatal(err)
	}
}

func TestPaginatedPreflightRejectsBlockStorageBootBeforeCreation(t *testing.T) {
	pages := 0
	client := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/instances":
			if r.Method != "GET" {
				t.Fatal("attempted provisioning")
			}
			fmt.Fprint(w, `{"instances":[]}`)
		case "/regions/ewr/availability":
			fmt.Fprint(w, `{"available_plans":["vx1-g-2c-8g"]}`)
		case "/plans":
			pages++
			if pages == 1 {
				fmt.Fprint(w, `{"plans":[{"id":"vc2-1c-1gb","disk":25}],"meta":{"links":{"next":"second-page+/=="}}}`)
			} else {
				if r.URL.Query().Get("cursor") != "second-page+/==" {
					t.Error(r.URL)
				}
				fmt.Fprint(w, `{"plans":[{"id":"vx1-g-2c-8g","type":"vx1","disk":0,"storage_type":"block"}]}`)
			}
		default:
			t.Error("should reject unsupported boot before further requests", r.URL)
			http.NotFound(w, r)
		}
	})
	err := client.Validate(t.Context(), cloud.Spec{RunID: testRun, Region: "ewr", InstanceType: "vx1-g-2c-8g"})
	if err == nil || !strings.Contains(err.Error(), "block-storage boot") || pages != 2 {
		t.Fatal(err)
	}
}
