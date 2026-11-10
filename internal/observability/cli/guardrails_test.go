// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"testing"

	"github.com/confighub/cub-manage/internal/celrule/celruletest"
)

func TestServiceMonitorCoverageFinding(t *testing.T) {
	uncovered := "apiVersion: v1\nkind: Service\nmetadata:\n  name: api\n  namespace: shop\n  annotations:\n    observability.confighub.com/servicemonitor-coverage: uncovered\n"
	result := celruletest.Eval(t, celNoCoverageFinding, uncovered)
	celruletest.Expect(t, result, "metadata.annotations.observability~1confighub~1com/servicemonitor-coverage")
	celruletest.ExpectResource(t, result, "shop/api", "v1/Service")
	celruletest.Expect(t, celruletest.Eval(t, celNoCoverageFinding, "apiVersion: v1\nkind: Service\nmetadata:\n  name: api\n"))
}

func TestUnrelatedResourcesPass(t *testing.T) {
	celruletest.UnrelatedResourcesPass(t, Pack.Rules)
}
