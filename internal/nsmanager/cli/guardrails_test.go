// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"testing"

	"github.com/confighub/cub-manage/internal/celrule/celruletest"
)

func TestNamespaceRules(t *testing.T) {
	unlabeled := "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: shop\n  labels:\n    team: web\n"
	result := celruletest.Eval(t, celHasPodSecurity, unlabeled)
	celruletest.Expect(t, result, "metadata.labels.pod-security~1kubernetes~1io/enforce")
	celruletest.ExpectResource(t, result, "/shop", "v1/Namespace")

	labeled := "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: shop\n  labels:\n    pod-security.kubernetes.io/enforce: baseline\n"
	celruletest.Expect(t, celruletest.Eval(t, celHasPodSecurity, labeled))
	celruletest.Expect(t, celruletest.Eval(t, celHasPodSecurity, "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: settings\n"))
}

func TestUnrelatedResourcesPass(t *testing.T) {
	celruletest.UnrelatedResourcesPass(t, Pack.Rules)
}
