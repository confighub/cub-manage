// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

// Package snapshot loads the fleet-wide view of the pod-bearing workloads placement depends on and assembles it into the
// scheduling analysis model.
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

	"github.com/confighub/cub-manage/internal/scheduling"
)

// UnitMeta is the per-Unit metadata joined onto resources.
type UnitMeta = cubapi.UnitMeta

// ClusterNone is the cluster key for Units the fleet view cannot attribute to a
// cluster.
const ClusterNone = cubapi.ClusterNone

// Snapshot is a fleet-wide placement view.
type Snapshot struct {
	// Clusters holds the analysis entities per cluster, excluding canonical
	// (base/policy) definitions.
	Clusters map[string]*scheduling.ClusterWorkloads
	// Resources is every parsed resource, including canonical ones.
	Resources []scheduling.FleetResource
	// Units is in-scope Unit metadata by UnitID.
	Units map[string]UnitMeta
	// Filter is the ConfigHub Unit `where` predicate the snapshot was scoped by
	// (empty = the whole fleet the user can view).
	Filter string `json:"filter,omitempty"`
}

// loader names the resource types the placement model needs: everything that carries a pod
// template, which is what a nodeSelector, toleration or affinity sits on.
var loader = cubapi.SnapshotLoader[scheduling.FleetResource]{
	ResourceTypes: []string{
		"apps/v1/Deployment",
		"apps/v1/StatefulSet",
		"apps/v1/DaemonSet",
		"apps/v1/ReplicaSet",
		"batch/v1/Job",
		"batch/v1/CronJob",
		"batch/v1beta1/CronJob",
		"v1/Pod",
	},
	New: func(o cubapi.Origin, doc map[string]any) scheduling.FleetResource {
		return scheduling.FleetResource{
			Origin: scheduling.ResourceOrigin{
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
	// Canonical definitions stay out of cluster analysis.
	var forAnalysis []scheduling.FleetResource
	for _, r := range snap.Resources {
		if !r.Origin.Canonical {
			forAnalysis = append(forAnalysis, r)
		}
	}
	clusters := scheduling.BuildFleet(forAnalysis)
	if err := joinTargetFacts(ctx, c, clusters, snap.Units); err != nil {
		return nil, err
	}
	return &Snapshot{
		Clusters:  clusters,
		Resources: snap.Resources,
		Units:     snap.Units,
		Filter:    snap.Filter,
	}, nil
}

// idsPerQuery bounds how many IDs one IN filter names, so that the query string
// stays within what the server accepts however large the fleet.
const idsPerQuery = 100

// joinTargetFacts reads the Target of each cluster for its facts: its zones,
// its node labels and its default spread constraints. A cluster is keyed by its
// Target's slug, so the facts are joined by slug.
func joinTargetFacts(ctx context.Context, c *cubapi.Client, clusters map[string]*scheduling.ClusterWorkloads, units map[string]UnitMeta) error {
	targetIDs := map[string]bool{}
	for _, u := range units {
		if u.TargetID != "" && clusters[u.TargetSlug] != nil {
			targetIDs[u.TargetID] = true
		}
	}
	factsBySlug := map[string]map[string]string{}
	for ids := range slices.Chunk(slices.Sorted(maps.Keys(targetIDs)), idsPerQuery) {
		targets, err := cubapi.ListTargets(ctx, c, cubapi.Where{}.InStrings("TargetID", ids),
			cubapi.ListOpts{Select: "TargetID,Slug,Facts"})
		if err != nil {
			return fmt.Errorf("list Targets for their facts: %w", err)
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
	for name, cw := range clusters {
		facts, ok := factsBySlug[name]
		if name == ClusterNone || !ok {
			cw.Facts = scheduling.UnknownClusterFacts()
			continue
		}
		cw.Facts = scheduling.NewClusterFacts(name, facts)
	}
	return nil
}
