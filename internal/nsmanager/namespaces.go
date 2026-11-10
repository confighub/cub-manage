// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package nsmanager

import (
	"sort"

	"github.com/confighub/sdk/configkit/k8skit"
)

// PlaceholderNamespace is the base-template placeholder value (set-namespace
// replaces it at render). Resources still carrying it are unrendered bases that
// vet-placeholders gates from deploying, so the namespace and duplicate analyses
// skip them.
const PlaceholderNamespace = "confighubplaceholder"

// NamespaceSummary is what one cluster's config says about one namespace: whether
// a Namespace object declares it, the Pod Security Standard level that object
// enforces, and how many workloads run in it.
type NamespaceSummary struct {
	Cluster            string `json:"cluster"`
	Namespace          string `json:"namespace"`
	HasNamespaceObject bool   `json:"hasNamespaceObject"`
	PodSecurityEnforce string `json:"podSecurityEnforce,omitempty"`
	WorkloadCount      int    `json:"workloadCount"`
}

// SummarizeNamespaces summarizes every namespace in one cluster that has a
// Namespace object or a workload. The placeholder namespace is skipped, and so
// are the built-in namespaces (kube-system, default, ...): the cluster creates
// and owns them, so they need no Namespace Unit and no tenant's pod-security
// level.
func SummarizeNamespaces(c *ClusterNamespaces) []NamespaceSummary {
	nsObjects := map[string]*NamespaceEntity{}
	for _, n := range c.Namespaces {
		nsObjects[n.Name] = n
	}
	workloads := map[string]int{}
	for _, w := range c.Workloads {
		workloads[w.Namespace]++
	}

	names := map[string]bool{}
	add := func(ns string) {
		if ns != "" && ns != PlaceholderNamespace && !k8skit.IsBuiltInNamespace(ns) {
			names[ns] = true
		}
	}
	for name := range nsObjects {
		add(name)
	}
	for _, w := range c.Workloads {
		add(w.Namespace)
	}

	out := make([]NamespaceSummary, 0, len(names))
	for ns := range names {
		obj, hasObj := nsObjects[ns]
		e := NamespaceSummary{
			Cluster:            c.Cluster,
			Namespace:          ns,
			HasNamespaceObject: hasObj,
			WorkloadCount:      workloads[ns],
		}
		if hasObj {
			e.PodSecurityEnforce = obj.PodSecurityEnforce()
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Namespace < out[j].Namespace })
	return out
}

// DuplicateNamespace is a finding: two or more Namespace objects that resolve to
// the same name and are bound to the same Target (would deploy to the same
// cluster) — a collision.
type DuplicateNamespace struct {
	Target    string   `json:"target"`
	Namespace string   `json:"namespace"`
	Count     int      `json:"count"`
	UnitSlugs []string `json:"unitSlugs"`
}

// DuplicateNamespaces flags Namespace objects that collide on (Target, name).
// Only Target-bound Namespaces are considered (an unbound base doesn't deploy);
// the placeholder name is exempt (an unrendered base, gated by vet-placeholders).
func DuplicateNamespaces(clusters map[string]*ClusterNamespaces) []DuplicateNamespace {
	type key struct{ target, name string }
	groups := map[key][]string{}
	for _, c := range clusters {
		for _, n := range c.Namespaces {
			if n.Origin.Target == "" || n.Name == PlaceholderNamespace {
				continue
			}
			k := key{n.Origin.Target, n.Name}
			groups[k] = append(groups[k], n.Origin.UnitSlug)
		}
	}
	var out []DuplicateNamespace
	for k, slugs := range groups {
		if len(slugs) < 2 {
			continue
		}
		sort.Strings(slugs)
		out = append(out, DuplicateNamespace{
			Target: k.target, Namespace: k.name, Count: len(slugs), UnitSlugs: slugs,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Target != out[j].Target {
			return out[i].Target < out[j].Target
		}
		return out[i].Namespace < out[j].Namespace
	})
	return out
}
