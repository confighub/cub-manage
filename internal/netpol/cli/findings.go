// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"sort"

	"github.com/spf13/cobra"

	"github.com/confighub/cub-manage/internal/cub"
	"github.com/confighub/cub-manage/internal/kit"
	"github.com/confighub/cub-manage/internal/netpol"
	"github.com/confighub/cub-manage/internal/netpol/snapshot"
)

// analyzers are the analyzers findings runs, which --analyzer names.
var analyzers = []string{"missing-default-deny-ingress", "uncovered-ingress", "allow-all", "metadata-egress", "ingress-egress-asymmetry"}

func newFindingsCmd() *cobra.Command {
	var output string
	var filter kit.FilterFlags
	var only kit.FindingFilter
	cmd := &cobra.Command{
		Use:   "findings",
		Short: "Report NetworkPolicy hygiene issues across the fleet, most severe first",
		Long: `findings analyzes the fleet and reports NetworkPolicy issues, most severe first:

  missing-default-deny-ingress  (high) a namespace with workloads but no default-deny ingress
  uncovered-ingress             (high) a workload no ingress NetworkPolicy selects
  metadata-egress               (high) an egress ipBlock that reaches the cloud metadata IP
                                169.254.169.254
  allow-all                     (medium) a policy rule with an empty from/to, which admits
                                every peer
  ingress-egress-asymmetry      (medium) one side allows a flow the other side drops

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

// findings is every NetworkPolicy finding in the snapshot, cluster by cluster, in
// the shared form.
func findings(snap *snapshot.Snapshot) []kit.Finding {
	names := make([]string, 0, len(snap.Clusters))
	for n := range snap.Clusters {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []kit.Finding
	for _, cn := range names {
		for _, f := range netpol.AnalyzeFindings(snap.Clusters[cn]) {
			out = append(out, kit.Finding{
				Severity: f.Severity, Analyzer: f.Analyzer, Cluster: f.Cluster, Namespace: f.Namespace,
				Kind: f.Kind, Name: f.Resource, Space: f.Origin.Space, Unit: f.Origin.UnitSlug, Message: f.Message,
			})
		}
	}
	return out
}
