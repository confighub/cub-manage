// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"testing"

	"github.com/confighub/cub-manage/internal/celrule/kubescapetest"
)

func TestKubescapeChecksPassTheirUpstreamTests(t *testing.T) {
	kubescapetest.RunUpstreamTests(t, "../../../third_party/kubescape/cel-admission-library", kubescapeChecks)
}
