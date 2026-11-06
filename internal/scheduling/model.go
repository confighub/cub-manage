// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

// Package scheduling is the scheduling-constraint analysis engine: it parses
// the fields of pod-bearing workloads that the scheduler reads (nodeSelector,
// tolerations, node affinity, pod affinity and anti-affinity, topology spread
// constraints) into a typed model, joins each cluster's Target facts (its zones,
// node labels and default spread constraints), and reports where each workload
// may land, how its replicas spread, and anti-patterns across the fleet.
//
// Parsing is lenient: malformed documents are skipped, never errored on.
package scheduling

// ResourceOrigin records where a resource came from in ConfigHub.
type ResourceOrigin struct {
	Cluster      string            `json:"cluster"`
	Target       string            `json:"target,omitempty"`
	Space        string            `json:"space"`
	SpaceID      string            `json:"spaceId"`
	SpaceLabels  map[string]string `json:"spaceLabels,omitempty"`
	UnitID       string            `json:"unitId"`
	UnitSlug     string            `json:"unitSlug"`
	ResourceName string            `json:"resourceName"`
	Canonical    bool              `json:"canonical,omitempty"`
}

// FleetResource is a parsed resource document plus its ConfigHub origin.
type FleetResource struct {
	Origin ResourceOrigin
	Doc    any
}

// Toleration is a parsed pod toleration.
type Toleration struct {
	Key      string `json:"key,omitempty"`
	Operator string `json:"operator,omitempty"`
	Value    string `json:"value,omitempty"`
	Effect   string `json:"effect,omitempty"`
}

// WorkloadPlacement is a pod-bearing workload reduced to its placement fields.
type WorkloadPlacement struct {
	Kind                    string            `json:"kind"`
	Name                    string            `json:"name"`
	Namespace               string            `json:"namespace"`
	NodeSelector            map[string]string `json:"nodeSelector,omitempty"`
	Tolerations             []Toleration      `json:"tolerations,omitempty"`
	HasNodeAffinity         bool              `json:"hasNodeAffinity"`
	HasRequiredNodeAffinity bool              `json:"hasRequiredNodeAffinity"`
	// PodLabels are the pod template's labels, which a spread constraint or
	// anti-affinity term has to select for the Pods to count themselves.
	PodLabels map[string]string `json:"podLabels,omitempty"`
	// Replicas is spec.replicas as written; nil when unset or not a field of
	// the kind.
	Replicas        *int               `json:"replicas,omitempty"`
	TopologySpread  []SpreadConstraint `json:"topologySpreadConstraints,omitempty"`
	PodAffinity     []AffinityTerm     `json:"podAffinity,omitempty"`
	PodAntiAffinity []AffinityTerm     `json:"podAntiAffinity,omitempty"`
	Origin          ResourceOrigin     `json:"origin"`
}

// HasNodeSelector reports whether the workload pins to nodes by label.
func (w *WorkloadPlacement) HasNodeSelector() bool { return len(w.NodeSelector) > 0 }

// HasTolerations reports whether the workload tolerates any taint.
func (w *WorkloadPlacement) HasTolerations() bool { return len(w.Tolerations) > 0 }

// Constrained reports whether the workload actually restricts which nodes it lands
// on: a nodeSelector or a required node affinity. Tolerations alone do NOT
// constrain placement — they only permit scheduling onto tainted nodes.
func (w *WorkloadPlacement) Constrained() bool {
	return w.HasNodeSelector() || w.HasRequiredNodeAffinity
}

