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

// The checks' failures, evaluated on each resource of the types their rules
// select (r is the resource).

var roleTypes = []string{"rbac.authorization.k8s.io/v1/Role", "rbac.authorization.k8s.io/v1/ClusterRole"}

const roleWithRules = "has(r.rules)"

// ruleFailures fails the field of each rule of a Role whose values include one
// of bad, a CEL list, with detail.
func ruleFailures(field, bad, detail string) string {
	return "lists.range(size(r.rules)).filter(i, has(r.rules[i]." + field + ") && r.rules[i]." + field + ".exists(v, v in " + bad + "))" +
		".map(i, " + celrule.Failure(celrule.Indexed("rules", "i", field), celrule.Some("r.rules[i]."+field), detail) + ")"
}

var (
	// Each wildcard is reported at its field, so a rule with two is two failures.
	celNoWildcards = celrule.When(roleWithRules,
		ruleFailures("verbs", "['*']", "'rule ' + string(i) + ' allows every verb'")+" + "+
			ruleFailures("resources", "['*']", "'rule ' + string(i) + ' covers every resource'")+" + "+
			ruleFailures("apiGroups", "['*']", "'rule ' + string(i) + ' covers every API group'"))

	celNoEscalation = celrule.When(roleWithRules,
		ruleFailures("verbs", "['escalate', 'bind', 'impersonate']", "'rule ' + string(i) + ' grants escalate, bind or impersonate'"))

	celNoClusterAdmin = celrule.When("r.roleRef.name == 'cluster-admin'",
		"["+celrule.Failure(celrule.Lit("roleRef.name"), celrule.Some("r.roleRef.name"), celrule.Lit("the binding grants cluster-admin"))+"]")
)

var bindingTypes = []string{"rbac.authorization.k8s.io/v1/ClusterRoleBinding", "rbac.authorization.k8s.io/v1/RoleBinding"}

// subjectFailures fails each subject s of a binding for which bad holds.
func subjectFailures(bad, detail string) string {
	return "(has(r.subjects) ? lists.range(size(r.subjects)).filter(i, [r.subjects[i]].exists(s, " + bad + "))" +
		".map(i, " + celrule.Failure(celrule.Indexed("subjects", "i", "name"), celrule.Some("r.subjects[i].name"), detail) + ") : [])"
}

// grantFailures fails each rule of a role granting one of verbs, or every verb, on one of
// resources.
func grantFailures(resources, verbs, detail string) string {
	return celrule.When(roleWithRules, "lists.range(size(r.rules)).filter(i, has(r.rules[i].resources) && r.rules[i].resources.exists(x, x in "+resources+") && "+
		"has(r.rules[i].verbs) && r.rules[i].verbs.exists(v, v == '*' || v in "+verbs+"))"+
		".map(i, "+celrule.Failure(celrule.Indexed("rules", "i", "resources"), celrule.Some("r.rules[i].resources"), detail)+")")
}

var (
	celNoAnonymous = subjectFailures("s.name in ['system:anonymous', 'system:unauthenticated']",
		"'the binding grants its role to ' + r.subjects[i].name + ', anyone who can reach the API server'")
	celNoSystemMasters = subjectFailures("s.kind == 'Group' && s.name == 'system:masters'",
		"'the binding names system:masters, whose members bypass authorization altogether'")
	celDefaultSAUnbound = subjectFailures("s.kind == 'ServiceAccount' && s.name == 'default'",
		"'the binding grants its role to a default ServiceAccount, and so to every pod that names none'")
	celNodesProxy = grantFailures("['nodes/proxy', 'nodes/*']", "['get', 'create', 'update', 'patch', 'delete', 'list', 'watch']",
		"'rule ' + string(i) + ' grants nodes/proxy, which reaches the kubelet API and runs commands in any pod on the node'")
	celCSRApproval = grantFailures("['certificatesigningrequests/approval']", "['update', 'patch']",
		"'rule ' + string(i) + ' can approve certificate signing requests, and so mint credentials'") + " + " +
		grantFailures("['signers']", "['approve']", "'rule ' + string(i) + ' can approve certificates for a signer'")
	celEndpointsEdit = grantFailures("['endpoints', 'endpointslices']", "['create', 'update', 'patch', 'delete', 'deletecollection']",
		"'rule ' + string(i) + ' can write Endpoints, and so send traffic for a Service anywhere'")
	celSecretsAccess = grantFailures("['secrets']", "['get', 'list', 'watch']",
		"'rule ' + string(i) + ' can read Secrets'")
	celPodCreate = grantFailures("['pods', 'deployments', 'replicasets', 'statefulsets', 'daemonsets', 'jobs', 'cronjobs', 'replicationcontrollers']", "['create']",
		"'rule ' + string(i) + ' can create pods, or workloads that create them, and so run as any ServiceAccount of the namespace'")
)

