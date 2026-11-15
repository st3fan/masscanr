package service

import (
	"fmt"
	"net/netip"
	"net/url"
	"strconv"

	"github.com/gofiber/fiber/v3"
)

func (a *API) listScanResults(c fiber.Ctx) error {
	limit, err := resourceLimit(c)
	if err != nil {
		return bad(c, err)
	}
	filter := ResultFilter{RunID: c.Query("filter[run_id]"), TargetID: c.Query("filter[target_id]"), After: c.Query("after"), Sort: c.Query("sort"), Limit: limit}
	if text := c.Query("filter[ip]"); text != "" {
		filter.IP, err = netip.ParseAddr(text)
		if err != nil || !filter.IP.Is4() {
			return bad(c, fmt.Errorf("filter[ip] must be an IPv4 address"))
		}
	}
	if text := c.Query("filter[port]"); text != "" {
		port, e := strconv.ParseUint(text, 10, 16)
		if e != nil {
			return bad(c, fmt.Errorf("filter[port] must be a TCP port"))
		}
		value := uint16(port)
		filter.Port = &value
	}
	meta := map[string]any{"absence": "unknown; results contain positive open observations only"}
	if filter.RunID != "" {
		coverage, e := a.Store.GetResultCoverage(c.Context(), filter.RunID)
		if e != nil {
			return problem(c, e)
		}
		meta["coverage"] = coverage
	} else {
		meta["coverage"] = "per-result; filter by run_id for complete run coverage"
	}
	results, err := a.Store.ListScanResults(c.Context(), filter)
	if err != nil {
		return bad(c, err)
	}
	resources := make([]jsonAPIResource, 0, len(results))
	for _, result := range results {
		resource, e := makeResource("scan-results", result)
		if e != nil {
			return problem(c, e)
		}
		resources = append(resources, resource)
	}
	links := map[string]any{"self": string(c.Request().URI().RequestURI())}
	if len(resources) == limit {
		q := url.Values{}
		q.Set("limit", strconv.Itoa(limit))
		q.Set("after", resources[len(resources)-1].ID)
		if filter.Sort != "" {
			q.Set("sort", filter.Sort)
		}
		if filter.RunID != "" {
			q.Set("filter[run_id]", filter.RunID)
		}
		if filter.TargetID != "" {
			q.Set("filter[target_id]", filter.TargetID)
		}
		if filter.IP.IsValid() {
			q.Set("filter[ip]", filter.IP.String())
		}
		if filter.Port != nil {
			q.Set("filter[port]", strconv.Itoa(int(*filter.Port)))
		}
		links["next"] = c.Path() + "?" + q.Encode()
	}
	return sendJSONAPI(c, 200, map[string]any{"data": resources, "links": links, "meta": meta})
}
