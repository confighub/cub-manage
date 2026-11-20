// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

// The NetworkPolicy guardrail pack. The mechanism -- policy Space, Triggers,
// the group selection on each in-scope Space, and reporting the Units a Trigger
// marked -- is managerkit/guardrails'. What is netpol's is the rules. Those that
// depend on more than one resource, such as whether any policy selects a
// workload, read the cluster's other resources (cluster_checks.go).

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/confighub/sdk/core/cubapi"
	"github.com/confighub/sdk/managerkit/guardrails"

	"github.com/confighub/cub-manage/internal/celrule"
	"github.com/confighub/cub-manage/internal/cub"
)

// The checks' failures, evaluated on each NetworkPolicy, or on the workload or
// Namespace a check is about (r is the resource).

var networkPolicy = []string{"networking.k8s.io/v1/NetworkPolicy"}

// egressPeerFailures fails each peer of each egress rule of a NetworkPolicy whose
// ipBlock does not pass ok, a condition on peer, at field of the peer, with
// value and detail. i and j index the rule and the peer.
func egressPeerFailures(ok, field, value, detail string) string {
	egress := "r.spec.egress"
	return celrule.When("has("+egress+")",
		"lists.range(size("+egress+")).filter(i, has("+egress+"[i].to)).map(i, "+
			"lists.range(size("+egress+"[i].to)).filter(j, has("+egress+"[i].to[j].ipBlock) && !("+ok+")).map(j, "+
			celrule.Failure("'spec.egress.' + string(i) + '.to.' + string(j) + '."+field+"'", value, detail)+")).flatten()")
}

// peer is the egress peer egressPeerFailures's conditions read.
const peer = "r.spec.egress[i].to[j]"

// egressRuleFailures fails each egress rule of a NetworkPolicy for which bad, a
// condition on rule, holds, at field of the rule, with value and detail. i
// indexes the rule.
func egressRuleFailures(bad, field, value, detail string) string {
	egress := "r.spec.egress"
	return celrule.When("has("+egress+")",
		"lists.range(size("+egress+")).filter(i, "+bad+").map(i, "+
			celrule.Failure(celrule.Indexed("spec.egress", "i", field), value, detail)+")")
}

// rule is the egress rule egressRuleFailures's conditions read.
const rule = "r.spec.egress[i]"

// CEL conditions on rule.
var (
	// noDestinations: the rule names no destinations, so it allows any.
	noDestinations = "(!has(" + rule + ".to) || size(" + rule + ".to) == 0)"
	// noPorts: the rule names no ports, so it allows every one.
	noPorts = "(!has(" + rule + ".ports) || size(" + rule + ".ports) == 0)"
)

// allowsPort is a CEL condition that rule admits port on protocol: it names no
// ports, or one that admits it. A port entry's protocol is TCP unless it says
// otherwise, and one with no port admits every port of its protocol. A port is
// compared as a string, since one read from YAML may be an int or a double, and
// a named port matches only a pod's port of that name; a range is left to the
// findings, which can compare numbers.
func allowsPort(protocol string, port int, names ...string) string {
	values := []string{celrule.Lit(strconv.Itoa(port))}
	for _, n := range names {
		values = append(values, celrule.Lit(n))
	}
	return "(" + noPorts + " || " + rule + ".ports.exists(p, " +
		"(has(p.protocol) ? p.protocol : 'TCP') == " + celrule.Lit(protocol) +
		" && (!has(p.port) || string(p.port) in [" + strings.Join(values, ", ") + "])))"
}

// policyIsolatesEgress: the policy isolates egress, explicitly or by having
// egress rules and no policyTypes.
const policyIsolatesEgress = "(has(r.spec.policyTypes) ? 'Egress' in r.spec.policyTypes : has(r.spec.egress))"

