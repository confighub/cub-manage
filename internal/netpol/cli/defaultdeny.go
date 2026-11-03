// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/confighub/sdk/cliutil"
	"github.com/confighub/sdk/core/cubapi"
	"github.com/confighub/sdk/managerkit/write"

	"github.com/confighub/cub-manage/internal/cub"
	"github.com/confighub/cub-manage/internal/kit"
	"github.com/confighub/cub-manage/internal/netpol"
	"github.com/confighub/cub-manage/internal/netpol/snapshot"
)

func newDefaultDenyCmd() *cobra.Command {
	var output, clusterFilter, spaceOverride string
	var egress bool
	var filter kit.FilterFlags
	var commit cliutil.CommitFlags
	cmd := &cobra.Command{
		Use:   "default-deny [<namespace>]",
		Short: "Make a default-deny NetworkPolicy Unit for a namespace, or for every namespace that lacks one",
		Long: `default-deny generates a default-deny NetworkPolicy (podSelector {}, selecting
all pods) and makes it a Unit alongside the namespace's workloads, in the Space
they live in -- closing the "namespace has no default-deny" coverage gap as data.

With a namespace, it makes that namespace's policy. The Space (and cluster) are
inferred from the namespace's workloads; use --cluster or --space to choose when
the namespace is in more than one.

Without one, it makes a policy for every namespace in the fleet scope (--where,
--component, the label shorthands, --cluster) that has workloads but no
default-deny -- with ingress alone, the gap the missing-default-deny-ingress
finding reports. A namespace whose Space cannot be inferred is reported, not
guessed.

By default the policy denies all ingress. With --egress it also denies egress but
allows DNS to kube-dns, since a bare egress default-deny breaks name resolution.

A namespace that already has a default-deny -- a policy selecting every pod that
isolates ingress, and egress with --egress, and admits no traffic to every peer
-- is left as it is, whatever its Unit is named. When another Unit's policy
denies ingress and --egress is asked for, the Unit default-deny-egress-<namespace>
adds the egress half beside it. Otherwise the Unit default-deny-<namespace> is
created, or brought up to date if it exists, so re-running changes nothing.`,
		Example: `  cub manage netpol default-deny payments
  cub manage netpol default-deny --environment prod
  cub manage netpol default-deny --cluster prod-east --commit --change-desc "close ingress gaps"`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			summary := "create default-deny NetworkPolicies for the namespaces that lack one"
			if len(args) == 1 {
				summary = fmt.Sprintf("create default-deny NetworkPolicy for namespace %s", args[0])
			} else if spaceOverride != "" {
				return fmt.Errorf("--space chooses among the Spaces of one namespace; name the namespace, or scope the fleet with --where")
			}
			changeDesc, dryRun, err := commit.Validate(summary)
			if err != nil {
				return err
			}
			client, err := cub.Preflight(cmd.Context())
			if err != nil {
				return err
			}
			where, err := filter.Predicate()
			if err != nil {
				return err
			}
			snap, err := snapshot.Load(cmd.Context(), client, where)
			if err != nil {
				return err
			}
			var creations []cub.Creation
			var others []cubapi.UnitOutcome
			space := ""
			if len(args) == 1 {
				dest, err := resolveCreateDest(snap, args[0], clusterFilter, spaceOverride)
				if err != nil {
					return err
				}
				space = dest.spaceSlug
				creation, existing := planNamespace(snap.Clusters[dest.cluster], dest, args[0], egress)
				if creation != nil {
					creations = append(creations, *creation)
				}
				others = existing
			} else {
				creations, others = planDefaultDenies(snap, clusterFilter, egress)
			}
			res := cub.EnsureUnits(cmd.Context(), client, creations, write.Change(changeDesc, dryRun))
			res.Outcomes = append(res.Outcomes, others...)
			return write.ReportMutations(cmd, "default-deny", space, dryRun, output, res)
		},
	}
	kit.AddOutputFlag(cmd, &output)
	kit.AddFilterFlags(cmd, &filter)
	kit.AddClusterFlag(cmd, &clusterFilter)
	cmd.Flags().StringVar(&spaceOverride, "space", "", "with a namespace, the Space to create the Unit in when the namespace spans more than one")
	cmd.Flags().BoolVar(&egress, "egress", false, "also deny egress (allowing DNS egress to kube-dns)")
	commit.Bind(cmd)
	return cmd
}

