// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

// This tool's guardrail pack. The mechanism -- policy Space, Triggers, the group
// selection on each in-scope Space, and reporting the Units a Trigger marked
// -- is managerkit/guardrails'. What is this tool's is the rules.

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/confighub/sdk/core/cubapi"
	"github.com/confighub/sdk/managerkit/guardrails"

	"github.com/confighub/cub-manage/internal/celrule"
	"github.com/confighub/cub-manage/internal/cub"
)

// Guardrail CEL expressions, evaluated per resource (r aliases the resource).

const roleWithRules = "r.kind in ['Role', 'ClusterRole'] && has(r.rules)"

// ruleFailures fails the field of each rule of a Role whose values include one
// of bad, a CEL list, with detail.
func ruleFailures(field, bad, detail string) string {
	return "lists.range(size(r.rules)).filter(i, has(r.rules[i]." + field + ") && r.rules[i]." + field + ".exists(v, v in " + bad + "))" +
		".map(i, " + celrule.Failure(celrule.Indexed("rules", "i", field), celrule.Some("r.rules[i]."+field), detail) + ")"
}

var (
	// Each wildcard is reported at its field, so a rule with two is two failures.
	celNoWildcards = celrule.Result(celrule.When(roleWithRules,
		ruleFailures("verbs", "['*']", "'rule ' + string(i) + ' allows every verb'")+" + "+
			ruleFailures("resources", "['*']", "'rule ' + string(i) + ' covers every resource'")+" + "+
			ruleFailures("apiGroups", "['*']", "'rule ' + string(i) + ' covers every API group'")))

	celNoEscalation = celrule.Result(celrule.When(roleWithRules,
		ruleFailures("verbs", "['escalate', 'bind', 'impersonate']", "'rule ' + string(i) + ' grants escalate, bind or impersonate'")))

	celNoClusterAdmin = celrule.Result(celrule.When("r.kind == 'ClusterRoleBinding' && r.roleRef.name == 'cluster-admin'",
		"["+celrule.Failure(celrule.Lit("roleRef.name"), celrule.Some("r.roleRef.name"), celrule.Lit("the binding grants cluster-admin"))+"]"))
)

var Pack = guardrails.Pack{
	Group: "rbac",
	Rules: []guardrails.Rule{
		{Slug: "rbac-no-wildcards", DisplayName: "No wildcards in RBAC roles", Description: "Warns on Roles/ClusterRoles with wildcard verbs, resources, or apiGroups. Fix: enumerate the specific verbs/resources the role needs.", Expression: celNoWildcards},
		{Slug: "rbac-no-privilege-escalation", DisplayName: "No privilege escalation in RBAC roles", Description: "Warns on Roles/ClusterRoles granting escalate, bind, or impersonate. Fix: remove these verbs; they allow privilege escalation.", Expression: celNoEscalation},
		{Slug: "rbac-no-cluster-admin-binding", DisplayName: "No bindings to cluster-admin", Description: "Warns on ClusterRoleBindings to cluster-admin. Fix: bind a scoped role instead.", Expression: celNoClusterAdmin},
	},
}

func preflight(ctx context.Context) (*cubapi.Client, error) { return cub.Preflight(ctx) }

func newGuardrailsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "guardrails",
		Short: "Install and inspect the RBAC guardrail policy pack",
		Long: `guardrails manages a small pack of RBAC validation policies, defined once in a
policy Space and enforced in each Space that opts into the group:

  rbac-no-wildcards              no wildcard verbs/resources/apiGroups
  rbac-no-privilege-escalation   no escalate/bind/impersonate verbs
  rbac-no-cluster-admin-binding  no ClusterRoleBindings to cluster-admin

Triggers are created with Warn=true (advisory ValidationWarnings, never blocking), so
installing on an existing fleet never blocks anyone. Promote one to blocking
later with: cub trigger update <slug> --space <policy-space> --unwarn

Except one resource from a rule with 'cub manage guardrails exception add';
'cub manage guardrails exception list' shows what is excepted, and why.`,
	}
	cmd.AddCommand(Pack.InstallCmd(preflight))
	return cmd
}
