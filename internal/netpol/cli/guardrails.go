// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

// The NetworkPolicy guardrail pack. The mechanism -- policy Space, Triggers,
// the group selection on each in-scope Space, and reporting the Units a Trigger
// marked -- is managerkit/guardrails'. What is netpol's is the rules, and the
// annotate command, which turns ingress coverage, which no single resource can
// express, into an annotation the pack's own rule then warns on.

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
	"github.com/confighub/cub-manage/internal/netpol/snapshot"
)

// coverageAnnotation is written by `guardrails annotate` onto each workload Unit
// no ingress NetworkPolicy selects, and read by the coverage rule
// (annotate-then-validate).
const coverageAnnotation = "netpol.confighub.com/ingress-coverage"

// Guardrail CEL expressions, evaluated per resource (r aliases the resource).

// egressPeerFailures fails each peer of each egress rule of a NetworkPolicy whose
// ipBlock does not pass ok, a condition on peer, at field of the peer, with
// value and detail. i and j index the rule and the peer.
func egressPeerFailures(ok, field, value, detail string) string {
	egress := "r.spec.egress"
	return celrule.When("r.kind == 'NetworkPolicy' && has("+egress+")",
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
	return celrule.When("r.kind == 'NetworkPolicy' && has("+egress+")",
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
	celNoAllowAllIngress = celrule.Result(celrule.When("r.kind == 'NetworkPolicy' && has(r.spec.ingress)",
		"lists.range(size(r.spec.ingress)).filter(i, !(has(r.spec.ingress[i].from) && size(r.spec.ingress[i].from) > 0)).map(i, "+
			celrule.Failure(celrule.Indexed("spec.ingress", "i", "from"), "r.spec.ingress[i].?from",
				"'ingress rule ' + string(i) + ' names no sources, so it admits all of them'")+")"))

	// No egress rule may permit the whole internet via 0.0.0.0/0 or ::/0.
	celNoWideCidrEgress = celrule.Result(egressPeerFailures("!("+peer+".ipBlock.cidr in ['0.0.0.0/0', '::/0'])",
		"ipBlock.cidr", celrule.Some(peer+".ipBlock.cidr"), "'egress rule ' + string(i) + ' allows the whole internet'"))

	// No egress rule may allow every destination on every port. One naming
	// ports, such as HTTPS to anywhere, says what it allows.
	celNoOpenEgress = celrule.Result(egressRuleFailures(noDestinations+" && "+noPorts,
		"to", rule+".?to", "'egress rule ' + string(i) + ' names no destinations and no ports, so it allows all egress'"))

	// A policy that isolates egress and allows some must allow DNS -- UDP 53,
	// or the port the cluster DNS names "dns" -- to somewhere the cluster DNS
	// can be, or the pods it selects cannot resolve names. A policy with no
	// egress rules denies all egress deliberately, and is left alone.
	celEgressAllowsDNS = celrule.Result(celrule.When(
		"r.kind == 'NetworkPolicy' && "+policyIsolatesEgress+" && has(r.spec.egress) && size(r.spec.egress) > 0 && "+
			"!lists.range(size(r.spec.egress)).exists(i, "+allowsPort("UDP", 53, "dns")+" && ("+noDestinations+" || "+
			rule+".to.exists(t, has(t.namespaceSelector) || has(t.ipBlock))))",
		"["+celrule.Failure("'spec.egress'", celrule.None,
			"'no egress rule allows DNS (UDP 53), so the pods this policy selects cannot resolve names once it is enforced'")+"]"))

	// celNoMetadataEgress fails an egress ipBlock whose CIDR contains the cloud
	// metadata IP unless an except names it. The CIDRs are listed rather than
	// computed, since CEL has no CIDR arithmetic; the excepts listed are those
	// that cover the IP, so an except as wide as the block it is in counts.
	// An egress rule that names no destinations reaches the metadata IP too,
	// when it allows the metadata service's port, TCP 80.
	celNoMetadataEgress = celrule.Result(egressPeerFailures(
		fmt.Sprintf("!(%[1]s.ipBlock.cidr in [%[2]s]) || (has(%[1]s.ipBlock.except) && %[1]s.ipBlock.except.exists(e, e in [%[2]s, '%[3]s/32']))",
			peer, metadataCIDRs(), metadataIP),
		"ipBlock.except", peer+".ipBlock.?except",
		"'egress rule ' + string(i) + ' reaches the cloud metadata IP through ' + "+peer+".ipBlock.cidr + ' without excepting it'") +
		" + " + egressRuleFailures(noDestinations+" && "+allowsPort("TCP", 80),
		"to", rule+".?to", "'egress rule ' + string(i) + ' names no destinations and allows TCP 80, so it reaches the cloud metadata IP'"))

	// Annotate-then-validate: warn while a coverage annotation is present.
	celNoCoverageFinding = celrule.AnnotationFinding(coverageAnnotation,
		"no ingress NetworkPolicy selects this workload, as guardrails annotate found")
)

var Pack = guardrails.Pack{
	Group: "netpol",
	Rules: []guardrails.Rule{
		{Slug: "netpol-no-allow-all-ingress", DisplayName: "Ingress rules name their sources", Description: "Warns on a NetworkPolicy ingress rule with an empty `from` (admits all sources). Fix: enumerate the allowed sources.", Expression: celNoAllowAllIngress},
		{Slug: "netpol-no-wide-cidr-egress", DisplayName: "No egress to the whole internet", Description: "Warns on an egress rule permitting 0.0.0.0/0 or ::/0. Fix: restrict the CIDR, or except the cloud-metadata IP with the netpol-except-metadata profile.", Expression: celNoWideCidrEgress},
		{Slug: "netpol-no-open-egress", DisplayName: "Egress rules name destinations or ports", Description: "Warns on an egress rule with neither `to` nor `ports`, which allows every destination on every port. Fix: name the destinations, or the ports, such as 443, that the pods need.", Expression: celNoOpenEgress},
		{Slug: "netpol-no-metadata-egress", DisplayName: "No egress to the cloud metadata IP", Description: "Warns on an egress rule that reaches the cloud metadata IP 169.254.169.254: an ipBlock containing it without excepting it, or a rule with no `to` that allows TCP 80. Fix: the netpol-except-metadata profile, or name the destinations.", Expression: celNoMetadataEgress},
		{Slug: "netpol-egress-allows-dns", DisplayName: "Egress policies allow DNS", Description: "Warns on a NetworkPolicy that isolates egress and allows some, but no DNS (UDP 53): once enforced, the pods it selects cannot resolve names. A policy with no egress rules, a deliberate deny-all, is not checked. Fix: allow UDP and TCP 53 to the cluster DNS. Policies add up, so where a separate policy allows DNS, except this one, and `cub manage netpol findings` checks the union.", Expression: celEgressAllowsDNS},
		{Slug: "netpol-ingress-coverage", DisplayName: "Workloads have an ingress NetworkPolicy", Description: "Warns while a netpol.confighub.com/ingress-coverage annotation is present (set by `cub manage netpol guardrails annotate`): a workload no ingress NetworkPolicy selects. Fix: `cub manage netpol default-deny <namespace>` and an allow policy, then re-run annotate.", Expression: celNoCoverageFinding},
	},
}

func preflight(ctx context.Context) (*cubapi.Client, error) { return cub.Preflight(ctx) }

func newGuardrailsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "guardrails",
		Short: "Install, inspect, and feed the NetworkPolicy guardrail policy pack",
		Long: `guardrails manages a pack of NetworkPolicy validation policies, defined once in a
policy Space and enforced in each Space that opts into the group:

  netpol-no-allow-all-ingress  ingress rules must name their sources
  netpol-no-wide-cidr-egress   no egress to 0.0.0.0/0 or ::/0
  netpol-no-open-egress        egress rules must name destinations or ports
  netpol-no-metadata-egress    no egress to the cloud metadata IP
  netpol-egress-allows-dns     a policy that isolates egress must allow DNS
  netpol-ingress-coverage      warns while a workload is annotated as selected by no
                               ingress NetworkPolicy (§ annotate)

Triggers are created with Warn=true (advisory ValidationWarnings, never blocking).
Promote one to blocking later with:
  cub trigger update <slug> --space <policy-space> --unwarn

The coverage rule realizes the annotate-then-validate model: whether a policy
selects a workload depends on two Units, which a per-Unit vet-cel can't see, so
'guardrails annotate' writes the coverage gap onto the workload's Unit and this
Trigger turns it into a warning.

Except one resource from a rule with 'cub manage guardrails exception add';
'cub manage guardrails exception list' shows what is excepted, and why.`,
	}
	cmd.AddCommand(Pack.InstallCmd(preflight), Pack.AnnotateCmd(preflight, annotateSpec))
	return cmd
}

