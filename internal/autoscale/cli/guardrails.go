// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

// This tool's guardrail pack. The mechanism -- policy Space, Triggers, the group
// selection on each in-scope Space, and reporting the Units a Trigger marked
// -- is managerkit/guardrails'. What is this tool's is the rules.

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/confighub/sdk/core/cubapi"
	"github.com/confighub/sdk/managerkit/guardrails"

	"github.com/confighub/cub-manage/internal/celrule"
	"github.com/confighub/cub-manage/internal/cub"
)

// celAutoscalerNotPinned passes unless an autoscaler is pinned (min == max, so
// it can't actually scale). An HPA's maxReplicas is required and minReplicas
// defaults to 1; a ScaledObject's bounds are both optional, so an absent bound
// is treated as unpinned.
var celAutoscalerNotPinned = (celrule.When("r.kind == 'HorizontalPodAutoscaler' && has(r.spec.minReplicas) && r.spec.minReplicas >= r.spec.maxReplicas",
	"["+celrule.Failure(celrule.Lit("spec.minReplicas"), celrule.Some("r.spec.minReplicas"),
		"'minReplicas ' + string(r.spec.minReplicas) + ' leaves no room below maxReplicas ' + string(r.spec.maxReplicas)")+"]") +
	" + " +
	celrule.When("r.kind == 'ScaledObject' && has(r.spec.minReplicaCount) && has(r.spec.maxReplicaCount) && r.spec.minReplicaCount >= r.spec.maxReplicaCount",
		"["+celrule.Failure(celrule.Lit("spec.minReplicaCount"), celrule.Some("r.spec.minReplicaCount"),
			"'minReplicaCount ' + string(r.spec.minReplicaCount) + ' leaves no room below maxReplicaCount ' + string(r.spec.maxReplicaCount)")+"]"))

var Pack = guardrails.Pack{
	Group: "autoscale",
	Checks: []guardrails.Check{
		{Control: "CH-1201", Name: "autoscale-not-pinned", Rule: "autoscale-not-pinned", DisplayName: "Autoscalers can scale",
			Description: "Warns on an HPA/ScaledObject with min == max (it can't actually scale). Fix: `cub manage autoscale set-hpa --min <lower> --max <higher>` or a profile.",
			Engine:      guardrails.EngineCEL, Priority: guardrails.PriorityLow,
			ResourceTypes: []string{"autoscaling/v1/HorizontalPodAutoscaler", "autoscaling/v2/HorizontalPodAutoscaler", "keda.sh/v1alpha1/ScaledObject"},
			Fix:           "cub manage autoscale set-hpa --min <lower> --max <higher>",
			Failures:      celAutoscalerNotPinned},
		{Control: "CH-1202", Name: "autoscale-min-replicas", Rule: "autoscale-min-replicas", DisplayName: "Autoscalers keep two replicas",
			Description: "Warns on a HorizontalPodAutoscaler whose minReplicas is below 2, or unset, which is 1: at its minimum, one pod's loss is an outage. A KEDA ScaledObject may scale to zero by design, and is not checked. Fix: `cub manage autoscale set-hpa --min 2`.",
			Engine:      guardrails.EngineCEL, Priority: guardrails.PriorityMedium,
			ResourceTypes: []string{"autoscaling/v1/HorizontalPodAutoscaler", "autoscaling/v2/HorizontalPodAutoscaler"},
			Fix:           "cub manage autoscale set-hpa --min 2",
			Failures: celrule.When("!(has(r.spec) && has(r.spec.minReplicas) && r.spec.minReplicas >= 2)",
				"["+celrule.Failure(celrule.Lit("spec.minReplicas"), "r.?spec.?minReplicas",
					"'the autoscaler can scale down to ' + (has(r.spec) && has(r.spec.minReplicas) ? string(r.spec.minReplicas) : '1, as minReplicas is unset,') + ' replica'")+"]"),
			Corresponds: []string{"SC:horizontalpodautoscaler-replicas", "KL:hpa-minimum-three-replicas"}},
	},
}

func preflight(ctx context.Context) (*cubapi.Client, error) { return cub.Preflight(ctx) }

func newGuardrailsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "guardrails",
		Short: "Install and inspect the autoscaling guardrail policy pack",
		Long: `guardrails manages a pack of autoscaling validation policies, defined once in a
policy Space and enforced in each Space that opts into the group:

  autoscale-not-pinned   an HPA/ScaledObject must not have min == max (it would
                         be unable to scale) — a per-resource vet-cel check

The rule is created with Warn=true (advisory ValidationWarnings, never blocking).
Promote it to blocking with:
cub manage guardrails update autoscale-not-pinned --enforce --commit

Schema validation, which checks the ScaledObjects convert-keda writes, is the
validate rule of the tool-level basic group: 'cub manage guardrails install'.

Except one resource from a rule with 'cub manage guardrails exception add';
'cub manage guardrails exception list' shows what is excepted, and why.`,
	}
	cmd.AddCommand(Pack.InstallCmd(preflight))
	return cmd
}
