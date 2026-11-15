// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import "testing"

// The basic pack installs, and each of its checks is described.
func TestBasicPackChecks(t *testing.T) {
	if err := basicPack.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, check := range basicPack.Checks {
		if check.DisplayName == "" || check.Description == "" {
			t.Errorf("check %s has no DisplayName or Description", check.Name)
		}
	}
}

// Control numbers and check names are unique across the packs, since a finding names its check
// and a framework report names its control.
func TestPacksDoNotShareChecks(t *testing.T) {
	names, controls := map[string]string{}, map[string]string{}
	for _, pack := range allPacks {
		if err := pack.Validate(); err != nil {
			t.Errorf("the %s pack: %v", pack.Group, err)
		}
		for _, check := range pack.Checks {
			if other, ok := names[check.Name]; ok {
				t.Errorf("check %s is in the %s and %s packs", check.Name, other, pack.Group)
			}
			if other, ok := controls[check.Control]; ok {
				t.Errorf("control %s is in the %s and %s packs", check.Control, other, pack.Group)
			}
			names[check.Name], controls[check.Control] = pack.Group, pack.Group
		}
	}
}
