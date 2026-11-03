// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

// Package snapshot loads the fleet-wide view of the Kubernetes config the namespace envelope covers and assembles it into the
// nsmanager analysis model.
//
// Reading the fleet is cubapi.SnapshotLoader's job. What is tool-specific is which
// resource types the model needs and what a resource becomes once it arrives.
package snapshot

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/confighub/sdk/core/cubapi"

	"github.com/confighub/cub-manage/internal/nsmanager"
)

// UnitMeta is the per-Unit metadata joined onto resources.
type UnitMeta = cubapi.UnitMeta

// ClusterNone is the cluster key for Units the fleet view cannot attribute to a
// cluster.
const ClusterNone = cubapi.ClusterNone

// Snapshot is a fleet-wide namespace-envelope view.
type Snapshot struct {
	// Clusters holds the analysis entities per cluster, excluding canonical
	// (base/policy) definitions.
	Clusters map[string]*nsmanager.ClusterNamespaces
	// Resources is every parsed resource, including canonical ones.
	Resources []nsmanager.FleetResource
	// Units is in-scope Unit metadata by UnitID.
	Units map[string]UnitMeta
	// Filter is the ConfigHub Unit `where` predicate the snapshot was scoped by
	// (empty = the whole fleet the user can view).
	Filter string `json:"filter,omitempty"`
}

// loader names the resource types the envelope model needs: the namespace itself and
// everything the envelope is meant to contain.
var loader = cubapi.SnapshotLoader[nsmanager.FleetResource]{
	ResourceTypes: []string{
		"v1/Namespace",
		"apps/v1/Deployment",
		"apps/v1/StatefulSet",
		"apps/v1/DaemonSet",
		"apps/v1/ReplicaSet",
		"batch/v1/Job",
		"batch/v1/CronJob",
		"batch/v1beta1/CronJob",
		"v1/Pod",
	},
	New: func(o cubapi.Origin, doc map[string]any) nsmanager.FleetResource {
		return nsmanager.FleetResource{
			Origin: nsmanager.ResourceOrigin{
				Cluster:      o.Cluster,
				Target:       o.Target,
				Space:        o.Space,
				SpaceID:      o.SpaceID,
				SpaceLabels:  o.SpaceLabels,
				UnitID:       o.UnitID,
				UnitSlug:     o.UnitSlug,
				ResourceName: o.ResourceName,
				Canonical:    o.Canonical,
			},
			Doc: doc,
		}
	},
}

// Load fetches and assembles the fleet snapshot, scoped by a single ConfigHub
// Unit `where` predicate (empty = everything the user can view).
func Load(ctx context.Context, c *cubapi.Client, where string) (*Snapshot, error) {
	snap, err := loader.Load(ctx, c, where)
	if err != nil {
		return nil, err
	}
	if err := setComponents(ctx, c, snap.Resources); err != nil {
		return nil, err
	}
	// Canonical definitions stay out of cluster analysis.
	var forAnalysis []nsmanager.FleetResource
	for _, r := range snap.Resources {
		if !r.Origin.Canonical {
			forAnalysis = append(forAnalysis, r)
		}
	}
	return &Snapshot{
		Clusters:  nsmanager.BuildFleet(forAnalysis),
		Resources: snap.Resources,
		Units:     snap.Units,
		Filter:    snap.Filter,
	}, nil
}

// BaseWhere selects the Units of base Spaces: those with no release Target, which deploy
// nowhere. It is the Spaces' own record of that, so it needs no label to be set on them.
const BaseWhere = "Space.ReleaseTargetID IS NULL"

// LoadBases is Load for the Units of base Spaces (BaseWhere), with each Space as the cluster
// of its resources: a base deploys nowhere, so each is analyzed on its own.
func LoadBases(ctx context.Context, c *cubapi.Client, where string) (*Snapshot, error) {
	snap, err := loader.Load(ctx, c, cubapi.NewWhere(where).And(BaseWhere).String())
	if err != nil {
		return nil, err
	}
	resources := make([]nsmanager.FleetResource, len(snap.Resources))
	for i, r := range snap.Resources {
		r.Origin.Cluster = r.Origin.Space
		resources[i] = r
	}
	clusters := nsmanager.BuildFleet(resources)
	for _, c := range clusters {
		c.Base = true
	}
	return &Snapshot{
		Clusters:  clusters,
		Resources: resources,
		Units:     snap.Units,
		Filter:    snap.Filter,
	}, nil
}

// setComponents records on each resource the slug of the Component its Space is
// a Variant of, which consistency groups by. A Space names its Component by ID,
// so this reads the in-scope Spaces' ComponentIDs and then the Components' slugs.
// idsPerQuery bounds how many IDs one IN filter names, so that the query string
// stays within what the server accepts however large the fleet.
const idsPerQuery = 100

func setComponents(ctx context.Context, c *cubapi.Client, resources []nsmanager.FleetResource) error {
	spaceIDs := map[string]bool{}
	for _, r := range resources {
		spaceIDs[r.Origin.SpaceID] = true
	}
	if len(spaceIDs) == 0 {
		return nil
	}
	componentOfSpace := map[string]string{}
	componentIDs := map[string]bool{}
	for ids := range slices.Chunk(slices.Sorted(maps.Keys(spaceIDs)), idsPerQuery) {
		spaces, err := cubapi.ListSpaces(ctx, c, cubapi.Where{}.InStrings("SpaceID", ids),
			cubapi.ListOpts{Select: "SpaceID,ComponentID"})
		if err != nil {
			return fmt.Errorf("list Spaces for their Components: %w", err)
		}
		for _, es := range spaces {
			if es.Space == nil || es.Space.ComponentID == nil {
				continue
			}
			id := es.Space.ComponentID.String()
			componentOfSpace[es.Space.SpaceID.String()] = id
			componentIDs[id] = true
		}
	}
	if len(componentIDs) == 0 {
		return nil
	}
	slugs := map[string]string{}
	for ids := range slices.Chunk(slices.Sorted(maps.Keys(componentIDs)), idsPerQuery) {
		components, err := cubapi.ListComponents(ctx, c, cubapi.Where{}.InStrings("ComponentID", ids),
			cubapi.ListOpts{Select: "ComponentID,Slug"})
		if err != nil {
			return fmt.Errorf("list Components: %w", err)
		}
		for _, ec := range components {
			if ec.Component != nil {
				slugs[ec.Component.ComponentID.String()] = ec.Component.Slug
			}
		}
	}
	for i := range resources {
		resources[i].Origin.Component = slugs[componentOfSpace[resources[i].Origin.SpaceID]]
	}
	return nil
}
