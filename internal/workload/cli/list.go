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

func newListCmd() *cobra.Command {
	var output string
	var filter kit.FilterFlags
	var only kit.ResourceFilter
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List workloads and PodDisruptionBudgets across the fleet",
		Long: `list enumerates the resources ConfigHub holds that the workload-posture model
reasons about — pod-bearing workloads (Deployment, StatefulSet, DaemonSet,
ReplicaSet, Job, CronJob, Pod) and PodDisruptionBudgets — with the cluster,
Space, and Unit each came from. Canonical base/policy definitions are included
and flagged.

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
	kit.AddResourceFlags(cmd, &only, "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job", "CronJob", "Pod", "PodDisruptionBudget")
	return cmd
}

func resourceRows(snap *snapshot.Snapshot, only kit.ResourceFilter) []kit.ResourceRow {
	var rows []kit.ResourceRow
	for _, r := range snap.Resources {
		kind, name, namespace, ok := workload.ResourceMeta(r.Doc)
		if !ok {
			continue
		}
		// The explorer shows workloads and PDBs (the families the snapshot pulls).
		if !workload.IsWorkloadKind(kind) && kind != "PodDisruptionBudget" {
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
