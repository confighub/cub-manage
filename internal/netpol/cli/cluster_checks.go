// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

// The checks that read the cluster's other resources: which NetworkPolicies select a
// workload, which workloads a policy selects, and what the policies at the other end of a
// flow allow. Each declares the resources it reads as OtherData, which its rule's Trigger
// selects for each Space from the Units released to the same Target, and reads them as
// other.<key>, matching selectors with selects, podLabels and namespaceLabels.

import (
	"strings"

	"github.com/confighub/sdk/managerkit/guardrails"

	"github.com/confighub/cub-manage/internal/celrule"
	"github.com/confighub/cub-manage/internal/netpol"
)

// The other resources the checks read.
var (
	otherPolicies   = guardrails.OtherData{Key: "policies", ResourceTypes: networkPolicy}
	otherWorkloads  = guardrails.OtherData{Key: "workloads", ResourceTypes: celrule.WorkloadTypes()}
	otherNamespaces = guardrails.OtherData{Key: "namespaces", ResourceTypes: []string{"v1/Namespace"}}
)

// orEmpty is the value of the field at path of x, a CEL expression, or empty when it is
// missing or null, such as a policy's ingress rules.
func orEmpty(x, path, empty string) string {
	optional := x + ".?" + strings.ReplaceAll(path, ".", ".?")
	return "(" + optional + ".orValue(null) == null ? " + empty + " : " + x + "." + path + ")"
}

// present is whether the field at path of x is set to something other than null.
func present(x, path string) string {
	return "(" + x + ".?" + strings.ReplaceAll(path, ".", ".?") + ".orValue(null) != null)"
}

// podSelectorOf is the policy p's podSelector as the API server reads it: a missing or null
// one selects every pod in the namespace.
func podSelectorOf(p string) string { return orEmpty(p, "spec.podSelector", "{}") }

// emptySelector is whether the selector s selects everything: it names no labels and no
// expressions.
func emptySelector(s string) string {
	return "(size(" + orEmpty(s, "matchLabels", "{}") + ") == 0 && size(" + orEmpty(s, "matchExpressions", "[]") + ") == 0)"
}

// The directions a policy governs, with the field of its rules and of their peers.
type direction struct {
	name, rules, peers string
}

var (
	ingress = direction{name: "Ingress", rules: "ingress", peers: "from"}
	egress  = direction{name: "Egress", rules: "egress", peers: "to"}
)

func (d direction) opposite() direction {
	if d == ingress {
		return egress
	}
	return ingress
}

// isolates is whether the policy p isolates the pods it selects in direction d: as its
// policyTypes say, or, when it names none, always for ingress and for egress when it has
// egress rules.
func isolates(p string, d direction) string {
	types := orEmpty(p, "spec.policyTypes", "[]")
	otherwise := "true"
	if d == egress {
		otherwise = "size(" + orEmpty(p, "spec.egress", "[]") + ") > 0"
	}
	return "(size(" + types + ") > 0 ? " + celrule.Lit(d.name) + " in " + types + " : " + otherwise + ")"
}

// rulesOf are the policy p's rules in direction d, and peersOf the peers of one of them.
func rulesOf(p string, d direction) string    { return orEmpty(p, "spec."+d.rules, "[]") }
func peersOf(rule string, d direction) string { return orEmpty(rule, d.peers, "[]") }

// selectsPods is whether the policy p selects the pods of the workload w.
func selectsPods(p, w string) string {
	return "(" + celrule.NamespaceOf(p) + " == " + celrule.NamespaceOf(w) +
		" && selects(" + podSelectorOf(p) + ", podLabels(" + w + ")))"
}

// peerMatches is whether a peer of a rule of a policy in the namespace policyNamespace names
// the pods of the workload w: its namespaceSelector selects w's namespace, by the labels the
// Namespace has, or, with none, w is in the policy's namespace; and its podSelector, if any,
// selects w's pods. A peer that is only an ipBlock names no pods.
func peerMatches(peer, w, policyNamespace string) string {
	namespace := celrule.NamespaceOf(w)
	return "((" + present(peer, "podSelector") + " || " + present(peer, "namespaceSelector") + ") && " +
		"(" + present(peer, "namespaceSelector") +
		" ? selects(" + peer + ".namespaceSelector, namespaceLabels(" + celrule.Other(otherNamespaces.Key) + ", " + namespace + "))" +
		" : " + namespace + " == " + policyNamespace + ") && " +
		"(" + present(peer, "podSelector") + " ? selects(" + peer + ".podSelector, podLabels(" + w + ")) : true))"
}

