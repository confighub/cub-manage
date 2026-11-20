// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"testing"

	"github.com/confighub/cub-manage/internal/celrule/celruletest"
)

func TestNamespaceHasQuotaAndLimitRange(t *testing.T) {
	onCluster := map[string]any{"TargetID": "4d6b2a1e-0000-4000-8000-000000000001"}
	shop := "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: shop\n"
	for _, tc := range []struct{ check, key, kind string }{
		{"namespace-resource-quota", "quotas", "ResourceQuota"},
		{"namespace-limit-range", "limitranges", "LimitRange"},
	} {
		check := celruletest.Check(t, Pack, tc.check)
		if !check.Optional {
			t.Errorf("%s is installed unless asked for", tc.check)
		}
		in := func(namespace string) celruletest.Other {
			return celruletest.Other{tc.key: {"apiVersion: v1\nkind: " + tc.kind + "\nmetadata:\n  name: x\n  namespace: " + namespace + "\n"}}
		}
		celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, in("shop"), check, shop))
		result := celruletest.EvalCheckOther(t, onCluster, nil, in("billing"), check, shop)
		celruletest.Expect(t, result, "metadata.name")
		celruletest.ExpectResource(t, result, "/shop", "v1/Namespace")
		celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, nil, check, "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: kube-system\n"))
		celruletest.Expect(t, celruletest.EvalCheckOther(t, map[string]any{}, nil, nil, check, shop))
	}
}
