// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

// This tool's guardrail pack. The mechanism -- policy Space, Triggers, the group
// selection on each in-scope Space, and reporting the Units a Trigger marked
// -- is managerkit/guardrails'. What is this tool's is the rules.

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/confighub/sdk/core/cubapi"
	"github.com/confighub/sdk/managerkit/guardrails"

	"github.com/confighub/cub-manage/internal/celrule"
	"github.com/confighub/cub-manage/internal/cub"
	"github.com/confighub/cub-manage/internal/nsmanager"
)

const podSecurityEnforceLabel = "pod-security.kubernetes.io/enforce"

// Guardrail CEL expressions, evaluated per resource (r aliases the resource).
var (
	// A Namespace must carry the pod-security enforce label.
	celHasPodSecurity = celrule.Result(celrule.When(
		"r.kind == 'Namespace' && !(has(r.metadata.labels) && '"+podSecurityEnforceLabel+"' in r.metadata.labels)",
		"["+celrule.Failure(celrule.Lit(celrule.MapKeyPath("metadata.labels", podSecurityEnforceLabel)), celrule.None,
			celrule.Lit("the Namespace has no "+podSecurityEnforceLabel+" label"))+"]"))
)

var Pack = guardrails.Pack{
	Group: "namespace",
	Rules: []guardrails.Rule{
		{Slug: "namespace-has-pod-security", DisplayName: "Namespaces set a Pod Security level", Description: "Warns on a Namespace with no pod-security.kubernetes.io/enforce label. Fix: `cub manage namespace pod-security set --namespace <ns>`.", Expression: celHasPodSecurity},
		{Slug: "namespace-pod-security-standard", DisplayName: "Workloads meet their Pod Security level", Description: "Warns on a workload that does not meet the Pod Security Standard level in its own pod-security.kubernetes.io/enforce label, which `cub manage namespace pod-security set` copies from its namespace -- so admission would reject its pods. Unlabeled workloads are not checked. Fix: `cub manage workload harden`, or what `cub manage namespace pod-security` lists.", Function: VetPodSecurityFunction},
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
		Short: "Install the namespace guardrail pack: Pod Security Standard labels and levels",
		Long: `guardrails manages a pack of namespace-governance validation policies, defined
once in a policy Space and enforced in each Space that opts into the group:

  namespace-has-pod-security       a Namespace must carry a pod-security enforce label
  namespace-pod-security-standard  a workload must meet the level in its own enforce
                                   label, which 'pod-security set' copies from its
                                   namespace

Triggers are created with Warn=true (advisory ValidationWarnings, never blocking).
Promote one to blocking once its warnings are worked down with:
  cub manage guardrails update <rule> --enforce --commit

The namespace-name invariant (metadata.namespace == normalizeName(Component)) is
enforced separately, by a cluster-selected mutating set-namespace Trigger — the
promotable active-correction option, wired outside this advisory pack.

Except one resource from a rule with 'cub manage guardrails exception add';
'cub manage guardrails exception list' shows what is excepted, and why.`,
	}
	cmd.AddCommand(Pack.InstallCmd(installPreflight))
	return cmd
}
