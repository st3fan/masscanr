package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/st3fan/masscanr/internal/cloud"
	"github.com/st3fan/masscanr/internal/controller"
)

type fakeCatalog struct {
	regions func(context.Context) ([]cloud.Region, error)
	types   func(context.Context, string) ([]cloud.InstanceType, error)
}

func (c fakeCatalog) Regions(ctx context.Context) ([]cloud.Region, error) { return c.regions(ctx) }
func (c fakeCatalog) InstanceTypes(ctx context.Context, region string) ([]cloud.InstanceType, error) {
	return c.types(ctx, region)
}

func noScanner(t *testing.T) factory {
	t.Helper()
	return func(string, controller.Timeouts) runFunc {
		t.Fatal("catalog command constructed a scanner")
		return nil
	}
}

func TestListRegionsWithoutScanCredentials(t *testing.T) {
	t.Setenv("VULTR_API_KEY", "") // Calling scan credential loading would fail.
	calls := 0
	catalog := fakeCatalog{regions: func(context.Context) ([]cloud.Region, error) {
		calls++
		return []cloud.Region{
			{ID: "ewr", Name: "New Jersey", Country: "US", Continent: "North America"},
			{ID: "atl", Name: "Atlanta", Country: "US", Continent: "North America"},
		}, nil
	}}
	var out, diagnostics bytes.Buffer
	if err := execute(t.Context(), []string{"list-regions"}, &out, &diagnostics, noScanner(t), catalog); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if calls != 1 || len(lines) != 3 || strings.Fields(lines[1])[0] != "atl" || !strings.Contains(lines[2], "New Jersey") || !strings.Contains(lines[0], "COUNTRY") {
		t.Fatal(out.String(), calls)
	}
	if diagnostics.Len() != 0 {
		t.Fatal(diagnostics.String())
	}
}

func TestListInstanceTypesDefaultsAndRegionOverride(t *testing.T) {
	t.Setenv("VULTR_API_KEY", "")
	for _, args := range [][]string{{"list-instance-types"}, {"list-instance-types", "--region=ewr"}, {"list-instance-types", "--region", "ewr"}} {
		want := "atl"
		if len(args) > 1 {
			want = "ewr"
		}
		calls := 0
		catalog := fakeCatalog{types: func(_ context.Context, region string) ([]cloud.InstanceType, error) {
			calls++
			if region != want {
				t.Errorf("region %q, expected %q", region, want)
			}
			return []cloud.InstanceType{{ID: "vhp-1c-1gb-amd", Family: "vhp", VCPUs: 1, MemoryMB: 1024, DiskGB: 25, DiskCount: 1, DiskType: "nvme", CPUType: "shared", CPUVendor: "amd"}, {ID: "vc2-1c-1gb", Family: "vc2", VCPUs: 1, MemoryMB: 1024, DiskGB: 25}}, nil
		}}
		var out, diagnostics bytes.Buffer
		if err := execute(t.Context(), args, &out, &diagnostics, noScanner(t), catalog); err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSpace(out.String()), "\n")
		if calls != 1 || len(lines) != 3 || strings.Fields(lines[1])[0] != "vc2-1c-1gb" || !strings.Contains(out.String(), "1024") || !strings.Contains(out.String(), "nvme") || !strings.Contains(out.String(), "shared") {
			t.Fatal(out.String(), calls)
		}
	}
}

func TestCatalogHelpAndBadArgumentsNeverFetch(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		success bool
	}{
		{[]string{"list-regions", "--help"}, true},
		{[]string{"list-instance-types", "--help"}, true},
		{[]string{"help", "list-regions"}, true},
		{[]string{"help", "list-instance-types"}, true},
		{[]string{"list-regions", "atl"}, false},
		{[]string{"list-regions", "--region=atl"}, false},
		{[]string{"list-instance-types", "unexpected"}, false},
		{[]string{"list-instance-types", "--region="}, false},
		{[]string{"list-instance-types", "--region"}, false},
		{[]string{"help", "unknown"}, false},
	} {
		var out, diagnostics bytes.Buffer
		// A nil catalog fails if argument handling attempts a fetch.
		err := execute(t.Context(), tc.args, &out, &diagnostics, noScanner(t), nil)
		if (err == nil) != tc.success || err != nil && strings.Contains(err.Error(), "catalog is unavailable") {
			t.Fatal(tc.args, err)
		}
	}
}

type brokenOutput struct{}

func (brokenOutput) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestCatalogErrorsCancellationAndOutputFailure(t *testing.T) {
	failure := errors.New("catalog unavailable")
	catalog := fakeCatalog{regions: func(context.Context) ([]cloud.Region, error) { return nil, failure }}
	var out, diagnostics bytes.Buffer
	if err := execute(t.Context(), []string{"list-regions"}, &out, &diagnostics, noScanner(t), catalog); !errors.Is(err, failure) || out.Len() != 0 {
		t.Fatal(err, out.String())
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := execute(ctx, []string{"list-regions"}, &out, &diagnostics, noScanner(t), nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	catalog.regions = func(context.Context) ([]cloud.Region, error) { return nil, nil }
	if err := execute(t.Context(), []string{"list-regions"}, brokenOutput{}, &diagnostics, noScanner(t), catalog); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal(err)
	}
}
