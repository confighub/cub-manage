// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"testing"

	"github.com/confighub/cub-manage/internal/celrule/celruletest"
)

func TestNamespaceRequired(t *testing.T) {
	result := celruletest.Eval(t, celNamespaceRequired, "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\n")
	celruletest.Expect(t, result, "metadata.namespace")
	celruletest.ExpectResource(t, result, "/web", "apps/v1/Deployment")
	if len(result.FailedAttributes) == 1 && result.FailedAttributes[0].Value != nil {
		t.Errorf("value = %v, want none for a missing namespace", result.FailedAttributes[0].Value)
	}
	if len(result.Details) == 1 && result.Details[0] != "the Deployment sets no metadata.namespace" {
		t.Errorf("detail = %q", result.Details[0])
	}

	for name, doc := range map[string]string{
		"namespaced":     "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\n  namespace: shop\n",
		"cluster-scoped": "apiVersion: rbac.authorization.k8s.io/v1\nkind: ClusterRole\nmetadata:\n  name: reader\n",
		"namespace":      "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: shop\n",
	} {
		t.Run(name, func(t *testing.T) {
			celruletest.Expect(t, celruletest.Eval(t, celNamespaceRequired, doc))
		})
	}
}

// Each rule is a validating function or a CEL expression, never both or neither.
func TestBasicPackRules(t *testing.T) {
	seen := map[string]bool{}
	for _, rule := range basicPack.Rules {
		if seen[rule.Slug] {
			t.Errorf("rule %s appears twice", rule.Slug)
		}
		seen[rule.Slug] = true
		if (rule.Function == "") == (rule.Expression == "") {
			t.Errorf("rule %s sets both or neither of Function and Expression", rule.Slug)
		}
		if rule.DisplayName == "" || rule.Description == "" {
			t.Errorf("rule %s has no DisplayName or Description", rule.Slug)
		}
	}
}
