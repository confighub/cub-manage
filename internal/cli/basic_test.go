// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import "testing"

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
