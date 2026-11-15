package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/st3fan/masscanr/internal/cloud"
)

type catalogFixture struct {
	regions []cloud.Region
	plans   []cloud.InstanceType
	region  string
	err     error
}

func (f *catalogFixture) Regions(context.Context) ([]cloud.Region, error) {
	return f.regions, f.err
}
func (f *catalogFixture) InstanceTypes(_ context.Context, region string) ([]cloud.InstanceType, error) {
	f.region = region
	return f.plans, f.err
}

func TestCatalogAPI(t *testing.T) {
	f := &catalogFixture{regions: []cloud.Region{{ID: "ewr", Name: "New Jersey", Country: "US", Continent: "NA"}, {ID: "atl", Name: "Atlanta", Country: "US", Continent: "NA"}}, plans: []cloud.InstanceType{{ID: "plan-b", VCPUs: 2, MemoryMB: 2048}, {ID: "plan-a", Family: "vc2", VCPUs: 1, MemoryMB: 1024, DiskCount: 1, DiskGB: 25, StorageType: "local", CPUType: "shared", CPUVendor: "intel"}}}
	h := (&API{Catalog: f}).Handler()
	w := request(h, "GET", "/v1/catalog/regions", "")
	var regions []regionResponse
	if err := decodeTestList(w.Body.Bytes(), &regions); err != nil || w.Code != 200 || len(regions) != 2 || regions[0].ID != "atl" || regions[0].Country != "US" {
		t.Fatalf("regions: status=%d body=%s error=%v", w.Code, w.Body, err)
	}
	w = request(h, "GET", "/v1/catalog/instance-types", "")
	var plans []instanceTypeResponse
	if err := decodeTestList(w.Body.Bytes(), &plans); err != nil || w.Code != 200 || f.region != "atl" || len(plans) != 2 || plans[0].ID != "plan-a" || plans[0].MemoryMB != 1024 {
		t.Fatalf("plans: status=%d body=%s error=%v region=%s", w.Code, w.Body, err, f.region)
	}
	w = request(h, "GET", "/v1/catalog/instance-types?region=ewr", "")
	if w.Code != 200 || f.region != "ewr" {
		t.Fatal(w.Code, f.region)
	}
	for _, invalid := range []string{"ATL", "atl%2F..", strings.Repeat("a", 65)} {
		w = request(h, "GET", "/v1/catalog/instance-types?region="+invalid, "")
		if w.Code != 400 {
			t.Fatalf("invalid %q: %d %s", invalid, w.Code, w.Body)
		}
	}
}

func TestCatalogAPIErrorsDoNotLeakProviderDetails(t *testing.T) {
	f := &catalogFixture{err: errors.New("provider token fixture-secret")}
	h := (&API{Catalog: f}).Handler()
	for _, path := range []string{"/v1/catalog/regions", "/v1/catalog/instance-types"} {
		w := request(h, "GET", path, "")
		if w.Code != 502 || strings.Contains(w.Body.String(), "fixture-secret") {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
	}
	f.err = cloud.ErrNotFound
	if w := request(h, "GET", "/v1/catalog/instance-types?region=unknown", ""); w.Code != 404 {
		t.Fatal(w.Code, w.Body)
	}
	if w := request((&API{}).Handler(), "GET", "/v1/catalog/regions", ""); w.Code != 503 {
		t.Fatal(w.Code, w.Body)
	}
}
