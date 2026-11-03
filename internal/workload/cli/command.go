// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

// Package cli is the 'cub manage workload' command group.
package cli

import (
	"github.com/spf13/cobra"

	"github.com/confighub/cub-manage/internal/kit"
)

// Domain is this command group's name under 'cub manage'.
const Domain = "workload"

// InvocationName is how to spell this command group in a suggestion the user
// can paste back into a shell.
func InvocationName() string { return kit.InvocationName(Domain) }

// NewCommand builds the 'workload' command group.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   Domain,
		Short: "Workload readiness: security context, resources, probes, and availability",
		Long: `workload manages the security and reliability posture of Kubernetes
workloads across the fleet -- container and pod security context, resource
requests and limits, probes, and availability (PodDisruptionBudget coverage and
spreading a workload's own replicas with pod anti-affinity or topology spread)
-- as a per-workload readiness scorecard, findings, and fixes.

Which nodes a workload may land on is 'cub manage scheduling'; how many
replicas it runs, when an autoscaler sets that, is 'cub manage autoscale'.

` + kit.DomainHelpFooter,
	}
	kit.AddGroups(cmd)
	kit.AddReadCommands(cmd,
		newSnapshotCmd(),
		newListCmd(),
		newReadinessCmd(),
		newAvailabilityCmd(),
		newFindingsCmd(),
	)
	kit.AddWriteCommands(cmd,
		newHardenCmd(),
		newSetResourcesCmd(),
		newSetProbesCmd(),
		newEnsurePDBCmd(),
		newEnsureSpreadCmd(),
		newFleetEditCmd(),
		newGuardrailsCmd(),
	)
	return cmd
}
