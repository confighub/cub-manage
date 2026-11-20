// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"strings"
	"testing"

	"github.com/confighub/cub-manage/internal/celrule/celruletest"
)

// A binding's role is a Role in its namespace or a ClusterRole on its cluster, or one of the
// ClusterRoles Kubernetes ships.
func TestBindingRoleExists(t *testing.T) {
	check := celruletest.Check(t, Pack, "rbac-binding-role-exists")
	onCluster := map[string]any{"TargetID": "4d6b2a1e-0000-4000-8000-000000000001"}
	binding := func(kind, roleKind, role string) string {
		return "apiVersion: rbac.authorization.k8s.io/v1\nkind: " + kind + "\nmetadata:\n  name: b\n  namespace: shop\n" +
			"roleRef: {apiGroup: rbac.authorization.k8s.io, kind: " + roleKind + ", name: " + role + "}\n" +
			"subjects:\n  - {kind: ServiceAccount, name: web, namespace: shop}\n"
	}
	role := func(kind, namespace, name string) string {
		return "apiVersion: rbac.authorization.k8s.io/v1\nkind: " + kind + "\nmetadata:\n  name: " + name + "\n  namespace: " + namespace + "\n"
	}
	roles := celruletest.Other{"roles": {role("Role", "shop", "reader"), role("ClusterRole", "", "auditor")}}

	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, roles, check, binding("RoleBinding", "Role", "reader")))
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, roles, check, binding("RoleBinding", "ClusterRole", "auditor")))
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, roles, check, binding("ClusterRoleBinding", "ClusterRole", "view")))
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, roles, check, binding("ClusterRoleBinding", "ClusterRole", "system:auth-delegator")))

	result := celruletest.EvalCheckOther(t, onCluster, nil, roles, check, binding("RoleBinding", "Role", "writer"))
	celruletest.Expect(t, result, "roleRef.name")
	celruletest.ExpectResource(t, result, "shop/b", "rbac.authorization.k8s.io/v1/RoleBinding")
	// A Role in another namespace, or a ClusterRole named as a Role, is not the one named.
	billing := celruletest.Other{"roles": {role("Role", "billing", "reader"), role("ClusterRole", "", "reader")}}
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, billing, check, binding("RoleBinding", "Role", "reader")), "roleRef.name")
	celruletest.Expect(t, celruletest.EvalCheckOther(t, map[string]any{}, nil, nil, check, binding("RoleBinding", "Role", "writer")))

	// A Role the API server creates in kube-system is bound there, but not in another namespace.
	reader := binding("RoleBinding", "Role", "extension-apiserver-authentication-reader")
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, nil, check, strings.Replace(reader, "namespace: shop", "namespace: kube-system", 1)))
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, nil, check, reader), "roleRef.name")
	// A built-in ClusterRole's name as a Role is not built in.
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, nil, check, binding("RoleBinding", "Role", "admin")), "roleRef.name")
}
