// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"strings"
	"testing"

	"sigs.k8s.io/yaml"

	"github.com/confighub/sdk/configkit/yqkit"

	"github.com/confighub/cub-manage/internal/scheduling"
)

const deployment = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
spec:
  template:
    metadata:
      labels:
        app: web
    spec:
      topologySpreadConstraints:
        - maxSkew: 2
          topologyKey: kubernetes.io/hostname
          whenUnsatisfiable: ScheduleAnyway
          labelSelector:
            matchLabels:
              app: web
      affinity:
        nodeAffinity:
          requiredDuringSchedulingIgnoredDuringExecution:
            nodeSelectorTerms: []
      containers:
        - name: web
`

// evalWorkload applies expr to the Deployment above and parses the result into
// the scheduling model.
func evalWorkload(t *testing.T, expr string) (*scheduling.WorkloadPlacement, map[string]any) {
	t.Helper()
	out, err := yqkit.EvalYQExpression(expr, deployment)
	if err != nil {
		t.Fatalf("eval %s: %v", expr, err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("parse result: %v\n%s", err, out)
	}
	clusters := scheduling.BuildFleet([]scheduling.FleetResource{{Origin: scheduling.ResourceOrigin{Cluster: "c"}, Doc: doc}})
	return clusters["c"].Workloads[0], doc
}

func spreadKeys(w *scheduling.WorkloadPlacement) string {
	var keys []string
	for _, c := range w.TopologySpread {
		keys = append(keys, c.TopologyKey+"="+c.WhenUnsatisfiable)
	}
	return strings.Join(keys, ",")
}

func TestUpsertSpread(t *testing.T) {
	// A new key is added; the constraint on another key is kept.
	w, _ := evalWorkload(t, upsertSpreadYQ(spreadConstraint{topologyKey: scheduling.ZoneKey, maxSkew: 1, whenUnsatisfiable: scheduling.DoNotSchedule,
		minDomains: 3, matchLabelKeys: []string{"pod-template-hash"}, nodeTaintsPolicy: "Honor"}))
	if got := spreadKeys(w); got != "kubernetes.io/hostname=ScheduleAnyway,topology.kubernetes.io/zone=DoNotSchedule" {
		t.Errorf("add: got %s", got)
	}
	zone := w.TopologySpread[1]
	if zone.MinDomains == nil || *zone.MinDomains != 3 || zone.NodeTaintsPolicy != "Honor" || len(zone.MatchLabelKeys) != 1 ||
		!zone.LabelSelector.Matches(w.PodLabels) {
		t.Errorf("add: constraint written wrong: %+v", zone)
	}

	// The same key is replaced, switching soft to hard rather than adding a second.
	w, _ = evalWorkload(t, upsertSpreadYQ(spreadConstraint{topologyKey: scheduling.HostnameKey, maxSkew: 1, whenUnsatisfiable: scheduling.DoNotSchedule}))
	if got := spreadKeys(w); got != "kubernetes.io/hostname=DoNotSchedule" {
		t.Errorf("replace: got %s", got)
	}

	// Removing the last constraint removes the list.
	_, doc := evalWorkload(t, removeSpreadYQ(scheduling.HostnameKey))
	podSpec := doc["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
	if _, ok := podSpec["topologySpreadConstraints"]; ok {
		t.Errorf("remove: empty topologySpreadConstraints left behind: %v", podSpec)
	}
}

func TestAntiAffinity(t *testing.T) {
	w, _ := evalWorkload(t, antiAffinityYQ(scheduling.HostnameKey, false, 100, false))
	if len(w.PodAntiAffinity) != 1 || w.PodAntiAffinity[0].Required || w.PodAntiAffinity[0].Weight != 100 ||
		!w.PodAntiAffinity[0].LabelSelector.Matches(w.PodLabels) || !w.HasRequiredNodeAffinity {
		t.Fatalf("preferred: got %+v (node affinity kept: %v)", w.PodAntiAffinity, w.HasRequiredNodeAffinity)
	}

	// Switching to required replaces the preferred term on the key.
	expr := antiAffinityYQ(scheduling.HostnameKey, false, 100, false) + " | " + antiAffinityYQ(scheduling.HostnameKey, true, 100, false)
	w, _ = evalWorkload(t, expr)
	if len(w.PodAntiAffinity) != 1 || !w.PodAntiAffinity[0].Required {
		t.Fatalf("required: got %+v", w.PodAntiAffinity)
	}

	// Removing it leaves the node affinity, and no empty podAntiAffinity.
	expr += " | " + antiAffinityYQ(scheduling.HostnameKey, false, 100, true)
	w, doc := evalWorkload(t, expr)
	affinity := doc["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)["affinity"].(map[string]any)
	if len(w.PodAntiAffinity) != 0 || !w.HasRequiredNodeAffinity {
		t.Errorf("remove: got %+v", w.PodAntiAffinity)
	}
	if _, ok := affinity["podAntiAffinity"]; ok {
		t.Errorf("remove: empty podAntiAffinity left behind: %v", affinity)
	}
}

func TestTopologySpreadSoftProfile(t *testing.T) {
	w, _ := evalWorkload(t, topologySpreadSoftYQ)
	if got := spreadKeys(w); got != "topology.kubernetes.io/zone=ScheduleAnyway,kubernetes.io/hostname=ScheduleAnyway" {
		t.Errorf("got %s", got)
	}
	for _, c := range w.TopologySpread {
		if c.MaxSkew != 1 || !c.LabelSelector.Matches(w.PodLabels) {
			t.Errorf("constraint written wrong: %+v", c)
		}
	}
}

func TestSpreadConstraintValidate(t *testing.T) {
	ok := spreadConstraint{topologyKey: scheduling.ZoneKey, maxSkew: 1, whenUnsatisfiable: scheduling.ScheduleAnyway}
	if err := ok.validate(); err != nil {
		t.Errorf("valid: %v", err)
	}
	bad := ok
	bad.minDomains = 2
	if err := bad.validate(); err == nil {
		t.Errorf("minDomains with ScheduleAnyway: want an error")
	}
	bad = ok
	bad.nodeTaintsPolicy = "honor"
	if err := bad.validate(); err == nil {
		t.Errorf("lower-case policy: want an error")
	}
}
