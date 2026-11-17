package service

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/st3fan/masscanr/internal/cloud"
	"github.com/st3fan/masscanr/internal/cloud/digitalocean"
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

func (a *API) regions(c fiber.Ctx) error {
	catalog, err := a.catalogFor(c.Query("provider"))
	if err != nil {
		return bad(c, err)
	}
	if catalog == nil {
		return respond(c, 503, map[string]string{"error": "provider catalog unavailable"})
	}
	ctx, cancel := context.WithTimeout(c.Context(), time.Minute)
	defer cancel()
	regions, err := catalog.Regions(ctx)
	if err != nil {
		if handled, response := digitalOceanCatalogError(c, c.Query("provider"), err); handled {
			return response
		}
		return respond(c, 502, map[string]string{"error": "provider catalog unavailable"})
	}
	result := make([]regionResponse, 0, len(regions))
	for _, region := range regions {
		result = append(result, regionResponse{ID: region.ID, Name: region.Name, Country: region.Country, Continent: region.Continent})
	}
	slices.SortFunc(result, func(a, b regionResponse) int { return strings.Compare(a.ID, b.ID) })
	return respond(c, 200, result)
}

func (a *API) instanceTypes(c fiber.Ctx) error {
	provider := c.Query("provider")
	catalog, err := a.catalogFor(provider)
	if err != nil {
		return bad(c, err)
	}
	region := c.Query("region")
	if region == "" {
		if provider == "digitalocean" {
			return bad(c, errors.New("region is required for DigitalOcean"))
		}
		region = scan.DefaultRegion
	}
	if len(region) > 64 || strings.IndexFunc(region, func(ch rune) bool { return ch != '-' && (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') }) >= 0 {
		return bad(c, errors.New("invalid region ID"))
	}
	if catalog == nil {
		return respond(c, 503, map[string]string{"error": "provider catalog unavailable"})
	}
	ctx, cancel := context.WithTimeout(c.Context(), time.Minute)
	defer cancel()
	plans, err := catalog.InstanceTypes(ctx, region)
	if err != nil {
		if handled, response := digitalOceanCatalogError(c, provider, err); handled {
			return response
		}
		if errors.Is(err, cloud.ErrNotFound) {
			return respond(c, 404, map[string]string{"error": "region unavailable"})
		} else {
			return respond(c, 502, map[string]string{"error": "provider catalog unavailable"})
		}
	}
	result := make([]instanceTypeResponse, 0, len(plans))
	for _, plan := range plans {
		result = append(result, instanceTypeResponse{ID: plan.ID, Family: plan.Family, VCPUs: plan.VCPUs, MemoryMB: plan.MemoryMB, DiskCount: plan.DiskCount, DiskGB: plan.DiskGB, StorageType: plan.StorageType, DiskType: plan.DiskType, CPUType: plan.CPUType, CPUVendor: plan.CPUVendor})
	}
	slices.SortFunc(result, func(a, b instanceTypeResponse) int { return strings.Compare(a.ID, b.ID) })
	return respond(c, 200, result)
}

func digitalOceanCatalogError(c fiber.Ctx, provider string, err error) (bool, error) {
	if provider != "digitalocean" {
		return false, nil
	}
	if errors.Is(err, digitalocean.ErrTokenRequired) {
		return true, respond(c, 503, map[string]string{"error": "DIGITALOCEAN_API_TOKEN is required for the DigitalOcean catalog"})
	}
	var apiErr *digitalocean.HTTPError
	if errors.As(err, &apiErr) && (apiErr.Status == 401 || apiErr.Status == 403) {
		return true, respond(c, 502, map[string]string{"error": "DigitalOcean API token was rejected or lacks catalog permission"})
	}
	return false, nil
}

func (a *API) catalogFor(provider string) (cloud.Catalog, error) {
	if provider == "" || provider == "vultr" {
		return a.Catalog, nil
	}
	if provider != "digitalocean" {
		return nil, errors.New("unsupported provider")
	}
	return a.Catalogs[provider], nil
}
