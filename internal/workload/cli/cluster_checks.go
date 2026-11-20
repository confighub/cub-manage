// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

// The workload checks that read the cluster's other resources: whether a ServiceAccount whose
// token a pod mounts is bound to anything, whether a PodDisruptionBudget leaves a pod free to
// go, and whether an autoscaler keeps a workload at its replicas. Each declares what it reads as
// OtherData, which its rule's Trigger selects for each Space from the Units released to the same
// Target. A Unit with no Target is on no known cluster, so they pass it.

import (
	"github.com/confighub/sdk/managerkit/guardrails"

	"github.com/confighub/cub-manage/internal/celrule"
)

var (
	otherServiceAccounts = guardrails.OtherData{Key: "serviceaccounts", ResourceTypes: []string{"v1/ServiceAccount"}}
	otherBindings        = guardrails.OtherData{Key: "bindings", ResourceTypes: []string{
		"rbac.authorization.k8s.io/v1/RoleBinding", "rbac.authorization.k8s.io/v1/ClusterRoleBinding"}}
	otherWorkloads   = guardrails.OtherData{Key: "workloads", ResourceTypes: celrule.WorkloadTypes()}
	otherAutoscalers = guardrails.OtherData{Key: "autoscalers", ResourceTypes: celrule.AutoscalerTypes}
)

var (
	serviceAccount = celrule.ServiceAccountOf("r")

	// mountsToken: the pod mounts its ServiceAccount's token: its own
	// automountServiceAccountToken, or, unset, the ServiceAccount's, which is true unless set
	// false, as it is for one ConfigHub does not hold.
	mountsToken = "(" + celrule.PodSpecOf("r") + ".?automountServiceAccountToken.orValue(null) != null ? " +
		celrule.PodSpecOf("r") + ".automountServiceAccountToken == true : !" + celrule.Other(otherServiceAccounts.Key) +
		".exists(s, " + celrule.NamespaceOf("s") + " == " + celrule.NamespaceOf("r") + " && s.metadata.name == " + serviceAccount +
		" && s.?automountServiceAccountToken.orValue(true) == false))"

	// bound: a RoleBinding or ClusterRoleBinding names the pod's ServiceAccount as a subject. A
	// RoleBinding's subject is in the binding's namespace unless it names another.
	bound = celrule.Other(otherBindings.Key) + ".exists(b, (b.?subjects.orValue(null) == null ? [] : b.subjects).exists(s, " +
		"s.?kind.orValue('') == 'ServiceAccount' && s.?name.orValue('') == " + serviceAccount + " && " +
		"(s.?namespace.orValue('') != '' ? s.namespace : b.kind == 'RoleBinding' ? " + celrule.NamespaceOf("b") + " : '') == " +
		celrule.NamespaceOf("r") + "))"

	celServiceAccountToken = celrule.When(celrule.OnACluster+" && podLabels(r) != null && "+mountsToken+" && !"+bound,
		"["+celrule.Failure(celrule.PodSpecPath()+" + '.automountServiceAccountToken'", celrule.PodSpecOf("r")+".?automountServiceAccountToken",
			"'the pods mount the token of ServiceAccount ' + "+serviceAccount+" + ', which nothing binds to a role, so they need none'")+"]")
)

// replicasOf is how many pods the workload w runs: its autoscaler's minimum when one scales it,
// otherwise its replicas, 1 when unset.
func replicasOf(w string) string {
	scaling := celrule.Other(otherAutoscalers.Key) + ".filter(a, " + celrule.Scales("a", w) + ")"
	return "(size(" + scaling + ") > 0 ? " + scaling + ".map(a, " + celrule.MinReplicas("a") + ")[0] : int(" +
		w + ".?spec.?replicas.orValue(1)))"
}

// replicatedKinds are the kinds whose pods replicasOf counts. A budget selecting the pods of a
// DaemonSet, Job or CronJob, which have no replica count, is not checked against minAvailable.
const replicatedKinds = "['Deployment', 'StatefulSet', 'ReplicaSet', 'ReplicationController', 'Rollout', 'Pod']"

var (
	budgetSelected = celrule.Other(otherWorkloads.Key) + ".filter(w, " + celrule.NamespaceOf("w") + " == " + celrule.NamespaceOf("r") +
		" && podLabels(w) != null && r.?spec.?selector.orValue(null) != null && selects(r.spec.selector, podLabels(w)))"
	// budgetPods is how many pods the budget selects, summed as the size of a list of that many.
	budgetPods   = "size(" + budgetSelected + ".map(w, w.kind == 'Pod' ? [0] : lists.range(" + replicasOf("w") + ")).flatten())"
	minAvailable = "r.spec.minAvailable"
	// mustStay is how many of the pods minAvailable keeps: a number, or a percentage rounded up.
	mustStay = "(type(" + minAvailable + ") == string ? (int(" + minAvailable + ".substring(0, size(" + minAvailable + ") - 1)) * " +
		budgetPods + " + 99) / 100 : int(" + minAvailable + "))"

	celPDBAllowsEviction = celrule.When("string(r.?spec.?maxUnavailable.orValue('')) in ['0', '0%']",
		"["+celrule.Failure("'spec.maxUnavailable'", celrule.Some("r.spec.maxUnavailable"),
			"'maxUnavailable 0 lets no pod be evicted, so a node drain waits forever'")+"]") +
		" + " + celrule.When(celrule.OnACluster+" && r.?spec.?minAvailable.orValue(null) != null && "+
		"size("+budgetSelected+") > 0 && "+budgetSelected+".all(w, w.kind in "+replicatedKinds+") && "+
		budgetPods+" > 0 && "+mustStay+" >= "+budgetPods,
		"["+celrule.Failure("'spec.minAvailable'", celrule.Some(minAvailable),
			"'minAvailable ' + string("+minAvailable+") + ' keeps all ' + string("+budgetPods+") + ' of the pods it selects, so none can be evicted and a node drain waits forever'")+"]")

	// autoscaled: an autoscaler on the Unit's cluster scales r.
	autoscaled          = celrule.OnACluster + " && " + celrule.Other(otherAutoscalers.Key) + ".exists(a, " + celrule.Scales("a", "r") + ")"
	inListedEnvironment = "params['replicas.environments'].split(',').exists(e, e.trim().lowerAscii() == " + spaceLabels +
		"[?'Environment'].orValue('').lowerAscii())"

	// A workload in a listed environment runs replicas.min, unless an autoscaler scales it. One
	// setting no replicas runs 1; off a cluster, where its autoscaler cannot be looked for, it is
	// taken to be autoscaled.
	celReplicas = failIf(inListedEnvironment+" && !("+autoscaled+") && "+
		"(r.?spec.?replicas.orValue(null) != null ? r.spec.replicas < int(params['replicas.min']) : "+
		celrule.OnACluster+" && 1 < int(params['replicas.min']))",
		celrule.Lit("spec.replicas"), "r.?spec.?replicas",
		"(r.?spec.?replicas.orValue(null) != null ? 'the workload runs ' + string(r.spec.replicas) + ' replicas' : "+
			"'the workload sets no replicas, so runs 1,') + ' in ' + "+spaceLabels+"['Environment'] + "+
			"', fewer than ' + params['replicas.min'] + ', and no autoscaler scales it'")
)
