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
		Short: "Workload placement: nodeSelector, tolerations, and node affinity",
		Long: `scheduling manages where Kubernetes workloads may land -- nodeSelector,
tolerations, and node affinity -- across the fleet.

A toleration only permits a pod onto a tainted node; it does not put it there,
so a workload counts as constrained only with a nodeSelector or a required node
affinity. Spreading a workload's own replicas across nodes or zones is
availability, and is 'cub manage workload ensure-spread'.

` + kit.DomainHelpFooter,
	}
	kit.AddGroups(cmd)
	kit.AddReadCommands(cmd,
		newSnapshotCmd(),
		newListCmd(),
		newPlacementCmd(),
		newFindingsCmd(),
	)
	kit.AddWriteCommands(cmd,
		newSetNodeSelectorCmd(),
		newSetTolerationsCmd(),
		newSetNodeAffinityCmd(),
		newFleetEditCmd(),
		newGuardrailsCmd(),
	)
	return cmd
}
