// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"github.com/spf13/cobra"

	"github.com/confighub/cub-manage/internal/autoscale"
	"github.com/confighub/cub-manage/internal/autoscale/snapshot"
	"github.com/confighub/cub-manage/internal/cub"
	"github.com/confighub/cub-manage/internal/kit"
)

// analyzers are the analyzers findings runs, which --analyzer names.
var analyzers = []string{"pinned-autoscaler", "pdb-blocks-min-scale", "missing-autoscaler"}

func newFindingsCmd() *cobra.Command {
	var output string
	var filter kit.FilterFlags
	var only kit.FindingFilter
	cmd := &cobra.Command{
		Use:   "findings",
		Short: "Report autoscaling issues across the fleet, most severe first",
		Long: `findings analyzes the fleet and reports autoscaling issues, most severe first:

  pinned-autoscaler     (medium) an HPA or ScaledObject with min == max, which cannot scale
  pdb-blocks-min-scale  (medium) a PodDisruptionBudget whose minAvailable blocks all
                        voluntary eviction when the workload is at its autoscaler's
                        minReplicas
  missing-autoscaler         (low) a Deployment or StatefulSet no autoscaler targets

Canonical base and policy Spaces are excluded from analysis.

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
	kit.AddFindingFlags(cmd, &only, analyzers...)
	return cmd
}

// findings is every autoscaling finding in the snapshot, in the shared form.
func findings(snap *snapshot.Snapshot) []kit.Finding {
	var out []kit.Finding
	for _, f := range autoscale.Findings(snap.Clusters) {
		out = append(out, kit.Finding{
			Severity: f.Severity, Analyzer: f.Analyzer, Cluster: f.Cluster, Namespace: f.Namespace,
			Kind: f.Kind, Name: f.Name, Space: f.Space, Unit: f.UnitSlug, Message: f.Message,
		})
	}
	return out
}
