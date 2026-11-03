// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

// Package cli is the 'cub manage rbac' command group.
package cli

import (
	"github.com/spf13/cobra"

	"github.com/confighub/cub-manage/internal/kit"
)

// Domain is this command group's name under 'cub manage'.
const Domain = "rbac"

// InvocationName is how to spell this command group in a suggestion the user
// can paste back into a shell.
func InvocationName() string { return kit.InvocationName(Domain) }

// NewCommand builds the 'rbac' command group.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   Domain,
		Short: "RBAC: Roles, ClusterRoles, their bindings, and ServiceAccounts",
		Long: `rbac manages Kubernetes RBAC -- Role, ClusterRole, RoleBinding,
ClusterRoleBinding and ServiceAccount -- across the fleet: inventory, effective
access ('who-can', and its inverse 'access'), hygiene findings, and edits to
roles and bindings.

Which RBAC a namespace should start with is part of the namespace template that
'cub manage namespace backfill' copies; whether that RBAC is sound is this
command's subject.

` + kit.DomainHelpFooter,
	}
	kit.AddGroups(cmd)
	kit.AddReadCommands(cmd,
		newSnapshotCmd(),
		newListCmd(),
		newWhoCanCmd(),
		newAccessCmd(),
		newFindingsCmd(),
	)
	kit.AddWriteCommands(cmd,
		newEditCmd(),
		newFleetEditCmd(),
		newGuardrailsCmd(),
	)
	return cmd
}
