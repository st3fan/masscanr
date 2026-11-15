package service

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/gofiber/fiber/v3"
)

func resourceLimit(c fiber.Ctx) (int, error) {
	limit := 100
	if text := c.Query("limit"); text != "" {
		n, err := strconv.Atoi(text)
		if err != nil || n < 1 || n > 1000 {
			return 0, fmt.Errorf("limit must be 1..1000")
		}
		limit = n
	}
	return limit, nil
}

func (a *API) createTarget(c fiber.Ctx) error {
	var in struct {
		Name    string   `json:"name"`
		Entries []string `json:"entries"`
	}
	if err := decode(c, &in); err != nil {
		return bad(c, err)
	}
	if !validName(in.Name) {
		return bad(c, fmt.Errorf("target name must contain 1..200 bytes"))
	}
	if _, err := NormalizeTargetEntries(in.Entries); err != nil {
		return bad(c, err)
	}
	target, err := a.Store.CreateTarget(c.Context(), in.Name, in.Entries)
	if err != nil {
		return problem(c, err)
	}
	c.Set("Location", "/v1/targets/"+target.ID)
	return respond(c, 201, target)
}

func (a *API) getTarget(c fiber.Ctx) error {
	target, err := a.Store.GetTarget(c.Context(), c.Params("id"))
	if err != nil {
		return problem(c, err)
	}
	return respond(c, 200, target)
}

func (a *API) listTargets(c fiber.Ctx) error {
	limit, err := resourceLimit(c)
	if err != nil {
		return bad(c, err)
	}
	targets, err := a.Store.ListTargets(c.Context(), c.Query("after"), limit)
	if err != nil {
		return problem(c, err)
	}
	return respond(c, 200, targets)
}

func (a *API) patchTarget(c fiber.Ctx) error {
	var in struct {
		Revision int64     `json:"revision"`
		Name     *string   `json:"name"`
		Entries  *[]string `json:"entries"`
		Archived *bool     `json:"archived"`
	}
	if err := decode(c, &in); err != nil {
		return bad(c, err)
	}
	if in.Revision < 1 || in.Name == nil && in.Entries == nil && in.Archived == nil {
		return bad(c, fmt.Errorf("revision and changed attributes are required"))
	}
	if in.Name != nil && !validName(*in.Name) {
		return bad(c, fmt.Errorf("target name must contain 1..200 bytes"))
	}
	if in.Entries != nil {
		if _, err := NormalizeTargetEntries(*in.Entries); err != nil {
			return bad(c, err)
		}
	}
	if in.Archived != nil && !*in.Archived {
		return bad(c, fmt.Errorf("archived may only be set to true"))
	}
	target, err := a.Store.UpdateTarget(c.Context(), c.Params("id"), in.Revision, TargetPatch{Name: in.Name, Entries: in.Entries, Archived: in.Archived})
	if err != nil {
		return problem(c, err)
	}
	return respond(c, 200, target)
}

func (a *API) createScanPlan(c fiber.Ctx) error {
	var in struct {
		Name     string            `json:"name"`
		TargetID string            `json:"target_id"`
		Settings *ScanPlanSettings `json:"settings"`
	}
	if err := decode(c, &in); err != nil {
		return bad(c, err)
	}
	if !validName(in.Name) || in.TargetID == "" {
		return bad(c, fmt.Errorf("scan plan name and target_id are required"))
	}
	target, err := a.Store.GetTarget(c.Context(), in.TargetID)
	if err != nil {
		return problem(c, err)
	}
	if target.ArchivedAt != nil {
		return problem(c, ErrConflict)
	}
	settings := DefaultScanPlanSettings()
	if in.Settings != nil {
		settings = *in.Settings
	}
	if _, _, err = settings.Config(target.Inputs()); err != nil {
		return bad(c, err)
	}
	plan, err := a.Store.CreateScanPlan(c.Context(), in.Name, in.TargetID, settings)
	if err != nil {
		return problem(c, err)
	}
	c.Set("Location", "/v1/scan-plans/"+plan.ID)
	return respond(c, 201, plan)
}

func (a *API) getScanPlan(c fiber.Ctx) error {
	plan, err := a.Store.GetScanPlan(c.Context(), c.Params("id"))
	if err != nil {
		return problem(c, err)
	}
	return respond(c, 200, plan)
}

func (a *API) listScanPlans(c fiber.Ctx) error {
	limit, err := resourceLimit(c)
	if err != nil {
		return bad(c, err)
	}
	plans, err := a.Store.ListScanPlans(c.Context(), c.Query("after"), limit)
	if err != nil {
		return problem(c, err)
	}
	return respond(c, 200, plans)
}

func (a *API) patchScanPlan(c fiber.Ctx) error {
	var in struct {
		Revision int64                      `json:"revision"`
		Name     *string                    `json:"name"`
		TargetID *string                    `json:"target_id"`
		Settings map[string]json.RawMessage `json:"settings"`
	}
	if err := decode(c, &in); err != nil {
		return bad(c, err)
	}
	if in.Revision < 1 || in.Name == nil && in.TargetID == nil && len(in.Settings) == 0 {
		return bad(c, fmt.Errorf("revision and changed attributes are required"))
	}
	plan, err := a.Store.GetScanPlan(c.Context(), c.Params("id"))
	if err != nil {
		return problem(c, err)
	}
	if plan.Revision != in.Revision {
		return problem(c, ErrConflict)
	}
	if in.Name != nil {
		plan.Name = *in.Name
	}
	if in.TargetID != nil {
		plan.TargetID = *in.TargetID
	}
	if len(in.Settings) > 0 {
		data, _ := json.Marshal(plan.Settings)
		var fields map[string]json.RawMessage
		if err = json.Unmarshal(data, &fields); err != nil {
			return problem(c, err)
		}
		for key, value := range in.Settings {
			if _, ok := fields[key]; !ok {
				return bad(c, fmt.Errorf("unknown scan plan setting %q", key))
			}
			fields[key] = value
		}
		data, err = json.Marshal(fields)
		if err != nil {
			return problem(c, err)
		}
		if err = json.Unmarshal(data, &plan.Settings); err != nil {
			return bad(c, fmt.Errorf("invalid scan plan settings"))
		}
	}
	if !validName(plan.Name) || plan.TargetID == "" {
		return bad(c, fmt.Errorf("scan plan name and target_id are required"))
	}
	target, err := a.Store.GetTarget(c.Context(), plan.TargetID)
	if err != nil {
		return problem(c, err)
	}
	if target.ArchivedAt != nil {
		return problem(c, ErrConflict)
	}
	if _, _, err = plan.Settings.Config(target.Inputs()); err != nil {
		return bad(c, err)
	}
	plan, err = a.Store.UpdateScanPlan(c.Context(), plan, in.Revision)
	if err != nil {
		return problem(c, err)
	}
	return respond(c, 200, plan)
}

func (a *API) listPlanRuns(c fiber.Ctx) error {
	limit, err := resourceLimit(c)
	if err != nil {
		return bad(c, err)
	}
	runs, err := a.Store.ListRunsForPlan(c.Context(), c.Params("id"), c.Query("after"), limit)
	if err != nil {
		return problem(c, err)
	}
	return respond(c, 200, runs)
}
