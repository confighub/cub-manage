// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

// Package cli is the 'cub manage netpol' command group.
package cli

import (
	"github.com/spf13/cobra"

	"github.com/confighub/cub-manage/internal/kit"
)

// Domain is this command group's name under 'cub manage'.
const Domain = "netpol"

// InvocationName is how to spell this command group in a suggestion the user
// can paste back into a shell.
func InvocationName() string { return kit.InvocationName(Domain) }

// NewCommand builds the 'netpol' command group.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   Domain,
		Short: "NetworkPolicy: coverage, connectivity, and default-deny",
		Long: `netpol manages Kubernetes NetworkPolicies across the fleet, reasoning about
them together with the Namespaces, workloads and Services they have to cover:
coverage, effective connectivity ('who-can-reach', 'reachable-from'), findings,
and authoring policies -- including allows derived from the dependency graph
ConfigHub Links record. Fixes to existing policies are the netpol-* profiles,
applied to one Unit with 'cub manage profile apply' or across the fleet with
'fleet-edit'.

` + kit.DomainHelpFooter,
	}
	kit.AddGroups(cmd)
	kit.AddReadCommands(cmd,
		newSnapshotCmd(),
		newListCmd(),
		newCoverageCmd(),
		newWhoCanReachCmd(),
		newReachableFromCmd(),
		newFindingsCmd(),
	)
	kit.AddWriteCommands(cmd,
		newDefaultDenyCmd(),
		newAllowCmd(),
		newAllowFromLinksCmd(),
		newFleetEditCmd(),
		newGuardrailsCmd(),
	)
	return cmd
}
