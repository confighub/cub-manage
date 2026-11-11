// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/confighub/sdk/core/cubapi"
	goclientnew "github.com/confighub/sdk/core/openapi/goclient-new"
	"github.com/confighub/sdk/managerkit/write"

	"github.com/confighub/cub-manage/internal/cub"
	"github.com/confighub/cub-manage/internal/netpol"
	"github.com/confighub/cub-manage/internal/netpol/snapshot"
)

// unitManagedByLabel marks Units this tool authors.
const unitManagedByLabel = "cub manage netpol"

// createDest is the resolved Space (and Target) a generated policy Unit should
// be created in, so it sits alongside the workloads it governs and deploys to
// the same cluster.
type createDest struct {
	spaceID    goclientnew.UUID
	spaceSlug  string
	targetSlug string
	cluster    string
}

// resolveCreateDest infers the Space/Target for a namespace from the managed
// workloads in it. It requires the namespace to contain at least one managed
// workload, and a single Space (use --cluster / --space to disambiguate).
func resolveCreateDest(snap *snapshot.Snapshot, namespace, clusterFilter, spaceOverride string) (createDest, error) {
	type cand struct{ cluster, spaceID, spaceSlug, targetSlug string }
	var cands []cand
	seenSpace := map[string]bool{}
	for clusterName, c := range snap.Clusters {
		if clusterFilter != "" && clusterName != clusterFilter {
			continue
		}
		for _, w := range c.Workloads {
			if netpol.NamespaceOf(w.Namespace) != namespace {
				continue
			}
			meta, ok := snap.Units[w.Origin.UnitID]
			if !ok {
				continue
			}
			if spaceOverride != "" && meta.SpaceSlug != spaceOverride {
				continue
			}
			if seenSpace[meta.SpaceID] {
				continue
			}
			seenSpace[meta.SpaceID] = true
			cands = append(cands, cand{clusterName, meta.SpaceID, meta.SpaceSlug, meta.TargetSlug})
		}
	}
	if len(cands) == 0 {
		return createDest{}, fmt.Errorf("no managed workloads found in namespace %q%s — cannot infer the Space to create the policy in (try --space, or --cluster)",
			namespace, clusterNote(clusterFilter, spaceOverride))
	}
	clusters := map[string]bool{}
	for _, c := range cands {
		clusters[c.cluster] = true
	}
	if clusterFilter == "" && len(clusters) > 1 {
		return createDest{}, fmt.Errorf("namespace %q exists in multiple clusters (%s); specify --cluster", namespace, strings.Join(sortedKeys(clusters), ", "))
	}
	if len(cands) > 1 {
		slugs := make([]string, 0, len(cands))
		for _, c := range cands {
			slugs = append(slugs, c.spaceSlug)
		}
		sort.Strings(slugs)
		return createDest{}, fmt.Errorf("namespace %q spans multiple Spaces (%s); specify --space", namespace, strings.Join(slugs, ", "))
	}

	c := cands[0]
	sid, err := uuid.Parse(c.spaceID)
	if err != nil {
		return createDest{}, fmt.Errorf("parse space id %q: %w", c.spaceID, err)
	}
	return createDest{spaceID: sid, spaceSlug: c.spaceSlug, targetSlug: c.targetSlug, cluster: c.cluster}, nil
}

// policyCreation is the Unit a generated policy is made as, in the Space its
// workloads live in.
func policyCreation(dest createDest, slug, manifest string) cub.Creation {
	return cub.Creation{
		SpaceSlug: dest.spaceSlug,
		Unit: goclientnew.Unit{
			Slug:          slug,
			DisplayName:   slug,
			ToolchainType: "Kubernetes/YAML",
			SpaceID:       dest.spaceID,
			Labels:        map[string]string{"managed-by": unitManagedByLabel},
		},
		Data: manifest,
	}
}

// ensurePolicy makes one generated policy Unit and reports it.
func ensurePolicy(cmd *cobra.Command, client *cubapi.Client, command string, dest createDest, slug, manifest string, ch cubapi.Change, output string) error {
	res := cub.EnsureUnits(cmd.Context(), client, []cub.Creation{policyCreation(dest, slug, manifest)}, ch)
	return write.ReportMutations(cmd, command, dest.spaceSlug, ch.DryRun(), output, res)
}

// destFromUnitMeta builds a createDest from an existing Unit's metadata — used
// when the target Space/Target are already known (e.g. the producer Unit a Link
// points to), so no namespace inference is needed.
func destFromUnitMeta(meta snapshot.UnitMeta) (createDest, error) {
	sid, err := uuid.Parse(meta.SpaceID)
	if err != nil {
		return createDest{}, fmt.Errorf("parse space id %q: %w", meta.SpaceID, err)
	}
	return createDest{spaceID: sid, spaceSlug: meta.SpaceSlug, targetSlug: meta.TargetSlug}, nil
}

func clusterNote(clusterFilter, spaceOverride string) string {
	var parts []string
	if clusterFilter != "" {
		parts = append(parts, "cluster "+clusterFilter)
	}
	if spaceOverride != "" {
		parts = append(parts, "space "+spaceOverride)
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// findPolicy locates a NetworkPolicy entity by its Space slug and Unit slug.
func findPolicy(snap *snapshot.Snapshot, space, unit string) *netpol.NetworkPolicyEntity {
	for _, c := range snap.Clusters {
		for _, np := range c.NetworkPolicies {
			if np.Origin.Space == space && np.Origin.UnitSlug == unit {
				return np
			}
		}
	}
	return nil
}
