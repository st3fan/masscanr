package service

import (
	"fmt"

	"github.com/gofiber/fiber/v3"
)

func (a *API) createExclusion(c fiber.Ctx) error {
	var in struct {
		Name    string   `json:"name"`
		Entries []string `json:"entries"`
	}
	if err := decode(c, &in); err != nil {
		return bad(c, err)
	}
	if !validName(in.Name) {
		return bad(c, fmt.Errorf("exclusion name must contain 1..200 bytes"))
	}
	if _, err := NormalizeExclusionEntries(in.Entries); err != nil {
		return bad(c, err)
	}
	exclusion, err := a.Store.CreateExclusion(c.Context(), in.Name, in.Entries)
	if err != nil {
		return problem(c, err)
	}
	c.Set("Location", "/v1/exclusions/"+exclusion.ID)
	return respond(c, 201, exclusion)
}

func (a *API) getExclusion(c fiber.Ctx) error {
	exclusion, err := a.Store.GetExclusion(c.Context(), c.Params("id"))
	if err != nil {
		return problem(c, err)
	}
	return respond(c, 200, exclusion)
}

func (a *API) listExclusions(c fiber.Ctx) error {
	limit, err := resourceLimit(c)
	if err != nil {
		return bad(c, err)
	}
	exclusions, err := a.Store.ListExclusions(c.Context(), c.Query("after"), limit)
	if err != nil {
		return problem(c, err)
	}
	return respond(c, 200, exclusions)
}

func (a *API) patchExclusion(c fiber.Ctx) error {
	var in struct {
		Revision int64     `json:"revision"`
		Entries  *[]string `json:"entries"`
		Archived *bool     `json:"archived"`
	}
	if err := decode(c, &in); err != nil {
		return bad(c, err)
	}
	if in.Revision < 1 || in.Entries == nil && in.Archived == nil {
		return bad(c, fmt.Errorf("revision and changed attributes are required"))
	}
	if in.Entries != nil {
		if _, err := NormalizeExclusionEntries(*in.Entries); err != nil {
			return bad(c, err)
		}
	}
	if in.Archived != nil && (!*in.Archived || in.Entries != nil) {
		return bad(c, fmt.Errorf("archived may only be set to true on its own"))
	}
	exclusion, err := a.Store.UpdateExclusion(c.Context(), c.Params("id"), in.Revision, ExclusionPatch{Entries: in.Entries, Archived: in.Archived})
	if err != nil {
		return problem(c, err)
	}
	return respond(c, 200, exclusion)
}
