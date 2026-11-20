// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"github.com/confighub/sdk/managerkit/guardrails"

	"github.com/confighub/cub-manage/internal/celrule"
)

// otherWorkloads are the workloads on an autoscaler's cluster, which autoscale-target reads.
var otherWorkloads = guardrails.OtherData{Key: "workloads", ResourceTypes: celrule.WorkloadTypes()}

var (
	scaleTargets = celrule.Other(otherWorkloads.Key) + ".filter(w, " + celrule.Scales("r", "w") + ")"
	targetKind   = "(r.kind == 'ScaledObject' ? r.?spec.?scaleTargetRef.?kind.orValue('Deployment') : r.?spec.?scaleTargetRef.?kind.orValue(''))"

	// unrequested is whether some container of a target does not request the resource, a CEL
	// string, which a utilization metric is a percentage of.
	unrequested = func(resource string) string {
		return scaleTargets + ".exists(w, (" + celrule.PodSpecOf("w") + ".?containers.orValue(null) == null ? [] : " +
			celrule.PodSpecOf("w") + ".containers).exists(c, !(" + resource + " in c.?resources.?requests.orValue({}))))"
	}
	unrequestedDetail = func(resource string) string {
		return "'the target\\'s containers do not all request ' + " + resource + " + ', so utilization of it cannot be computed'"
	}

	// metricsFailures fails each utilization metric of CPU or memory whose resource the target's
	// containers do not all request: an HPA's Resource metrics and v1 targetCPUUtilizationPercentage,
	// and a ScaledObject's cpu and memory triggers, whose metricType is Utilization unless set.
	metricsFailures = "(r.kind == 'HorizontalPodAutoscaler' ? " +
		"lists.range(size(r.?spec.?metrics.orValue(null) == null ? [] : r.spec.metrics)).filter(i, " +
		"r.spec.metrics[i].?type.orValue('') == 'Resource' && r.spec.metrics[i].?resource.?name.orValue('') in ['cpu', 'memory'] && " +
		"r.spec.metrics[i].resource.?target.?type.orValue('') == 'Utilization' && " + unrequested("r.spec.metrics[i].resource.name") + ").map(i, " +
		celrule.Failure("'spec.metrics.' + string(i) + '.resource'", celrule.Some("r.spec.metrics[i].resource"), unrequestedDetail("r.spec.metrics[i].resource.name")) +
		") + (r.?spec.?targetCPUUtilizationPercentage.orValue(null) != null && " + unrequested("'cpu'") + " ? [" +
		celrule.Failure("'spec.targetCPUUtilizationPercentage'", celrule.Some("r.spec.targetCPUUtilizationPercentage"), unrequestedDetail("'cpu'")) +
		"] : []) : " +
		"lists.range(size(r.?spec.?triggers.orValue(null) == null ? [] : r.spec.triggers)).filter(i, " +
		"r.spec.triggers[i].?type.orValue('') in ['cpu', 'memory'] && r.spec.triggers[i].?metricType.orValue('Utilization') == 'Utilization' && " +
		unrequested("r.spec.triggers[i].type") + ").map(i, " +
		celrule.Failure("'spec.triggers.' + string(i)", celrule.Some("r.spec.triggers[i]"), unrequestedDetail("r.spec.triggers[i].type")) + "))"

	// celAutoscaleTarget fails an autoscaler whose target is not on its cluster, or sets
	// replicas, which each apply resets the scale to, or whose utilization metrics name a
	// resource its containers do not request. A Unit with no Target is on no known cluster.
	celAutoscaleTarget = celrule.When(celrule.OnACluster,
		"(size("+scaleTargets+") == 0 ? ["+
			celrule.Failure("'spec.scaleTargetRef'", "r.?spec.?scaleTargetRef",
				"'no ' + "+targetKind+" + ' ' + r.?spec.?scaleTargetRef.?name.orValue('') + ' in namespace ' + "+celrule.NamespaceOf("r")+" + ' on the cluster to scale'")+
			"] : ("+scaleTargets+".exists(w, w.?spec.?replicas.orValue(null) != null) ? ["+
			celrule.Failure("'spec.scaleTargetRef'", celrule.Some("r.spec.scaleTargetRef"),
				"'the target sets replicas, which each apply resets the scale to'")+
			"] : []) + "+metricsFailures+")")
)

var autoscaleTarget = guardrails.Check{Control: "CH-1203", Name: "autoscale-target", Rule: "autoscale-target", DisplayName: "Autoscalers have a target to scale",
	Description: "Warns on an HPA or ScaledObject whose scale target is not on its cluster; whose target sets replicas, which each apply resets the scale to; or with a CPU or memory utilization metric when the target's containers do not all request that resource, so the utilization cannot be computed. Not checked for a Unit with no Target. Fix: correct scaleTargetRef, remove replicas from the target, or set the containers' requests.",
	Engine:      guardrails.EngineCEL, Priority: guardrails.PriorityHigh,
	ResourceTypes: celrule.AutoscalerTypes, Failures: celAutoscaleTarget, OtherData: []guardrails.OtherData{otherWorkloads},
	Corresponds: []string{"SC:horizontalpodautoscaler-has-target", "SC:deployment-targeted-by-hpa-does-not-have-replicas-configured", "KL:dangling-horizontalpodautoscaler", "KY:check-hpa-exists"}}
