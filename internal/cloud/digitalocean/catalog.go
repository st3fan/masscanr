package digitalocean

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/st3fan/masscanr/internal/cloud"
)

const fedoraImage = "fedora-44-x64"

type pages struct {
	Links struct {
		Pages struct {
			Next string `json:"next"`
		} `json:"pages"`
	} `json:"links"`
}

func (c *Client) Regions(ctx context.Context) ([]cloud.Region, error) {
	path := "/regions?per_page=200"
	var out []cloud.Region
	for page := 0; page < 100; page++ {
		var response struct {
			Regions []struct {
				Slug      string `json:"slug"`
				Name      string `json:"name"`
				Available bool   `json:"available"`
			} `json:"regions"`
			pages
		}
		if err := c.request(ctx, "GET", path, nil, &response); err != nil {
			return nil, err
		}
		for _, r := range response.Regions {
			if r.Available {
				out = append(out, cloud.Region{ID: r.Slug, Name: r.Name})
			}
		}
		var err error
		path, err = c.nextPath(response.Links.Pages.Next)
		if err != nil {
			return nil, err
		}
		if path == "" {
			slices.SortFunc(out, func(a, b cloud.Region) int { return strings.Compare(a.ID, b.ID) })
			return out, nil
		}
	}
	return nil, fmt.Errorf("DigitalOcean region pagination limit exceeded")
}

func (c *Client) InstanceTypes(ctx context.Context, region string) ([]cloud.InstanceType, error) {
	if !validSlug(region) {
		return nil, fmt.Errorf("invalid DigitalOcean region")
	}
	path := "/sizes?per_page=200"
	var out []cloud.InstanceType
	for page := 0; page < 100; page++ {
		var response struct {
			Sizes []struct {
				Slug      string   `json:"slug"`
				VCPUs     int      `json:"vcpus"`
				Memory    int      `json:"memory"`
				Disk      int      `json:"disk"`
				Available bool     `json:"available"`
				Regions   []string `json:"regions"`
			} `json:"sizes"`
			pages
		}
		if err := c.request(ctx, "GET", path, nil, &response); err != nil {
			return nil, err
		}
		for _, s := range response.Sizes {
			if s.Available && slices.Contains(s.Regions, region) && s.Disk > 0 {
				out = append(out, cloud.InstanceType{ID: s.Slug, Family: "droplet", VCPUs: s.VCPUs, MemoryMB: s.Memory, DiskGB: s.Disk, DiskCount: 1})
			}
		}
		var err error
		path, err = c.nextPath(response.Links.Pages.Next)
		if err != nil {
			return nil, err
		}
		if path == "" {
			slices.SortFunc(out, func(a, b cloud.InstanceType) int { return strings.Compare(a.ID, b.ID) })
			return out, nil
		}
	}
	return nil, fmt.Errorf("DigitalOcean size pagination limit exceeded")
}

func (c *Client) imageAvailable(ctx context.Context, region string) (bool, error) {
	path := "/images?type=distribution&per_page=200"
	for page := 0; page < 100; page++ {
		var response struct {
			Images []struct {
				Slug    string   `json:"slug"`
				Regions []string `json:"regions"`
				Status  string   `json:"status"`
			} `json:"images"`
			pages
		}
		if err := c.request(ctx, "GET", path, nil, &response); err != nil {
			return false, err
		}
		for _, image := range response.Images {
			if image.Slug == fedoraImage && slices.Contains(image.Regions, region) && image.Status == "available" {
				return true, nil
			}
		}
		var err error
		path, err = c.nextPath(response.Links.Pages.Next)
		if err != nil {
			return false, err
		}
		if path == "" {
			return false, nil
		}
	}
	return false, fmt.Errorf("DigitalOcean image pagination limit exceeded")
}

func validSlug(s string) bool {
	if s == "" || len(s) > 100 {
		return false
	}
	for _, r := range s {
		if r < 'a' || r > 'z' {
			if r < '0' || r > '9' {
				if r != '-' {
					return false
				}
			}
		}
	}
	return true
}

func escapeSlug(s string) string { return url.PathEscape(s) }
