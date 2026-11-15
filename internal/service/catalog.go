package service

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/st3fan/masscanr/internal/cloud"
	"github.com/st3fan/masscanr/internal/scan"
)

type regionResponse struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Country   string `json:"country"`
	Continent string `json:"continent"`
}

type instanceTypeResponse struct {
	ID          string `json:"id"`
	Family      string `json:"family"`
	VCPUs       int    `json:"vcpus"`
	MemoryMB    int    `json:"memory_mb"`
	DiskCount   int    `json:"disk_count"`
	DiskGB      int    `json:"disk_gb"`
	StorageType string `json:"storage_type"`
	DiskType    string `json:"disk_type"`
	CPUType     string `json:"cpu_type"`
	CPUVendor   string `json:"cpu_vendor"`
}

func (a *API) regions(w http.ResponseWriter, r *http.Request) {
	if a.Catalog == nil {
		respond(w, 503, map[string]string{"error": "provider catalog unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	regions, err := a.Catalog.Regions(ctx)
	if err != nil {
		respond(w, 502, map[string]string{"error": "provider catalog unavailable"})
		return
	}
	result := make([]regionResponse, 0, len(regions))
	for _, region := range regions {
		result = append(result, regionResponse{ID: region.ID, Name: region.Name, Country: region.Country, Continent: region.Continent})
	}
	slices.SortFunc(result, func(a, b regionResponse) int { return strings.Compare(a.ID, b.ID) })
	respond(w, 200, result)
}

func (a *API) instanceTypes(w http.ResponseWriter, r *http.Request) {
	region := r.URL.Query().Get("region")
	if region == "" {
		region = scan.DefaultRegion
	}
	if len(region) > 64 || strings.IndexFunc(region, func(ch rune) bool { return ch != '-' && (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') }) >= 0 {
		bad(w, errors.New("invalid region ID"))
		return
	}
	if a.Catalog == nil {
		respond(w, 503, map[string]string{"error": "provider catalog unavailable"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	plans, err := a.Catalog.InstanceTypes(ctx, region)
	if err != nil {
		if errors.Is(err, cloud.ErrNotFound) {
			respond(w, 404, map[string]string{"error": "region unavailable"})
		} else {
			respond(w, 502, map[string]string{"error": "provider catalog unavailable"})
		}
		return
	}
	result := make([]instanceTypeResponse, 0, len(plans))
	for _, plan := range plans {
		result = append(result, instanceTypeResponse{ID: plan.ID, Family: plan.Family, VCPUs: plan.VCPUs, MemoryMB: plan.MemoryMB, DiskCount: plan.DiskCount, DiskGB: plan.DiskGB, StorageType: plan.StorageType, DiskType: plan.DiskType, CPUType: plan.CPUType, CPUVendor: plan.CPUVendor})
	}
	slices.SortFunc(result, func(a, b instanceTypeResponse) int { return strings.Compare(a.ID, b.ID) })
	respond(w, 200, result)
}