func rbacCheck(control, name, priority, displayName, description, failures string, types []string, corresponds ...string) guardrails.Check {
	return guardrails.Check{Control: control, Name: name, Rule: name, DisplayName: displayName, Description: description,
		Engine: guardrails.EngineCEL, Priority: priority, ResourceTypes: types, Failures: failures, Corresponds: corresponds}
}

func optional(c guardrails.Check) guardrails.Check {
	c.Optional = true
	return c
}

var Pack = guardrails.Pack{
	Group: "rbac",
	Checks: []guardrails.Check{
		{Control: "CH-0401", Name: "rbac-no-wildcards", Rule: "rbac-no-wildcards", DisplayName: "No wildcards in RBAC roles",
			Description: "Warns on Roles/ClusterRoles with wildcard verbs, resources, or apiGroups. Fix: enumerate the specific verbs/resources the role needs.",
			Engine:      guardrails.EngineCEL, Priority: guardrails.PriorityHigh, ResourceTypes: roleTypes, Failures: celNoWildcards,
			Corresponds: []string{"KL:wildcard-in-rules", "KY:restrict-wildcard-resources", "KY:restrict-wildcard-verbs", "CIS:5.1.3", "KS:C-0187"}},
		{Control: "CH-0402", Name: "rbac-no-privilege-escalation", Rule: "rbac-no-privilege-escalation", DisplayName: "No privilege escalation in RBAC roles",
			Description: "Warns on Roles/ClusterRoles granting escalate, bind, or impersonate. Fix: remove these verbs; they allow privilege escalation.",
			Engine:      guardrails.EngineCEL, Priority: guardrails.PriorityHigh, ResourceTypes: roleTypes, Failures: celNoEscalation,
			Corresponds: []string{"KY:restrict-escalation-verbs-roles", "CIS:5.1.8", "KS:C-0191"}},
		{Control: "CH-0403", Name: "rbac-no-cluster-admin-binding", Rule: "rbac-no-cluster-admin-binding", DisplayName: "No bindings to cluster-admin",
			Description: "Warns on a ClusterRoleBinding or RoleBinding to cluster-admin. Fix: bind a scoped role instead.",
			Engine:      guardrails.EngineCEL, Priority: guardrails.PriorityHigh,
			ResourceTypes: bindingTypes, Failures: celNoClusterAdmin,
			Corresponds: []string{"KL:cluster-admin-role-binding", "KY:restrict-binding-clusteradmin", "CIS:5.1.1", "KS:C-0185"}},
		rbacCheck("CH-0404", "rbac-no-anonymous-subjects", guardrails.PriorityHigh, "No bindings to anonymous users",
			"Warns on a binding to system:anonymous or system:unauthenticated, which grants its role to anyone who can reach the API server. Fix: bind the identities that need the role.",
			celNoAnonymous, bindingTypes, "KS:C-0262", "GK:disallowanonymous"),
		rbacCheck("CH-0405", "rbac-no-system-masters", guardrails.PriorityHigh, "No bindings to the system masters group",
			"Warns on a binding naming the system:masters group, whose members bypass authorization, so the binding is either pointless or a mistake. Fix: remove the subject.",
			celNoSystemMasters, bindingTypes, "KY:restrict-binding-system-groups", "KS:C-0246"),
		rbacCheck("CH-0406", "rbac-nodes-proxy", guardrails.PriorityHigh, "No node proxy access",
			"Warns on a Role or ClusterRole rule granting nodes/proxy, which reaches the kubelet API and runs commands in any pod on the node. Fix: remove it; a monitoring agent that needs node metrics reads nodes/metrics.",
			celNodesProxy, roleTypes, "KY:restrict-clusterrole-nodesproxy", "KS:C-0279"),
		rbacCheck("CH-0407", "rbac-csr-approval", guardrails.PriorityHigh, "No certificate approval",
			"Warns on a rule that can update certificatesigningrequests/approval or approve for signers, which lets its holder mint credentials for any identity. Fix: leave approval to the cluster's own controllers.",
			celCSRApproval, roleTypes, "KS:C-0280"),
		rbacCheck("CH-0408", "rbac-endpoints-edit", guardrails.PriorityMedium, "No writing Endpoints",
			"Warns on a rule that can create, update, patch or delete Endpoints or EndpointSlices, which can send a Service's traffic to any address (CVE-2021-25740). Fix: leave them to the endpoints controllers.",
			celEndpointsEdit, roleTypes, "GK:block-endpoint-edit-default-role", "KY:restrict-edit-for-endpoints"),
		rbacCheck("CH-0409", "rbac-secrets-access", guardrails.PriorityMedium, "No reading Secrets",
			"Warns on a rule that can get, list or watch Secrets. A controller that manages Secrets needs it, which is what an exception is for. Fix: narrow the rule to the resourceNames the role needs, or except the role.",
			celSecretsAccess, roleTypes, "KL:access-to-secrets", "KY:restrict-secret-role-verbs", "CIS:5.1.2", "KS:C-0186"),
		optional(rbacCheck("CH-0410", "rbac-pod-create", guardrails.PriorityLow, "No creating pods",
			"Warns on a rule that can create pods or the workloads that create them, which lets its holder run as any ServiceAccount of the namespace. Optional: deployment controllers need it. Fix: except the roles that need it.",
			celPodCreate, roleTypes, "KL:access-to-create-pods", "CIS:5.1.4", "KS:C-0188")),
		rbacCheck("CH-0412", "rbac-default-service-account-unbound", guardrails.PriorityMedium, "Default ServiceAccounts are not bound",
			"Warns on a binding granting a role to a namespace's default ServiceAccount, which every pod naming no ServiceAccount runs as. Fix: create a ServiceAccount for the workload and bind that.",
			celDefaultSAUnbound, bindingTypes, "CIS:5.1.5", "KS:C-0189"),
	},
}

