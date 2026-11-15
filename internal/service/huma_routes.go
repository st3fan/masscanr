package service

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humafiber"
	"github.com/gofiber/fiber/v3"
)

func (a *API) initHuma(app *fiber.App) {
	config := huma.DefaultConfig("masscanr API", "1.0.0")
	config.DocsPath = "" // The bundled Stoplight renderer is served locally.
	config.SchemasPath = ""
	config.Formats = map[string]huma.Format{jsonAPIMediaType: huma.DefaultJSONFormat}
	config.DefaultFormat = jsonAPIMediaType
	config.NoFormatFallback = true
	config.CreateHooks = nil // Do not add $schema to JSON:API documents.
	config.OpenAPI.Components.Schemas = huma.NewMapRegistry("#/components/schemas/", func(t reflect.Type, hint string) string {
		name := huma.DefaultSchemaNamer(t, hint)
		if pkg := t.PkgPath(); pkg != "" {
			parts := strings.Split(pkg, "/")
			name = parts[len(parts)-1] + name
		}
		return name
	})
	a.Huma = humafiber.New(app, config)
	a.docs(app)
}

func resourceSchema(attributes *huma.Schema, typ string) *huma.Schema {
	return &huma.Schema{Type: "object", Required: []string{"type", "id", "attributes"}, Properties: map[string]*huma.Schema{
		"type":          {Type: "string", Const: typ},
		"id":            {Type: "string"},
		"attributes":    attributes,
		"relationships": {Type: "object", AdditionalProperties: true},
		"links":         {Type: "object", AdditionalProperties: true},
	}}
}

func requestResourceSchema(attributes *huma.Schema, typ string, requireID bool) *huma.Schema {
	resource := resourceSchema(attributes, typ)
	delete(resource.Properties, "relationships")
	delete(resource.Properties, "links")
	resource.Required = []string{"type", "attributes"}
	if requireID {
		resource.Required = append(resource.Required, "id")
	}
	return resource
}

func (a *API) schemaFor(v any) *huma.Schema {
	return huma.SchemaFromType(a.Huma.OpenAPI().Components.Schemas, reflect.TypeOf(v))
}

func (a *API) attributesSchema(v any) *huma.Schema {
	schema := a.schemaFor(v)
	if schema.Ref != "" {
		schema = a.Huma.OpenAPI().Components.Schemas.SchemaFromRef(schema.Ref)
	}
	copy := *schema
	copy.Properties = make(map[string]*huma.Schema, len(schema.Properties))
	for name, property := range schema.Properties {
		if name != "id" {
			copy.Properties[name] = property
		}
	}
	copy.Required = nil
	for _, name := range schema.Required {
		if name != "id" {
			copy.Required = append(copy.Required, name)
		}
	}
	return &copy
}