// annotateSpec is the netpol half of `guardrails annotate`: which workload Units
// no ingress policy selects, and what to write on them. The rest of the command
// is managerkit/guardrails'.
var annotateSpec = guardrails.AnnotateSpec{
	Key:   coverageAnnotation,
	Noun:  "workload Unit",
	Short: "Annotate each workload Unit no ingress NetworkPolicy selects",
	Long: `annotate runs the coverage analysis and writes a netpol.confighub.com/
ingress-coverage annotation onto each workload Unit that no ingress NetworkPolicy
selects. Paired with the netpol-ingress-coverage rule from 'guardrails install',
this turns the cross-Unit coverage gap into an advisory ValidationWarning.

Re-run after adding policies: a covered workload's annotation is removed. Dry run
unless --commit --change-desc.`,
	Targets: func(ctx context.Context, c *cubapi.Client, where, cluster string) ([]guardrails.Target, error) {
		snap, err := snapshot.Load(ctx, c, where)
		if err != nil {
			return nil, err
		}
		var out []guardrails.Target
		for clusterName, cl := range snap.Clusters {
			if cluster != "" && clusterName != cluster {
				continue
			}
			// Where nothing enforces NetworkPolicy, no policy can cover a
			// workload, so a warning for each would only be noise.
			if cl.Enforcement.NotEnforced() {
				continue
			}
			_, workloads := cl.Coverage()
			for _, w := range workloads {
				if w.IngressCovered || w.Origin.UnitID == "" || w.Origin.SpaceID == "" {
					continue
				}
				out = append(out, guardrails.Target{
					SpaceID: w.Origin.SpaceID, UnitSlug: w.Origin.UnitSlug, Value: "uncovered",
					Space: w.Origin.Space, Cluster: clusterName, Namespace: w.Namespace,
					Detail: w.Kind + " " + w.Name,
				})
			}
		}
		return out, nil
	},
}