func preflight(ctx context.Context) (*cubapi.Client, error) { return cub.Preflight(ctx) }

func newGuardrailsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "guardrails",
		Short: "Install and inspect the RBAC guardrail policy pack",
		Long: `guardrails manages a small pack of RBAC validation policies, defined once in a
policy Space and enforced in each Space that opts into the group:

  rbac-no-wildcards                     no wildcard verbs/resources/apiGroups
  rbac-no-privilege-escalation          no escalate/bind/impersonate verbs
  rbac-no-cluster-admin-binding         no bindings to cluster-admin
  rbac-no-anonymous-subjects            no bindings to system:anonymous or
                                        system:unauthenticated
  rbac-no-system-masters                no bindings to system:masters
  rbac-nodes-proxy                      no nodes/proxy access
  rbac-csr-approval                     no approving certificate signing requests
  rbac-endpoints-edit                   no writing Endpoints or EndpointSlices
  rbac-secrets-access                   no reading Secrets
  rbac-default-service-account-unbound  no roles bound to a default ServiceAccount
  rbac-pod-create                       no creating pods (optional: --check)

Triggers are created with Warn=true (advisory ValidationWarnings, never blocking), so
installing on an existing fleet never blocks anyone. Promote one to blocking
later with: cub trigger update <slug> --space <policy-space> --unwarn

Except one resource from a rule with 'cub manage guardrails exception add';
'cub manage guardrails exception list' shows what is excepted, and why.`,
	}
	cmd.AddCommand(Pack.InstallCmd(preflight))
	return cmd
}
