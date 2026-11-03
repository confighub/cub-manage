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

	"github.com/spf13/cobra"

	"github.com/confighub/sdk/core/cubapi"
	"github.com/confighub/sdk/managerkit/guardrails"

	"github.com/confighub/cub-manage/internal/cub"
	"github.com/confighub/cub-manage/internal/netpol/snapshot"
)

// coverageAnnotation is written by `guardrails annotate` onto each workload Unit
// no ingress NetworkPolicy selects, and read by the coverage rule
// (annotate-then-validate).
const coverageAnnotation = "netpol.confighub.com/ingress-coverage"

// Guardrail CEL expressions, evaluated per resource (r aliases the resource).
const (
	// Every ingress rule must name its sources — an empty `from` admits all.
	celNoAllowAllIngress = "r.kind != 'NetworkPolicy' || !has(r.spec.ingress) || r.spec.ingress.all(rule, has(rule.from) && size(rule.from) > 0)"
	// No egress rule may permit the whole internet via 0.0.0.0/0.
	celNoWideCidrEgress = "r.kind != 'NetworkPolicy' || !has(r.spec.egress) || r.spec.egress.all(rule, !has(rule.to) || rule.to.all(peer, !has(peer.ipBlock) || peer.ipBlock.cidr != '0.0.0.0/0'))"
	// Annotate-then-validate: warn while a coverage annotation is present. The
	// null check is required: a resource written with a bare `annotations:` key
	// has the key present with a null value, so has() is true but `in` against
	// null raises "no such overload" and the rule reports a spurious failure.
	celNoCoverageFinding = "!has(r.metadata.annotations) || r.metadata.annotations == null || !('" + coverageAnnotation + "' in r.metadata.annotations)"
)

// celNoMetadataEgress fails an egress ipBlock whose CIDR contains the cloud
// metadata IP unless an except names it. The CIDRs are listed rather than
// computed, since CEL has no CIDR arithmetic; metadataExcepts are the excepts
// that cover the IP, so an except as wide as the block it is in counts.
var celNoMetadataEgress = fmt.Sprintf("r.kind != 'NetworkPolicy' || !has(r.spec.egress) || r.spec.egress.all(rule, !has(rule.to) || "+
	"rule.to.all(peer, !has(peer.ipBlock) || !(peer.ipBlock.cidr in [%s]) || "+
	"(has(peer.ipBlock.except) && peer.ipBlock.except.exists(e, e in [%s, '%s/32']))))",
	metadataCIDRs(), metadataCIDRs(), metadataIP)

var Pack = guardrails.Pack{
	Group: "netpol",
	Rules: []guardrails.Rule{
		{Slug: "netpol-no-allow-all-ingress", Description: "Warns on a NetworkPolicy ingress rule with an empty `from` (admits all sources). Fix: enumerate the allowed sources.", Expression: celNoAllowAllIngress},
		{Slug: "netpol-no-wide-cidr-egress", Description: "Warns on an egress rule permitting 0.0.0.0/0. Fix: restrict the CIDR, or except the cloud-metadata IP with the netpol-except-metadata profile.", Expression: celNoWideCidrEgress},
		{Slug: "netpol-no-metadata-egress", Description: "Warns on an egress ipBlock that reaches the cloud metadata IP 169.254.169.254 without excepting it. Fix: the netpol-except-metadata profile.", Expression: celNoMetadataEgress},
		{Slug: "netpol-ingress-coverage", Description: "Warns while a netpol.confighub.com/ingress-coverage annotation is present (set by `cub manage netpol guardrails annotate`): a workload no ingress NetworkPolicy selects. Fix: `cub manage netpol default-deny <namespace>` and an allow policy, then re-run annotate.", Expression: celNoCoverageFinding},
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
  netpol-no-wide-cidr-egress   no egress to 0.0.0.0/0
  netpol-no-metadata-egress    no egress to the cloud metadata IP
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
