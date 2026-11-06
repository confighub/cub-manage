// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package scheduling

import (
	"fmt"
	"sort"
	"strings"

	api "github.com/confighub/sdk/core/function/api"
)

// Finding is one placement issue on one workload.
type Finding struct {
	Severity  api.Score `json:"severity"`
	Analyzer  string    `json:"analyzer"`
	Cluster   string    `json:"cluster"`
	Namespace string    `json:"namespace,omitempty"`
	Kind      string    `json:"kind"`
	Name      string    `json:"name"`
	Space     string    `json:"space"`
	UnitSlug  string    `json:"unitSlug"`
	Message   string    `json:"message"`
}

// Analyzers.
const (
	analyzerPlacement            = "toleration-without-placement"
	analyzerSpreadMissesOwnPods  = "spread-selector-misses-own-pods"
	analyzerMinDomainsSoft       = "spread-min-domains-not-hard"
	analyzerMinDomainsTooFew     = "spread-min-domains-exceeds-zones"
	analyzerUnknownTopologyKey   = "unknown-topology-key"
	analyzerRolloutSkew          = "spread-rollout-without-match-label-keys"
	analyzerHardHostAntiAffinity = "required-anti-affinity-per-node"
)

// Analyzers lists every analyzer [Findings] runs, in the order help text names them.
var Analyzers = []string{
	analyzerPlacement,
	analyzerSpreadMissesOwnPods,
	analyzerMinDomainsSoft,
	analyzerMinDomainsTooFew,
	analyzerUnknownTopologyKey,
	analyzerRolloutSkew,
	analyzerHardHostAntiAffinity,
}

// podTemplateHash is the label a Deployment puts on each ReplicaSet's Pods,
// which matchLabelKeys uses to spread each revision on its own.
const podTemplateHash = "pod-template-hash"

// Findings computes scheduling anti-patterns across the fleet, most-severe first.
//
// Placement: a workload that tolerates a taint but does not constrain where it
// lands (no nodeSelector and no required node affinity), so it may schedule onto
// general nodes -- usually not the intent of adding a toleration. Checks that
// need a cluster's node pools and taints wait for those Target facts.
//
// Spread: constraints that cannot work as written (minDomains without
// DoNotSchedule, which the API server rejects; a selector that does not select
// the workload's own Pods, so they never count themselves), constraints the
// cluster cannot satisfy (a topologyKey no node carries; minDomains above the
// cluster's zone count with more replicas than the zones can hold), and hard
// constraints that misbehave over a rollout or a node shortage.
func Findings(clusters map[string]*ClusterWorkloads) []Finding {
	var out []Finding
	for _, c := range clusters {
		facts := c.FactsOrUnknown()
		for _, w := range c.Workloads {
			add := func(sev api.Score, analyzer, msg string) {
				out = append(out, Finding{
					Severity: sev, Analyzer: analyzer, Cluster: w.Origin.Cluster, Namespace: w.Namespace,
					Kind: w.Kind, Name: w.Name, Space: w.Origin.Space, UnitSlug: w.Origin.UnitSlug, Message: msg,
				})
			}
			if w.HasTolerations() && !w.Constrained() {
				add(api.ScoreMedium, analyzerPlacement, fmt.Sprintf("tolerates taint(s) [%s] but has no nodeSelector or required node affinity — may schedule onto general nodes",
					strings.Join(w.TolerationKeys(), ", ")))
			}
			spreadFindings(w, facts, add)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Severity != out[j].Severity {
			return api.ScoreToNumber[out[i].Severity] > api.ScoreToNumber[out[j].Severity]
		}
		if out[i].Cluster != out[j].Cluster {
			return out[i].Cluster < out[j].Cluster
		}
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func spreadFindings(w *WorkloadPlacement, facts *ClusterFacts, add func(api.Score, string, string)) {
	for _, c := range w.TopologySpread {
		key := c.TopologyKey
		if c.MinDomains != nil && !c.Hard() {
			add(api.ScoreHigh, analyzerMinDomainsSoft, fmt.Sprintf("spread constraint on %s sets minDomains with whenUnsatisfiable %s; minDomains is valid only with %s, so the API server rejects the workload",
				key, c.WhenUnsatisfiable, DoNotSchedule))
		}
		if !c.LabelSelector.Matches(w.PodLabels) {
			add(api.ScoreMedium, analyzerSpreadMissesOwnPods, fmt.Sprintf("spread constraint on %s has a labelSelector that does not select the workload's own Pods, so they do not count toward the skew and can pile into one domain",
				key))
		}
		if !facts.HasNodeLabelKey(key) {
			if c.Hard() {
				add(api.ScoreHigh, analyzerUnknownTopologyKey, fmt.Sprintf("hard spread constraint on %s, which no node in the cluster carries — the Pods cannot schedule", key))
			} else {
				add(api.ScoreLow, analyzerUnknownTopologyKey, fmt.Sprintf("spread constraint on %s, which no node in the cluster carries — it has no effect", key))
			}
		}
		if key == ZoneKey && c.Hard() && c.MinDomains != nil && len(facts.Zones) > 0 && *c.MinDomains > len(facts.Zones) &&
			w.Replicas != nil && *w.Replicas > len(facts.Zones)*c.MaxSkew {
			add(api.ScoreMedium, analyzerMinDomainsTooFew, fmt.Sprintf("minDomains %d exceeds the cluster's %d zone(s), which then hold at most %d of %d replicas (maxSkew %d per zone); the rest stay Pending until nodes in more zones exist",
				*c.MinDomains, len(facts.Zones), len(facts.Zones)*c.MaxSkew, *w.Replicas, c.MaxSkew))
		}
		if w.Kind == "Deployment" && c.Hard() && !contains(c.MatchLabelKeys, podTemplateHash) {
			add(api.ScoreLow, analyzerRolloutSkew, fmt.Sprintf("hard spread constraint on %s counts old and new ReplicaSets' Pods together, so a rollout can leave the new revision unevenly spread; add matchLabelKeys [%s]",
				key, podTemplateHash))
		}
	}
	for _, t := range append(append([]AffinityTerm{}, w.PodAffinity...), w.PodAntiAffinity...) {
		if !facts.HasNodeLabelKey(t.TopologyKey) {
			add(api.ScoreLow, analyzerUnknownTopologyKey, fmt.Sprintf("pod affinity or anti-affinity term on %s, which no node in the cluster carries — it has no effect", t.TopologyKey))
		}
	}
	for _, t := range w.PodAntiAffinity {
		if t.Required && t.TopologyKey == HostnameKey && t.LabelSelector.Matches(w.PodLabels) {
			add(api.ScoreLow, analyzerHardHostAntiAffinity, "required pod anti-affinity per node: replicas beyond the number of eligible nodes, and a rollout's surge Pods, stay Pending; a topology spread constraint on "+HostnameKey+" with maxSkew 1 spreads as evenly without blocking")
		}
	}
}
