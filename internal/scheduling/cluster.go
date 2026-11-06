// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package scheduling

import (
	"fmt"
	"strconv"
	"strings"
)

// Target facts the scheduling analysis reads. The Cluster.* facts are collected
// from the cluster (by `cub k8s collect` and the bots that run in it); the
// Scheduling.* fact is set by an operator.
const (
	FactDistribution  = "Cluster.Distribution"
	FactZones         = "Cluster.Zones"
	FactNodeLabelKeys = "Cluster.NodeLabelKeys"
	// FactDefaultTopologySpread records the cluster-level default spread
	// constraints its kube-scheduler applies, which cannot be read from the
	// cluster: "builtin" (Kubernetes' own defaults), "none" (defaulting
	// disabled), or a comma-separated list of topologyKey=maxSkew:whenUnsatisfiable.
	FactDefaultTopologySpread = "Scheduling.DefaultTopologySpread"
)

// Where a cluster's default spread constraints came from.
const (
	// DefaultFromFact: the Target's Scheduling.DefaultTopologySpread fact.
	DefaultFromFact = "target-fact"
	// DefaultManaged: a managed distribution, whose kube-scheduler configuration
	// cannot be changed and is taken to be Kubernetes' built-in default.
	DefaultManaged = "managed-distribution"
	// DefaultAssumed: nothing recorded; Kubernetes' built-in default is assumed.
	DefaultAssumed = "assumed"
)

// managedDistributions are the Cluster.Distribution values of managed
// Kubernetes services, whose kube-scheduler configuration users cannot change.
var managedDistributions = map[string]bool{"eks": true, "gke": true, "aks": true}

// BuiltinDefaultSpread is what kube-scheduler applies to a Pod with no spread
// constraints of its own when no cluster-level defaults are configured.
func BuiltinDefaultSpread() []SpreadConstraint {
	return []SpreadConstraint{
		{MaxSkew: 3, TopologyKey: HostnameKey, WhenUnsatisfiable: ScheduleAnyway},
		{MaxSkew: 5, TopologyKey: ZoneKey, WhenUnsatisfiable: ScheduleAnyway},
	}
}

// ClusterFacts is what the analysis knows about one cluster from its Target.
type ClusterFacts struct {
	Target       string `json:"target,omitempty"`
	Distribution string `json:"distribution,omitempty"`
	// Zones and NodeLabelKeys are empty when they were not collected.
	Zones         []string      `json:"zones,omitempty"`
	NodeLabelKeys []string      `json:"nodeLabelKeys,omitempty"`
	DefaultSpread DefaultSpread `json:"defaultSpread"`
}

// DefaultSpread is a cluster's default spread constraints and how they are known.
type DefaultSpread struct {
	// Constraints is empty when defaulting is disabled.
	Constraints []SpreadConstraint `json:"constraints"`
	Source      string             `json:"source"`
	// Error explains a Scheduling.DefaultTopologySpread fact that could not be
	// parsed; the built-in default is assumed instead.
	Error string `json:"error,omitempty"`
}

// Managed reports whether the cluster is a managed Kubernetes service.
func (f *ClusterFacts) Managed() bool { return f != nil && managedDistributions[f.Distribution] }

// HasNodeLabelKey reports whether some node carries the label key. It reports
// true when node label keys were not collected, so unknown never reads as absent.
func (f *ClusterFacts) HasNodeLabelKey(key string) bool {
	if f == nil || len(f.NodeLabelKeys) == 0 {
		return true
	}
	return contains(f.NodeLabelKeys, key)
}

// NewClusterFacts reads a Target's facts.
func NewClusterFacts(target string, facts map[string]string) *ClusterFacts {
	f := &ClusterFacts{
		Target:        target,
		Distribution:  facts[FactDistribution],
		Zones:         splitCSV(facts[FactZones]),
		NodeLabelKeys: splitCSV(facts[FactNodeLabelKeys]),
	}
	f.DefaultSpread = defaultSpread(f, facts[FactDefaultTopologySpread])
	return f
}

