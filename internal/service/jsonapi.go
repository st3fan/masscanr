package service

import (
	"encoding/json"
	"fmt"
	"mime"
	"net/url"
	"reflect"
	"strconv"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/st3fan/masscanr/internal/runapi"
)

const jsonAPIMediaType = "application/vnd.api+json"

type jsonAPIError struct {
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type jsonAPIResource struct {
	Type          string                     `json:"type"`
	ID            string                     `json:"id"`
	Attributes    map[string]json.RawMessage `json:"attributes"`
	Relationships map[string]any             `json:"relationships,omitempty"`
	Links         map[string]any             `json:"links,omitempty"`
}

func requestResourceType(c fiber.Ctx) string {
	if strings.HasSuffix(c.Path(), "/start") {
		return "runs"
	}
	if strings.HasPrefix(c.Path(), "/v1/targets") {
		return "targets"
	}
	if strings.HasPrefix(c.Path(), "/v1/scan-plans") {
		return "scan-plans"
	}
	return "scans"
}

func invalidJSONAPIParams(params map[string]string) bool {
	for key := range params {
		if key != "ext" && key != "profile" {
			return true
		}
	}
	return false
}

func acceptsResponse(header string, download bool) bool {
	if header == "" {
		return true
	}
	for _, part := range strings.Split(header, ",") {
		media, params, err := mime.ParseMediaType(strings.TrimSpace(part))
		if err != nil {
			continue
		}
		if q, ok := params["q"]; ok {
			weight, err := strconv.ParseFloat(q, 64)
			if err != nil || weight <= 0 || weight > 1 {
				continue
			}
		}
		delete(params, "q")
		if media == "*/*" && len(params) == 0 || media == jsonAPIMediaType && !invalidJSONAPIParams(params) {
			return true
		}
		if download && len(params) == 0 && (media == "text/csv" || media == "application/x-ndjson") {
			return true
		}
	}
	return false
}

func resourceType(c fiber.Ctx, v any) string {
	switch v.(type) {
	case Target:
		return "targets"
	case ScanPlan:
		return "scan-plans"
	case Plan:
		if strings.HasPrefix(c.Path(), "/v1/scan-plans/") {
			return "scan-preflights"
		}
		return "plans"
	case Run:
		return "runs"
	case inspection:
		return "inspections"
	case regionResponse:
		return "regions"
	case instanceTypeResponse:
		return "instance-types"
	}
	if strings.HasPrefix(c.Path(), "/v1/targets") {
		return "targets"
	}
	if strings.HasPrefix(c.Path(), "/v1/scan-plans") {
		return "scan-plans"
	}
	if strings.Contains(c.Path(), "/catalog/regions") {
		return "regions"
	}
	if strings.Contains(c.Path(), "/catalog/instance-types") {
		return "instance-types"
	}
	if strings.Contains(c.Path(), "/inspection") {
		return "inspections"
	}
	if strings.HasPrefix(c.Path(), "/v1/runs") || strings.HasSuffix(c.Path(), "/runs") || strings.HasSuffix(c.Path(), "/stop") || strings.HasSuffix(c.Path(), "/start") {
		return "runs"
	}
	return "scans"
}

func makeResource(typ string, value any) (jsonAPIResource, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return jsonAPIResource{}, err
	}
	var attributes map[string]json.RawMessage
	if err = json.Unmarshal(b, &attributes); err != nil {
		return jsonAPIResource{}, err
	}
	var id string
	_ = json.Unmarshal(attributes["id"], &id)
	if typ == "inspections" {
		_ = json.Unmarshal(attributes["run_id"], &id)
	}
	if id == "" {
		return jsonAPIResource{}, fmt.Errorf("resource ID missing")
	}
	delete(attributes, "id")
	resource := jsonAPIResource{Type: typ, ID: id, Attributes: attributes}
	var scanPlanID string
	_ = json.Unmarshal(attributes["scan_plan_id"], &scanPlanID)
	if scanPlanID != "" {
		resource.Relationships = map[string]any{"scan-plan": map[string]any{"data": map[string]string{"type": "scan-plans", "id": scanPlanID}}}
	}
	var targetID string
	_ = json.Unmarshal(attributes["target_id"], &targetID)
	if targetID != "" {
		if resource.Relationships == nil {
			resource.Relationships = map[string]any{}
		}
		resource.Relationships["target"] = map[string]any{"data": map[string]string{"type": "targets", "id": targetID}}
	}
	if typ == "runs" {
		var plan map[string]json.RawMessage
		if json.Unmarshal(attributes["preflight"], &plan) == nil {
			var planID string
			_ = json.Unmarshal(plan["id"], &planID)
			if planID != "" {
				if resource.Relationships == nil {
					resource.Relationships = map[string]any{}
				}
				resource.Relationships["preflight"] = map[string]any{"data": map[string]string{"type": "scan-preflights", "id": planID}}
			}
		}
	}
	if typ == "inspections" {
		resource.Relationships = map[string]any{"run": map[string]any{"data": map[string]string{"type": "runs", "id": id}}}
	}
	return resource, nil
}

