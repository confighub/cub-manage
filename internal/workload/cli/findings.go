// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"github.com/spf13/cobra"

	"github.com/confighub/cub-manage/internal/cub"
	"github.com/confighub/cub-manage/internal/kit"
	"github.com/confighub/cub-manage/internal/workload"
	"github.com/confighub/cub-manage/internal/workload/snapshot"
)

// analyzers are the analyzers findings runs, which --analyzer names.
var analyzers = workload.AllChecks

func newFindingsCmd() *cobra.Command {
	var output string
	var filter kit.FilterFlags
	var only kit.FindingFilter
	cmd := &cobra.Command{
		Use:   "findings",
		Short: "Report workload readiness issues across the fleet, most severe first",
		Long: `findings flattens the fleet readiness scorecard into one finding per failed
check, most severe first. The analyzers are the checks, by scorecard dimension:

  security      privileged-container, privilege-escalation-allowed,
                missing-run-as-non-root, writable-root-filesystem,
                capabilities-not-dropped, missing-seccomp-profile,
                service-account-token-mounted
  resources     missing-memory-limit, missing-cpu-limit, missing-memory-request,
                missing-cpu-request
  probes        missing-readiness-probe, missing-liveness-probe
  hygiene       missing-termination-log-fallback
  availability  missing-pdb, pdb-blocks-eviction, missing-spread

A failing security, resources or availability check is high, a failing probe is
medium, a warning is one step lower, and hygiene is always low: privileged-container
is high, writable-root-filesystem medium.

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

// findings is every readiness finding in the snapshot, in the shared form.
func findings(snap *snapshot.Snapshot) []kit.Finding {
	var out []kit.Finding
	for _, f := range workload.Findings(snap.Clusters) {
		out = append(out, kit.Finding{
			Severity: f.Severity, Analyzer: f.Analyzer, Cluster: f.Cluster, Namespace: f.Namespace,
			Kind: f.Kind, Name: f.Name, Space: f.Space, Unit: f.UnitSlug, Message: f.Message,
		})
	}
	return out
}
