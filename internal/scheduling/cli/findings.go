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

// analyzers are the analyzers findings runs, which --analyzer names.
var analyzers = []string{"toleration-without-placement"}

func newFindingsCmd() *cobra.Command {
	var output string
	var filter kit.FilterFlags
	var only kit.FindingFilter
	cmd := &cobra.Command{
		Use:   "findings",
		Short: "Report placement issues across the fleet, most severe first",
		Long: `findings analyzes the fleet and reports placement issues, most severe first:

  placement  a workload that tolerates a taint but does not constrain where it lands
             (no nodeSelector and no required node affinity), so it may schedule onto
             general nodes, which is rarely what the toleration was for

Checks that need a cluster's node pools and taints wait for those facts to be
recorded on its Target.

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
