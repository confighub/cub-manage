// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"github.com/spf13/cobra"

	"github.com/confighub/cub-manage/internal/cub"
	"github.com/confighub/cub-manage/internal/kit"
	"github.com/confighub/cub-manage/internal/scheduling"
	"github.com/confighub/cub-manage/internal/scheduling/snapshot"
)

func newFindingsCmd() *cobra.Command {
	var output string
	var filter kit.FilterFlags
	var only kit.FindingFilter
	cmd := &cobra.Command{
		Use:   "findings",
		Short: "Report scheduling-constraint issues across the fleet, most severe first",
		Long: `findings analyzes the fleet and reports scheduling-constraint issues, most
severe first:

  toleration-without-placement      tolerates a taint but does not constrain where it
                                    lands (no nodeSelector and no required node
                                    affinity), so it may schedule onto general nodes
  spread-min-domains-not-hard       minDomains without DoNotSchedule, which the API
                                    server rejects
  unknown-topology-key              a spread constraint or pod (anti-)affinity term
                                    on a node label no node in the cluster carries:
                                    a hard spread constraint leaves the Pods Pending,
                                    anything else has no effect
  spread-selector-misses-own-pods   a spread constraint whose labelSelector does not
                                    select the workload's own Pods, so they never
                                    count toward the skew
  spread-min-domains-exceeds-zones  minDomains above the cluster's zone count, with
                                    more replicas than the zones can then hold
  spread-rollout-without-match-label-keys
                                    a Deployment's hard spread constraint without
                                    matchLabelKeys [pod-template-hash], so a rollout
                                    counts both revisions' Pods together
  required-anti-affinity-per-node   required pod anti-affinity per node, which blocks
                                    scaling past the node count and a rollout's surge

The checks on node labels and zones use the facts collected onto each cluster's
Target, and are skipped where those were not collected. Checks that need a
cluster's node pools and taints wait for those facts.

Narrow the report with --severity, --analyzer, --cluster and --namespace.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := only.Validate(); err != nil {
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
			return kit.RenderFindings(cmd, output, kit.NewFindingsReport(findings(snap), only, snap.Filter))
		},
	}
	kit.AddOutputFlag(cmd, &output)
	kit.AddFilterFlags(cmd, &filter)
	kit.AddFindingFlags(cmd, &only, scheduling.Analyzers...)
	return cmd
}

// findings is every placement finding in the snapshot, in the shared form.
func findings(snap *snapshot.Snapshot) []kit.Finding {
	var out []kit.Finding
	for _, f := range scheduling.Findings(snap.Clusters) {
		out = append(out, kit.Finding{
			Severity: f.Severity, Analyzer: f.Analyzer, Cluster: f.Cluster, Namespace: f.Namespace,
			Kind: f.Kind, Name: f.Name, Space: f.Space, Unit: f.UnitSlug, Message: f.Message,
		})
	}
	return out
}
