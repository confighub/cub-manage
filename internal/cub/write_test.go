// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cub

import "testing"

func TestWhereResourceTypes(t *testing.T) {
	got := WhereResourceTypes("apps/v1/Deployment", "batch/v1/Job")
	want := `ConfigHub.ResourceType IN ('apps/v1/Deployment', 'batch/v1/Job')`
	if got != want {
		t.Errorf("got %s\nwant %s", got, want)
	}
}
