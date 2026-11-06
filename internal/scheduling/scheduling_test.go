// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package scheduling

import (
	"encoding/json"
	"testing"

	api "github.com/confighub/sdk/core/function/api"
)

func buildOne(t *testing.T, manifest string) *WorkloadPlacement {
	t.Helper()
	var doc any
	if err := json.Unmarshal([]byte(manifest), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	clusters := BuildFleet([]FleetResource{{Origin: ResourceOrigin{Cluster: "c1"}, Doc: doc}})
	c := clusters["c1"]
	if c == nil || len(c.Workloads) != 1 {
		t.Fatalf("want 1 workload, got %+v", c)
	}
	return c.Workloads[0]
}

func TestParse_Placement(t *testing.T) {
	w := buildOne(t, `{
      "apiVersion": "apps/v1", "kind": "Deployment",
      "metadata": {"name": "gpu-job", "namespace": "ml"},
      "spec": {"template": {"spec": {
        "nodeSelector": {"pool": "gpu"},
        "tolerations": [{"key": "nvidia.com/gpu", "operator": "Exists", "effect": "NoSchedule"}],
        "affinity": {"nodeAffinity": {"requiredDuringSchedulingIgnoredDuringExecution": {"nodeSelectorTerms": []}}},
        "containers": [{"name": "app"}]
      }}}
    }`)
	if w.NodeSelector["pool"] != "gpu" {
		t.Errorf("nodeSelector not parsed: %v", w.NodeSelector)
	}
	if len(w.Tolerations) != 1 || w.Tolerations[0].Key != "nvidia.com/gpu" {
		t.Errorf("tolerations not parsed: %v", w.Tolerations)
	}
	if !w.HasRequiredNodeAffinity {
		t.Errorf("required node affinity not detected")
	}
	if !w.Constrained() {
		t.Errorf("workload with nodeSelector should be constrained")
	}
}

func TestParse_CronJobPath(t *testing.T) {
	w := buildOne(t, `{
      "apiVersion": "batch/v1", "kind": "CronJob",
      "metadata": {"name": "cron", "namespace": "x"},
      "spec": {"jobTemplate": {"spec": {"template": {"spec": {
        "nodeSelector": {"pool": "batch"},
        "containers": [{"name": "task"}]
      }}}}}
    }`)
	if w.NodeSelector["pool"] != "batch" {
		t.Errorf("CronJob nodeSelector not parsed from jobTemplate path: %v", w.NodeSelector)
	}
}

func TestConstrained_TolerationAloneIsNotConstrained(t *testing.T) {
	w := buildOne(t, `{
      "apiVersion": "apps/v1", "kind": "Deployment",
      "metadata": {"name": "d", "namespace": "x"},
      "spec": {"template": {"spec": {
        "tolerations": [{"key": "spot", "operator": "Exists"}],
        "containers": [{"name": "app"}]
      }}}
    }`)
	if w.Constrained() {
		t.Errorf("tolerations alone must not count as constrained")
	}
	if !w.HasTolerations() {
		t.Errorf("tolerations should be detected")
	}
}

func TestFindings_TolerationWithoutPlacement(t *testing.T) {
	// Tolerates a taint but pins nothing -> flagged.
	unconstrained := buildFleet(t, `{
      "apiVersion": "apps/v1", "kind": "Deployment",
      "metadata": {"name": "loose", "namespace": "x"},
      "spec": {"template": {"spec": {
        "tolerations": [{"key": "gpu", "operator": "Exists"}],
        "containers": [{"name": "app"}]
      }}}
    }`)
	fs := Findings(unconstrained)
	if len(fs) != 1 || fs[0].Analyzer != analyzerPlacement || fs[0].Severity != api.ScoreMedium {
		t.Fatalf("expected one medium placement finding, got %+v", fs)
	}

	// Tolerates AND pins with a nodeSelector -> not flagged.
	constrained := buildFleet(t, `{
      "apiVersion": "apps/v1", "kind": "Deployment",
      "metadata": {"name": "tight", "namespace": "x"},
      "spec": {"template": {"spec": {
        "nodeSelector": {"pool": "gpu"},
        "tolerations": [{"key": "gpu", "operator": "Exists"}],
        "containers": [{"name": "app"}]
      }}}
    }`)
	if fs := Findings(constrained); len(fs) != 0 {
		t.Errorf("constrained workload should not be flagged, got %+v", fs)
	}
}

func buildFleet(t *testing.T, manifest string) map[string]*ClusterWorkloads {
	t.Helper()
	var doc any
	if err := json.Unmarshal([]byte(manifest), &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return BuildFleet([]FleetResource{{Origin: ResourceOrigin{Cluster: "c1"}, Doc: doc}})
}

const spreadDeployment = `{
  "apiVersion": "apps/v1", "kind": "Deployment",
  "metadata": {"name": "web", "namespace": "app"},
  "spec": {"replicas": 6, "template": {
    "metadata": {"labels": {"app": "web"}},
    "spec": {
      "topologySpreadConstraints": [
        {"maxSkew": 1, "topologyKey": "topology.kubernetes.io/zone", "whenUnsatisfiable": "DoNotSchedule", "minDomains": 3,
         "labelSelector": {"matchLabels": {"app": "web"}}, "matchLabelKeys": ["pod-template-hash"]},
        {"maxSkew": 2, "topologyKey": "kubernetes.io/hostname", "whenUnsatisfiable": "ScheduleAnyway",
         "labelSelector": {"matchExpressions": [{"key": "app", "operator": "In", "values": ["web"]}]}}
      ],
      "affinity": {"podAntiAffinity": {
        "requiredDuringSchedulingIgnoredDuringExecution": [{"topologyKey": "kubernetes.io/hostname", "labelSelector": {"matchLabels": {"app": "web"}}}],
        "preferredDuringSchedulingIgnoredDuringExecution": [{"weight": 50, "podAffinityTerm": {"topologyKey": "topology.kubernetes.io/zone", "labelSelector": {"matchLabels": {"app": "web"}}}}]
      }},
      "containers": [{"name": "app"}]
    }}}
}`

func TestParse_SpreadAndAntiAffinity(t *testing.T) {
	w := buildOne(t, spreadDeployment)
	if w.Replicas == nil || *w.Replicas != 6 {
		t.Errorf("replicas: got %v", w.Replicas)
	}
	if w.PodLabels["app"] != "web" {
		t.Errorf("pod labels: got %v", w.PodLabels)
	}
	if len(w.TopologySpread) != 2 {
		t.Fatalf("want 2 spread constraints, got %+v", w.TopologySpread)
	}
	zone := w.TopologySpread[0]
	if zone.TopologyKey != ZoneKey || zone.MaxSkew != 1 || !zone.Hard() || zone.MinDomains == nil || *zone.MinDomains != 3 ||
		len(zone.MatchLabelKeys) != 1 || !zone.LabelSelector.Matches(w.PodLabels) {
		t.Errorf("zone constraint parsed wrong: %+v", zone)
	}
	host := w.TopologySpread[1]
	if host.Hard() || !host.LabelSelector.Matches(w.PodLabels) {
		t.Errorf("hostname constraint parsed wrong (soft, matchExpressions selecting own Pods): %+v", host)
	}
	if len(w.PodAntiAffinity) != 2 || !w.PodAntiAffinity[0].Required || w.PodAntiAffinity[1].Required || w.PodAntiAffinity[1].Weight != 50 {
		t.Errorf("pod anti-affinity parsed wrong: %+v", w.PodAntiAffinity)
	}
}

func TestSpreadConstraint_DefaultWhenUnsatisfiableIsHard(t *testing.T) {
	if !(SpreadConstraint{}).Hard() {
		t.Errorf("an unset whenUnsatisfiable defaults to DoNotSchedule")
	}
}

func TestLabelSelector_AbsentMatchesNothing(t *testing.T) {
	if (LabelSelector{}).Matches(map[string]string{"app": "web"}) {
		t.Errorf("an absent selector selects no Pods")
	}
	if !(LabelSelector{Present: true}).Matches(map[string]string{"app": "web"}) {
		t.Errorf("an empty selector selects every Pod")
	}
}

func TestParseDefaultSpread(t *testing.T) {
	cs, err := ParseDefaultSpread("topology.kubernetes.io/zone=1:DoNotSchedule, kubernetes.io/hostname=2:ScheduleAnyway")
	if err != nil || len(cs) != 2 || cs[0].MaxSkew != 1 || !cs[0].Hard() || cs[1].TopologyKey != HostnameKey || cs[1].Hard() {
		t.Errorf("list: got %+v, %v", cs, err)
	}
	if cs, err := ParseDefaultSpread("none"); err != nil || len(cs) != 0 {
		t.Errorf("none: got %+v, %v", cs, err)
	}
	if cs, err := ParseDefaultSpread("Builtin"); err != nil || len(cs) != 2 {
		t.Errorf("builtin: got %+v, %v", cs, err)
	}
	for _, bad := range []string{"zone", "zone=x:ScheduleAnyway", "zone=0:ScheduleAnyway", "zone=1:Sometimes", "zone=1"} {
		if _, err := ParseDefaultSpread(bad); err == nil {
			t.Errorf("%q: want an error", bad)
		}
	}
}

func TestClusterFacts_DefaultSource(t *testing.T) {
	cases := []struct {
		facts      map[string]string
		source     string
		n          int
		wantsError bool
	}{
		{map[string]string{}, DefaultAssumed, 2, false},
		{map[string]string{FactDistribution: "eks"}, DefaultManaged, 2, false},
		{map[string]string{FactDistribution: "gke", FactDefaultTopologySpread: "none"}, DefaultFromFact, 0, false},
		{map[string]string{FactDefaultTopologySpread: "topology.kubernetes.io/zone=1:ScheduleAnyway"}, DefaultFromFact, 1, false},
		{map[string]string{FactDistribution: "aks", FactDefaultTopologySpread: "garbage"}, DefaultManaged, 2, true},
	}
	for _, c := range cases {
		f := NewClusterFacts("t", c.facts)
		if f.DefaultSpread.Source != c.source || len(f.DefaultSpread.Constraints) != c.n || (f.DefaultSpread.Error != "") != c.wantsError {
			t.Errorf("%v: got %+v", c.facts, f.DefaultSpread)
		}
	}
}

func TestEffectiveSpread(t *testing.T) {
	explicit := buildOne(t, spreadDeployment)
	if cs, src := EffectiveSpread(explicit, nil); src != SpreadExplicit || len(cs) != 2 {
		t.Errorf("explicit: got %s %+v", src, cs)
	}
	bare := buildOne(t, `{"apiVersion": "apps/v1", "kind": "StatefulSet", "metadata": {"name": "db"},
      "spec": {"template": {"spec": {"containers": [{"name": "db"}]}}}}`)
	if cs, src := EffectiveSpread(bare, nil); src != SpreadClusterDefault || len(cs) != 2 {
		t.Errorf("no facts: want the built-in default, got %s %+v", src, cs)
	}
	off := NewClusterFacts("t", map[string]string{FactDefaultTopologySpread: "none"})
	if _, src := EffectiveSpread(bare, off); src != SpreadNone {
		t.Errorf("defaulting off: want none, got %s", src)
	}
	job := buildOne(t, `{"apiVersion": "batch/v1", "kind": "Job", "metadata": {"name": "j"},
      "spec": {"template": {"spec": {"containers": [{"name": "j"}]}}}}`)
	if _, src := EffectiveSpread(job, nil); src != SpreadNone {
		t.Errorf("a Job's Pods are not defaulted, got %s", src)
	}
}

func analyzersOf(fs []Finding) map[string]api.Score {
	out := map[string]api.Score{}
	for _, f := range fs {
		if prev, ok := out[f.Analyzer]; !ok || api.ScoreToNumber[f.Severity] > api.ScoreToNumber[prev] {
			out[f.Analyzer] = f.Severity
		}
	}
	return out
}

func TestFindings_Spread(t *testing.T) {
	fleet := buildFleet(t, spreadDeployment)
	fleet["c1"].Facts = NewClusterFacts("c1", map[string]string{
		FactZones:         "us-east-1a,us-east-1b",
		FactNodeLabelKeys: "kubernetes.io/hostname,topology.kubernetes.io/zone",
	})
	got := analyzersOf(Findings(fleet))
	// minDomains 3 over 2 zones holds 2 of 6 replicas; required anti-affinity per node.
	want := map[string]api.Score{analyzerMinDomainsTooFew: api.ScoreMedium, analyzerHardHostAntiAffinity: api.ScoreLow}
	if len(got) != len(want) {
		t.Fatalf("want %v, got %v", want, got)
	}
	for a, sev := range want {
		if got[a] != sev {
			t.Errorf("%s: want %s, got %s", a, sev, got[a])
		}
	}
}

func TestFindings_SpreadMisconfigured(t *testing.T) {
	fleet := buildFleet(t, `{
      "apiVersion": "apps/v1", "kind": "Deployment",
      "metadata": {"name": "web", "namespace": "app"},
      "spec": {"template": {"metadata": {"labels": {"app": "web"}}, "spec": {
        "topologySpreadConstraints": [
          {"maxSkew": 1, "topologyKey": "rack", "labelSelector": {"matchLabels": {"app": "web"}}},
          {"maxSkew": 1, "topologyKey": "topology.kubernetes.io/zone", "whenUnsatisfiable": "ScheduleAnyway", "minDomains": 2,
           "labelSelector": {"matchLabels": {"app": "other"}}}
        ],
        "containers": [{"name": "app"}]
      }}}
    }`)
	fleet["c1"].Facts = NewClusterFacts("c1", map[string]string{FactNodeLabelKeys: "kubernetes.io/hostname,topology.kubernetes.io/zone"})
	got := analyzersOf(Findings(fleet))
	want := map[string]api.Score{
		analyzerUnknownTopologyKey:  api.ScoreHigh,   // hard constraint on "rack", which no node carries
		analyzerMinDomainsSoft:      api.ScoreHigh,   // minDomains with ScheduleAnyway
		analyzerSpreadMissesOwnPods: api.ScoreMedium, // selects app=other
		analyzerRolloutSkew:         api.ScoreLow,    // the hard "rack" constraint has no matchLabelKeys
	}
	if len(got) != len(want) {
		t.Fatalf("want %v, got %v", want, got)
	}
	for a, sev := range want {
		if got[a] != sev {
			t.Errorf("%s: want %s, got %s", a, sev, got[a])
		}
	}

	// Without collected node label keys, an unknown key is not reported.
	fleet["c1"].Facts = NewClusterFacts("c1", map[string]string{})
	if _, ok := analyzersOf(Findings(fleet))[analyzerUnknownTopologyKey]; ok {
		t.Errorf("unknown-topology-key needs collected node label keys")
	}
}
