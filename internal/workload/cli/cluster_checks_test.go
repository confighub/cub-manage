// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"strings"
	"testing"

	"github.com/confighub/cub-manage/internal/celrule/celruletest"
)

var clusterContext = map[string]any{"TargetID": "4d6b2a1e-0000-4000-8000-000000000001"}

func deploymentOf(name string, replicas string) string {
	doc := "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: " + name + "\n  namespace: shop\nspec:\n"
	if replicas != "" {
		doc += "  replicas: " + replicas + "\n"
	}
	return doc + "  template:\n    metadata:\n      labels: {app: " + name + "}\n    spec:\n      serviceAccountName: " + name + "\n"
}

func TestServiceAccountToken(t *testing.T) {
	check := celruletest.Check(t, Pack, "service-account-token")
	web := deploymentOf("web", "")
	binding := func(kind, namespace, subject string) string {
		return "apiVersion: rbac.authorization.k8s.io/v1\nkind: " + kind + "\nmetadata:\n  name: b\n  namespace: " + namespace +
			"\nroleRef: {kind: ClusterRole, name: view}\nsubjects:\n  - " + subject + "\n"
	}

	result := celruletest.EvalCheckOther(t, clusterContext, nil, nil, check, web)
	celruletest.Expect(t, result, "spec.template.spec.automountServiceAccountToken")
	celruletest.ExpectResource(t, result, "shop/web", "apps/v1/Deployment")
	// A RoleBinding's subject is in its namespace; a ClusterRoleBinding's names one.
	celruletest.Expect(t, celruletest.EvalCheckOther(t, clusterContext, nil,
		celruletest.Other{"bindings": {binding("RoleBinding", "shop", "{kind: ServiceAccount, name: web}")}}, check, web))
	celruletest.Expect(t, celruletest.EvalCheckOther(t, clusterContext, nil,
		celruletest.Other{"bindings": {binding("ClusterRoleBinding", "", "{kind: ServiceAccount, name: web, namespace: shop}")}}, check, web))
	celruletest.Expect(t, celruletest.EvalCheckOther(t, clusterContext, nil,
		celruletest.Other{"bindings": {binding("RoleBinding", "billing", "{kind: ServiceAccount, name: web}")}}, check, web),
		"spec.template.spec.automountServiceAccountToken")
	// The pod, or its ServiceAccount, can turn the token off.
	celruletest.Expect(t, celruletest.EvalCheckOther(t, clusterContext, nil, nil, check,
		strings.Replace(web, "serviceAccountName: web", "serviceAccountName: web\n      automountServiceAccountToken: false", 1)))
	celruletest.Expect(t, celruletest.EvalCheckOther(t, clusterContext, nil, celruletest.Other{"serviceaccounts": {
		"apiVersion: v1\nkind: ServiceAccount\nmetadata:\n  name: web\n  namespace: shop\nautomountServiceAccountToken: false\n",
	}}, check, web))
	celruletest.Expect(t, celruletest.EvalCheckOther(t, map[string]any{}, nil, nil, check, web))
}

