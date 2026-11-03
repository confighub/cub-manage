// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"github.com/spf13/cobra"

	"github.com/confighub/cub-manage/internal/cub"
	"github.com/confighub/cub-manage/internal/kit"
	"github.com/confighub/cub-manage/internal/rbac"
	"github.com/confighub/cub-manage/internal/rbac/snapshot"
)

// analyzers are the analyzers findings runs, which --analyzer names.
var analyzers = []string{"wildcard-rule", "privilege-escalation-verb", "risky-grant", "cluster-admin-binding", "orphaned-binding", "unbound-service-account"}

func newFindingsCmd() *cobra.Command {
	var output string
	var filter kit.FilterFlags
	var only kit.FindingFilter
	cmd := &cobra.Command{
		Use:   "findings",
		Short: "Report RBAC hygiene issues across the fleet, most severe first",
		Long: `findings analyzes the fleet and reports RBAC issues, most severe first:

  wildcard-rule              (high) wildcard verbs, resources or apiGroups in a role
  privilege-escalation-verb  (high) escalate, bind or impersonate
  cluster-admin-binding      (high, medium for a RoleBinding) a binding to cluster-admin
  risky-grant                (medium) secrets, pod exec or attach, webhook or CRD writes
  orphaned-binding           (medium) a binding whose role does not exist on the cluster
  unbound-service-account    (low) a ServiceAccount nothing binds

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

// findings is every RBAC finding in the snapshot, in the shared form.
func findings(snap *snapshot.Snapshot) []kit.Finding {
	var out []kit.Finding
	for _, f := range rbac.AnalyzeFleet(snap.Clusters) {
		out = append(out, kit.Finding{
			Severity: f.Severity, Analyzer: f.Analyzer, Cluster: f.Cluster, Namespace: f.Namespace,
			Kind: f.ResourceKind, Name: f.ResourceName, Space: f.Origin.Space, Unit: f.Origin.UnitSlug, Message: f.Message,
		})
	}
	return out
}
