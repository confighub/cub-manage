// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package scheduling

import "strings"

// Well-known node labels a spread constraint or pod affinity term names as its
// topology domain.
const (
	HostnameKey = "kubernetes.io/hostname"
	ZoneKey     = "topology.kubernetes.io/zone"
)

// whenUnsatisfiable values.
const (
	DoNotSchedule  = "DoNotSchedule"
	ScheduleAnyway = "ScheduleAnyway"
)

// LabelSelector is a parsed Kubernetes label selector. Present distinguishes an
// absent selector, which selects no Pods in a spread constraint or affinity
// term, from an empty one, which selects every Pod in the namespace.
type LabelSelector struct {
	Present          bool                  `json:"-"`
	MatchLabels      map[string]string     `json:"matchLabels,omitempty"`
	MatchExpressions []SelectorRequirement `json:"matchExpressions,omitempty"`
}

// SelectorRequirement is one matchExpressions clause.
type SelectorRequirement struct {
	Key      string   `json:"key"`
	Operator string   `json:"operator"`
	Values   []string `json:"values,omitempty"`
}

// Matches reports whether labels satisfy the selector. An absent selector
// matches nothing.
func (s LabelSelector) Matches(labels map[string]string) bool {
	if !s.Present {
		return false
	}
	for k, v := range s.MatchLabels {
		if got, ok := labels[k]; !ok || got != v {
			return false
		}
	}
	for _, req := range s.MatchExpressions {
		if !req.matches(labels) {
			return false
		}
	}
	return true
}

func (r SelectorRequirement) matches(labels map[string]string) bool {
	val, has := labels[r.Key]
	switch strings.ToLower(r.Operator) {
	case "in":
		return has && contains(r.Values, val)
	case "notin":
		return !has || !contains(r.Values, val)
	case "exists":
		return has
	case "doesnotexist":
		return !has
	}
	return false
}

func contains(vals []string, v string) bool {
	for _, x := range vals {
		if x == v {
			return true
		}
	}
	return false
}

// SpreadConstraint is one parsed entry of a pod's topologySpreadConstraints.
type SpreadConstraint struct {
	MaxSkew     int    `json:"maxSkew"`
	TopologyKey string `json:"topologyKey"`
	// WhenUnsatisfiable is as written; [SpreadConstraint.Hard] applies the API
	// default (DoNotSchedule) when it is empty.
	WhenUnsatisfiable  string        `json:"whenUnsatisfiable,omitempty"`
	MinDomains         *int          `json:"minDomains,omitempty"`
	LabelSelector      LabelSelector `json:"labelSelector"`
	MatchLabelKeys     []string      `json:"matchLabelKeys,omitempty"`
	NodeAffinityPolicy string        `json:"nodeAffinityPolicy,omitempty"`
	NodeTaintsPolicy   string        `json:"nodeTaintsPolicy,omitempty"`
}

// Hard reports whether the scheduler refuses to place a Pod that would break
// the constraint, rather than only preferring nodes that keep it.
func (c SpreadConstraint) Hard() bool {
	return c.WhenUnsatisfiable == "" || c.WhenUnsatisfiable == DoNotSchedule
}

// AffinityTerm is one parsed pod affinity or anti-affinity term, required or
// preferred.
type AffinityTerm struct {
	TopologyKey   string        `json:"topologyKey"`
	Required      bool          `json:"required"`
	Weight        int           `json:"weight,omitempty"`
	LabelSelector LabelSelector `json:"labelSelector"`
}

func parseSpreadConstraints(v any) []SpreadConstraint {
	var out []SpreadConstraint
	for _, e := range asArray(v) {
		er, ok := asRecord(e)
		if !ok {
			continue
		}
		key, _ := asString(er["topologyKey"])
		when, _ := asString(er["whenUnsatisfiable"])
		nap, _ := asString(er["nodeAffinityPolicy"])
		ntp, _ := asString(er["nodeTaintsPolicy"])
		c := SpreadConstraint{
			MaxSkew:            asInt(er["maxSkew"]),
			TopologyKey:        key,
			WhenUnsatisfiable:  when,
			LabelSelector:      parseSelector(er["labelSelector"]),
			MatchLabelKeys:     asStrings(er["matchLabelKeys"]),
			NodeAffinityPolicy: nap,
			NodeTaintsPolicy:   ntp,
		}
		if _, ok := er["minDomains"]; ok {
			n := asInt(er["minDomains"])
			c.MinDomains = &n
		}
		out = append(out, c)
	}
	return out
}

// parsePodAffinity returns the terms of affinity.<field> (podAffinity or
// podAntiAffinity), required terms first.
func parsePodAffinity(affinity map[string]any, field string) []AffinityTerm {
	pa, ok := asRecord(affinity[field])
	if !ok {
		return nil
	}
	var out []AffinityTerm
	for _, e := range asArray(pa["requiredDuringSchedulingIgnoredDuringExecution"]) {
		if er, ok := asRecord(e); ok {
			out = append(out, affinityTerm(er, true, 0))
		}
	}
	for _, e := range asArray(pa["preferredDuringSchedulingIgnoredDuringExecution"]) {
		er, ok := asRecord(e)
		if !ok {
			continue
		}
		term, _ := asRecord(er["podAffinityTerm"])
		out = append(out, affinityTerm(term, false, asInt(er["weight"])))
	}
	return out
}

func affinityTerm(rec map[string]any, required bool, weight int) AffinityTerm {
	key, _ := asString(rec["topologyKey"])
	return AffinityTerm{TopologyKey: key, Required: required, Weight: weight, LabelSelector: parseSelector(rec["labelSelector"])}
}

func parseSelector(v any) LabelSelector {
	rec, ok := asRecord(v)
	if !ok {
		return LabelSelector{}
	}
	out := LabelSelector{Present: true, MatchLabels: asStringMap(rec["matchLabels"])}
	for _, e := range asArray(rec["matchExpressions"]) {
		er, ok := asRecord(e)
		if !ok {
			continue
		}
		key, _ := asString(er["key"])
		op, _ := asString(er["operator"])
		out.MatchExpressions = append(out.MatchExpressions, SelectorRequirement{Key: key, Operator: op, Values: asStrings(er["values"])})
	}
	return out
}