var (
	// Every ingress rule must name its sources — an empty `from` admits all.
	celNoAllowAllIngress = celrule.When("has(r.spec.ingress)",
		"lists.range(size(r.spec.ingress)).filter(i, !(has(r.spec.ingress[i].from) && size(r.spec.ingress[i].from) > 0)).map(i, "+
			celrule.Failure(celrule.Indexed("spec.ingress", "i", "from"), "r.spec.ingress[i].?from",
				"'ingress rule ' + string(i) + ' names no sources, so it admits all of them'")+")")

	// No egress rule may permit the whole internet via 0.0.0.0/0 or ::/0.
	celNoWideCidrEgress = egressPeerFailures("!("+peer+".ipBlock.cidr in ['0.0.0.0/0', '::/0'])",
		"ipBlock.cidr", celrule.Some(peer+".ipBlock.cidr"), "'egress rule ' + string(i) + ' allows the whole internet'")

	// No egress rule may allow every destination on every port. One naming
	// ports, such as HTTPS to anywhere, says what it allows.
	celNoOpenEgress = egressRuleFailures(noDestinations+" && "+noPorts,
		"to", rule+".?to", "'egress rule ' + string(i) + ' names no destinations and no ports, so it allows all egress'")

	// A policy that isolates egress and allows some must allow DNS -- UDP 53,
	// or the port the cluster DNS names "dns" -- to somewhere the cluster DNS
	// can be, or the pods it selects cannot resolve names. A policy with no
	// egress rules denies all egress deliberately, and is left alone.
	celEgressAllowsDNS = celrule.When(
		""+policyIsolatesEgress+" && has(r.spec.egress) && size(r.spec.egress) > 0 && "+
			"!lists.range(size(r.spec.egress)).exists(i, "+allowsPort("UDP", 53, "dns")+" && ("+noDestinations+" || "+
			rule+".to.exists(t, has(t.namespaceSelector) || has(t.ipBlock))))",
		"["+celrule.Failure("'spec.egress'", celrule.None,
			"'no egress rule allows DNS (UDP 53), so the pods this policy selects cannot resolve names once it is enforced'")+"]")

	// celNoMetadataEgress fails an egress ipBlock whose CIDR contains the cloud
	// metadata IP unless an except names it. The CIDRs are listed rather than
	// computed, since CEL has no CIDR arithmetic; the excepts listed are those
	// that cover the IP, so an except as wide as the block it is in counts.
	// An egress rule that names no destinations reaches the metadata IP too,
	// when it allows the metadata service's port, TCP 80.
	celNoMetadataEgress = egressPeerFailures(
		fmt.Sprintf("!(%[1]s.ipBlock.cidr in [%[2]s]) || (has(%[1]s.ipBlock.except) && %[1]s.ipBlock.except.exists(e, e in [%[2]s, '%[3]s/32']))",
			peer, metadataCIDRs(), metadataIP),
		"ipBlock.except", peer+".ipBlock.?except",
		"'egress rule ' + string(i) + ' reaches the cloud metadata IP through ' + "+peer+".ipBlock.cidr + ' without excepting it'") +
		" + " + egressRuleFailures(noDestinations+" && "+allowsPort("TCP", 80),
		"to", rule+".?to", "'egress rule ' + string(i) + ' names no destinations and allows TCP 80, so it reaches the cloud metadata IP'")
)

