// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

// This tool's guardrail pack. The mechanism -- policy Space, Triggers, the group
// selection on each in-scope Space, and reporting the Units a Trigger marked
// -- is managerkit/guardrails'. What is this tool's is the rules, and which
// Units the annotate command has something to say about.

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/confighub/sdk/core/cubapi"
	"github.com/confighub/sdk/managerkit/guardrails"

	"github.com/confighub/cub-manage/internal/celrule"
	"github.com/confighub/cub-manage/internal/cub"
	"github.com/confighub/cub-manage/internal/nsmanager"
	"github.com/confighub/cub-manage/internal/nsmanager/snapshot"
)

// envelopeAnnotation is written by `guardrails annotate` onto the Namespace Unit
// of each incomplete namespace, naming the members it is missing, and read by the
// envelope-coverage rule (annotate-then-validate).
const envelopeAnnotation = "namespace.confighub.com/envelope-coverage"

const podSecurityEnforceLabel = "pod-security.kubernetes.io/enforce"

// Guardrail CEL expressions, evaluated per resource (r aliases the resource).
var (
	// A Namespace must carry the pod-security enforce label.
	celHasPodSecurity = celrule.Result(celrule.When(
		"r.kind == 'Namespace' && !(has(r.metadata.labels) && '"+podSecurityEnforceLabel+"' in r.metadata.labels)",
		"["+celrule.Failure(celrule.Lit(celrule.MapKeyPath("metadata.labels", podSecurityEnforceLabel)), celrule.None,
			celrule.Lit("the Namespace has no "+podSecurityEnforceLabel+" label"))+"]"))
	// Annotate-then-validate: warn while an envelope finding annotation is present.
	celNoEnvelopeFinding = celrule.AnnotationFinding(envelopeAnnotation,
		"the namespace is missing members of its envelope, as guardrails annotate found")
)

var Pack = guardrails.Pack{
	Group: "namespace",
	Rules: []guardrails.Rule{
		{Slug: "namespace-has-pod-security", DisplayName: "Namespaces set a Pod Security level", Description: "Warns on a Namespace with no pod-security.kubernetes.io/enforce label. Fix: `cub manage namespace apply-envelope --namespace <ns>`.", Expression: celHasPodSecurity},
		{Slug: "namespace-pod-security-standard", DisplayName: "Workloads meet their Pod Security level", Description: "Warns on a workload that does not meet the Pod Security Standard level in its own pod-security.kubernetes.io/enforce label, which `cub manage namespace apply-envelope` copies from its namespace -- so admission would reject its pods. Unlabeled workloads are not checked. Fix: `cub manage workload harden`, or what `cub manage namespace pod-security` lists.", Function: VetPodSecurityFunction},
		{Slug: "namespace-envelope-coverage", DisplayName: "Namespaces have their whole envelope", Description: "Warns while a namespace.confighub.com/envelope-coverage annotation is present (set by `cub manage namespace guardrails annotate`). Fix: close the envelope gap, then re-run annotate.", Expression: celNoEnvelopeFinding},
	},
}

func preflight(ctx context.Context) (*cubapi.Client, error) { return cub.Preflight(ctx) }

// installPreflight also refuses a server whose vet-pod-security-standard needs a
// level: the namespace-pod-security-standard rule passes none, so it would fail
// on every Unit there.
func installPreflight(ctx context.Context) (*cubapi.Client, error) {
	c, err := preflight(ctx)
	if err != nil {
		return nil, err
	}
	ok, err := ChecksLabeledLevels(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("check the server's %s: %w", VetPodSecurityFunction, err)
	}
	if !ok {
		return nil, fmt.Errorf("this ConfigHub server's %s needs a level, so the namespace-pod-security-standard rule would fail on every Unit; it needs a server whose %s checks each workload at its own %s label",
			VetPodSecurityFunction, VetPodSecurityFunction, nsmanager.PodSecurityEnforceLabel)
	}
	return c, nil
}

func newGuardrailsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "guardrails",
		Short: "Install, inspect, and feed the namespace-envelope guardrail policy pack",
		Long: `guardrails manages a pack of namespace-governance validation policies, defined
once in a policy Space and enforced in each Space that opts into the group:

  namespace-has-pod-security       a Namespace must carry a pod-security enforce label
  namespace-pod-security-standard  a workload must meet the level in its own enforce
                                   label, which apply-envelope copies from its namespace
  namespace-envelope-coverage      warns while a namespace is annotated as missing part of
                                   its envelope (§ annotate)

Triggers are created with Warn=true (advisory ValidationWarnings, never blocking).
Promote one to blocking later with:
  cub trigger update <slug> --space <policy-space> --unwarn

The envelope-coverage rule realizes the annotate-then-validate model: the manager
can't set ValidationWarnings directly (only a failed Trigger can), so 'guardrails
annotate' writes a finding annotation onto each incomplete namespace's Namespace
Unit and this Trigger turns it into a warning.

The namespace-name invariant (metadata.namespace == normalizeName(Component)) is
enforced separately, by a cluster-selected mutating set-namespace Trigger — the
promotable active-correction option, wired outside this advisory pack.

Except one resource from a rule with 'cub manage guardrails exception add';
'cub manage guardrails exception list' shows what is excepted, and why.`,
	}
	cmd.AddCommand(Pack.InstallCmd(installPreflight), Pack.AnnotateCmd(preflight, annotateSpec))
	return cmd
}

// annotateSpec is the namespace half of `guardrails annotate`: which namespaces
// have an incomplete envelope, and what to write on their Namespace Unit. The
// rest of the command is managerkit/guardrails'.
var annotateSpec = guardrails.AnnotateSpec{
	Key:   envelopeAnnotation,
	Noun:  "Namespace Unit",
	Short: "Annotate the Namespace Unit of each incomplete namespace with what it is missing",
	Long: `annotate runs the envelope analysis and writes a namespace.confighub.com/
envelope-coverage annotation (value = the missing members) onto the v1/Namespace
Unit of each incomplete namespace. Paired with the namespace-envelope-coverage rule from
'guardrails install', this turns a set-aware finding into an advisory
ValidationWarning — the manager cannot set warnings directly, only a failed rule can.

Only namespaces that have a Namespace Unit are annotated (there is nowhere else
to put the annotation). Re-run after fixing config. Dry run unless --commit
--change-desc.`,
	Targets: func(ctx context.Context, c *cubapi.Client, where, cluster string) ([]guardrails.Target, error) {
		snap, err := snapshot.Load(ctx, c, where)
		if err != nil {
			return nil, err
		}
		var out []guardrails.Target
		for _, cl := range snap.Clusters {
			if cluster != "" && cl.Cluster != cluster {
				continue
			}
			for _, e := range nsmanager.AnalyzeCluster(cl) {
				if e.Complete || e.UnitSlug == "" || e.SpaceID == "" {
					continue // complete, or no Namespace Unit to annotate
				}
				out = append(out, guardrails.Target{
					SpaceID: e.SpaceID, UnitSlug: e.UnitSlug,
					Value:   strings.Join(e.Missing, ","),
					Cluster: e.Cluster, Namespace: e.Namespace,
				})
			}
		}
		return out, nil
	},
}
