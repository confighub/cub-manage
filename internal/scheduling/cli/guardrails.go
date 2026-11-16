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
	"github.com/confighub/cub-manage/internal/scheduling"
)

// celTolerationNeedsPlacement passes unless a controller has tolerations but
// neither a non-empty nodeSelector nor a required node affinity (i.e. it tolerates
// a taint but doesn't actually pin where it lands). It fails at the tolerations.
var celTolerationNeedsPlacement = celrule.When("has(r.spec.template.spec.tolerations)"+
	" && !(has(r.spec.template.spec.nodeSelector) && size(r.spec.template.spec.nodeSelector) > 0)"+
	" && !(has(r.spec.template.spec.affinity) && has(r.spec.template.spec.affinity.nodeAffinity) && has(r.spec.template.spec.affinity.nodeAffinity.requiredDuringSchedulingIgnoredDuringExecution))",
	"["+celrule.Failure(celrule.Lit("spec.template.spec.tolerations"), celrule.Some("r.spec.template.spec.tolerations"),
		celrule.Lit("the pods tolerate a taint but have no nodeSelector or required node affinity, so they can land on any node"))+"]")

// spreadConstraints is a controller's topology spread constraints, and
// spreadFailures fails each of them for which fails, a condition on the
// constraint c, holds, at field, with value and detail. i indexes the
// constraint; c is bound to it as the element of a one-element list.
const spreadConstraints = "r.spec.template.spec.topologySpreadConstraints"

func spreadFailures(fails, field, value, detail string) string {
	return celrule.When("has("+spreadConstraints+")",
		"lists.range(size("+spreadConstraints+")).filter(i, ["+spreadConstraints+"[i]].all(c, "+fails+")).map(i, "+
			celrule.Failure(celrule.Indexed("spec.template.spec.topologySpreadConstraints", "i", field), value, detail)+")")
}

// celSpreadSelectsOwnPods passes unless a controller has a topology spread
// constraint with no labelSelector, or whose matchLabels its own pod template
// does not carry: its Pods then do not count toward the skew. (matchExpressions
// are not evaluated; the findings analyzer checks them.)
var celSpreadSelectsOwnPods = spreadFailures(
	"!(has(c.labelSelector)"+
		" && (!has(c.labelSelector.matchLabels) || (has(r.spec.template.metadata.labels)"+
		" && c.labelSelector.matchLabels.all(k, k in r.spec.template.metadata.labels && r.spec.template.metadata.labels[k] == c.labelSelector.matchLabels[k]))))",
	"labelSelector", spreadConstraints+"[i].?labelSelector",
	"'topology spread constraint ' + string(i) + ' does not select the pods of this workload, so they do not count toward its skew'")

// celMinDomainsNeedsDoNotSchedule passes unless a controller's spread constraint
// sets minDomains with a whenUnsatisfiable other than DoNotSchedule (its
// default), which the API server rejects.
var celMinDomainsNeedsDoNotSchedule = spreadFailures(
	"has(c.minDomains) && has(c.whenUnsatisfiable) && c.whenUnsatisfiable != 'DoNotSchedule'",
	"whenUnsatisfiable", celrule.Some(spreadConstraints+"[i].whenUnsatisfiable"),
	"'topology spread constraint ' + string(i) + ' sets minDomains with whenUnsatisfiable ' + "+spreadConstraints+"[i].whenUnsatisfiable + ', which the API server rejects'")

// targetFacts are the facts of the Target of the Unit under test, empty when it has none.
const targetFacts = "(functionContext.?TargetFacts.orValue(null) != null ? functionContext.TargetFacts : {})"

const podTemplate = "r.spec.template.spec"

