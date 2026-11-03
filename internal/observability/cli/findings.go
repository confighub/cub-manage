// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"github.com/spf13/cobra"

	"github.com/confighub/cub-manage/internal/cub"
	"github.com/confighub/cub-manage/internal/kit"
	"github.com/confighub/cub-manage/internal/observability"
	"github.com/confighub/cub-manage/internal/observability/snapshot"
)

// analyzers are the analyzers findings runs, which --analyzer names.
var analyzers = []string{"unmonitored-service", "dangling-servicemonitor"}

func newFindingsCmd() *cobra.Command {
	var output string
	var filter kit.FilterFlags
	var only kit.FindingFilter
	cmd := &cobra.Command{
		Use:   "findings",
		Short: "Report observability gaps across the fleet, most severe first",
		Long: `findings analyzes the fleet and reports observability gaps, most severe first:

  unmonitored-service      (medium) a Service that exposes metrics but no ServiceMonitor selects
  dangling-servicemonitor  (low) a ServiceMonitor that selects no Service in its namespace

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

// findings is every observability finding in the snapshot, in the shared form.
func findings(snap *snapshot.Snapshot) []kit.Finding {
	var out []kit.Finding
	for _, f := range observability.Findings(snap.Clusters) {
		out = append(out, kit.Finding{
			Severity: f.Severity, Analyzer: f.Analyzer, Cluster: f.Cluster, Namespace: f.Namespace,
			Kind: f.Kind, Name: f.Name, Space: f.Space, Unit: f.UnitSlug, Message: f.Message,
		})
	}
	return out
}
