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
	result := celruletest.EvalCheck(t, celruletest.Check(t, Pack, "scheduling-spread-selects-own-pods"), spread)
	celruletest.Expect(t, result, "spec.template.spec.topologySpreadConstraints.1.labelSelector")
	celruletest.ExpectResource(t, result, "shop/web", "apps/v1/Deployment")

	celruletest.Expect(t, celruletest.EvalCheck(t, celruletest.Check(t, Pack, "scheduling-min-domains-needs-donotschedule"), spread),
		"spec.template.spec.topologySpreadConstraints.1.whenUnsatisfiable")
	celruletest.Expect(t, celruletest.EvalCheck(t, celruletest.Check(t, Pack, "scheduling-toleration-needs-placement"), spread), "spec.template.spec.tolerations")
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
	for _, check := range Pack.Checks {
		t.Run(check.Name, func(t *testing.T) {
			celruletest.Expect(t, celruletest.EvalCheck(t, check, placed))
		})
	}
	// A constraint without a labelSelector selects nothing.
	noSelector := "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\nspec:\n  template:\n    spec:\n      topologySpreadConstraints:\n        - maxSkew: 1\n          topologyKey: zone\n      containers:\n        - name: web\n"
	celruletest.Expect(t, celruletest.EvalCheck(t, celruletest.Check(t, Pack, "scheduling-spread-selects-own-pods"), noSelector), "spec.template.spec.topologySpreadConstraints.0.labelSelector")
}

func TestUnrelatedResourcesPass(t *testing.T) {
	celruletest.UnrelatedResourcesPass(t, Pack)
}

const spreadless = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  namespace: shop
spec:
  replicas: 3
  template:
    spec:
      tolerations:
        - key: node-role.kubernetes.io/control-plane
          effect: NoSchedule
      topologySpreadConstraints:
        - maxSkew: 1
          topologyKey: example.com/rack
          whenUnsatisfiable: DoNotSchedule
        - maxSkew: 1
          topologyKey: example.com/row
          whenUnsatisfiable: ScheduleAnyway
      affinity:
        podAntiAffinity:
          requiredDuringSchedulingIgnoredDuringExecution:
            - topologyKey: kubernetes.io/hostname
            - topologyKey: example.com/room
`

func facts(kv ...string) map[string]any {
	f := map[string]any{}
	for i := 0; i+1 < len(kv); i += 2 {
		f[kv[i]] = kv[i+1]
	}
	return map[string]any{"TargetFacts": f}
}

func TestMoreSchedulingChecks(t *testing.T) {
	keys := facts("Cluster.NodeLabelKeys", "kubernetes.io/hostname,topology.kubernetes.io/zone")
	celruletest.Expect(t, celruletest.EvalCheckIn(t, keys, celruletest.Check(t, Pack, "topology-key-exists"), spreadless),
		"spec.template.spec.topologySpreadConstraints.0.topologyKey",
		"spec.template.spec.affinity.podAntiAffinity.requiredDuringSchedulingIgnoredDuringExecution.1.topologyKey")
	celruletest.Expect(t, celruletest.EvalCheck(t, celruletest.Check(t, Pack, "topology-key-exists"), spreadless))

	celruletest.Expect(t, celruletest.EvalCheck(t, celruletest.Check(t, Pack, "no-control-plane"), spreadless), "spec.template.spec.tolerations.0.key")

	unspread := "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\nspec:\n  replicas: 3\n  template:\n    spec:\n      containers:\n        - name: web\n"
	spread := celruletest.Check(t, Pack, "replicas-spread")
	celruletest.Expect(t, celruletest.EvalCheckIn(t, facts("Cluster.Distribution", "kubeadm"), spread, unspread), "spec.template.spec.topologySpreadConstraints")
	for name, context := range map[string]map[string]any{
		"managed":        facts("Cluster.Distribution", "eks"),
		"default spread": facts("Cluster.Distribution", "kubeadm", "Scheduling.DefaultTopologySpread", "[]"),
		"no facts":       {},
	} {
		t.Run(name, func(t *testing.T) { celruletest.Expect(t, celruletest.EvalCheckIn(t, context, spread, unspread)) })
	}
	celruletest.Expect(t, celruletest.EvalCheckIn(t, facts("Cluster.Distribution", "kubeadm"), spread, spreadless))
}
