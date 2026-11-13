// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package netpol

import (
	"fmt"
	"net"
	"slices"
	"sort"

	api "github.com/confighub/sdk/core/function/api"
)

// metadataIP is the cloud instance-metadata endpoint that egress policies should
// not expose (AWS/GCP/Azure link-local address).
const metadataIP = "169.254.169.254"

// Finding is one hygiene/anti-pattern result attributed to a resource (or a
// namespace, when no single resource owns it).
type Finding struct {
	Analyzer  string         `json:"analyzer"`
	Severity  api.Score      `json:"severity"`
	Cluster   string         `json:"cluster"`
	Namespace string         `json:"namespace,omitempty"`
	Kind      string         `json:"kind,omitempty"`
	Resource  string         `json:"resource,omitempty"`
	Message   string         `json:"message"`
	Origin    ResourceOrigin `json:"origin,omitempty"`
}

// AnalyzeFindings runs the analyzer set over one cluster:
//   - not-enforced (the cluster's Target says nothing enforces NetworkPolicy)
//   - missing-default-deny-ingress (namespace with workloads, no default-deny)
//   - uncovered-ingress (workload selected by no ingress policy)
//   - egress-blocks-dns (egress-isolated workload no policy lets resolve names)
//   - allow-all (an ingress rule with no sources, or an egress rule with
//     neither destinations nor ports)
//   - metadata-egress (egress rule that reaches the cloud metadata IP)
//   - ingress-egress-asymmetry (one side allows the flow, the other drops it)
//   - selects-nothing (a policy, or a peer of its rules, selecting no workload)
//
// A cluster that does not enforce NetworkPolicy is analyzed all the same, as
// if it did, so that its policies are right by the time it does.
func AnalyzeFindings(c *ClusterNetpol) []Finding {
	idx := newClusterIndex(c)
	namespaces, workloads := c.Coverage()
	var fs []Finding

	if c.Enforcement.NotEnforced() {
		fs = append(fs, Finding{
			Analyzer: "not-enforced", Severity: api.ScoreHigh, Cluster: c.Cluster,
			Message: fmt.Sprintf("nothing enforces NetworkPolicy on cluster %q (its Target's %s fact is empty), so its %d NetworkPolicies have no effect; the findings below describe them as if they did",
				c.Cluster, EnforcerFact, len(c.NetworkPolicies)),
		})
	}

	for _, nc := range namespaces {
		if nc.Workloads > 0 && !nc.DefaultDenyIngress {
			fs = append(fs, Finding{
				Analyzer: "missing-default-deny-ingress", Severity: api.ScoreHigh,
				Cluster: c.Cluster, Namespace: nc.Namespace,
				Message: fmt.Sprintf("namespace %q has %d workload(s) but no default-deny ingress NetworkPolicy", nc.Namespace, nc.Workloads),
			})
		}
	}

	for _, wc := range workloads {
		if !wc.IngressCovered {
			fs = append(fs, Finding{
				Analyzer: "uncovered-ingress", Severity: api.ScoreHigh,
				Cluster: c.Cluster, Namespace: wc.Namespace, Kind: wc.Kind, Resource: wc.Name, Origin: wc.Origin,
				Message: fmt.Sprintf("%s %q in namespace %q is not selected by any ingress NetworkPolicy", wc.Kind, wc.Name, wc.Namespace),
			})
		}
	}

	for _, p := range c.NetworkPolicies {
		if hasAllowAllRule(p.Ingress) {
			fs = append(fs, Finding{
				Analyzer: "allow-all", Severity: api.ScoreMedium,
				Cluster: c.Cluster, Namespace: NamespaceOf(p.Namespace), Kind: "NetworkPolicy", Resource: p.Name, Origin: p.Origin,
				Message: fmt.Sprintf("NetworkPolicy %q has an allow-all ingress rule (empty `from`), permitting all sources", p.Name),
			})
		}
		if slices.ContainsFunc(p.Egress, NetworkPolicyRule.openEgress) {
			fs = append(fs, Finding{
				Analyzer: "allow-all", Severity: api.ScoreMedium,
				Cluster: c.Cluster, Namespace: NamespaceOf(p.Namespace), Kind: "NetworkPolicy", Resource: p.Name, Origin: p.Origin,
				Message: fmt.Sprintf("NetworkPolicy %q has an allow-all egress rule (no `to` and no ports), permitting every destination on every port", p.Name),
			})
		}
		for _, rule := range p.Egress {
			if !rule.reachesMetadata() {
				continue
			}
			via := fmt.Sprintf("names no destinations and allows TCP %d", metadataPort)
			for _, peer := range rule.Peers {
				if peer.IPBlock != nil && IPBlockExposesMetadata(peer.IPBlock) {
					via = fmt.Sprintf("allows CIDR %s without excepting it", peer.IPBlock.CIDR)
					break
				}
			}
			fs = append(fs, Finding{
				Analyzer: "metadata-egress", Severity: api.ScoreHigh,
				Cluster: c.Cluster, Namespace: NamespaceOf(p.Namespace), Kind: "NetworkPolicy", Resource: p.Name, Origin: p.Origin,
				Message: fmt.Sprintf("NetworkPolicy %q egress permits cloud metadata IP %s: a rule %s", p.Name, metadataIP, via),
			})
		}
	}

	fs = append(fs, idx.dnsFindings()...)
	fs = append(fs, idx.asymmetryFindings()...)
	fs = append(fs, idx.selectsNothingFindings(c.NetworkPolicies)...)

	sort.SliceStable(fs, func(i, j int) bool {
		// Whether the cluster enforces policy at all frames every other finding.
		if a, b := fs[i].Analyzer == "not-enforced", fs[j].Analyzer == "not-enforced"; a != b {
			return a
		}
		if fs[i].Severity != fs[j].Severity {
			return api.ScoreToNumber[fs[i].Severity] > api.ScoreToNumber[fs[j].Severity]
		}
		if fs[i].Analyzer != fs[j].Analyzer {
			return fs[i].Analyzer < fs[j].Analyzer
		}
		if fs[i].Namespace != fs[j].Namespace {
			return fs[i].Namespace < fs[j].Namespace
		}
		return fs[i].Resource < fs[j].Resource
	})
	return fs
}

