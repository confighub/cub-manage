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

func newListCmd() *cobra.Command {
	var output string
	var filter kit.FilterFlags
	var only kit.ResourceFilter
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List pod-bearing workloads across the fleet",
		Long: `list enumerates the pod-bearing workloads ConfigHub holds (Deployment, StatefulSet,
DaemonSet, ReplicaSet, Job, CronJob, Pod) with the cluster, Space, and Unit each
came from. Canonical base/policy definitions are included and flagged.

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
			return kit.RenderResources(cmd, output, resourceRows(snap, only))
		},
	}
	kit.AddOutputFlag(cmd, &output)
	kit.AddFilterFlags(cmd, &filter)
	kit.AddResourceFlags(cmd, &only, "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job", "CronJob", "Pod")
	return cmd
}

func resourceRows(snap *snapshot.Snapshot, only kit.ResourceFilter) []kit.ResourceRow {
	var rows []kit.ResourceRow
	for _, r := range snap.Resources {
		kind, name, namespace, ok := scheduling.ResourceMeta(r.Doc)
		if !ok || !scheduling.IsWorkloadKind(kind) {
			continue
		}
		row := kit.ResourceRow{
			Cluster:   r.Origin.Cluster,
			Kind:      kind,
			Name:      name,
			Namespace: namespace,
			Space:     r.Origin.Space,
			Unit:      r.Origin.UnitSlug,
			Canonical: r.Origin.Canonical,
		}
		if only.Keep(row) {
			rows = append(rows, row)
		}
	}
	return rows
}