// workloadName names the workload w as a finding does: its kind, namespace and name.
func workloadName(w string) string {
	return w + ".kind + ' ' + " + celrule.NamespaceOf(w) + " + '/' + " + w + ".metadata.name"
}

// enforced says the Unit's cluster may enforce NetworkPolicy: its Target does not record that
// no network plugin enforces it. Where none does, no policy covers a workload, so a coverage
// finding for each would only be noise.
var enforced = "!(" + celrule.Lit(netpol.EnforcerFact) + " in " + targetFacts + " && " +
	targetFacts + "[" + celrule.Lit(netpol.EnforcerFact) + "] == '')"

// targetFacts are the facts of the Unit's Target, empty when it has none.
const targetFacts = "(functionContext.?TargetFacts.orValue(null) != null ? functionContext.TargetFacts : {})"

var (
	// celIngressCoverage fails a workload that no policy isolating ingress selects.
	celIngressCoverage = celrule.When(
		celrule.OnACluster+" && "+enforced+" && podLabels(r) != null && "+
			"!"+celrule.Other(otherPolicies.Key)+".exists(p, "+isolates("p", ingress)+" && "+selectsPods("p", "r")+")",
		"["+celrule.Failure(celrule.PodLabelsPath(), celrule.Some("podLabels(r)"),
			"'no NetworkPolicy isolating ingress in namespace ' + "+celrule.NamespaceOf("r")+" + ' selects its pods'")+"]")

	// celSelectsPods fails a policy whose podSelector selects no workload in its namespace, or,
	// when it selects some, each peer of its rules whose podSelector selects none there. An
	// empty selector selects whatever is there, and a peer with a namespaceSelector may name
	// workloads in namespaces ConfigHub does not hold, so neither is checked.
	celSelectsPods = celrule.When(celrule.OnACluster,
		"(!"+emptySelector(podSelectorOf("r"))+" && !"+selectsAnyIn(podSelectorOf("r"))+
			" ? ["+celrule.Failure("'spec.podSelector'", celrule.Some(podSelectorOf("r")),
			"'selects no workload in namespace ' + "+celrule.NamespaceOf("r"))+"]"+
			" : "+peerSelectsNothing(ingress)+" + "+peerSelectsNothing(egress)+")")

	// celDefaultDeny fails a Namespace that holds workloads and no policy denying ingress by
	// default: one selecting every pod, isolating ingress, with no rule admitting all traffic.
	celDefaultDeny = celrule.When(
		celrule.OnACluster+" && "+enforced+" && "+
			celrule.Other(otherWorkloads.Key)+".exists(w, "+celrule.NamespaceOf("w")+" == r.metadata.name) && "+
			"!"+celrule.Other(otherPolicies.Key)+".exists(p, "+celrule.NamespaceOf("p")+" == r.metadata.name && "+
			emptySelector(podSelectorOf("p"))+" && "+isolates("p", ingress)+" && "+
			"!"+rulesOf("p", ingress)+".exists(rule, "+admitsAll("rule", ingress)+"))",
		"["+celrule.Failure("'metadata.name'", celrule.Some("r.metadata.name"),
			"'namespace ' + r.metadata.name + ' holds workloads, and no NetworkPolicy denies ingress to them by default'")+"]")

	// celAllowedAtBothEnds fails each rule of a policy that names the pods at the other end of
	// a flow from the pods it selects, where those pods are isolated in the other direction and
	// none of their policies allows the flow: the traffic is dropped there.
	celAllowedAtBothEnds = droppedAtOtherEnd(egress, "reach") + " + " + droppedAtOtherEnd(ingress, "be reached by")
)

// selectsAnyIn is whether the selector s selects some workload in r's namespace.
func selectsAnyIn(s string) string {
	return celrule.Other(otherWorkloads.Key) + ".exists(w, " + celrule.NamespaceOf("w") + " == " +
		celrule.NamespaceOf("r") + " && selects(" + s + ", podLabels(w)))"
}

