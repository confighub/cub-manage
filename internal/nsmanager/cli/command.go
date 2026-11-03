// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

// Package cli is the 'cub manage namespace' command group.
package cli

import (
	"github.com/spf13/cobra"

	"github.com/confighub/cub-manage/internal/kit"
)

// Domain is this command group's name under 'cub manage'.
const Domain = "namespace"

// InvocationName is how to spell this command group in a suggestion the user
// can paste back into a shell.
func InvocationName() string { return kit.InvocationName(Domain) }

// NewCommand builds the 'namespace' command group.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   Domain,
		Short: "Namespaces: the Namespace object, its Pod Security Standard, and its name across variants",
		Long: `namespace manages Kubernetes namespaces themselves -- the Namespace object,
its Pod Security Standard labels, its name, and whether that name and level are
the same in every variant of a component -- across the fleet.

It stops at the namespace. Whether a namespace's NetworkPolicies cover it is
'cub manage netpol', and whether its RBAC is sound is 'cub manage rbac'. What
keeps a namespace's workloads from a stricter Pod Security Standard is reported
here ('pod-security') and fixed with 'cub manage workload harden'.

` + kit.DomainHelpFooter,
	}
	kit.AddGroups(cmd)
	kit.AddReadCommands(cmd,
		newSnapshotCmd(),
		newListCmd(),
		newEnvelopeCmd(),
		newPodSecurityCmd(),
		newConsistencyCmd(),
		newFindingsCmd(),
	)
	kit.AddWriteCommands(cmd,
		newApplyEnvelopeCmd(),
		newBackfillCmd(),
		newFleetEditCmd(),
		newGuardrailsCmd(),
	)
	return cmd
}
