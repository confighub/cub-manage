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
