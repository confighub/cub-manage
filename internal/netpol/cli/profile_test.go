// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"strings"
	"testing"
)

// The metadata except goes only on blocks that contain the address, so the list
// has to hold each enclosing CIDR as a policy writes it.
func TestMetadataCIDRs(t *testing.T) {
	got := metadataCIDRs()
	for _, want := range []string{`"0.0.0.0/0"`, `"128.0.0.0/1"`, `"169.254.0.0/16"`, `"169.254.169.254/31"`} {
		if !strings.Contains(got, want) {
			t.Errorf("metadataCIDRs() lacks %s: %s", want, got)
		}
	}
	if strings.Contains(got, `/32"`) {
		t.Errorf("metadataCIDRs() includes the /32: %s", got)
	}
}
