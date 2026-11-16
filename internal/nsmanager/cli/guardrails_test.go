// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"testing"

	"github.com/confighub/cub-manage/internal/celrule/celruletest"
)

func TestNamespaceRules(t *testing.T) {
	unlabeled := "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: shop\n  labels:\n    team: web\n"
	result := celruletest.EvalCheck(t, celruletest.Check(t, Pack, "namespace-has-pod-security"), unlabeled)
	celruletest.Expect(t, result, "metadata.labels.pod-security~1kubernetes~1io/enforce")
	celruletest.ExpectResource(t, result, "/shop", "v1/Namespace")

	labeled := "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: shop\n  labels:\n    pod-security.kubernetes.io/enforce: baseline\n"
	celruletest.Expect(t, celruletest.EvalCheck(t, celruletest.Check(t, Pack, "namespace-has-pod-security"), labeled))

	notPrivileged := celruletest.Check(t, Pack, "namespace-not-privileged")
	privileged := "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: kube-system\n  labels:\n    pod-security.kubernetes.io/enforce: privileged\n"
	celruletest.Expect(t, celruletest.EvalCheck(t, notPrivileged, privileged), "metadata.labels.pod-security~1kubernetes~1io/enforce")
	celruletest.Expect(t, celruletest.EvalCheck(t, notPrivileged, labeled))
	celruletest.Expect(t, celruletest.EvalCheck(t, notPrivileged, unlabeled))
	celruletest.Expect(t, celruletest.EvalCheck(t, notPrivileged, "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: bare\n  labels:\n"))
}

func TestUnrelatedResourcesPass(t *testing.T) {
	celruletest.UnrelatedResourcesPass(t, Pack)
}