var Pack = guardrails.Pack{
	Group: "netpol",
	Checks: []guardrails.Check{
		{Control: "CH-0501", Name: "netpol-no-allow-all-ingress", Rule: "netpol-no-allow-all-ingress", DisplayName: "Ingress rules name their sources",
			Description: "Warns on a NetworkPolicy ingress rule with an empty `from` (admits all sources). Fix: enumerate the allowed sources.",
			Engine:      guardrails.EngineCEL, Priority: guardrails.PriorityMedium, Failures: celNoAllowAllIngress, ResourceTypes: networkPolicy,
			Corresponds: []string{"KL:non-isolated-pod"}},
		{Control: "CH-0502", Name: "netpol-no-wide-cidr-egress", Rule: "netpol-no-wide-cidr-egress", DisplayName: "No egress to the whole internet",
			Description: "Warns on an egress rule permitting 0.0.0.0/0 or ::/0. Fix: restrict the CIDR, or except the cloud-metadata IP with the netpol-except-metadata profile.",
			Engine:      guardrails.EngineCEL, Priority: guardrails.PriorityMedium, Failures: celNoWideCidrEgress, ResourceTypes: networkPolicy},
		{Control: "CH-0503", Name: "netpol-no-open-egress", Rule: "netpol-no-open-egress", DisplayName: "Egress rules name destinations or ports",
			Description: "Warns on an egress rule with neither `to` nor `ports`, which allows every destination on every port. Fix: name the destinations, or the ports, such as 443, that the pods need.",
			Engine:      guardrails.EngineCEL, Priority: guardrails.PriorityMedium, Failures: celNoOpenEgress, ResourceTypes: networkPolicy},
		{Control: "CH-0504", Name: "netpol-no-metadata-egress", Rule: "netpol-no-metadata-egress", DisplayName: "No egress to the cloud metadata IP",
			Description: "Warns on an egress rule that reaches the cloud metadata IP 169.254.169.254: an ipBlock containing it without excepting it, or a rule with no `to` that allows TCP 80. Fix: the netpol-except-metadata profile, or name the destinations.",
			Engine:      guardrails.EngineCEL, Priority: guardrails.PriorityHigh, Failures: celNoMetadataEgress, ResourceTypes: networkPolicy},
		{Control: "CH-0505", Name: "netpol-egress-allows-dns", Rule: "netpol-egress-allows-dns", DisplayName: "Egress policies allow DNS",
			Description: "Warns on a NetworkPolicy that isolates egress and allows some, but no DNS (UDP 53): once enforced, the pods it selects cannot resolve names. A policy with no egress rules, a deliberate deny-all, is not checked. Fix: allow UDP and TCP 53 to the cluster DNS. Policies add up, so where a separate policy allows DNS, except this one, and `cub manage netpol findings` checks the union.",
			Engine:      guardrails.EngineCEL, Priority: guardrails.PriorityHigh, Failures: celEgressAllowsDNS, ResourceTypes: networkPolicy},
		{Control: "CH-0506", Name: "netpol-ingress-coverage", Rule: "netpol-ingress-coverage", DisplayName: "Workloads have an ingress NetworkPolicy",
			Description: "Warns on a workload that no NetworkPolicy isolating ingress selects, among the NetworkPolicies on its cluster: its pods accept traffic from anywhere. Not checked where the cluster records that nothing enforces NetworkPolicy, nor for a Unit with no Target. Fix: `cub manage netpol default-deny <namespace>` and an allow policy.",
			Engine:      guardrails.EngineCEL, Priority: guardrails.PriorityLow, Failures: celIngressCoverage,
			ResourceTypes: celrule.WorkloadTypes(), OtherData: []guardrails.OtherData{otherPolicies},
			Fix:         "cub manage netpol default-deny <namespace>",
			Corresponds: []string{"SC:pod-networkpolicy", "KS:C-0206", "KS:C-0260"}},
		{Control: "CH-0507", Name: "netpol-selects-pods", Rule: "netpol-selects-pods", DisplayName: "NetworkPolicies select workloads",
			Description: "Warns on a NetworkPolicy whose podSelector selects no workload on its cluster in its namespace, which protects nothing, and on a rule's peer whose podSelector selects none, which allows nothing: most often a label renamed or misspelled. A peer with a namespaceSelector, and an empty selector, are not checked. Fix: correct the selector, or except the policy if it selects pods made outside ConfigHub.",
			Engine:      guardrails.EngineCEL, Priority: guardrails.PriorityLow, Failures: celSelectsPods,
			ResourceTypes: networkPolicy, OtherData: []guardrails.OtherData{otherWorkloads},
			Corresponds: []string{"SC:networkpolicy-targets-pod", "KL:dangling-networkpolicy", "KL:dangling-networkpolicypeer-podselector"}},
		{Control: "CH-0508", Name: "netpol-default-deny", Rule: "netpol-default-deny", DisplayName: "Namespaces deny ingress by default",
			Description: "Warns on a Namespace holding workloads on its cluster where no NetworkPolicy denies ingress by default: one selecting every pod, isolating ingress, with no rule admitting all traffic. Not checked where the cluster records that nothing enforces NetworkPolicy, nor for a Unit with no Target. Fix: `cub manage netpol default-deny <namespace>`.",
			Engine:      guardrails.EngineCEL, Priority: guardrails.PriorityMedium, Failures: celDefaultDeny,
			ResourceTypes: []string{"v1/Namespace"}, OtherData: []guardrails.OtherData{otherWorkloads, otherPolicies},
			Fix:         "cub manage netpol default-deny <namespace>",
			Corresponds: []string{"CIS:5.3.2"}},
		{Control: "CH-0509", Name: "netpol-allowed-at-both-ends", Rule: "netpol-allowed-at-both-ends", DisplayName: "Allowed flows are allowed at both ends",
			Description: "Warns on a NetworkPolicy rule that allows the pods it selects to reach, or be reached by, a workload whose own policies isolate the other direction and do not allow the flow, so the traffic is dropped there. Only rules naming their peers are checked; ports are not compared. Fix: add the matching ingress or egress allow at the other end.",
			Engine:      guardrails.EngineCEL, Priority: guardrails.PriorityMedium, Failures: celAllowedAtBothEnds,
			ResourceTypes: networkPolicy, OtherData: []guardrails.OtherData{otherWorkloads, otherPolicies, otherNamespaces}},
	},
}

func preflight(ctx context.Context) (*cubapi.Client, error) { return cub.Preflight(ctx) }

func newGuardrailsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "guardrails",
		Short: "Install and inspect the NetworkPolicy guardrail policy pack",
		Long: `guardrails manages a pack of NetworkPolicy validation policies, defined once in a
policy Space and enforced in each Space that opts into the group:

  netpol-no-allow-all-ingress  ingress rules must name their sources
  netpol-no-wide-cidr-egress   no egress to 0.0.0.0/0 or ::/0
  netpol-no-open-egress        egress rules must name destinations or ports
  netpol-no-metadata-egress    no egress to the cloud metadata IP
  netpol-egress-allows-dns     a policy that isolates egress must allow DNS
  netpol-ingress-coverage      a workload has a NetworkPolicy isolating its ingress
  netpol-selects-pods          a policy, and each podSelector peer, selects a workload
  netpol-default-deny          a namespace with workloads denies ingress by default
  netpol-allowed-at-both-ends  a flow a rule allows is allowed at the other end too

Triggers are created with Warn=true (advisory ValidationWarnings, never blocking).
Promote one to blocking later with:
  cub trigger update <slug> --space <policy-space> --unwarn

The last four read the NetworkPolicies, workloads and Namespaces on the
cluster: those of the Units released to the same Target as the Space, which each
Space selects for itself. A change to one of them re-runs the rules on the Units
that read it. A Unit with no Target is on no known cluster, so they pass it.

Except one resource from a rule with 'cub manage guardrails exception add';
'cub manage guardrails exception list' shows what is excepted, and why.`,
	}
	cmd.AddCommand(Pack.InstallCmd(preflight))
	return cmd
}
