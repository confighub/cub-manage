// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package netpol

// ingressplan turns flows into the NetworkPolicy changes that admit them and
// nothing else. Policies add up: a pod admits whatever any policy selecting it
// admits, so a narrow allow beside a rule admitting every source changes
// nothing. The plan therefore takes the rules admitting every source out of the
// policies that select a destination, and adds the flows' rules in their place.

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// PolicyEdit is a change to an existing NetworkPolicy.
type PolicyEdit struct {
	Policy *NetworkPolicyEntity
	// Removed is how many of its ingress rules admitted every source, and are
	// taken out.
	Removed int
	// Added are the rules admitting the flows to the destinations it selects.
	Added []NetworkPolicyRule
	// Ingress is the policy's ingress rules after the edit.
	Ingress []NetworkPolicyRule
}

// PolicyCreate is a new NetworkPolicy admitting flows to a destination no
// policy can be edited for.
type PolicyCreate struct {
	Dest  *WorkloadEntity
	Name  string
	Rules []NetworkPolicyRule
}

// AccessChange is what the plan changes about who can reach a destination.
type AccessChange struct {
	Dest  *WorkloadEntity
	Gains []*WorkloadEntity
	Loses []*WorkloadEntity
	// WasOpen is whether every other workload in the cluster could reach it
	// before.
	WasOpen bool
}

// IngressPlan is what admitting a cluster's flows takes.
type IngressPlan struct {
	Cluster string
	Flows   []Flow
	Edits   []*PolicyEdit
	Creates []*PolicyCreate
	Access  []AccessChange
	// Blocked are flows the source's egress policies still block.
	Blocked []Flow
	Skipped []RefSkip
}

// PlanIngress plans the policy changes that make each flow's destination
// admit exactly the flows to it, plus whatever the rules naming sources already
// admit.
//
// For a destination selected by no policy that isolates ingress, a new policy
// is created. Otherwise the rules admitting every source are taken out of each
// policy selecting it, and the flows' rules added to the first of them. A
// policy that also selects a workload whose flows differ is not edited, since
// the edit would apply to that workload too; when such a policy admits every
// source, the destination stays open through it, and is skipped.
func PlanIngress(c *ClusterNetpol, flows []Flow) IngressPlan {
	plan := IngressPlan{Cluster: c.Cluster, Flows: flows}
	desired := map[*WorkloadEntity][]NetworkPolicyRule{}
	var dests []*WorkloadEntity
	portsBySource := map[*WorkloadEntity]map[*WorkloadEntity][]NetworkPolicyPort{}
	for _, f := range flows {
		if portsBySource[f.Dest] == nil {
			portsBySource[f.Dest] = map[*WorkloadEntity][]NetworkPolicyPort{}
			dests = append(dests, f.Dest)
		}
		portsBySource[f.Dest][f.Source] = unionPorts(portsBySource[f.Dest][f.Source], f.Ports)
	}
	sortWorkloadPtrs(dests)
	for _, dst := range dests {
		desired[dst] = flowRules(portsBySource[dst])
	}

	edits := map[*NetworkPolicyEntity]*PolicyEdit{}
	var planned []*WorkloadEntity
	for _, dst := range dests {
		rules := desired[dst]
		selecting := policiesSelecting(c, dst)
		if len(selecting) == 0 {
			plan.Creates = append(plan.Creates, &PolicyCreate{Dest: dst, Name: "allow-" + dst.Name + "-ingress", Rules: rules})
			planned = append(planned, dst)
			continue
		}
		var editable []*NetworkPolicyEntity
		var blocking string
		for _, p := range selecting {
			other := otherSelected(c, p, dst, rules, desired)
			if other == nil {
				editable = append(editable, p)
				continue
			}
			if blocking == "" && hasAllowAll(p.Ingress) {
				blocking = fmt.Sprintf("stays open to every source through %s, which also selects %s", qualified(NamespaceOf(p.Namespace), p.Name), WorkloadName(other))
			}
		}
		if blocking != "" {
			plan.Skipped = append(plan.Skipped, RefSkip{Cluster: c.Cluster, Namespace: NamespaceOf(dst.Namespace), Via: WorkloadName(dst), Reason: blocking})
			continue
		}
		planned = append(planned, dst)
		if len(editable) == 0 {
			plan.Creates = append(plan.Creates, &PolicyCreate{Dest: dst, Name: "allow-" + dst.Name + "-ingress", Rules: rules})
			continue
		}
		for i, p := range editable {
			e := edits[p]
			if e == nil {
				e = newPolicyEdit(p)
				edits[p] = e
			}
			if i == 0 {
				e.add(rules)
			}
		}
	}

	after := &ClusterNetpol{Cluster: c.Cluster, Workloads: c.Workloads, Namespaces: c.Namespaces, Services: c.Services}
	for _, p := range c.NetworkPolicies {
		e := edits[p]
		if e == nil || !e.changes() {
			after.NetworkPolicies = append(after.NetworkPolicies, p)
			continue
		}
		edited := *p
		edited.Ingress = e.Ingress
		after.NetworkPolicies = append(after.NetworkPolicies, &edited)
		plan.Edits = append(plan.Edits, e)
	}
	for _, cr := range plan.Creates {
		after.NetworkPolicies = append(after.NetworkPolicies, cr.entity())
	}
	sort.Slice(plan.Edits, func(i, j int) bool { return policyKey(plan.Edits[i].Policy) < policyKey(plan.Edits[j].Policy) })

	for _, dst := range planned {
		before := WhoCanReach(c, dst)
		now := WhoCanReach(after, dst)
		ch := AccessChange{Dest: dst, Gains: workloadsMinus(now, before), Loses: workloadsMinus(before, now), WasOpen: len(before) == len(c.Workloads)-1}
		if len(ch.Gains) > 0 || len(ch.Loses) > 0 {
			plan.Access = append(plan.Access, ch)
		}
	}
	afterIdx := newClusterIndex(after)
	for _, f := range flows {
		if !afterIdx.CanReach(f.Source, f.Dest) {
			plan.Blocked = append(plan.Blocked, f)
		}
	}
	return plan
}

