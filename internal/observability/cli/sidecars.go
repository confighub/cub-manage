// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/confighub/sdk/managerkit"

	"github.com/confighub/cub-manage/internal/cub"
	"github.com/confighub/cub-manage/internal/kit"
	"github.com/confighub/cub-manage/internal/observability"
	"github.com/confighub/cub-manage/internal/observability/snapshot"
)

type sidecarsReport struct {
	Workloads []observability.SidecarResult `json:"workloads"`
	Totals    struct {
		Workloads   int `json:"workloads"`
		WithSidecar int `json:"withSidecar"`
		Without     int `json:"without"`
	} `json:"totals"`
	SidecarNames []string `json:"sidecarNames"`
	Filter       string   `json:"filter,omitempty"`
}

func newSidecarsCmd() *cobra.Command {
	var output string
	var filter kit.FilterFlags
	var clusterFilter, namespaceFilter string
	var sidecarNames []string
	var issuesOnly bool
	cmd := &cobra.Command{
		Use:   "sidecars",
		Short: "Report which workloads carry a telemetry (otel) sidecar container",
		Long: `sidecars reports, per workload, whether it has a telemetry-collector sidecar
container. By default it looks for the conventional OpenTelemetry container names
(otel-collector, otc-container, opentelemetry-collector, otel-agent); override
with --sidecar.

Filter with --cluster / --namespace; --issues-only shows workloads without a
sidecar.`,
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
			report := buildSidecarsReport(snap, sidecarNames, clusterFilter, namespaceFilter, issuesOnly)
			if done, err := managerkit.Render(cmd.OutOrStdout(), output, report); done || err != nil {
				return err
			}
			printSidecarsTable(cmd, report)
			return nil
		},
	}
	kit.AddOutputFlag(cmd, &output)
	kit.AddFilterFlags(cmd, &filter)
	kit.AddClusterFlag(cmd, &clusterFilter)
	kit.AddNamespaceFlag(cmd, &namespaceFilter)
	cmd.Flags().StringArrayVar(&sidecarNames, "sidecar", nil, "sidecar container name(s) to look for (default: the otel names)")
	kit.AddIssuesOnlyFlag(cmd, &issuesOnly, "workloads without the sidecar")
	return cmd
}

func buildSidecarsReport(snap *snapshot.Snapshot, sidecarNames []string, clusterFilter, namespaceFilter string, issuesOnly bool) sidecarsReport {
	var report sidecarsReport
	report.Filter = snap.Filter
	report.SidecarNames = sidecarNames
	if len(report.SidecarNames) == 0 {
		report.SidecarNames = observability.DefaultOtelContainerNames
	}
	for _, r := range observability.AnalyzeSidecars(snap.Clusters, sidecarNames) {
		if clusterFilter != "" && r.Cluster != clusterFilter {
			continue
		}
		if namespaceFilter != "" && r.Namespace != namespaceFilter {
			continue
		}
		report.Totals.Workloads++
		if r.HasSidecar {
			report.Totals.WithSidecar++
		} else {
			report.Totals.Without++
		}
		if issuesOnly && r.HasSidecar {
			continue
		}
		report.Workloads = append(report.Workloads, r)
	}
	return report
}

func printSidecarsTable(cmd *cobra.Command, r sidecarsReport) {
	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "CLUSTER\tNAMESPACE\tKIND\tNAME\tSIDECAR")
	for _, w := range r.Workloads {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", w.Cluster, kit.Dash(w.Namespace), w.Kind, w.Name, kit.Dash(w.Sidecar))
	}
	_ = tw.Flush()
	kit.Fprintln(cmd.OutOrStdout(), fmt.Sprintf("\n%d workloads (%d with sidecar, %d without)",
		r.Totals.Workloads, r.Totals.WithSidecar, r.Totals.Without))
}
