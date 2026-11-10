// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
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
	result := celruletest.Eval(t, celNoWildcards, clusterRole)
	celruletest.Expect(t, result, "rules.1.resources", "rules.1.apiGroups")
	celruletest.ExpectResource(t, result, "/operator", "rbac.authorization.k8s.io/v1/ClusterRole")

	result = celruletest.Eval(t, celNoEscalation, clusterRole)
	celruletest.Expect(t, result, "rules.1.verbs")

	binding := `apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: ops
roleRef:
  kind: ClusterRole
  name: cluster-admin
`
	result = celruletest.Eval(t, celNoClusterAdmin, binding)
	celruletest.Expect(t, result, "roleRef.name")
	celruletest.ExpectResource(t, result, "/ops", "rbac.authorization.k8s.io/v1/ClusterRoleBinding")
}

func TestRBACRulesPass(t *testing.T) {
	role := "apiVersion: rbac.authorization.k8s.io/v1\nkind: Role\nmetadata:\n  name: reader\n  namespace: shop\nrules:\n  - apiGroups: [\"\"]\n    resources: [\"pods\"]\n    verbs: [\"get\"]\n"
	for name, expression := range map[string]string{"no-wildcards": celNoWildcards, "no-escalation": celNoEscalation, "no-cluster-admin": celNoClusterAdmin} {
		t.Run(name, func(t *testing.T) {
			celruletest.Expect(t, celruletest.Eval(t, expression, role))
		})
	}
	// A Role with no rules grants nothing.
	celruletest.Expect(t, celruletest.Eval(t, celNoWildcards, "apiVersion: rbac.authorization.k8s.io/v1\nkind: Role\nmetadata:\n  name: empty\n"))
}

func TestUnrelatedResourcesPass(t *testing.T) {
	celruletest.UnrelatedResourcesPass(t, Pack.Rules)
}
