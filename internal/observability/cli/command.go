// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

// Package cli is the 'cub manage observability' command group.
package cli

import (
	"github.com/spf13/cobra"

	"github.com/confighub/cub-manage/internal/kit"
)

// Domain is this command group's name under 'cub manage'.
const Domain = "observability"

// InvocationName is how to spell this command group in a suggestion the user
// can paste back into a shell.
func InvocationName() string { return kit.InvocationName(Domain) }

// NewCommand builds the 'observability' command group.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   Domain,
		Short: "Observability: ServiceMonitor coverage and telemetry sidecars",
		Long: `observability manages the observability posture of Kubernetes workloads
across the fleet: whether each Service that exposes metrics is selected by a
Prometheus ServiceMonitor, and OpenTelemetry or other telemetry sidecars.

ServiceMonitor coverage is a property of two Units, the Service and the
ServiceMonitor that selects it, so it is computed over the whole fleet rather
than one Unit at a time. A sidecar this injects is a container like any other:
'cub manage workload readiness' scores its security context and resources.

` + kit.DomainHelpFooter,
	}
	kit.AddGroups(cmd)
	kit.AddReadCommands(cmd,
		newSnapshotCmd(),
		newListCmd(),
		newCoverageCmd(),
		newSidecarsCmd(),
		newFindingsCmd(),
	)
	kit.AddWriteCommands(cmd,
		newEnsureServiceMonitorCmd(),
		newInjectSidecarCmd(),
		newFleetEditCmd(),
		newGuardrailsCmd(),
	)
	return cmd
}
