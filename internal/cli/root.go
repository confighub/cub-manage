// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

// Package cli is cub-manage's root command. It runs the same whether invoked
// standalone as cub-manage or by cub as the plugin command 'cub manage'.
package cli

import (
	"github.com/spf13/cobra"

	autoscale "github.com/confighub/cub-manage/internal/autoscale/cli"
	netpol "github.com/confighub/cub-manage/internal/netpol/cli"
	namespace "github.com/confighub/cub-manage/internal/nsmanager/cli"
	observability "github.com/confighub/cub-manage/internal/observability/cli"
	rbac "github.com/confighub/cub-manage/internal/rbac/cli"
	scheduling "github.com/confighub/cub-manage/internal/scheduling/cli"
	workload "github.com/confighub/cub-manage/internal/workload/cli"
)

// Summary is the one-line description of the tool, also the plugin's.
const Summary = "Manage Kubernetes configuration across a ConfigHub fleet: namespaces, RBAC, NetworkPolicy, workloads, scheduling, autoscaling, and observability"

// NewRoot builds the root command with every domain attached.
func NewRoot() *cobra.Command {
	root := &cobra.Command{
		Use:   "cub-manage",
		Short: Summary,
		Long: `cub-manage manages Kubernetes configuration stored as data in ConfigHub Units,
across a whole fleet of clusters at once. Each command group owns one concern:

  namespace      Namespace objects, their Pod Security Standard, and their names
  rbac           Roles, ClusterRoles, their bindings, and ServiceAccounts
  netpol         NetworkPolicy coverage and connectivity
  workload       workload readiness: security context, resources, probes, availability
  scheduling     placement: nodeSelector, tolerations, node affinity
  autoscale      HorizontalPodAutoscalers and KEDA ScaledObjects
  observability  ServiceMonitor coverage and telemetry sidecars

Each group reads the fleet ('snapshot', 'list', 'findings', and its own
analyses), fixes what it finds by editing Units rather than clusters, and
installs a guardrail pack that keeps it fixed.

It uses your cub session: run 'cub auth login' first if you are not signed in,
and 'preflight' to check.`,
		SilenceUsage: true,
		// main prints the error.
		SilenceErrors: true,
	}
	root.AddGroup(
		&cobra.Group{ID: "domains", Title: "Command groups:"},
		&cobra.Group{ID: "tool", Title: "Tool commands:"},
	)
	for _, c := range []*cobra.Command{
		namespace.NewCommand(),
		rbac.NewCommand(),
		netpol.NewCommand(),
		workload.NewCommand(),
		scheduling.NewCommand(),
		autoscale.NewCommand(),
		observability.NewCommand(),
	} {
		c.GroupID = "domains"
		root.AddCommand(c)
	}
	for _, c := range []*cobra.Command{newGuardrailsCmd(), newProfileCmd(), newPreflightCmd(), newVersionCmd()} {
		c.GroupID = "tool"
		root.AddCommand(c)
	}
	root.SetHelpCommandGroupID("tool")
	root.SetCompletionCommandGroupID("tool")
	return root
}
