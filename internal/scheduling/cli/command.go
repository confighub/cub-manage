// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

// Package cli is the 'cub manage scheduling' command group.
package cli

import (
	"github.com/spf13/cobra"

	"github.com/confighub/cub-manage/internal/kit"
)

// Domain is this command group's name under 'cub manage'.
const Domain = "scheduling"

// InvocationName is how to spell this command group in a suggestion the user
// can paste back into a shell.
func InvocationName() string { return kit.InvocationName(Domain) }

// NewCommand builds the 'scheduling' command group.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   Domain,
		Short: "Scheduling constraints: placement (nodeSelector, tolerations, node affinity) and spread (topology spread, pod anti-affinity)",
		Long: `scheduling manages the constraints the Kubernetes scheduler reads, across the
fleet:

  placement  which nodes a workload may land on: nodeSelector, tolerations,
             node affinity
  spread     how its replicas spread across zones and nodes: topology spread
             constraints, pod anti-affinity, and each cluster's default spread
             constraints

A toleration only permits a pod onto a tainted node; it does not put it there,
so a workload counts as constrained only with a nodeSelector or a required node
affinity. A Deployment, ReplicaSet or StatefulSet with no spread constraints of
its own gets its cluster's defaults, which 'spread' reports from the cluster's
Target.

` + kit.DomainHelpFooter,
	}
	kit.AddGroups(cmd)
	kit.AddReadCommands(cmd,
		newSnapshotCmd(),
		newListCmd(),
		newPlacementCmd(),
		newSpreadCmd(),
		newFindingsCmd(),
	)
	kit.AddWriteCommands(cmd,
		newSetNodeSelectorCmd(),
		newSetTolerationsCmd(),
		newSetNodeAffinityCmd(),
		newSetTopologySpreadCmd(),
		newSetPodAntiAffinityCmd(),
		newFleetEditCmd(),
		newGuardrailsCmd(),
	)
	return cmd
}
