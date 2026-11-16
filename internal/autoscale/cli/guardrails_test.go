// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"strings"
	"testing"

	"github.com/confighub/cub-manage/internal/celrule/celruletest"
)

func TestAutoscalerNotPinned(t *testing.T) {
	pinned := "apiVersion: autoscaling/v2\nkind: HorizontalPodAutoscaler\nmetadata:\n  name: web\n  namespace: shop\nspec:\n  minReplicas: 3\n  maxReplicas: 3\n"
	result := celruletest.EvalCheck(t, celruletest.Check(t, Pack, "autoscale-not-pinned"), pinned)
	celruletest.Expect(t, result, "spec.minReplicas")
	celruletest.ExpectResource(t, result, "shop/web", "autoscaling/v2/HorizontalPodAutoscaler")
	if len(result.Details) == 1 && !strings.Contains(result.Details[0], "minReplicas 3") {
		t.Errorf("detail %q does not give the bounds", result.Details[0])
	}

	scaledObject := "apiVersion: keda.sh/v1alpha1\nkind: ScaledObject\nmetadata:\n  name: worker\nspec:\n  minReplicaCount: 2\n  maxReplicaCount: 2\n"
	celruletest.Expect(t, celruletest.EvalCheck(t, celruletest.Check(t, Pack, "autoscale-not-pinned"), scaledObject), "spec.minReplicaCount")

	for _, doc := range []string{
		"apiVersion: autoscaling/v2\nkind: HorizontalPodAutoscaler\nmetadata:\n  name: web\nspec:\n  minReplicas: 2\n  maxReplicas: 5\n",
		"apiVersion: autoscaling/v2\nkind: HorizontalPodAutoscaler\nmetadata:\n  name: web\nspec:\n  maxReplicas: 5\n",
		"apiVersion: keda.sh/v1alpha1\nkind: ScaledObject\nmetadata:\n  name: worker\nspec:\n  maxReplicaCount: 2\n",
	} {
		celruletest.Expect(t, celruletest.EvalCheck(t, celruletest.Check(t, Pack, "autoscale-not-pinned"), doc))
	}
}

func TestUnrelatedResourcesPass(t *testing.T) {
	celruletest.UnrelatedResourcesPass(t, Pack)
}

func TestAutoscaleMinReplicas(t *testing.T) {
	check := celruletest.Check(t, Pack, "autoscale-min-replicas")
	celruletest.Expect(t, celruletest.EvalCheck(t, check, "apiVersion: autoscaling/v2\nkind: HorizontalPodAutoscaler\nmetadata:\n  name: web\nspec:\n  maxReplicas: 5\n"), "spec.minReplicas")
	celruletest.Expect(t, celruletest.EvalCheck(t, check, "apiVersion: autoscaling/v2\nkind: HorizontalPodAutoscaler\nmetadata:\n  name: web\nspec:\n  minReplicas: 1\n  maxReplicas: 5\n"), "spec.minReplicas")
	celruletest.Expect(t, celruletest.EvalCheck(t, check, "apiVersion: autoscaling/v2\nkind: HorizontalPodAutoscaler\nmetadata:\n  name: web\nspec:\n  minReplicas: 2\n  maxReplicas: 5\n"))
}