// celReplicasSpread fails a workload of two or more replicas with neither topology spread
// constraints nor pod anti-affinity, on a Target whose facts say its cluster spreads nothing by
// default: a cluster not run by EKS, GKE or AKS, whose default spread cannot change, with no
// Scheduling.DefaultTopologySpread recorded. A Target without facts passes.
var celReplicasSpread = celrule.When(
	"has(r.spec) && has(r.spec.replicas) && r.spec.replicas >= 2"+
		" && !(has("+podTemplate+".topologySpreadConstraints) && size("+podTemplate+".topologySpreadConstraints) > 0)"+
		" && !(has("+podTemplate+".affinity) && has("+podTemplate+".affinity.podAntiAffinity))"+
		" && '"+scheduling.FactDistribution+"' in "+targetFacts+
		" && !("+targetFacts+"['"+scheduling.FactDistribution+"'] in ['eks', 'gke', 'aks'])"+
		" && !('"+scheduling.FactDefaultTopologySpread+"' in "+targetFacts+")",
	"["+celrule.Failure(celrule.Lit("spec.template.spec.topologySpreadConstraints"), celrule.None,
		"'the workload runs ' + string(r.spec.replicas) + ' replicas with no topology spread or pod anti-affinity, on a cluster with no default spread recorded, so they can all land on one node'")+"]")

// hasPath holds when every field of the path under the pod template is set: has() on a field
// selects its parent, which is an error when the parent is not there.
func hasPath(path string) string {
	rest := strings.Split(strings.TrimPrefix(path, podTemplate+"."), ".")
	conds := make([]string, len(rest))
	for i := range rest {
		conds[i] = "has(" + podTemplate + "." + strings.Join(rest[:i+1], ".") + ")"
	}
	return strings.Join(conds, " && ")
}

// hardKeyFailures fails each element c of list, the DoNotSchedule spread constraints or the
// required pod affinity or anti-affinity terms, that filter selects and whose topologyKey is not
// among the Target's node label keys: the pods cannot be scheduled without it.
func hardKeyFailures(list, filter, field, detail string) string {
	return "(" + hasPath(list) + " ? lists.range(size(" + list + ")).filter(i, [" + list + "[i]].all(c, " + filter +
		" && has(c.topologyKey) && !(','+" + targetFacts + "['" + scheduling.FactNodeLabelKeys + "'] + ',').contains(',' + c.topologyKey + ','))).map(i, " +
		celrule.Failure(celrule.Indexed(strings.TrimPrefix(list, "r."), "i", field), celrule.Some(list+"[i].topologyKey"), detail) + ") : [])"
}

var celTopologyKeyExists = celrule.When("'"+scheduling.FactNodeLabelKeys+"' in "+targetFacts,
	hardKeyFailures(spreadConstraints, "(!has(c.whenUnsatisfiable) || c.whenUnsatisfiable == 'DoNotSchedule')", "topologyKey",
		"'topology spread constraint ' + string(i) + ' requires the node label ' + "+spreadConstraints+"[i].topologyKey + ', which no node of the Target carries, so the pods stay Pending'")+" + "+
		hardKeyFailures(podTemplate+".affinity.podAntiAffinity.requiredDuringSchedulingIgnoredDuringExecution", "true", "topologyKey",
			"'required pod anti-affinity term ' + string(i) + ' uses the node label ' + "+podTemplate+".affinity.podAntiAffinity.requiredDuringSchedulingIgnoredDuringExecution[i].topologyKey + ', which no node of the Target carries'")+" + "+
		hardKeyFailures(podTemplate+".affinity.podAffinity.requiredDuringSchedulingIgnoredDuringExecution", "true", "topologyKey",
			"'required pod affinity term ' + string(i) + ' uses the node label ' + "+podTemplate+".affinity.podAffinity.requiredDuringSchedulingIgnoredDuringExecution[i].topologyKey + ', which no node of the Target carries'"))

const tolerations = "r.spec.template.spec.tolerations"

// celNoControlPlane fails each toleration of the control-plane taint, or the master taint that
// preceded it.
var celNoControlPlane = celrule.When("has("+tolerations+")",
	"lists.range(size("+tolerations+")).filter(i, has("+tolerations+"[i].key) && "+tolerations+"[i].key in ['node-role.kubernetes.io/control-plane', 'node-role.kubernetes.io/master'])"+
		".map(i, "+celrule.Failure(celrule.Indexed("spec.template.spec.tolerations", "i", "key"), celrule.Some(tolerations+"[i].key"),
		"'toleration ' + string(i) + ' lets the pods onto control-plane nodes'")+")")

