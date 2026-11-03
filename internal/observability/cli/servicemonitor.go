// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/confighub/sdk/cliutil"
	goclientnew "github.com/confighub/sdk/core/openapi/goclient-new"
	"github.com/confighub/sdk/managerkit/write"

	"github.com/confighub/cub-manage/internal/cub"
	"github.com/confighub/cub-manage/internal/kit"
	"github.com/confighub/cub-manage/internal/observability"
	"github.com/confighub/cub-manage/internal/observability/snapshot"
)

// unitManagedByLabel marks Units this tool authors (e.g. generated ServiceMonitors).
const unitManagedByLabel = "cub manage observability"

func newEnsureServiceMonitorCmd() *cobra.Command {
	var output, portName string
	var commit cliutil.CommitFlags
	cmd := &cobra.Command{
		Use:   "ensure-servicemonitor <space>/<service-unit>",
		Short: "Make a ServiceMonitor Unit that selects a Service",
		Long: `ensure-servicemonitor reads a Service Unit's labels, namespace, and metrics port
and makes a ServiceMonitor Unit, <service>-sm, that selects it, in the same Space
as the Service. It creates the Unit, or brings an existing one up to date, so
running it again changes nothing.

The endpoint port defaults to the Service's metrics port (--port to override).
Refuses when the Service has no metrics port and no --port. Dry-run unless
--commit --change-desc.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			changeDesc, dryRun, err := commit.Validate(fmt.Sprintf("ensure ServiceMonitor for %s", args[0]))
			if err != nil {
				return err
			}
			client, err := cub.Preflight(cmd.Context())
			if err != nil {
				return err
			}
			ref, err := write.ParseUnitRef(cmd.Context(), client, args[0])
			if err != nil {
				return err
			}
			snap, err := snapshot.Load(cmd.Context(), client, fmt.Sprintf("SpaceID = '%s'", ref.SpaceID.String()))
			if err != nil {
				return err
			}
			svc := findServiceBySlug(snap, ref.UnitSlug)
			if svc == nil {
				return fmt.Errorf("no Service found in Unit %s/%s", ref.SpaceSlug, ref.UnitSlug)
			}
			if len(svc.Labels) == 0 {
				return fmt.Errorf("Service %s has no labels — cannot derive a ServiceMonitor selector", svc.Name)
			}
			port := portName
			if port == "" {
				port = serviceMetricsPort(svc)
			}
			if port == "" {
				return fmt.Errorf("Service %s has no named metrics port — pass --port", svc.Name)
			}

			smSlug := svc.Name + "-sm"
			manifest := renderServiceMonitor(smSlug, svc.Namespace, svc.Labels, port)
			res := cub.EnsureUnits(cmd.Context(), client, []cub.Creation{{
				SpaceSlug: ref.SpaceSlug,
				Unit: goclientnew.Unit{
					Slug:          smSlug,
					DisplayName:   smSlug,
					ToolchainType: "Kubernetes/YAML",
					SpaceID:       ref.SpaceID,
					Labels:        map[string]string{"managed-by": unitManagedByLabel},
				},
				Data: manifest,
			}}, write.Change(changeDesc, dryRun))
			return write.ReportMutations(cmd, "ensure-servicemonitor", ref.SpaceSlug, dryRun, output, res)
		},
	}
	kit.AddOutputFlag(cmd, &output)
	commit.Bind(cmd)
	cmd.Flags().StringVar(&portName, "port", "", "endpoint port name (default: the Service's metrics port)")
	return cmd
}

func findServiceBySlug(snap *snapshot.Snapshot, unitSlug string) *observability.ServiceEntity {
	for _, c := range snap.Clusters {
		for _, s := range c.Services {
			if s.Origin.UnitSlug == unitSlug {
				return s
			}
		}
	}
	return nil
}

// serviceMetricsPort returns the Service's metrics port name, if any.
func serviceMetricsPort(svc *observability.ServiceEntity) string {
	for _, p := range svc.Ports {
		switch strings.ToLower(p.Name) {
		case "metrics", "http-metrics", "monitoring", "prometheus", "telemetry":
			return p.Name
		}
	}
	return ""
}

func renderServiceMonitor(name, namespace string, labels map[string]string, port string) string {
	var b strings.Builder
	b.WriteString("apiVersion: monitoring.coreos.com/v1\n")
	b.WriteString("kind: ServiceMonitor\n")
	b.WriteString("metadata:\n")
	b.WriteString("  name: " + name + "\n")
	if namespace != "" {
		b.WriteString("  namespace: " + namespace + "\n")
	}
	b.WriteString("spec:\n")
	b.WriteString("  selector:\n")
	b.WriteString("    matchLabels:\n")
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b.WriteString("      " + k + ": " + labels[k] + "\n")
	}
	b.WriteString("  endpoints:\n")
	b.WriteString("  - port: " + port + "\n")
	return b.String()
}