// asymmetryFindings reports flows that one side expresses intent for but the
// other side silently drops. Only pairs where an explicit allow exists on one
// side are considered, so the output is the actionable mismatch set, not every
// blocked pair.
func (idx *clusterIndex) asymmetryFindings() []Finding {
	var fs []Finding
	for _, a := range idx.workloads {
		for _, b := range idx.workloads {
			if a == b {
				continue
			}
			// A's egress explicitly allows reaching B, but B's ingress drops it.
			// "Specific" intent only — an allow-all rule is not a per-peer intent.
			if idx.egressAllowsSpecific(a, b) && idx.ingressIsolated(b) && !idx.ingressAllows(b, a) {
				fs = append(fs, Finding{
					Analyzer: "ingress-egress-asymmetry", Severity: api.ScoreMedium,
					Cluster: idx.cluster, Namespace: NamespaceOf(a.Namespace), Kind: a.Kind, Resource: a.Name, Origin: a.Origin,
					Message: fmt.Sprintf("%s %q egress allows reaching %s %q, but %q's ingress does not admit it — traffic is dropped at the destination",
						a.Kind, a.Name, b.Kind, b.Name, b.Name),
				})
			}
			// B's ingress explicitly allows A, but A's egress won't permit it.
			if idx.ingressAllowsSpecific(b, a) && idx.egressIsolated(a) && !idx.egressAllows(a, b) {
				fs = append(fs, Finding{
					Analyzer: "ingress-egress-asymmetry", Severity: api.ScoreMedium,
					Cluster: idx.cluster, Namespace: NamespaceOf(b.Namespace), Kind: b.Kind, Resource: b.Name, Origin: b.Origin,
					Message: fmt.Sprintf("%s %q ingress allows %s %q, but %q's egress does not permit it — traffic is dropped at the source",
						b.Kind, b.Name, a.Kind, a.Name, a.Name),
				})
			}
		}
	}
	return fs
}

// dnsNamespace is where the cluster DNS runs.
const dnsNamespace = "kube-system"

// dnsFindings reports each egress-isolated workload that no policy selecting it
// lets reach DNS, so it could not resolve a name once NetworkPolicy is
// enforced. Policies add up, so DNS allowed by any of them counts.
func (idx *clusterIndex) dnsFindings() []Finding {
	var fs []Finding
	for _, w := range idx.workloads {
		if !idx.egressIsolated(w) || idx.egressAllowsDNS(w) {
			continue
		}
		fs = append(fs, Finding{
			Analyzer: "egress-blocks-dns", Severity: api.ScoreHigh,
			Cluster: idx.cluster, Namespace: NamespaceOf(w.Namespace), Kind: w.Kind, Resource: w.Name, Origin: w.Origin,
			Message: fmt.Sprintf("%s %q is egress-isolated, but no NetworkPolicy selecting it allows DNS (UDP 53), so it cannot resolve names once policy is enforced",
				w.Kind, w.Name),
		})
	}
	return fs
}