func respondJSONAPI(c fiber.Ctx, status int, v any) error {
	if status == 204 {
		return c.SendStatus(status)
	}
	if status >= 400 {
		message := "request failed"
		if m, ok := v.(map[string]string); ok && m["error"] != "" {
			message = m["error"]
		}
		return sendJSONAPI(c, status, map[string]any{"errors": []jsonAPIError{{Status: fmt.Sprint(status), Detail: message}}})
	}
	if v == nil {
		return sendJSONAPI(c, status, map[string]any{"data": nil})
	}
	if active, ok := v.(runapi.ActiveRuns); ok {
		v = active.Runs
	}
	value := reflect.ValueOf(v)
	if value.Kind() == reflect.Slice || value.Kind() == reflect.Array {
		items := make([]jsonAPIResource, 0, value.Len())
		for i := 0; i < value.Len(); i++ {
			item := value.Index(i).Interface()
			r, err := makeResource(resourceType(c, item), item)
			if err != nil {
				return err
			}
			items = append(items, r)
		}
		doc := map[string]any{"data": items, "links": map[string]any{"self": string(c.Request().URI().RequestURI())}}
		if c.Path() == "/v1/scans" || c.Path() == "/v1/targets" || c.Path() == "/v1/scan-plans" || strings.HasSuffix(c.Path(), "/runs") && (strings.HasPrefix(c.Path(), "/v1/scans/") || strings.HasPrefix(c.Path(), "/v1/scan-plans/")) {
			limit, _ := strconv.Atoi(c.Query("limit", "100"))
			if len(items) == limit && limit > 0 {
				q := url.Values{}
				q.Set("limit", strconv.Itoa(limit))
				q.Set("after", items[len(items)-1].ID)
				doc["links"].(map[string]any)["next"] = c.Path() + "?" + q.Encode()
			}
		}
		if strings.HasPrefix(c.Path(), "/v1/runs") && c.Query("active") == "true" {
			doc["meta"] = map[string]any{"snapshot_version": runapi.SnapshotVersion}
		}
		return sendJSONAPI(c, status, doc)
	}
	r, err := makeResource(resourceType(c, v), v)
	if err != nil {
		return err
	}
	switch r.Type {
	case "scans":
		r.Links = map[string]any{"self": "/v1/scans/" + r.ID}
	case "targets":
		r.Links = map[string]any{"self": "/v1/targets/" + r.ID}
	case "scan-plans":
		r.Links = map[string]any{"self": "/v1/scan-plans/" + r.ID}
	case "scan-preflights":
		r.Links = map[string]any{"self": "/v1/scan-plans/" + scanPlanIDFromResource(r) + "/preflight"}
	case "runs":
		r.Links = map[string]any{"self": "/v1/runs/" + r.ID}
	case "inspections":
		r.Links = map[string]any{"self": "/v1/runs/" + r.ID + "/inspection"}
	}
	return sendJSONAPI(c, status, map[string]any{"data": r})
}

func sendJSONAPI(c fiber.Ctx, status int, doc any) error {
	b, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	c.Set("Content-Type", jsonAPIMediaType)
	c.Status(status)
	return c.Send(append(b, '\n'))
}

func scanPlanIDFromResource(r jsonAPIResource) string {
	var id string
	_ = json.Unmarshal(r.Attributes["scan_plan_id"], &id)
	return id
}
