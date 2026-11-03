// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cub

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/confighub/sdk/core/cubapi"
	goclientnew "github.com/confighub/sdk/core/openapi/goclient-new"
)

// UnitRef resolves a <space>/<unit> to its Space and Unit IDs.
type UnitRef struct {
	SpaceID   goclientnew.UUID
	SpaceSlug string
	UnitID    goclientnew.UUID
	UnitSlug  string
}

// ResolveUnit resolves a Space slug + Unit slug to their IDs.
func ResolveUnit(ctx context.Context, c *cubapi.Client, spaceSlug, unitSlug string) (UnitRef, error) {
	sp, err := cubapi.ResolveSpace(ctx, c, cubapi.ParseRef(spaceSlug), cubapi.ResolveOpts{})
	if err != nil {
		return UnitRef{}, fmt.Errorf("resolve space %q: %w", spaceSlug, err)
	}
	units, err := cubapi.ListUnits(ctx, c,
		cubapi.NewWhere(fmt.Sprintf("SpaceID = '%s' AND Slug = '%s'", sp.Space.SpaceID.String(), unitSlug)),
		cubapi.ListOpts{Select: "Slug,SpaceID,UnitID"})
	if err != nil {
		return UnitRef{}, err
	}
	for _, eu := range units {
		if eu.Unit != nil && eu.Unit.Slug == unitSlug {
			return UnitRef{SpaceID: sp.Space.SpaceID, SpaceSlug: spaceSlug, UnitID: eu.Unit.UnitID, UnitSlug: unitSlug}, nil
		}
	}
	return UnitRef{}, fmt.Errorf("unit %q not found in space %q: %w", unitSlug, spaceSlug, errUnitNotFound)
}

var errUnitNotFound = errors.New("no such unit")

// rawBodyErr reports failure for an endpoint whose success body is the configuration
// itself. cubapi.IsAPIError cannot be used for those: it treats a nil JSON200 field as a
// failure, and a raw-body response has no JSON200 to be non-nil.
func rawBodyErr(err error, resp interface {
	StatusCode() int
	Status() string
}) error {
	if err != nil {
		return err
	}
	if resp == nil {
		return fmt.Errorf("no response from server")
	}
	if resp.StatusCode() != http.StatusOK {
		return fmt.Errorf("request failed: %s", resp.Status())
	}
	return nil
}

// GetUnitData returns a Unit's config data as text, and the ETag it was served
// with, which a write made from it passes to UpdateUnitData so that it cannot
// land over a write made in between. Configuration is not a field of a Unit — it
// is read through the Unit's own data endpoint.
func GetUnitData(ctx context.Context, c *cubapi.Client, ref UnitRef) (data, etag string, err error) {
	res, err := c.API.DownloadUnitDataWithResponse(ctx, ref.SpaceID, ref.UnitID)
	if err := rawBodyErr(err, res); err != nil {
		return "", "", fmt.Errorf("get data for unit %s/%s: %w", ref.SpaceSlug, ref.UnitSlug, err)
	}
	return string(res.Body), res.HTTPResponse.Header.Get("ETag"), nil
}
