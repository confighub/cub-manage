// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"strings"
	"testing"

	"github.com/confighub/cub-manage/internal/celrule/celruletest"
)

func TestAutoscaleTarget(t *testing.T) {
	check := celruletest.Check(t, Pack, "autoscale-target")
	onCluster := map[string]any{"TargetID": "4d6b2a1e-0000-4000-8000-000000000001"}
	web := "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\n  namespace: shop\nspec:\n  template:\n    metadata:\n      labels: {app: web}\n" +
		"    spec:\n      containers:\n        - name: app\n          resources:\n            requests: {cpu: 100m}\n"
	workloads := celruletest.Other{"workloads": {web}}
	hpa := "apiVersion: autoscaling/v2\nkind: HorizontalPodAutoscaler\nmetadata:\n  name: web\n  namespace: shop\nspec:\n" +
		"  scaleTargetRef: {apiVersion: apps/v1, kind: Deployment, name: web}\n  maxReplicas: 10\n  metrics:\n" +
		"    - type: Resource\n      resource: {name: cpu, target: {type: Utilization, averageUtilization: 70}}\n"

	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, workloads, check, hpa))
	result := celruletest.EvalCheckOther(t, onCluster, nil, nil, check, hpa)
	celruletest.Expect(t, result, "spec.scaleTargetRef")
	celruletest.ExpectResource(t, result, "shop/web", "autoscaling/v2/HorizontalPodAutoscaler")
	// A target setting replicas has its scale reset by each apply.
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil,
		celruletest.Other{"workloads": {strings.Replace(web, "spec:\n  template", "spec:\n  replicas: 3\n  template", 1)}}, check, hpa),
		"spec.scaleTargetRef")
	// Memory utilization is a percentage of a request the container does not make.
	memory := strings.Replace(hpa, "name: cpu", "name: memory", 1)
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, workloads, check, memory), "spec.metrics.0.resource")

	// A ScaledObject's target is a Deployment unless it says otherwise, and a cpu trigger
	// measures utilization unless it says otherwise.
	scaledObject := "apiVersion: keda.sh/v1alpha1\nkind: ScaledObject\nmetadata:\n  name: web\n  namespace: shop\nspec:\n" +
		"  scaleTargetRef: {name: web}\n  triggers:\n    - type: cpu\n      metadata: {value: '70'}\n    - type: memory\n      metricType: AverageValue\n      metadata: {value: 512Mi}\n"
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, workloads, check, scaledObject))
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, workloads, check,
		strings.Replace(scaledObject, "metricType: AverageValue", "metricType: Utilization", 1)), "spec.triggers.1")
	// v1's targetCPUUtilizationPercentage.
	v1 := "apiVersion: autoscaling/v1\nkind: HorizontalPodAutoscaler\nmetadata:\n  name: web\n  namespace: shop\nspec:\n" +
		"  scaleTargetRef: {apiVersion: apps/v1, kind: Deployment, name: web}\n  maxReplicas: 10\n  targetCPUUtilizationPercentage: 70\n"
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, workloads, check, v1))
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil,
		celruletest.Other{"workloads": {strings.Replace(web, "requests: {cpu: 100m}", "requests: {memory: 64Mi}", 1)}}, check, v1),
		"spec.targetCPUUtilizationPercentage")
	celruletest.Expect(t, celruletest.EvalCheckOther(t, map[string]any{}, nil, nil, check, hpa))
}