func TestPDBAllowsEviction(t *testing.T) {
	check := celruletest.Check(t, Pack, "pdb-allows-eviction")
	pdb := func(policy string) string {
		return "apiVersion: policy/v1\nkind: PodDisruptionBudget\nmetadata:\n  name: web\n  namespace: shop\nspec:\n  " + policy +
			"\n  selector:\n    matchLabels: {app: web}\n"
	}
	three := celruletest.Other{"workloads": {deploymentOf("web", "3")}}

	celruletest.Expect(t, celruletest.EvalCheckOther(t, map[string]any{}, nil, nil, check, pdb("maxUnavailable: 0")), "spec.maxUnavailable")
	celruletest.Expect(t, celruletest.EvalCheckOther(t, clusterContext, nil, three, check, pdb("maxUnavailable: 1")))
	celruletest.Expect(t, celruletest.EvalCheckOther(t, clusterContext, nil, three, check, pdb("minAvailable: 2")))
	result := celruletest.EvalCheckOther(t, clusterContext, nil, three, check, pdb("minAvailable: 3"))
	celruletest.Expect(t, result, "spec.minAvailable")
	if len(result.Details) != 1 || !strings.Contains(result.Details[0], "keeps all 3") {
		t.Errorf("details = %q, want one saying it keeps all 3", result.Details)
	}
	// A percentage rounds up: 67% of 3 is 3, 50% is 2.
	celruletest.Expect(t, celruletest.EvalCheckOther(t, clusterContext, nil, three, check, pdb("minAvailable: 67%")), "spec.minAvailable")
	celruletest.Expect(t, celruletest.EvalCheckOther(t, clusterContext, nil, three, check, pdb("minAvailable: 50%")))
	// Two workloads' pods add up: 3 + 1.
	celruletest.Expect(t, celruletest.EvalCheckOther(t, clusterContext, nil, celruletest.Other{"workloads": {
		deploymentOf("web", "3"), strings.Replace(deploymentOf("web-canary", ""), "app: web-canary", "app: web", 1),
	}}, check, pdb("minAvailable: 3")))
	// An autoscaled workload counts at its autoscaler's minimum.
	hpa := "apiVersion: autoscaling/v2\nkind: HorizontalPodAutoscaler\nmetadata:\n  name: web\n  namespace: shop\nspec:\n" +
		"  scaleTargetRef: {apiVersion: apps/v1, kind: Deployment, name: web}\n  minReplicas: 2\n  maxReplicas: 10\n"
	celruletest.Expect(t, celruletest.EvalCheckOther(t, clusterContext, nil,
		celruletest.Other{"workloads": {deploymentOf("web", "3")}, "autoscalers": {hpa}}, check, pdb("minAvailable: 2")), "spec.minAvailable")
	// A DaemonSet's pods have no count to compare with.
	daemonSet := strings.Replace(deploymentOf("web", ""), "kind: Deployment", "kind: DaemonSet", 1)
	celruletest.Expect(t, celruletest.EvalCheckOther(t, clusterContext, nil, celruletest.Other{"workloads": {daemonSet}}, check, pdb("minAvailable: 1")))
}

func TestReplicasUnlessAutoscaled(t *testing.T) {
	check := celruletest.Check(t, Pack, "replicas")
	prod := map[string]any{"TargetID": clusterContext["TargetID"], "SpaceLabels": map[string]any{"Environment": "prod"}}
	hpa := "apiVersion: autoscaling/v2\nkind: HorizontalPodAutoscaler\nmetadata:\n  name: web\n  namespace: shop\nspec:\n" +
		"  scaleTargetRef: {apiVersion: apps/v1, kind: Deployment, name: web}\n  maxReplicas: 10\n"
	scaledObject := "apiVersion: keda.sh/v1alpha1\nkind: ScaledObject\nmetadata:\n  name: web\n  namespace: shop\nspec:\n" +
		"  scaleTargetRef: {name: web}\n"

	// On a cluster, a workload setting no replicas runs 1.
	celruletest.Expect(t, celruletest.EvalCheckOther(t, prod, nil, nil, check, deploymentOf("web", "")), "spec.replicas")
	celruletest.Expect(t, celruletest.EvalCheckOther(t, prod, nil, nil, check, deploymentOf("web", "1")), "spec.replicas")
	celruletest.Expect(t, celruletest.EvalCheckOther(t, prod, nil, nil, check, deploymentOf("web", "2")))
	// An autoscaler scales it, a ScaledObject's target being a Deployment unless it says otherwise.
	celruletest.Expect(t, celruletest.EvalCheckOther(t, prod, nil, celruletest.Other{"autoscalers": {hpa}}, check, deploymentOf("web", "")))
	celruletest.Expect(t, celruletest.EvalCheckOther(t, prod, nil, celruletest.Other{"autoscalers": {scaledObject}}, check, deploymentOf("web", "1")))
	celruletest.Expect(t, celruletest.EvalCheckOther(t, prod, nil, celruletest.Other{"autoscalers": {hpa}}, check, deploymentOf("api", "")), "spec.replicas")
	// Off a cluster, one setting none is taken to be autoscaled.
	celruletest.Expect(t, celruletest.EvalCheckOther(t, map[string]any{"SpaceLabels": map[string]any{"Environment": "prod"}}, nil, nil, check, deploymentOf("web", "")))
}
