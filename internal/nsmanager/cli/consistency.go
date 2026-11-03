// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/confighub/sdk/managerkit"

	"github.com/confighub/cub-manage/internal/cub"
	"github.com/confighub/cub-manage/internal/kit"
	"github.com/confighub/cub-manage/internal/nsmanager"
	"github.com/confighub/cub-manage/internal/nsmanager/snapshot"
)

type consistencyReport struct {
	Components []nsmanager.ComponentConsistency `json:"components"`
	Totals     struct {
		Components   int `json:"components"`
		Consistent   int `json:"consistent"`
		Inconsistent int `json:"inconsistent"`
	} `json:"totals"`
	Filter string `json:"filter,omitempty"`
}

func newConsistencyCmd() *cobra.Command {
	var output string
	var filter kit.FilterFlags
	var issuesOnly bool
	cmd := &cobra.Command{
		Use:   "consistency",
		Short: "Cross-variant consistency: does each of a component's workloads run in the same namespace on every cluster?",
		Long: `consistency groups the fleet's namespaces by the Component their Space is a Variant of, and
reports, per component, whether each of its workloads runs in the same
namespace on every cluster, and whether its pod-security enforce level is the
same in every variant Space (environment / region / cluster).

The invariant is that a workload keeps its namespace from cluster to cluster;
this is the read side of that invariant (its enforcement is a per-cluster
set-namespace Trigger). A component may span several namespaces, and a variant
may run parts another does not. Variants that share a cluster, such as staging
beside prod or one preview per pull request, have to use different namespaces,
so they are not compared. Only Variants of a Component participate;
canonical base Spaces are excluded.

This is a fleet-wide property no per-cluster controller or per-resource validator
can see. Narrow it with --component (one Component's variants) and --issues-only.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
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
			report := buildConsistencyReport(snap, issuesOnly)
			if done, err := managerkit.Render(cmd.OutOrStdout(), output, report); done || err != nil {
				return err
			}
			printConsistencyTable(cmd, report)
			return nil
		},
	}
	kit.AddOutputFlag(cmd, &output)
	kit.AddFilterFlags(cmd, &filter)
	kit.AddIssuesOnlyFlag(cmd, &issuesOnly, "components that are inconsistent across their variants")
	return cmd
}

func buildConsistencyReport(snap *snapshot.Snapshot, issuesOnly bool) consistencyReport {
	var report consistencyReport
	for _, cc := range nsmanager.AnalyzeConsistency(snap.Clusters) {
		report.Totals.Components++
		if cc.Consistent {
			report.Totals.Consistent++
		} else {
			report.Totals.Inconsistent++
		}
		if issuesOnly && cc.Consistent {
			continue
		}
		report.Components = append(report.Components, cc)
	}
	report.Filter = snap.Filter
	return report
}

func printConsistencyTable(cmd *cobra.Command, r consistencyReport) {
	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "COMPONENT\tVARIANTS\tNAMESPACES\tPOD-SECURITY\tCONSISTENT\tISSUES")
	for _, c := range r.Components {
		issues := "-"
		if len(c.Issues) > 0 {
			issues = strings.Join(c.Issues, "; ")
		}
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\t%s\n",
			c.Component, len(c.Variants), kit.Dash(strings.Join(c.Namespaces, ",")),
			kit.Dash(strings.Join(c.PodSecurityLevels, ",")), kit.YesNo(c.Consistent), issues)
	}
	_ = tw.Flush()
	kit.Fprintln(cmd.OutOrStdout(), fmt.Sprintf("\n%d components (%d consistent, %d inconsistent)",
		r.Totals.Components, r.Totals.Consistent, r.Totals.Inconsistent))
}
