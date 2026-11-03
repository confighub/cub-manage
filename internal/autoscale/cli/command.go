// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

// Package cli is the 'cub manage autoscale' command group.
package cli

import (
	"github.com/spf13/cobra"

	"github.com/confighub/cub-manage/internal/kit"
)

// Domain is this command group's name under 'cub manage'.
const Domain = "autoscale"

// InvocationName is how to spell this command group in a suggestion the user
// can paste back into a shell.
func InvocationName() string { return kit.InvocationName(Domain) }

// NewCommand builds the 'autoscale' command group.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   Domain,
		Short: "Autoscaling: HorizontalPodAutoscalers and KEDA ScaledObjects",
		Long: `autoscale manages Kubernetes autoscaling -- HorizontalPodAutoscalers and KEDA
ScaledObjects -- across the fleet: inventory, findings (including a
PodDisruptionBudget that blocks every eviction at an autoscaler's minimum
scale), edits to an HPA's bounds and targets, and converting an HPA to a KEDA
ScaledObject.

'convert-keda' runs the convert-hpa-to-keda function in-process, so it needs no
worker. Authoring the PodDisruptionBudget itself is 'cub manage workload
ensure-pdb'.

` + kit.DomainHelpFooter,
	}
	kit.AddGroups(cmd)
	kit.AddReadCommands(cmd,
		newSnapshotCmd(),
		newListCmd(),
		newFindingsCmd(),
	)
	kit.AddWriteCommands(cmd,
		newSetHPACmd(),
		newConvertCmd(),
		newFleetEditCmd(),
		newGuardrailsCmd(),
	)
	return cmd
}
