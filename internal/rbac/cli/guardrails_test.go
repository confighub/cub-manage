// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"strings"
	"testing"

	"github.com/confighub/cub-manage/internal/celrule/celruletest"
)

const clusterRole = `apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: operator
rules:
  - apiGroups: [""]
    resources: ["pods"]
    verbs: ["get", "list"]
  - apiGroups: ["*"]
    resources: ["*"]
    verbs: ["get", "impersonate"]
`

// The RBAC rules name each rule of the Role that fails, at the field to fix.
func TestRBACRulesNameTheFailingRule(t *testing.T) {
	result := celruletest.EvalCheck(t, celruletest.Check(t, Pack, "rbac-no-wildcards"), clusterRole)
	celruletest.Expect(t, result, "rules.1.resources", "rules.1.apiGroups")
	celruletest.ExpectResource(t, result, "/operator", "rbac.authorization.k8s.io/v1/ClusterRole")

	result = celruletest.EvalCheck(t, celruletest.Check(t, Pack, "rbac-no-privilege-escalation"), clusterRole)
	celruletest.Expect(t, result, "rules.1.verbs")

	binding := `apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: ops
roleRef:
  kind: ClusterRole
  name: cluster-admin
`
	result = celruletest.EvalCheck(t, celruletest.Check(t, Pack, "rbac-no-cluster-admin-binding"), binding)
	celruletest.Expect(t, result, "roleRef.name")
	celruletest.ExpectResource(t, result, "/ops", "rbac.authorization.k8s.io/v1/ClusterRoleBinding")
}

func TestRBACRulesPass(t *testing.T) {
	role := "apiVersion: rbac.authorization.k8s.io/v1\nkind: Role\nmetadata:\n  name: reader\n  namespace: shop\nrules:\n  - apiGroups: [\"\"]\n    resources: [\"pods\"]\n    verbs: [\"get\"]\n"
	for _, name := range []string{"rbac-no-wildcards", "rbac-no-privilege-escalation"} {
		t.Run(name, func(t *testing.T) {
			celruletest.Expect(t, celruletest.EvalCheck(t, celruletest.Check(t, Pack, name), role))
		})
	}
	binding := "apiVersion: rbac.authorization.k8s.io/v1\nkind: ClusterRoleBinding\nmetadata:\n  name: readers\nroleRef:\n  kind: ClusterRole\n  name: view\n"
	celruletest.Expect(t, celruletest.EvalCheck(t, celruletest.Check(t, Pack, "rbac-no-cluster-admin-binding"), binding))
	// A Role with no rules grants nothing.
	celruletest.Expect(t, celruletest.EvalCheck(t, celruletest.Check(t, Pack, "rbac-no-wildcards"), "apiVersion: rbac.authorization.k8s.io/v1\nkind: Role\nmetadata:\n  name: empty\n"))
}

func TestUnrelatedResourcesPass(t *testing.T) {
	celruletest.UnrelatedResourcesPass(t, Pack)
}

const riskyRole = `apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: risky
rules:
  - apiGroups: [""]
    resources: ["nodes/proxy"]
    verbs: ["get"]
  - apiGroups: ["certificates.k8s.io"]
    resources: ["certificatesigningrequests/approval"]
    verbs: ["update"]
  - apiGroups: ["certificates.k8s.io"]
    resources: ["signers"]
    verbs: ["approve"]
  - apiGroups: ["discovery.k8s.io"]
    resources: ["endpointslices"]
    verbs: ["patch"]
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["list"]
  - apiGroups: ["apps"]
    resources: ["deployments"]
    verbs: ["*"]
  - apiGroups: [""]
    resources: ["configmaps", "secrets"]
    verbs: ["create"]
`

const riskyBinding = `apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: risky
  namespace: shop
roleRef:
  kind: ClusterRole
  name: cluster-admin
subjects:
  - kind: Group
    name: system:authenticated
  - kind: User
    name: system:anonymous
  - kind: Group
    name: system:masters
  - kind: ServiceAccount
    name: default
    namespace: shop
`

// Each RBAC check fails the rules and subjects that violate it, by index.
func TestMoreRBACChecksFail(t *testing.T) {
	for check, paths := range map[string][]string{
		"rbac-nodes-proxy":                     {"rules.0.resources"},
		"rbac-csr-approval":                    {"rules.1.resources", "rules.2.resources"},
		"rbac-endpoints-edit":                  {"rules.3.resources"},
		"rbac-secrets-access":                  {"rules.4.resources"},
		"rbac-pod-create":                      {"rules.5.resources"},
		"rbac-no-anonymous-subjects":           {"subjects.1.name"},
		"rbac-no-system-masters":               {"subjects.2.name"},
		"rbac-default-service-account-unbound": {"subjects.3.name"},
		"rbac-no-cluster-admin-binding":        {"roleRef.name"},
	} {
		t.Run(check, func(t *testing.T) {
			resource := riskyRole
			if strings.HasPrefix(paths[0], "subjects") || strings.HasPrefix(paths[0], "roleRef") {
				resource = riskyBinding
			}
			celruletest.Expect(t, celruletest.EvalCheck(t, celruletest.Check(t, Pack, check), resource), paths...)
		})
	}
	// Reading Secrets it may only create, and a binding with no subjects, pass.
	celruletest.Expect(t, celruletest.EvalCheck(t, celruletest.Check(t, Pack, "rbac-secrets-access"),
		"apiVersion: rbac.authorization.k8s.io/v1\nkind: Role\nmetadata:\n  name: writer\nrules:\n  - resources: [secrets]\n    verbs: [create]\n"))
	celruletest.Expect(t, celruletest.EvalCheck(t, celruletest.Check(t, Pack, "rbac-no-anonymous-subjects"),
		"apiVersion: rbac.authorization.k8s.io/v1\nkind: ClusterRoleBinding\nmetadata:\n  name: none\nroleRef:\n  name: view\n"))
}