// peerSelectsNothing fails each peer of r's rules in direction d whose podSelector selects no
// workload in r's namespace.
func peerSelectsNothing(d direction) string {
	rules := rulesOf("r", d)
	peers := peersOf(rules+"[i]", d)
	peer := peers + "[j]"
	return "lists.range(size(" + rules + ")).map(i, lists.range(size(" + peers + ")).filter(j, " +
		"!" + present(peer, "namespaceSelector") + " && " + present(peer, "podSelector") + " && " +
		"!" + emptySelector(peer+".podSelector") + " && !" + selectsAnyIn(peer+".podSelector") + ").map(j, " +
		celrule.Failure(celrule.Lit("spec."+d.rules+".")+" + string(i) + "+celrule.Lit("."+d.peers+".")+" + string(j) + '.podSelector'",
			celrule.Some(peer+".podSelector"),
			celrule.Lit(strings.ToLower(d.name)+" rule ")+" + string(i) + ' peer ' + string(j) + ' selects no workload in namespace ' + "+celrule.NamespaceOf("r")) +
		")).flatten()"
}

// admitsAll is whether a policy rule in direction d admits all traffic: it names no ports, and
// no peers or an ipBlock spanning every address. One limited to some ports, such as a DNS
// allowance, leaves a default-deny in place.
func admitsAll(rule string, d direction) string {
	return "(size(" + orEmpty(rule, "ports", "[]") + ") == 0 && (size(" + peersOf(rule, d) + ") == 0 || " +
		peersOf(rule, d) + ".exists(peer, peer.?ipBlock.?cidr.orValue('').endsWith('/0'))))"
}

// isolatedIn is whether some policy isolating direction d selects the workload w.
func isolatedIn(w string, d direction) string {
	return celrule.Other(otherPolicies.Key) + ".exists(q, " + isolates("q", d) + " && " + selectsPods("q", w) + ")"
}

// allowsIn is whether a policy isolating direction d that selects the workload w has a rule
// admitting the workload from: one naming no peers, or a peer naming from's pods.
func allowsIn(w string, d direction, from string) string {
	return celrule.Other(otherPolicies.Key) + ".exists(q, " + isolates("q", d) + " && " + selectsPods("q", w) + " && " +
		rulesOf("q", d) + ".exists(allow, size(" + peersOf("allow", d) + ") == 0 || " +
		peersOf("allow", d) + ".exists(allowed, " + peerMatches("allowed", from, celrule.NamespaceOf("q")) + ")))"
}

// droppedAtOtherEnd fails each rule of r in direction d, while r isolates d, that names the
// pods of a workload there whose policies isolate the opposite direction and do not admit a
// workload r selects. A rule naming no peers allows every flow rather than one, so it is not
// checked.
func droppedAtOtherEnd(d direction, verb string) string {
	rules := rulesOf("r", d)
	peers := peersOf(rules+"[i]", d)
	opposite := d.opposite()
	sameWorkload := "(here.kind == there.kind && here.metadata.name == there.metadata.name && " +
		celrule.NamespaceOf("here") + " == " + celrule.NamespaceOf("there") + ")"
	dropped := celrule.Other(otherWorkloads.Key) + ".filter(there, " +
		peers + ".exists(peer, " + peerMatches("peer", "there", celrule.NamespaceOf("r")) + ") && " +
		isolatedIn("there", opposite) + " && " +
		celrule.Other(otherWorkloads.Key) + ".exists(here, !" + sameWorkload + " && " + selectsPods("r", "here") +
		" && !" + allowsIn("there", opposite, "here") + "))"
	return celrule.When(isolates("r", d),
		"lists.range(size("+rules+")).map(i, "+dropped+".map(there, "+
			celrule.Failure(celrule.Indexed("spec."+d.rules, "i", d.peers), celrule.Some(peers),
				celrule.Lit(strings.ToLower(d.name)+" rule ")+" + string(i) + "+celrule.Lit(" lets the pods it selects "+verb+" ")+" + "+
					workloadName("there")+" + ', whose "+strings.ToLower(opposite.name)+" policies do not allow it'")+
			")).flatten()")
}