// TolerationKeys returns the distinct toleration keys (for reporting).
func (w *WorkloadPlacement) TolerationKeys() []string {
	var keys []string
	seen := map[string]bool{}
	for _, t := range w.Tolerations {
		k := t.Key
		if k == "" {
			k = "*" // an empty key with Exists tolerates everything
		}
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	return keys
}

// ClusterWorkloads holds the placement entities of one cluster.
type ClusterWorkloads struct {
	Cluster   string               `json:"cluster"`
	Workloads []*WorkloadPlacement `json:"workloads"`
	// Facts is what the cluster's Target says about it; nil until the snapshot
	// joins it, and then [UnknownClusterFacts] for a cluster with no Target.
	Facts *ClusterFacts `json:"facts,omitempty"`
}

// FactsOrUnknown returns the cluster's facts, or the unknown facts when none
// were joined.
func (c *ClusterWorkloads) FactsOrUnknown() *ClusterFacts {
	if c == nil || c.Facts == nil {
		return UnknownClusterFacts()
	}
	return c.Facts
}

var workloadKinds = map[string]bool{
	"Deployment":  true,
	"StatefulSet": true,
	"DaemonSet":   true,
	"ReplicaSet":  true,
	"Job":         true,
	"CronJob":     true,
	"Pod":         true,
}

// IsWorkloadKind reports whether kind is a pod-bearing workload this tool scores.
func IsWorkloadKind(kind string) bool { return workloadKinds[kind] }

// BuildFleet indexes parsed fleet resources into per-cluster placement sets.
func BuildFleet(resources []FleetResource) map[string]*ClusterWorkloads {
	clusters := make(map[string]*ClusterWorkloads)
	forCluster := func(name string) *ClusterWorkloads {
		c, ok := clusters[name]
		if !ok {
			c = &ClusterWorkloads{Cluster: name}
			clusters[name] = c
		}
		return c
	}
	for _, fr := range resources {
		rec, ok := asRecord(fr.Doc)
		if !ok {
			continue
		}
		kind, hasKind := asString(rec["kind"])
		metadata, _ := asRecord(rec["metadata"])
		name, hasName := asString(metadata["name"])
		if !hasKind || !hasName || !workloadKinds[kind] {
			continue
		}
		namespace, _ := asString(metadata["namespace"])
		spec, _ := asRecord(rec["spec"])
		template := podTemplateOf(kind, rec)
		podSpec, _ := asRecord(template["spec"])
		w := &WorkloadPlacement{Kind: kind, Name: name, Namespace: namespace, Origin: fr.Origin}
		if _, ok := spec["replicas"]; ok {
			n := asInt(spec["replicas"])
			w.Replicas = &n
		}
		if templateMeta, ok := asRecord(template["metadata"]); ok {
			w.PodLabels = asStringMap(templateMeta["labels"])
		}
		if podSpec != nil {
			w.NodeSelector = asStringMap(podSpec["nodeSelector"])
			w.Tolerations = parseTolerations(podSpec["tolerations"])
			w.HasNodeAffinity, w.HasRequiredNodeAffinity = parseNodeAffinity(podSpec["affinity"])
			w.TopologySpread = parseSpreadConstraints(podSpec["topologySpreadConstraints"])
			if affinity, ok := asRecord(podSpec["affinity"]); ok {
				w.PodAffinity = parsePodAffinity(affinity, "podAffinity")
				w.PodAntiAffinity = parsePodAffinity(affinity, "podAntiAffinity")
			}
		}
		forCluster(fr.Origin.Cluster).Workloads = append(forCluster(fr.Origin.Cluster).Workloads, w)
	}
	return clusters
}

func parseTolerations(v any) []Toleration {
	arr := asArray(v)
	if len(arr) == 0 {
		return nil
	}
	out := make([]Toleration, 0, len(arr))
	for _, e := range arr {
		er, ok := asRecord(e)
		if !ok {
			continue
		}
		key, _ := asString(er["key"])
		op, _ := asString(er["operator"])
		val, _ := asString(er["value"])
		effect, _ := asString(er["effect"])
		out = append(out, Toleration{Key: key, Operator: op, Value: val, Effect: effect})
	}
	return out
}

func parseNodeAffinity(v any) (has, hasRequired bool) {
	affinity, ok := asRecord(v)
	if !ok {
		return false, false
	}
	na, ok := asRecord(affinity["nodeAffinity"])
	if !ok {
		return false, false
	}
	_, hasRequired = asRecord(na["requiredDuringSchedulingIgnoredDuringExecution"])
	preferred := asArray(na["preferredDuringSchedulingIgnoredDuringExecution"])
	return hasRequired || len(preferred) > 0, hasRequired
}

// podTemplateOf returns the pod template (metadata and spec) for a workload
// kind, descending through the kind-specific template path. A Pod is its own
// template.
func podTemplateOf(kind string, rec map[string]any) map[string]any {
	spec, _ := asRecord(rec["spec"])
	switch kind {
	case "Pod":
		return rec
	case "CronJob":
		jobTemplate, _ := asRecord(spec["jobTemplate"])
		jobSpec, _ := asRecord(jobTemplate["spec"])
		template, _ := asRecord(jobSpec["template"])
		return template
	default:
		template, _ := asRecord(spec["template"])
		return template
	}
}

// ResourceMeta extracts kind, name, namespace from a decoded resource document.
func ResourceMeta(doc any) (kind, name, namespace string, ok bool) {
	rec, isRec := asRecord(doc)
	if !isRec {
		return "", "", "", false
	}
	kind, _ = asString(rec["kind"])
	md, _ := asRecord(rec["metadata"])
	name, _ = asString(md["name"])
	namespace, _ = asString(md["namespace"])
	return kind, name, namespace, kind != "" && name != ""
}

// --- lenient decoding helpers ---

func asRecord(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

func asString(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

func asArray(v any) []any {
	a, ok := v.([]any)
	if !ok {
		return nil
	}
	return a
}

// asInt reads an integer, however the document decoder represented it.
func asInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case uint64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}

func asStrings(v any) []string {
	var out []string
	for _, e := range asArray(v) {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func asStringMap(v any) map[string]string {
	rec, ok := asRecord(v)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(rec))
	for k, val := range rec {
		if s, ok := val.(string); ok {
			out[k] = s
		}
	}
	return out
}
