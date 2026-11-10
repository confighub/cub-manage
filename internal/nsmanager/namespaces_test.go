// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package nsmanager

import (
	"strings"
	"testing"
)

func findSummary(es []NamespaceSummary, ns string) *NamespaceSummary {
	for i := range es {
		if es[i].Namespace == ns {
			return &es[i]
		}
	}
	return nil
}

func TestSummarizeNamespaces(t *testing.T) {
	resources := []FleetResource{
		// payments: a Namespace object with pod-security, and a workload.
		res("c1", "c1", "ns", nsDoc("payments", map[string]any{PodSecurityEnforceLabel: "baseline"})),
		res("c1", "c1", "web", workloadDoc("Deployment", "web", "payments")),
		// orders: a workload only -- no Namespace object.
		res("c1", "c1", "orders-web", workloadDoc("Deployment", "api", "orders")),
		// shipping: a Namespace object without pod-security.
		res("c1", "c1", "shipping-ns", nsDoc("shipping", nil)),
	}
	clusters := BuildFleet(resources)
	got := SummarizeNamespaces(clusters["c1"])

	pay := findSummary(got, "payments")
	if pay == nil || !pay.HasNamespaceObject || pay.PodSecurityEnforce != "baseline" || pay.WorkloadCount != 1 {
		t.Fatalf("payments = %+v, want a Namespace object enforcing baseline, with 1 workload", pay)
	}
	orders := findSummary(got, "orders")
	if orders == nil || orders.HasNamespaceObject || orders.WorkloadCount != 1 {
		t.Fatalf("orders = %+v, want 1 workload and no Namespace object", orders)
	}
	ship := findSummary(got, "shipping")
	if ship == nil || !ship.HasNamespaceObject || ship.PodSecurityEnforce != "" {
		t.Fatalf("shipping = %+v, want a Namespace object with no pod-security label", ship)
	}
}

func TestSummarizeNamespacesSkipsPlaceholder(t *testing.T) {
	clusters := BuildFleet([]FleetResource{
		res("c1", "c1", "ph", nsDoc(PlaceholderNamespace, nil)),
	})
	got := SummarizeNamespaces(clusters["c1"])
	if findSummary(got, PlaceholderNamespace) != nil {
		t.Errorf("placeholder namespace should be skipped, got %+v", got)
	}
}

// The cluster creates and owns its built-in namespaces, so a workload in
// kube-system needs no Namespace Unit, and a Unit declaring one is not asked
// for a tenant's pod-security level.
func TestSummarizeNamespacesSkipsBuiltInNamespaces(t *testing.T) {
	clusters := BuildFleet([]FleetResource{
		res("c1", "c1", "dns", workloadDoc("Deployment", "coredns", "kube-system")),
		res("c1", "c1", "csi", nsDoc("kube-system", nil)),
		res("c1", "c1", "web", workloadDoc("Deployment", "web", "default")),
	})
	got := SummarizeNamespaces(clusters["c1"])
	if len(got) != 0 {
		t.Errorf("built-in namespaces should be skipped, got %+v", got)
	}
}

func TestDuplicateNamespaces(t *testing.T) {
	resources := []FleetResource{
		// Two Namespace objects named "payments" on target prod → collision.
		res("prod", "prod", "ns-a", nsDoc("payments", nil)),
		res("prod", "prod", "ns-b", nsDoc("payments", nil)),
		// Same name on a different target → not a collision.
		res("dev", "dev", "ns-c", nsDoc("payments", nil)),
		// Placeholder base bound to a dummy target → exempt.
		res("base", "dummy", "ns-base-1", nsDoc(PlaceholderNamespace, nil)),
		res("base", "dummy", "ns-base-2", nsDoc(PlaceholderNamespace, nil)),
		// Unbound (no target) duplicates → not deployed, skipped.
		res("unbound", "", "ns-u1", nsDoc("orders", nil)),
		res("unbound", "", "ns-u2", nsDoc("orders", nil)),
	}
	dups := DuplicateNamespaces(BuildFleet(resources))
	if len(dups) != 1 {
		t.Fatalf("duplicates = %d, want 1: %+v", len(dups), dups)
	}
	d := dups[0]
	if d.Target != "prod" || d.Namespace != "payments" || d.Count != 2 {
		t.Errorf("duplicate = %+v, want prod/payments/2", d)
	}
	if strings.Join(d.UnitSlugs, ",") != "ns-a,ns-b" {
		t.Errorf("unitSlugs = %v, want [ns-a ns-b]", d.UnitSlugs)
	}
}
