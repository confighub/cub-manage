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
var celAutoscalerNotPinned = celrule.Result(
	celrule.When("r.kind == 'HorizontalPodAutoscaler' && has(r.spec.minReplicas) && r.spec.minReplicas >= r.spec.maxReplicas",
		"["+celrule.Failure(celrule.Lit("spec.minReplicas"), celrule.Some("r.spec.minReplicas"),
			"'minReplicas ' + string(r.spec.minReplicas) + ' leaves no room below maxReplicas ' + string(r.spec.maxReplicas)")+"]") +
		" + " +
		celrule.When("r.kind == 'ScaledObject' && has(r.spec.minReplicaCount) && has(r.spec.maxReplicaCount) && r.spec.minReplicaCount >= r.spec.maxReplicaCount",
			"["+celrule.Failure(celrule.Lit("spec.minReplicaCount"), celrule.Some("r.spec.minReplicaCount"),
				"'minReplicaCount ' + string(r.spec.minReplicaCount) + ' leaves no room below maxReplicaCount ' + string(r.spec.maxReplicaCount)")+"]"))

var Pack = guardrails.Pack{
	Group: "autoscale",
	Rules: []guardrails.Rule{
		{Slug: "autoscale-not-pinned", DisplayName: "Autoscalers can scale",
			Description: "Warns on an HPA/ScaledObject with min == max (it can't actually scale). Fix: `cub manage autoscale set-hpa --min <lower> --max <higher>` or a profile.",
			Expression:  celAutoscalerNotPinned},
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
cub trigger update <slug> --space <policy-space> --unwarn

Schema validation, which checks the ScaledObjects convert-keda writes, is the
tool-level schema group: 'cub manage guardrails install'.

Except one resource from a rule with 'cub manage guardrails exception add';
'cub manage guardrails exception list' shows what is excepted, and why.`,
	}
	cmd.AddCommand(Pack.InstallCmd(preflight))
	return cmd
}
