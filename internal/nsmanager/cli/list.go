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

func newListCmd() *cobra.Command {
	var output string
	var filter kit.FilterFlags
	var only kit.ResourceFilter
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List namespaces and the workloads occupying them across the fleet",
		Long: `list enumerates the resources ConfigHub holds that the namespace model reasons
about — Namespace, and the pod-bearing workloads that occupy one (Deployment,
StatefulSet, DaemonSet, ReplicaSet, Job, CronJob, Pod) — with the cluster, Space,
and Unit each came from. Canonical base/policy definitions are included and
flagged.

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
	kit.AddResourceFlags(cmd, &only, "Namespace", "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "Job", "CronJob", "Pod")
	return cmd
}

func resourceRows(snap *snapshot.Snapshot, only kit.ResourceFilter) []kit.ResourceRow {
	var rows []kit.ResourceRow
	for _, r := range snap.Resources {
		kind, name, namespace, ok := nsmanager.ResourceMeta(r.Doc)
		if !ok {
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
