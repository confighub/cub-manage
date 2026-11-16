// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"slices"
	"testing"

	"github.com/confighub/sdk/managerkit/guardrails"

	"github.com/confighub/cub-manage/third_party/kubescape/regolibrary"
)

// Each framework loads with its controls, and the packs' checks cover the CIS controls about
// workloads, RBAC and network policy that they implement.
func TestFrameworksMapToChecks(t *testing.T) {
	frameworks := regolibrary.Frameworks()
	byName := map[string]guardrails.Framework{}
	for _, f := range frameworks {
		if len(f.Controls) == 0 {
			t.Errorf("framework %s has no controls", f.Name)
		}
		byName[f.Name] = f
	}
	cis, ok := byName["cis-v1.12"]
	if !ok {
		t.Fatal("no cis-v1.12")
	}
	for control, check := range map[string]string{
		"5.1.1":  "rbac-no-cluster-admin-binding",
		"5.1.3":  "rbac-no-wildcards",
		"5.2.2":  "privileged",
		"5.2.5":  "host-namespaces",
		"5.2.6":  "allow-privilege-escalation",
		"5.2.7":  "workload-runs-nonroot",
		"5.2.1":  "namespace-has-pod-security",
		"5.2.9":  "capabilities-restricted",
		"5.2.10": "windows-host-process",
		"5.2.11": "host-path-volumes",
		"5.2.12": "host-ports",
		"5.3.2":  "netpol-ingress-coverage",
	} {
		if checks := checksCovering(t, cis, control); !slices.Contains(checks, check) {
			t.Errorf("CIS %s is covered by %v, not %s", control, checks, check)
		}
	}
	if title := cis.Controls[controlIndex(cis, "5.2.8")].Title; title != "Minimize the admission of containers with the NET_RAW capability" {
		t.Errorf("5.2.8 title = %q", title)
	}
	// Controls follow the benchmark's numbering.
	i, j := controlIndex(cis, "5.2.9"), controlIndex(cis, "5.2.10")
	if i < 0 || j < 0 || i > j {
		t.Errorf("5.2.9 at %d, 5.2.10 at %d", i, j)
	}
}

func controlIndex(f guardrails.Framework, id string) int {
	return slices.IndexFunc(f.Controls, func(c guardrails.FrameworkControl) bool { return c.ID == id })
}

// checksCovering are the checks of every pack corresponding to the framework's control id.
func checksCovering(t *testing.T, f guardrails.Framework, id string) []string {
	t.Helper()
	i := controlIndex(f, id)
	if i < 0 {
		t.Fatalf("%s has no control %s", f.Name, id)
	}
	var checks []string
	for _, p := range allPacks {
		for _, c := range p.Checks {
			if slices.ContainsFunc(c.Corresponds, func(s string) bool { return slices.Contains(f.Controls[i].Corresponds, s) }) {
				checks = append(checks, c.Name)
			}
		}
	}
	return checks
}