// UnknownClusterFacts is used for a cluster whose Target could not be read, or
// for Units with no Target.
func UnknownClusterFacts() *ClusterFacts {
	return &ClusterFacts{DefaultSpread: DefaultSpread{Constraints: BuiltinDefaultSpread(), Source: DefaultAssumed}}
}

func defaultSpread(f *ClusterFacts, fact string) DefaultSpread {
	fallback := DefaultSpread{Constraints: BuiltinDefaultSpread(), Source: DefaultAssumed}
	if f.Managed() {
		fallback.Source = DefaultManaged
	}
	fact = strings.TrimSpace(fact)
	if fact == "" {
		return fallback
	}
	constraints, err := ParseDefaultSpread(fact)
	if err != nil {
		fallback.Error = fmt.Sprintf("%s: %v", FactDefaultTopologySpread, err)
		return fallback
	}
	return DefaultSpread{Constraints: constraints, Source: DefaultFromFact}
}

// ParseDefaultSpread parses a Scheduling.DefaultTopologySpread value.
func ParseDefaultSpread(s string) ([]SpreadConstraint, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "builtin":
		return BuiltinDefaultSpread(), nil
	case "none":
		return []SpreadConstraint{}, nil
	}
	var out []SpreadConstraint
	for _, part := range splitCSV(s) {
		key, rest, ok := strings.Cut(part, "=")
		skewStr, when, ok2 := strings.Cut(rest, ":")
		if !ok || !ok2 || key == "" {
			return nil, fmt.Errorf("bad constraint %q (want topologyKey=maxSkew:whenUnsatisfiable, or builtin, or none)", part)
		}
		skew, err := strconv.Atoi(skewStr)
		if err != nil || skew < 1 {
			return nil, fmt.Errorf("bad maxSkew in %q (want an integer of at least 1)", part)
		}
		if when != DoNotSchedule && when != ScheduleAnyway {
			return nil, fmt.Errorf("bad whenUnsatisfiable in %q (want %s or %s)", part, DoNotSchedule, ScheduleAnyway)
		}
		out = append(out, SpreadConstraint{MaxSkew: skew, TopologyKey: key, WhenUnsatisfiable: when})
	}
	return out, nil
}

// defaultedKinds are the controllers whose Pods kube-scheduler gives the cluster
// default spread constraints: it derives their selector from the owning
// ReplicaSet or StatefulSet. (It also defaults Pods a Service selects; that is
// not modeled here, since the analysis does not read Services.)
var defaultedKinds = map[string]bool{"Deployment": true, "ReplicaSet": true, "StatefulSet": true}

// Spread names the source of a workload's effective spread constraints.
const (
	SpreadExplicit       = "explicit"
	SpreadClusterDefault = "cluster-default"
	SpreadNone           = "none"
)

// EffectiveSpread returns the spread constraints the scheduler applies to the
// workload's Pods, and where they come from: its own, the cluster default when
// it has none, or none.
func EffectiveSpread(w *WorkloadPlacement, facts *ClusterFacts) ([]SpreadConstraint, string) {
	if len(w.TopologySpread) > 0 {
		return w.TopologySpread, SpreadExplicit
	}
	if !defaultedKinds[w.Kind] {
		return nil, SpreadNone
	}
	if facts == nil {
		facts = UnknownClusterFacts()
	}
	if len(facts.DefaultSpread.Constraints) == 0 {
		return nil, SpreadNone
	}
	return facts.DefaultSpread.Constraints, SpreadClusterDefault
}

// FormatSpread renders constraints compactly, as
// topologyKey=maxSkew:whenUnsatisfiable (the Scheduling.DefaultTopologySpread
// form), with minDomains appended when set.
func FormatSpread(cs []SpreadConstraint) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		when := c.WhenUnsatisfiable
		if when == "" {
			when = DoNotSchedule
		}
		s := fmt.Sprintf("%s=%d:%s", c.TopologyKey, c.MaxSkew, when)
		if c.MinDomains != nil {
			s += fmt.Sprintf("(minDomains=%d)", *c.MinDomains)
		}
		out = append(out, s)
	}
	return out
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