// flowRules are the ingress rules admitting a destination's flows: one per set
// of ports, naming every source that connects on them.
func flowRules(portsBySource map[*WorkloadEntity][]NetworkPolicyPort) []NetworkPolicyRule {
	sources := map[string][]*WorkloadEntity{}
	portsOf := map[string][]NetworkPolicyPort{}
	for src, ports := range portsBySource {
		k := portsKey(ports)
		sources[k] = append(sources[k], src)
		portsOf[k] = ports
	}
	keys := make([]string, 0, len(sources))
	for k := range sources {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	rules := make([]NetworkPolicyRule, 0, len(keys))
	for _, k := range keys {
		var peers []NetworkPolicyPeer
		for _, src := range sortWorkloads(sources[k]) {
			peers = append(peers, sourcePeer(src))
		}
		rules = append(rules, NetworkPolicyRule{Peers: peers, Ports: portsOf[k]})
	}
	return rules
}

// unionPorts adds the ports b has that a lacks, in order.
func unionPorts(a, b []NetworkPolicyPort) []NetworkPolicyPort {
	out := slices.Clone(a)
	for _, p := range b {
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	sortPorts(out)
	return out
}

// sourcePeer selects a source's pods by its namespace's name and its own pod
// selector, which hold in every cluster the workload is deployed to alike.
func sourcePeer(src *WorkloadEntity) NetworkPolicyPeer {
	return NetworkPolicyPeer{
		NamespaceSelector: &LabelSelector{Present: true, MatchLabels: map[string]string{namespaceNameLabel: NamespaceOf(src.Namespace)}},
		PodSelector:       &LabelSelector{Present: true, MatchLabels: src.PolicySelector()},
	}
}

// policiesSelecting are the policies isolating a workload's ingress.
func policiesSelecting(c *ClusterNetpol, w *WorkloadEntity) []*NetworkPolicyEntity {
	var out []*NetworkPolicyEntity
	for _, p := range c.NetworkPolicies {
		if NamespaceOf(p.Namespace) == NamespaceOf(w.Namespace) && p.IsolatesIngress() && p.PodSelector.Matches(w.PodLabels) {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return policyKey(out[i]) < policyKey(out[j]) })
	return out
}

// otherSelected is a workload other than dst that p selects whose flows differ
// from dst's, nil when there is none.
func otherSelected(c *ClusterNetpol, p *NetworkPolicyEntity, dst *WorkloadEntity, rules []NetworkPolicyRule, desired map[*WorkloadEntity][]NetworkPolicyRule) *WorkloadEntity {
	want := rulesKey(rules)
	for _, w := range c.Workloads {
		if w == dst || NamespaceOf(w.Namespace) != NamespaceOf(p.Namespace) || !p.PodSelector.Matches(w.PodLabels) {
			continue
		}
		if rulesKey(desired[w]) != want {
			return w
		}
	}
	return nil
}

func newPolicyEdit(p *NetworkPolicyEntity) *PolicyEdit {
	e := &PolicyEdit{Policy: p}
	for _, r := range p.Ingress {
		if len(r.Peers) == 0 {
			e.Removed++
			continue
		}
		e.Ingress = append(e.Ingress, r)
	}
	return e
}

// add adds the rules the policy does not already have.
func (e *PolicyEdit) add(rules []NetworkPolicyRule) {
	have := map[string]bool{}
	for _, r := range e.Ingress {
		have[ruleKey(r)] = true
	}
	for _, r := range rules {
		if !have[ruleKey(r)] {
			have[ruleKey(r)] = true
			e.Added = append(e.Added, r)
			e.Ingress = append(e.Ingress, r)
		}
	}
}

func (e *PolicyEdit) changes() bool { return e.Removed > 0 || len(e.Added) > 0 }

// ResultKey identifies the policy's ingress rules after the edit, as IngressKey
// does before it.
func (e *PolicyEdit) ResultKey() string { return rulesKey(e.Ingress) }

// YQ is the set-yq expression making the edit to the Unit holding the policy:
// the same whichever Unit holds it, so a variant and its upstream take the same
// expression. A rule with no `from` peers admits every source.
func (e *PolicyEdit) YQ() string {
	added, err := json.Marshal(rulesDoc(e.Added))
	if err != nil {
		panic(err) // fixed-shape maps of strings and ints
	}
	return fmt.Sprintf(`(select(.kind == "NetworkPolicy" and .metadata.name == %s) | .spec.ingress) |= (((. // []) | map(select((.from // []) | map(select(. != null)) | length > 0))) + %s)`,
		strconv.Quote(e.Policy.Name), added)
}

func hasAllowAll(rules []NetworkPolicyRule) bool {
	for _, r := range rules {
		if len(r.Peers) == 0 {
			return true
		}
	}
	return false
}

// entity is the policy the create makes, for the after-the-plan model.
func (cr *PolicyCreate) entity() *NetworkPolicyEntity {
	return &NetworkPolicyEntity{
		Name: cr.Name, Namespace: NamespaceOf(cr.Dest.Namespace),
		PodSelector: LabelSelector{Present: true, MatchLabels: cr.Dest.PolicySelector()},
		PolicyTypes: []string{"Ingress"}, Ingress: cr.Rules,
	}
}

// SamePolicy reports whether p is the policy the create makes: the same pod
// selector, isolating ingress alone, with the same ingress rules.
func SamePolicy(p *NetworkPolicyEntity, cr *PolicyCreate) bool {
	if p == nil {
		return false
	}
	want := cr.entity()
	ingress, egress := p.Isolates()
	return ingress && !egress && CanonicalLabels(p.PodSelector.MatchLabels) == CanonicalLabels(want.PodSelector.MatchLabels) &&
		len(p.PodSelector.MatchExpressions) == 0 && IngressKey(p) == IngressKey(want)
}

// YAML is the NetworkPolicy the create makes.
func (cr *PolicyCreate) YAML() string {
	spec := map[string]any{
		"podSelector": matchLabels(cr.Dest.PolicySelector()),
		"policyTypes": []any{"Ingress"},
		"ingress":     rulesDoc(cr.Rules),
	}
	return mustYAML(networkPolicyDoc(cr.Name, NamespaceOf(cr.Dest.Namespace), spec))
}

// rulesDoc renders ingress rules as a NetworkPolicy writes them. Only the rules
// a plan makes are rendered, whose peers select by labels alone.
func rulesDoc(rules []NetworkPolicyRule) []any {
	out := make([]any, 0, len(rules))
	for _, r := range rules {
		peers := make([]any, 0, len(r.Peers))
		for _, p := range r.Peers {
			peer := map[string]any{}
			if p.NamespaceSelector != nil {
				peer["namespaceSelector"] = matchLabels(p.NamespaceSelector.MatchLabels)
			}
			if p.PodSelector != nil {
				peer["podSelector"] = matchLabels(p.PodSelector.MatchLabels)
			}
			peers = append(peers, peer)
		}
		rule := map[string]any{"from": peers}
		if len(r.Ports) > 0 {
			ports := make([]any, 0, len(r.Ports))
			for _, p := range r.Ports {
				ports = append(ports, portDoc(p))
			}
			rule["ports"] = ports
		}
		out = append(out, rule)
	}
	return out
}

func portDoc(p NetworkPolicyPort) map[string]any {
	entry := map[string]any{"protocol": p.Protocol}
	if n, err := strconv.Atoi(p.Port); err == nil {
		entry["port"] = n
	} else if p.Port != "" {
		entry["port"] = p.Port
	}
	return entry
}

// ruleKey identifies a rule by what it admits as written, for telling whether
// a policy already has it.
func ruleKey(r NetworkPolicyRule) string {
	b, err := json.Marshal(r)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func rulesKey(rules []NetworkPolicyRule) string {
	keys := make([]string, len(rules))
	for i, r := range rules {
		keys[i] = ruleKey(r)
	}
	return strings.Join(keys, "\n")
}

// IngressKey identifies a policy's ingress rules as parsed, for telling whether
// two copies of a policy, such as a variant's and its upstream's, have the same.
func IngressKey(p *NetworkPolicyEntity) string { return rulesKey(p.Ingress) }

func policyKey(p *NetworkPolicyEntity) string { return NamespaceOf(p.Namespace) + "/" + p.Name }

func workloadsMinus(a, b []*WorkloadEntity) []*WorkloadEntity {
	in := map[*WorkloadEntity]bool{}
	for _, w := range b {
		in[w] = true
	}
	var out []*WorkloadEntity
	for _, w := range a {
		if !in[w] {
			out = append(out, w)
		}
	}
	sortWorkloadPtrs(out)
	return out
}

func sortWorkloadPtrs(ws []*WorkloadEntity) {
	sort.SliceStable(ws, func(i, j int) bool { return WorkloadName(ws[i]) < WorkloadName(ws[j]) })
}
