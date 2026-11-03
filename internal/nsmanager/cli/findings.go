// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"github.com/spf13/cobra"

	"github.com/confighub/cub-manage/internal/cub"
	"github.com/confighub/cub-manage/internal/kit"
	"github.com/confighub/cub-manage/internal/nsmanager"
	"github.com/confighub/cub-manage/internal/nsmanager/snapshot"
)

// analyzers are the analyzers findings runs, which --analyzer names.
var analyzers = []string{"duplicate-namespace", "missing-namespace-object", "missing-pod-security", "namespace-name-inconsistent", "pod-security-inconsistent"}

func newFindingsCmd() *cobra.Command {
	var output string
	var filter kit.FilterFlags
	var only kit.FindingFilter
	cmd := &cobra.Command{
		Use:   "findings",
		Short: "Report namespace governance issues across the fleet, most severe first",
		Long: `findings analyzes the fleet and reports namespace issues, most severe first:

  duplicate-namespace          (high) two Namespace Units with one name on one Target
  missing-namespace-object     (medium) a namespace with workloads but no v1/Namespace Unit
                               (built-in namespaces excepted)
  missing-pod-security         (medium) a Namespace with no pod-security enforce label
  namespace-name-inconsistent  (medium) a Component that runs a workload in different
                               namespaces on different clusters
  pod-security-inconsistent    (low) a Component whose pod-security level differs between
                               its variants

These are properties of the whole set of resources across the fleet, which neither a
per-resource validator nor a tenancy controller in one cluster can see.

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

// findings is every namespace finding in the snapshot, in the shared form.
func findings(snap *snapshot.Snapshot) []kit.Finding {
	var out []kit.Finding
	for _, f := range nsmanager.AnalyzeFindings(snap.Clusters) {
		x := kit.Finding{
			Severity: f.Severity, Analyzer: f.Analyzer, Cluster: f.Cluster, Namespace: f.Namespace,
			Component: f.Component, Message: f.Message,
		}
		if f.Namespace != "" {
			x.Kind, x.Name = "Namespace", f.Namespace
		}
		out = append(out, x)
	}
	return out
}
