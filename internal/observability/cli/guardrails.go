// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

// This tool's guardrail pack. The mechanism -- policy Space, Triggers, the group
// selection on each in-scope Space, and reporting the Units a Trigger marked
// -- is managerkit/guardrails'. What is this tool's is the rules.

import (
	"context"
	"strings"

	"github.com/spf13/cobra"

	"github.com/confighub/sdk/core/cubapi"
	"github.com/confighub/sdk/managerkit/guardrails"

	"github.com/confighub/cub-manage/internal/celrule"
	"github.com/confighub/cub-manage/internal/cub"
	"github.com/confighub/cub-manage/internal/observability"
)

// otherMonitors are the ServiceMonitors on a Service's cluster, which the coverage
// check reads.
var otherMonitors = guardrails.OtherData{Key: "servicemonitors", ResourceTypes: []string{"monitoring.coreos.com/v1/ServiceMonitor"}}

// exposesMetrics: the Service r looks like a metrics scrape target, by a port named
// like a metrics port or a prometheus.io/scrape: "true" annotation.
var exposesMetrics = "(r.?metadata.?annotations.orValue(null) != null && " +
	"r.metadata.annotations[?'prometheus.io/scrape'].orValue('') == 'true' || " +
	"(r.?spec.?ports.orValue(null) != null ? r.spec.ports : []).exists(p, " +
	"p.?name.orValue('').lowerAscii() in ['" + strings.Join(observability.MetricsPortNames(), "', '") + "']))"

// monitorSelects is whether the ServiceMonitor m selects the Service s: its namespaceSelector
// names s's namespace -- any, a list, or by default its own -- and its selector selects s's
// labels. A missing selector selects every Service, as the analysis reads it.
func monitorSelects(m, s string) string {
	return "(" + m + ".?spec.?namespaceSelector.?any.orValue(false) == true || " +
		"(size(" + m + ".?spec.?namespaceSelector.?matchNames.orValue([])) > 0 ? " +
		celrule.NamespaceOf(s) + " in " + m + ".spec.namespaceSelector.matchNames : " +
		celrule.NamespaceOf(m) + " == " + celrule.NamespaceOf(s) + ")) && " +
		"selects(" + m + ".?spec.?selector.orValue(null) != null ? " + m + ".spec.selector : {}, " + s + ".?metadata.?labels.orValue({}))"
}

// monitors: the ServiceMonitor m selects the Service r.
var monitors = monitorSelects("m", "r")

// otherServices are the Services on a ServiceMonitor's cluster, which servicemonitor-selects reads.
var otherServices = guardrails.OtherData{Key: "services", ResourceTypes: []string{"v1/Service"}}

// celServiceMonitorSelects fails a ServiceMonitor that selects no Service on its cluster, so it
// scrapes nothing: most often a label renamed or misspelled. A Unit with no Target passes.
var celServiceMonitorSelects = celrule.When(
	celrule.OnACluster+" && !"+celrule.Other(otherServices.Key)+".exists(s, "+monitorSelects("r", "s")+")",
	"["+celrule.Failure("'spec.selector'", "r.?spec.?selector",
		"'the ServiceMonitor selects no Service on the cluster, so it scrapes nothing'")+"]")

// celServiceMonitorCoverage fails a metrics Service that no ServiceMonitor on its
// cluster selects. A Unit with no Target is on no known cluster, so it passes.
var celServiceMonitorCoverage = celrule.When(
	celrule.OnACluster+" && "+exposesMetrics+" && !"+celrule.Other(otherMonitors.Key)+".exists(m, "+monitors+")",
	"["+celrule.Failure("'metadata.labels'", "r.?metadata.?labels",
		"'no ServiceMonitor selects this metrics Service in namespace ' + "+celrule.NamespaceOf("r"))+"]")

var Pack = guardrails.Pack{
	Group: "observability",
	Checks: []guardrails.Check{
		{Control: "CH-1301", Name: "observability-servicemonitor-coverage", Rule: "observability-servicemonitor-coverage",
			DisplayName: "Metrics Services have a ServiceMonitor",
			Description: "Warns on a Service exposing metrics -- a port named metrics, http-metrics, monitoring, prometheus or telemetry, or a prometheus.io/scrape: \"true\" annotation -- that no ServiceMonitor on its cluster selects, so nothing scrapes it. Not checked for a Unit with no Target. Fix: `cub manage observability ensure-servicemonitor`.",
			Engine:      guardrails.EngineCEL, Priority: guardrails.PriorityLow,
			Fix:           "cub manage observability ensure-servicemonitor",
			ResourceTypes: []string{"v1/Service"}, OtherData: []guardrails.OtherData{otherMonitors},
			Failures: celServiceMonitorCoverage},
		{Control: "CH-1302", Name: "servicemonitor-selects", Rule: "servicemonitor-selects", DisplayName: "ServiceMonitors select a Service",
			Description: "Warns on a ServiceMonitor whose selector selects no Service on its cluster, in its namespace or those its namespaceSelector names, so it scrapes nothing: most often a label renamed or misspelled. Not checked for a Unit with no Target. Fix: correct the selector, or except a ServiceMonitor for Services made outside ConfigHub.",
			Engine:      guardrails.EngineCEL, Priority: guardrails.PriorityLow,
			ResourceTypes: otherMonitors.ResourceTypes, OtherData: []guardrails.OtherData{otherServices},
			Failures: celServiceMonitorSelects, Corresponds: []string{"KL:dangling-servicemonitor"}},
	},
}

func preflight(ctx context.Context) (*cubapi.Client, error) { return cub.Preflight(ctx) }

func newGuardrailsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "guardrails",
		Short: "Install, inspect, and feed the observability guardrail policy pack",
		Long: `guardrails manages the observability guardrail pack, defined once in a policy
Space and enforced in each Space that opts into the group:

  observability-servicemonitor-coverage   a Service exposing metrics has a
                                          ServiceMonitor that scrapes it
  servicemonitor-selects                  a ServiceMonitor selects some Service

The coverage rule reads the ServiceMonitors on the Service's cluster: those of
the Units released to the same Target as the Space, which each Space selects
for itself. A change to a ServiceMonitor re-runs the rule on the Units that read
it. A Unit with no Target is on no known cluster, so it passes. Triggers are
Warn=true; promote to blocking with
  cub trigger update observability-servicemonitor-coverage --space <policy-space> --unwarn

Except one resource from a rule with 'cub manage guardrails exception add';
'cub manage guardrails exception list' shows what is excepted, and why.`,
	}
	cmd.AddCommand(Pack.InstallCmd(preflight))
	return cmd
}