var Pack = guardrails.Pack{
	Group: "scheduling",
	Checks: []guardrails.Check{
		{Control: "CH-1101", Name: "scheduling-toleration-needs-placement", Rule: "scheduling-toleration-needs-placement",
			DisplayName: "Workloads that tolerate a taint choose their nodes",
			Engine:      guardrails.EngineCEL, Priority: guardrails.PriorityMedium, ResourceTypes: celrule.Controllers,
			Description: "Warns on a controller that tolerates a taint but has no nodeSelector or required node affinity (may schedule onto general nodes). Fix: `cub manage scheduling set-node-selector` / `set-node-affinity`, or a placement profile.",
			Failures:    celTolerationNeedsPlacement},
		{Control: "CH-1102", Name: "scheduling-spread-selects-own-pods", Rule: "scheduling-spread-selects-own-pods",
			DisplayName: "Topology spread selects the workload Pods",
			Engine:      guardrails.EngineCEL, Priority: guardrails.PriorityMedium, ResourceTypes: celrule.Controllers,
			Description: "Warns on a controller whose topology spread constraint has no labelSelector, or one its own pod template's labels do not match, so its Pods never count toward the skew. Fix: `cub manage scheduling set-topology-spread`, which selects the workload's own Pods.",
			Failures:    celSpreadSelectsOwnPods},
		{Control: "CH-1103", Name: "scheduling-min-domains-needs-donotschedule", Rule: "scheduling-min-domains-needs-donotschedule",
			DisplayName: "minDomains comes with DoNotSchedule",
			Engine:      guardrails.EngineCEL, Priority: guardrails.PriorityHigh, ResourceTypes: celrule.Controllers,
			Description: "Warns on a controller whose topology spread constraint sets minDomains without whenUnsatisfiable DoNotSchedule, which the API server rejects. Fix: `cub manage scheduling set-topology-spread --when-unsatisfiable DoNotSchedule --min-domains N`, or drop minDomains.",
			Failures:    celMinDomainsNeedsDoNotSchedule},
		{Control: "CH-1104", Name: "replicas-spread", Rule: "replicas-spread", DisplayName: "Replicas spread across nodes",
			Description: "Warns on a Deployment or StatefulSet of two or more replicas with no topology spread constraints or pod anti-affinity, on a Target whose cluster spreads nothing by default: one not run by EKS, GKE or AKS, with no Scheduling.DefaultTopologySpread fact. Its replicas can all land on one node. A Target without facts passes. Fix: `cub manage scheduling set-topology-spread <space>/<unit>`.",
			Engine:      guardrails.EngineCEL, Priority: guardrails.PriorityMedium, ResourceTypes: []string{"apps/v1/Deployment", "apps/v1/StatefulSet"},
			Failures:    celReplicasSpread,
			Corresponds: []string{"SC:deployment-has-host-podantiaffinity", "KL:no-anti-affinity", "KY:topologyspreadconstraints-policy"}},
		{Control: "CH-1105", Name: "topology-key-exists", Rule: "topology-key-exists", DisplayName: "Hard constraints use node labels the cluster has",
			Description: "Warns on a DoNotSchedule topology spread constraint or required pod affinity or anti-affinity term whose topologyKey no node of the Target carries, as its Cluster.NodeLabelKeys fact lists them: the pods stay Pending. A Target without the fact passes. Fix: use a label the nodes carry, such as topology.kubernetes.io/zone.",
			Engine:      guardrails.EngineCEL, Priority: guardrails.PriorityHigh, ResourceTypes: celrule.Controllers,
			Failures: celTopologyKeyExists},
		{Control: "CH-1106", Name: "no-control-plane", Rule: "no-control-plane", DisplayName: "Pods stay off control-plane nodes",
			Description: "Warns on a toleration of the node-role.kubernetes.io/control-plane taint, or of the master taint before it, which lets the pods run beside the API server and etcd. Fix: remove the toleration, or except a node agent that has to run everywhere.",
			Engine:      guardrails.EngineCEL, Priority: guardrails.PriorityHigh, ResourceTypes: celrule.Controllers,
			Failures:    celNoControlPlane,
			Corresponds: []string{"KY:restrict-controlplane-scheduling"}},
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