func (a *API) register(method, path, operationID, summary string, model any, handler fiber.Handler) {
	openPath := strings.ReplaceAll(strings.ReplaceAll(path, ":id", "{id}"), ":format", "{format}")
	typ := resourceTypeForPath(path, operationID)
	op := &huma.Operation{Method: method, Path: openPath, OperationID: operationID, Summary: summary, Tags: []string{typ}, Responses: map[string]*huma.Response{}}
	for _, parameter := range []string{"id", "format"} {
		if strings.Contains(openPath, "{"+parameter+"}") {
			p := &huma.Param{Name: parameter, In: "path", Required: true, Schema: &huma.Schema{Type: "string"}}
			if parameter == "format" {
				p.Schema.Enum = []any{"csv", "jsonl"}
			}
			op.Parameters = append(op.Parameters, p)
		}
	}
	if operationID == "listScans" || operationID == "listScanRuns" || operationID == "listScanPlanRuns" || operationID == "listTargets" || operationID == "listScanPlans" {
		op.Parameters = append(op.Parameters, &huma.Param{Name: "limit", In: "query", Schema: &huma.Schema{Type: "integer", Minimum: floatPtr(1), Maximum: floatPtr(1000)}}, &huma.Param{Name: "after", In: "query", Schema: &huma.Schema{Type: "string"}})
	}
	if operationID == "listActiveRuns" {
		op.Parameters = append(op.Parameters, &huma.Param{Name: "active", In: "query", Required: true, Schema: &huma.Schema{Type: "string", Const: "true"}})
	}
	if operationID == "listInstanceTypes" {
		op.Parameters = append(op.Parameters, &huma.Param{Name: "region", In: "query", Schema: &huma.Schema{Type: "string"}})
	}
	if operationID == "startScan" || operationID == "startScanPlan" {
		op.Parameters = append(op.Parameters, &huma.Param{Name: "Idempotency-Key", In: "header", Required: true, Schema: &huma.Schema{Type: "string", MinLength: intPtr(1), MaxLength: intPtr(200)}})
	}
	if operationID == "createScan" || operationID == "updateScan" || operationID == "startScan" || operationID == "createTarget" || operationID == "updateTarget" || operationID == "createScanPlan" || operationID == "updateScanPlan" || operationID == "startScanPlan" {
		var attrs *huma.Schema
		switch operationID {
		case "createScan":
			attrs = a.attributesSchema(Definition{})
			attrs.Required = []string{"targets"}
		case "updateScan":
			partial := a.attributesSchema(Definition{})
			partial.Required = nil
			attrs = &huma.Schema{Type: "object", Required: []string{"revision", "definition"}, Properties: map[string]*huma.Schema{"revision": {Type: "integer", Minimum: floatPtr(1)}, "definition": partial}}
		case "startScan":
			attrs = &huma.Schema{Type: "object", Required: []string{"plan_id"}, Properties: map[string]*huma.Schema{"plan_id": {Type: "string"}, "confirm": {Type: "boolean"}}}
		case "startScanPlan":
			attrs = &huma.Schema{Type: "object", Required: []string{"preflight_id"}, Properties: map[string]*huma.Schema{"preflight_id": {Type: "string"}, "confirm": {Type: "boolean"}}}
		case "createTarget":
			attrs = &huma.Schema{Type: "object", Required: []string{"name", "entries"}, Properties: map[string]*huma.Schema{"name": {Type: "string"}, "entries": {Type: "array", Items: &huma.Schema{Type: "string"}}}}
		case "updateTarget":
			attrs = &huma.Schema{Type: "object", Required: []string{"revision"}, Properties: map[string]*huma.Schema{"revision": {Type: "integer", Minimum: floatPtr(1)}, "name": {Type: "string"}, "entries": {Type: "array", Items: &huma.Schema{Type: "string"}}, "archived": {Type: "boolean"}}}
		case "createScanPlan":
			attrs = &huma.Schema{Type: "object", Required: []string{"name", "target_id"}, Properties: map[string]*huma.Schema{"name": {Type: "string"}, "target_id": {Type: "string"}, "settings": a.attributesSchema(ScanPlanSettings{})}}
		case "updateScanPlan":
			settings := a.attributesSchema(ScanPlanSettings{})
			settings.Required = nil
			attrs = &huma.Schema{Type: "object", Required: []string{"revision"}, Properties: map[string]*huma.Schema{"revision": {Type: "integer", Minimum: floatPtr(1)}, "name": {Type: "string"}, "target_id": {Type: "string"}, "settings": settings}}
		}
		example := map[string]any{"data": map[string]any{"type": requestTypeForOperation(operationID), "attributes": map[string]any{}}}
		switch operationID {
		case "createScan":
			example["data"].(map[string]any)["attributes"] = map[string]any{"targets": []string{"192.0.2.0/24"}}
		case "updateScan":
			example["data"].(map[string]any)["id"] = "scan-id"
			example["data"].(map[string]any)["attributes"] = map[string]any{"revision": 1, "definition": map[string]any{"ports": "443"}}
		case "startScan":
			example["data"].(map[string]any)["attributes"] = map[string]any{"plan_id": "plan-id", "confirm": true}
		case "startScanPlan":
			example["data"].(map[string]any)["attributes"] = map[string]any{"preflight_id": "preflight-id", "confirm": true}
		case "createTarget":
			example["data"].(map[string]any)["attributes"] = map[string]any{"name": "External Network", "entries": []string{"192.0.2.0/24", "AS3333"}}
		case "updateTarget":
			example["data"].(map[string]any)["id"] = "target-id"
			example["data"].(map[string]any)["attributes"] = map[string]any{"revision": 1, "name": "External Network"}
		case "createScanPlan":
			example["data"].(map[string]any)["attributes"] = map[string]any{"name": "Daily TCP", "target_id": "target-id"}
		case "updateScanPlan":
			example["data"].(map[string]any)["id"] = "scan-plan-id"
			example["data"].(map[string]any)["attributes"] = map[string]any{"revision": 1, "settings": map[string]any{"ports": "443"}}
		}
		op.RequestBody = &huma.RequestBody{Required: true, Content: map[string]*huma.MediaType{jsonAPIMediaType: {Schema: &huma.Schema{Type: "object", Required: []string{"data"}, Properties: map[string]*huma.Schema{"data": requestResourceSchema(attrs, requestTypeForOperation(operationID), operationID == "updateScan" || operationID == "updateTarget" || operationID == "updateScanPlan")}}, Example: example}}}
	}
	if operationID == "health" {
		op.Responses["200"] = &huma.Response{Description: "Ready", Content: map[string]*huma.MediaType{"application/json": {Schema: &huma.Schema{Type: "object", Required: []string{"status"}, Properties: map[string]*huma.Schema{"status": {Type: "string", Const: "ready"}}}, Example: map[string]string{"status": "ready"}}}}
		op.Responses["503"] = &huma.Response{Description: "Unavailable", Content: map[string]*huma.MediaType{"application/json": {Schema: &huma.Schema{Type: "object", Properties: map[string]*huma.Schema{"error": {Type: "string"}}}}}}
	} else {
		status := 200
		if operationID == "createScan" || operationID == "preflightScan" || operationID == "preflightScanPlan" || operationID == "createTarget" || operationID == "createScanPlan" {
			status = 201
		}
		if operationID == "startScan" || operationID == "startScanPlan" || operationID == "stopScan" || operationID == "stopRun" {
			status = 202
		}
		if method == "DELETE" {
			status = 204
		}
		if operationID == "exportRun" {
			op.Responses["200"] = &huma.Response{Description: "Complete saved export", Content: map[string]*huma.MediaType{"text/csv": {Schema: &huma.Schema{Type: "string"}, Example: "ip,port\n192.0.2.1,80\n"}, "application/x-ndjson": {Schema: &huma.Schema{Type: "string"}, Example: "{\"ip\":\"192.0.2.1\"}\n"}}}
		} else if status == 204 {
			op.Responses["204"] = &huma.Response{Description: "Deleted"}
		} else {
			attrs := a.attributesSchema(model)
			data := resourceSchema(attrs, typ)
			if strings.HasPrefix(operationID, "list") {
				data = &huma.Schema{Type: "array", Items: data}
			}
			schema := &huma.Schema{Type: "object", Required: []string{"data"}, Properties: map[string]*huma.Schema{"data": data, "links": {Type: "object", AdditionalProperties: true}, "meta": {Type: "object", AdditionalProperties: true}}}
			example := map[string]any{"data": exampleResource(typ, model)}
			if strings.HasPrefix(operationID, "list") {
				example["data"] = []any{exampleResource(typ, model)}
			}
			if operationID == "listActiveRuns" {
				example["meta"] = map[string]int{"snapshot_version": 1}
			}
			op.Responses[strconv.Itoa(status)] = &huma.Response{Description: "JSON:API resource document", Content: map[string]*huma.MediaType{jsonAPIMediaType: {Schema: schema, Example: example}}}
		}
		errorSchema := &huma.Schema{Type: "object", Required: []string{"errors"}, Properties: map[string]*huma.Schema{"errors": {Type: "array", Items: &huma.Schema{Type: "object", Required: []string{"status", "detail"}, Properties: map[string]*huma.Schema{"status": {Type: "string"}, "detail": {Type: "string"}}}}}}
		for _, code := range []string{"400", "404", "406", "409", "413", "415", "422", "500", "502", "503"} {
			op.Responses[code] = &huma.Response{Description: "JSON:API error", Content: map[string]*huma.MediaType{jsonAPIMediaType: {Schema: errorSchema, Example: map[string]any{"errors": []any{map[string]string{"status": code, "detail": "request failed"}}}}}}
		}
	}
	a.Huma.OpenAPI().AddOperation(op)
	a.Huma.Adapter().Handle(op, func(ctx huma.Context) {
		c := humafiber.Unwrap(ctx)
		if err := handler(c); err != nil {
			_ = c.App().Config().ErrorHandler(c, err)
		}
	})
}

