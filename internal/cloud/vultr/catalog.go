package vultr

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/st3fan/masscanr/internal/cloud"
)

func (c *Client) Regions(ctx context.Context) ([]cloud.Region, error) {
	path := "/regions?per_page=100"
	var regions []cloud.Region
	for pages := 0; pages < 1000; pages++ {
		var response struct {
			Regions []struct {
				ID        string `json:"id"`
				City      string `json:"city"`
				Country   string `json:"country"`
				Continent string `json:"continent"`
			} `json:"regions"`
			Meta pageMeta `json:"meta"`
		}
		if err := c.request(ctx, "GET", path, nil, &response); err != nil {
			return nil, err
		}
		for _, r := range response.Regions {
			regions = append(regions, cloud.Region{ID: r.ID, Name: r.City, Country: r.Country, Continent: r.Continent})
		}
		var err error
		path, err = nextPage(path, response.Meta.Links.Next)
		if err != nil {
			return nil, err
		}
		if path == "" {
			slices.SortFunc(regions, func(a, b cloud.Region) int { return strings.Compare(a.ID, b.ID) })
			return regions, nil
		}
	}
	return nil, fmt.Errorf("Vultr region pagination limit exceeded")
}

func (c *Client) availability(ctx context.Context, region string) ([]string, error) {
	if region == "" || strings.IndexFunc(region, func(r rune) bool { return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') }) >= 0 {
		return nil, fmt.Errorf("invalid region ID %q", region)
	}
	var response struct {
		Plans []string `json:"available_plans"`
	}
	err := c.request(ctx, "GET", "/regions/"+url.PathEscape(region)+"/availability", nil, &response)
	if errors.Is(err, cloud.ErrNotFound) {
		return nil, fmt.Errorf("unknown region %q", region)
	}
	return response.Plans, err
}

func (c *Client) InstanceTypes(ctx context.Context, region string) ([]cloud.InstanceType, error) {
	available, err := c.availability(ctx, region)
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]bool, len(available))
	for _, id := range available {
		allowed[id] = true
	}
	path := "/plans?per_page=100"
	var types []cloud.InstanceType
	for pages := 0; pages < 1000; pages++ {
		var response struct {
			Plans []struct {
				ID          string `json:"id"`
				Family      string `json:"type"`
				CPUType     string `json:"vcpu_type"`
				CPUVendor   string `json:"cpu_vendor"`
				VCPUs       int    `json:"vcpu_count"`
				MemoryMB    int    `json:"ram"`
				DiskGB      int    `json:"disk"`
				DiskCount   int    `json:"disk_count"`
				DiskType    string `json:"disk_type"`
				StorageType string `json:"storage_type"`
			} `json:"plans"`
			Meta pageMeta `json:"meta"`
		}
		if err := c.request(ctx, "GET", path, nil, &response); err != nil {
			return nil, err
		}
		for _, p := range response.Plans {
			if allowed[p.ID] {
				types = append(types, cloud.InstanceType{ID: p.ID, Family: p.Family, CPUType: p.CPUType, CPUVendor: p.CPUVendor, VCPUs: p.VCPUs, MemoryMB: p.MemoryMB, DiskGB: p.DiskGB, DiskCount: p.DiskCount, DiskType: p.DiskType, StorageType: p.StorageType})
			}
		}
		path, err = nextPage(path, response.Meta.Links.Next)
		if err != nil {
			return nil, err
		}
		if path == "" {
			slices.SortFunc(types, func(a, b cloud.InstanceType) int { return strings.Compare(a.ID, b.ID) })
			return types, nil
		}
	}
	return nil, fmt.Errorf("Vultr plan pagination limit exceeded")
}
