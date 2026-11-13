// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cub

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/confighub/sdk/core/cubapi"
)

// idsPerQuery bounds how many IDs one IN filter names, so that the query string
// stays within what the server accepts however large the fleet.
const idsPerQuery = 100

// TargetFactsBySlug reads the facts of the Targets the units are bound to, keyed
// by Target slug, which is how a fleet snapshot names a cluster. Only the Targets
// whose slug wanted accepts are read. A slug two Targets share keeps the first.
func TargetFactsBySlug(ctx context.Context, c *cubapi.Client, units map[string]cubapi.UnitMeta, wanted func(slug string) bool) (map[string]map[string]string, error) {
	targetIDs := map[string]bool{}
	for _, u := range units {
		if u.TargetID != "" && wanted(u.TargetSlug) {
			targetIDs[u.TargetID] = true
		}
	}
	factsBySlug := map[string]map[string]string{}
	for ids := range slices.Chunk(slices.Sorted(maps.Keys(targetIDs)), idsPerQuery) {
		targets, err := cubapi.ListTargets(ctx, c, cubapi.Where{}.InStrings("TargetID", ids),
			cubapi.ListOpts{Select: "TargetID,Slug,Facts"})
		if err != nil {
			return nil, fmt.Errorf("list Targets for their facts: %w", err)
		}
		for _, et := range targets {
			if et.Target == nil {
				continue
			}
			if _, seen := factsBySlug[et.Target.Slug]; !seen {
				factsBySlug[et.Target.Slug] = et.Target.Facts
			}
		}
	}
	return factsBySlug, nil
}