func exampleResource(typ string, model any) map[string]any {
	b, _ := json.Marshal(model)
	attrs := map[string]any{}
	_ = json.Unmarshal(b, &attrs)
	delete(attrs, "id")
	return map[string]any{"type": typ, "id": "example-id", "attributes": attrs}
}

func floatPtr(v float64) *float64 { return &v }
func intPtr(v int) *int           { return &v }

func requestTypeForOperation(id string) string {
	if id == "startScan" || id == "startScanPlan" {
		return "runs"
	}
	if id == "createTarget" || id == "updateTarget" {
		return "targets"
	}
	if id == "createScanPlan" || id == "updateScanPlan" {
		return "scan-plans"
	}
	return "scans"
}

func resourceTypeForPath(path, operationID string) string {
	switch operationID {
	case "health":
		return "health"
	case "preflightScan":
		return "plans"
	case "preflightScanPlan":
		return "scan-preflights"
	case "listRegions":
		return "regions"
	case "listInstanceTypes":
		return "instance-types"
	case "inspectRun":
		return "inspections"
	}
	if strings.Contains(path, "/runs") || strings.HasSuffix(path, "/start") || strings.HasSuffix(path, "/stop") {
		return "runs"
	}
	if strings.HasPrefix(path, "/v1/targets") {
		return "targets"
	}
	if strings.HasPrefix(path, "/v1/scan-plans") {
		return "scan-plans"
	}
	return "scans"
}