// egressAllowsDNS reports whether a policy isolating w's egress has a rule that
// admits DNS to somewhere the cluster DNS could be: any destination, the
// cluster DNS's namespace, or an ipBlock, such as a node-local cache or the
// cloud's resolver.
func (idx *clusterIndex) egressAllowsDNS(w *WorkloadEntity) bool {
	for _, p := range idx.policiesByNS[NamespaceOf(w.Namespace)] {
		if !p.IsolatesEgress() || !p.PodSelector.Matches(w.PodLabels) {
			continue
		}
		for _, rule := range p.Egress {
			if rule.allowsDNSPort() && idx.reachesDNSNamespace(rule, NamespaceOf(p.Namespace)) {
				return true
			}
		}
	}
	return false
}

func (idx *clusterIndex) reachesDNSNamespace(rule NetworkPolicyRule, policyNS string) bool {
	if len(rule.Peers) == 0 {
		return true
	}
	dnsLabels := idx.nsLabels[dnsNamespace]
	if dnsLabels == nil {
		dnsLabels = map[string]string{namespaceNameLabel: dnsNamespace}
	}
	for _, peer := range rule.Peers {
		switch {
		case peer.IPBlock != nil:
			return true
		case peer.NamespaceSelector != nil && peer.NamespaceSelector.Matches(dnsLabels):
			return true
		case peer.NamespaceSelector == nil && policyNS == dnsNamespace:
			return true
		}
	}
	return false
}

// selectsNothingFindings reports the policies whose podSelector selects no
// workload in their namespace, and the peers of their rules that select no
// workload in it: most often a label that was renamed, or misspelled. A policy
// that selects nothing protects nothing; a peer that selects nothing allows
// nothing, which shows only once the policy is enforced. A peer with a
// namespaceSelector is not checked, since its namespaces may hold workloads
// ConfigHub does not know of; nor is an empty selector, which selects whatever
// is there.
func (idx *clusterIndex) selectsNothingFindings(policies []*NetworkPolicyEntity) []Finding {
	var fs []Finding
	selectsAny := func(sel LabelSelector, ns string) bool {
		return slices.ContainsFunc(idx.workloads, func(w *WorkloadEntity) bool {
			return NamespaceOf(w.Namespace) == ns && sel.Matches(w.PodLabels)
		})
	}
	for _, p := range policies {
		ns := NamespaceOf(p.Namespace)
		finding := func(msg string) Finding {
			return Finding{
				Analyzer: "selects-nothing", Severity: api.ScoreLow,
				Cluster: idx.cluster, Namespace: ns, Kind: "NetworkPolicy", Resource: p.Name, Origin: p.Origin,
				Message: msg,
			}
		}
		if !p.PodSelector.Empty() && !selectsAny(p.PodSelector, ns) {
			fs = append(fs, finding(fmt.Sprintf("NetworkPolicy %q selects no workload ConfigHub has in namespace %q: check its podSelector, unless it selects pods made outside ConfigHub", p.Name, ns)))
			continue
		}
		for _, dir := range []struct {
			name  string
			rules []NetworkPolicyRule
		}{{"ingress", p.Ingress}, {"egress", p.Egress}} {
			for i, rule := range dir.rules {
				for j, peer := range rule.Peers {
					if peer.NamespaceSelector != nil || peer.PodSelector == nil || peer.PodSelector.Empty() || selectsAny(*peer.PodSelector, ns) {
						continue
					}
					fs = append(fs, finding(fmt.Sprintf("NetworkPolicy %q %s rule %d peer %d selects no workload ConfigHub has in namespace %q, so it may allow nothing: check its podSelector, unless it selects pods made outside ConfigHub",
						p.Name, dir.name, i, j, ns)))
				}
			}
		}
	}
	return fs
}

// hasAllowAllRule reports whether any rule in the set has no peer constraints
// (an empty `from`/`to`), which admits all peers.
func hasAllowAllRule(rules []NetworkPolicyRule) bool {
	for _, r := range rules {
		if len(r.Peers) == 0 {
			return true
		}
	}
	return false
}

// IPBlockExposesMetadata reports whether an egress ipBlock admits the cloud
// metadata IP — its CIDR contains the address and no `except` excludes it. A /32
// naming the address alone allows it by intent, so it is not reported.
func IPBlockExposesMetadata(ib *IPBlock) bool {
	_, cidr, err := net.ParseCIDR(ib.CIDR)
	if err != nil {
		return false
	}
	if ones, _ := cidr.Mask.Size(); ones == 32 {
		return false
	}
	mip := net.ParseIP(metadataIP)
	if !cidr.Contains(mip) {
		return false
	}
	for _, ex := range ib.Except {
		if _, exNet, err := net.ParseCIDR(ex); err == nil && exNet.Contains(mip) {
			return false
		}
	}
	return true
}
