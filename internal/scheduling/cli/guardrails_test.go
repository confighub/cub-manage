// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"testing"

	"github.com/confighub/cub-manage/internal/celrule/celruletest"
)

const spread = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  namespace: shop
spec:
  template:
    metadata:
      labels:
        app: web
    spec:
      tolerations:
        - key: gpu
          operator: Exists
      topologySpreadConstraints:
        - maxSkew: 1
          topologyKey: topology.kubernetes.io/zone
          whenUnsatisfiable: DoNotSchedule
          labelSelector:
            matchLabels:
              app: web
        - maxSkew: 1
          topologyKey: kubernetes.io/hostname
          whenUnsatisfiable: ScheduleAnyway
          minDomains: 2
          labelSelector:
            matchLabels:
              app: api
      containers:
        - name: web
          image: nginx:1.27
`

// The scheduling rules name the constraint that fails, by index.
func TestSchedulingRulesNameTheFailingConstraint(t *testing.T) {
	result := celruletest.Eval(t, celSpreadSelectsOwnPods, spread)
	celruletest.Expect(t, result, "spec.template.spec.topologySpreadConstraints.1.labelSelector")
	celruletest.ExpectResource(t, result, "shop/web", "apps/v1/Deployment")

	celruletest.Expect(t, celruletest.Eval(t, celMinDomainsNeedsDoNotSchedule, spread),
		"spec.template.spec.topologySpreadConstraints.1.whenUnsatisfiable")
	celruletest.Expect(t, celruletest.Eval(t, celTolerationNeedsPlacement, spread), "spec.template.spec.tolerations")
}

func TestSchedulingRulesPass(t *testing.T) {
	placed := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
spec:
  template:
    metadata:
      labels:
        app: web
    spec:
      nodeSelector:
        pool: gpu
      tolerations:
        - key: gpu
          operator: Exists
      topologySpreadConstraints:
        - maxSkew: 1
          topologyKey: topology.kubernetes.io/zone
          minDomains: 2
          labelSelector:
            matchLabels:
              app: web
      containers:
        - name: web
          image: nginx:1.27
`
	for name, expression := range map[string]string{
		"toleration-needs-placement": celTolerationNeedsPlacement,
		"spread-selects-own-pods":    celSpreadSelectsOwnPods,
		"min-domains":                celMinDomainsNeedsDoNotSchedule,
	} {
		t.Run(name, func(t *testing.T) {
			celruletest.Expect(t, celruletest.Eval(t, expression, placed))
		})
	}
	// A constraint without a labelSelector selects nothing.
	noSelector := "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\nspec:\n  template:\n    spec:\n      topologySpreadConstraints:\n        - maxSkew: 1\n          topologyKey: zone\n      containers:\n        - name: web\n"
	celruletest.Expect(t, celruletest.Eval(t, celSpreadSelectsOwnPods, noSelector), "spec.template.spec.topologySpreadConstraints.0.labelSelector")
}

func TestUnrelatedResourcesPass(t *testing.T) {
	celruletest.UnrelatedResourcesPass(t, Pack.Rules)
}
