// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/confighub/sdk/managerkit"

	"github.com/confighub/cub-manage/internal/autoscale"
	"github.com/confighub/cub-manage/internal/autoscale/snapshot"
	"github.com/confighub/cub-manage/internal/cub"
	"github.com/confighub/cub-manage/internal/kit"
)

// autoscalerRow is a list row with what an autoscaler adds: its scale target and
// replica range.
type autoscalerRow struct {
	kit.ResourceRow
	TargetKind string `json:"targetKind,omitempty"`
	TargetName string `json:"targetName"`
	Min        *int64 `json:"min,omitempty"`
	Max        *int64 `json:"max,omitempty"`
	Pinned     bool   `json:"pinned"`
}

func newListCmd() *cobra.Command {
	var output string
	var filter kit.FilterFlags
	var only kit.ResourceFilter
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List every HorizontalPodAutoscaler and KEDA ScaledObject in the fleet",
		Long: `list reports each autoscaler (HorizontalPodAutoscaler or KEDA ScaledObject)
across the fleet: its scale target, min/max replicas, and whether it is pinned
(min == max, so it can't actually scale).

Filter with --kind, --cluster, and --namespace.`,
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
			rows := autoscalerRows(snap, only)
			if done, err := managerkit.Render(cmd.OutOrStdout(), output, rows); done || err != nil {
				return err
			}
			printListTable(cmd, rows)
			return nil
		},
	}
	kit.AddOutputFlag(cmd, &output)
	kit.AddFilterFlags(cmd, &filter)
	kit.AddResourceFlags(cmd, &only, string(autoscale.KindHPA), string(autoscale.KindScaledObject))
	return cmd
}

func autoscalerRows(snap *snapshot.Snapshot, only kit.ResourceFilter) []autoscalerRow {
	rows := []autoscalerRow{}
	for _, c := range snap.Clusters {
		for _, a := range c.Autoscalers {
			row := autoscalerRow{
				ResourceRow: kit.ResourceRow{
					Cluster: a.Origin.Cluster, Kind: string(a.Kind), Name: a.Name, Namespace: a.Namespace,
					Space: a.Origin.Space, Unit: a.Origin.UnitSlug,
				},
				TargetKind: a.TargetKind, TargetName: a.TargetName,
				Min: a.Min, Max: a.Max, Pinned: a.Pinned(),
			}
			if only.Keep(row.ResourceRow) {
				rows = append(rows, row)
			}
		}
	}
	kit.SortResources(rows, func(r autoscalerRow) kit.ResourceRow { return r.ResourceRow })
	return rows
}

func printListTable(cmd *cobra.Command, rows []autoscalerRow) {
	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "CLUSTER\tNAMESPACE\tKIND\tNAME\tTARGET\tMIN\tMAX\tPINNED\tUNIT")
	for _, a := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			a.Cluster, kit.Dash(a.Namespace), shortKind(a.Kind), a.Name,
			targetLabel(a.TargetKind, a.TargetName), minMax(a.Min), minMax(a.Max), kit.YesNo(a.Pinned), kit.UnitLabel(a.Space, a.Unit))
	}
	_ = tw.Flush()
	kit.Fprintln(cmd.OutOrStdout(), fmt.Sprintf("\n%d resources", len(rows)))
}

func shortKind(kind string) string {
	switch kind {
	case string(autoscale.KindHPA):
		return "HPA"
	case string(autoscale.KindScaledObject):
		return "ScaledObject"
	}
	return kind
}

func targetLabel(kind, name string) string {
	if name == "" {
		return "-"
	}
	if kind == "" {
		return name
	}
	return kind + "/" + name
}

func minMax(v *int64) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprintf("%d", *v)
}