// planNamespace is what default-deny does for one namespace: the policy Unit to
// make or bring up to date, or none, with the Units whose policies already deny
// what was asked, reported as unchanged.
func planNamespace(c *netpol.ClusterNetpol, dest createDest, namespace string, egress bool) (*cub.Creation, []cubapi.UnitOutcome) {
	var ingressDenies, egressDenies []*netpol.NetworkPolicyEntity
	if c != nil {
		ingressDenies, egressDenies = c.DefaultDenies(namespace)
	}
	slug, manifest := netpol.DefaultDenyYAML(namespace, egress)
	switch {
	case len(ingressDenies) > 0 && (!egress || len(egressDenies) > 0):
		return nil, unchanged(covering(ingressDenies, egressDenies, egress))
	case len(ingressDenies) > 0 && !slices.ContainsFunc(ingressDenies, func(p *netpol.NetworkPolicyEntity) bool {
		return p.Origin.Space == dest.spaceSlug && p.Origin.UnitSlug == slug
	}):
		// Another Unit's policy denies ingress, and may well be named
		// default-deny: add the egress half under a name of its own.
		slug, manifest = netpol.DefaultDenyEgressYAML(namespace)
	}
	creation := policyCreation(dest, slug, manifest)
	return &creation, nil
}

// covering is the policies to name as a namespace's default-deny: the first that
// denies ingress, and with egress, preferably one that denies both.
func covering(ingressDenies, egressDenies []*netpol.NetworkPolicyEntity, egress bool) []*netpol.NetworkPolicyEntity {
	if !egress {
		return ingressDenies[:1]
	}
	for _, p := range ingressDenies {
		if slices.Contains(egressDenies, p) {
			return []*netpol.NetworkPolicyEntity{p}
		}
	}
	return []*netpol.NetworkPolicyEntity{ingressDenies[0], egressDenies[0]}
}

// unchanged reports the Units of policies as left as they are.
func unchanged(policies []*netpol.NetworkPolicyEntity) []cubapi.UnitOutcome {
	out := make([]cubapi.UnitOutcome, 0, len(policies))
	for _, p := range policies {
		out = append(out, cubapi.UnitOutcome{
			UnitID: p.Origin.UnitID, SpaceID: p.Origin.SpaceID, SpaceSlug: p.Origin.Space,
			UnitSlug: p.Origin.UnitSlug, Success: true,
		})
	}
	return out
}

// planDefaultDenies is a default-deny policy for every namespace in the snapshot
// that has workloads but not the default-deny asked for, and an outcome
// reporting each such namespace whose Space cannot be inferred.
func planDefaultDenies(snap *snapshot.Snapshot, clusterFilter string, egress bool) ([]cub.Creation, []cubapi.UnitOutcome) {
	var creations []cub.Creation
	var unresolved []cubapi.UnitOutcome
	clusters := make([]string, 0, len(snap.Clusters))
	for name := range snap.Clusters {
		clusters = append(clusters, name)
	}
	sort.Strings(clusters)
	for _, cluster := range clusters {
		if clusterFilter != "" && cluster != clusterFilter {
			continue
		}
		nss, _ := snap.Clusters[cluster].Coverage()
		for _, nc := range nss {
			if nc.Workloads == 0 || (nc.DefaultDenyIngress && (!egress || nc.DefaultDenyEgress)) {
				continue // covered, or no workloads to protect
			}
			dest, err := resolveCreateDest(snap, nc.Namespace, cluster, "")
			if err != nil {
				slug, _ := netpol.DefaultDenyYAML(nc.Namespace, egress)
				// Keyed by cluster and namespace: there is no Unit to key it by.
				reason := strings.TrimSuffix(err.Error(), "; specify --space")
				unresolved = append(unresolved, cubapi.UnitOutcome{
					UnitID: "unresolved:" + cluster + "/" + nc.Namespace, UnitSlug: slug,
					Error: fmt.Sprintf("cluster %s: %s; choose with 'default-deny %s --cluster %s --space <space>'",
						cluster, reason, nc.Namespace, cluster),
				})
				continue
			}
			if creation, _ := planNamespace(snap.Clusters[cluster], dest, nc.Namespace, egress); creation != nil {
				creations = append(creations, *creation)
			}
		}
	}
	return creations, unresolved
}
