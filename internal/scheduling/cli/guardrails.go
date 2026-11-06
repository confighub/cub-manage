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

	"github.com/confighub/cub-manage/internal/cub"
)

const controllerKinds = "['Deployment', 'StatefulSet', 'DaemonSet', 'ReplicaSet']"

// celTolerationNeedsPlacement passes unless a controller has tolerations but
// neither a non-empty nodeSelector nor a required node affinity (i.e. it tolerates
// a taint but doesn't actually pin where it lands).
const celTolerationNeedsPlacement = "!(r.kind in " + controllerKinds + ")" +
	" || !has(r.spec.template.spec.tolerations)" +
	" || (has(r.spec.template.spec.nodeSelector) && size(r.spec.template.spec.nodeSelector) > 0)" +
	" || (has(r.spec.template.spec.affinity) && has(r.spec.template.spec.affinity.nodeAffinity) && has(r.spec.template.spec.affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution))"

// celSpreadSelectsOwnPods passes unless a controller has a topology spread
// constraint with no labelSelector, or whose matchLabels its own pod template
// does not carry: its Pods then do not count toward the skew. (matchExpressions
// are not evaluated; the findings analyzer checks them.)
const celSpreadSelectsOwnPods = "!(r.kind in " + controllerKinds + ")" +
	" || !has(r.spec.template.spec.topologySpreadConstraints)" +
	" || r.spec.template.spec.topologySpreadConstraints.all(c, has(c.labelSelector)" +
	" && (!has(c.labelSelector.matchLabels) || (has(r.spec.template.metadata.labels)" +
	" && c.labelSelector.matchLabels.all(k, k in r.spec.template.metadata.labels && r.spec.template.metadata.labels[k] == c.labelSelector.matchLabels[k]))))"

// celMinDomainsNeedsDoNotSchedule passes unless a controller's spread constraint
// sets minDomains with a whenUnsatisfiable other than DoNotSchedule (its
// default), which the API server rejects.
const celMinDomainsNeedsDoNotSchedule = "!(r.kind in " + controllerKinds + ")" +
	" || !has(r.spec.template.spec.topologySpreadConstraints)" +
	" || r.spec.template.spec.topologySpreadConstraints.all(c, !has(c.minDomains) || !has(c.whenUnsatisfiable) || c.whenUnsatisfiable == 'DoNotSchedule')"

var Pack = guardrails.Pack{
	Group: "scheduling",
	Rules: []guardrails.Rule{
		{Slug: "scheduling-toleration-needs-placement",
			Description: "Warns on a controller that tolerates a taint but has no nodeSelector or required node affinity (may schedule onto general nodes). Fix: `cub manage scheduling set-node-selector` / `set-node-affinity`, or a placement profile.",
			Expression:  celTolerationNeedsPlacement},
		{Slug: "scheduling-spread-selects-own-pods",
			Description: "Warns on a controller whose topology spread constraint has no labelSelector, or one its own pod template's labels do not match, so its Pods never count toward the skew. Fix: `cub manage scheduling set-topology-spread`, which selects the workload's own Pods.",
			Expression:  celSpreadSelectsOwnPods},
		{Slug: "scheduling-min-domains-needs-donotschedule",
			Description: "Warns on a controller whose topology spread constraint sets minDomains without whenUnsatisfiable DoNotSchedule, which the API server rejects. Fix: `cub manage scheduling set-topology-spread --when-unsatisfiable DoNotSchedule --min-domains N`, or drop minDomains.",
			Expression:  celMinDomainsNeedsDoNotSchedule},
	},
}

func preflight(ctx context.Context) (*cubapi.Client, error) { return cub.Preflight(ctx) }

func newGuardrailsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "guardrails",
		Short: "Install and inspect the scheduling guardrail policy pack",
		Long: `guardrails manages a pack of scheduling validation policies, defined once in a
policy Space and enforced in each Space that opts into the group:

  scheduling-toleration-needs-placement       a controller that tolerates a taint
                                              must pin where it lands (nodeSelector
                                              or required node affinity)
  scheduling-spread-selects-own-pods          a topology spread constraint must
                                              select the workload's own Pods
  scheduling-min-domains-needs-donotschedule  minDomains only with
                                              whenUnsatisfiable DoNotSchedule

Each rule is a plain per-resource vet-cel check (a single Unit answers it), created
with Warn=true (advisory ValidationWarnings, never blocking). Promote one to blocking
with: cub trigger update <slug> --space <policy-space> --unwarn

The checks that need a cluster's facts (a topologyKey no node carries, minDomains
above the zone count) are in 'findings', since a Trigger sees one Unit and not its
cluster.

Except one resource from a rule with 'cub manage guardrails exception add';
'cub manage guardrails exception list' shows what is excepted, and why.`,
	}
	cmd.AddCommand(Pack.InstallCmd(preflight))
	return cmd
}
