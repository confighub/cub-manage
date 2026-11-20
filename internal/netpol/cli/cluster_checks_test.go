// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"strings"
	"testing"

	"github.com/confighub/cub-manage/internal/celrule/celruletest"
	"github.com/confighub/cub-manage/internal/netpol"
)

// onCluster is the FunctionContext of a Unit released to a Target.
var onCluster = map[string]any{"TargetID": "4d6b2a1e-0000-4000-8000-000000000001"}

func deployment(namespace, name, app string) string {
	return "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: " + name + "\n  namespace: " + namespace +
		"\nspec:\n  template:\n    metadata:\n      labels:\n        app: " + app + "\n"
}

func namespaceOf(name string, labels ...string) string {
	doc := "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: " + name + "\n"
	if len(labels) > 0 {
		doc += "  labels:\n"
		for _, label := range labels {
			key, value, _ := strings.Cut(label, "=")
			doc += "    " + key + ": " + value + "\n"
		}
	}
	return doc
}

var (
	web           = deployment("shop", "web", "web")
	apiDeployment = deployment("shop", "api", "api")
	pay           = deployment("billing", "pay", "pay")
	report        = "apiVersion: batch/v1\nkind: CronJob\nmetadata:\n  name: report\n  namespace: shop\nspec:\n  jobTemplate:\n    spec:\n      template:\n        metadata:\n          labels:\n            app: report\n"

	// webIngress isolates web's ingress, and admits api.
	webIngress = `apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: web-ingress
  namespace: shop
spec:
  podSelector:
    matchLabels: {app: web}
  policyTypes: [Ingress]
  ingress:
    - from:
        - podSelector:
            matchLabels: {app: api}
`
	// shopDefaultDeny denies all ingress in shop, but for port 53.
	shopDefaultDeny = `apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: default-deny
  namespace: shop
spec:
  podSelector: {}
  policyTypes: [Ingress]
  ingress:
    - ports:
        - port: 53
          protocol: UDP
`
	// apiEgress isolates api's egress, and lets it reach web.
	apiEgress = `apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: api-egress
  namespace: shop
spec:
  podSelector:
    matchLabels: {app: api}
  policyTypes: [Egress]
  egress:
    - to:
        - podSelector:
            matchLabels: {app: web}
`
)

// A workload is covered by a policy isolating ingress that selects its pods, in its namespace
// on its cluster, whatever kind keeps its pods' labels.
func TestIngressCoverage(t *testing.T) {
	check := celruletest.Check(t, Pack, "netpol-ingress-coverage")
	policies := celruletest.Other{"policies": {webIngress, apiEgress}}

	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, policies, check, web))
	// api's egress is isolated, not its ingress.
	result := celruletest.EvalCheckOther(t, onCluster, nil, policies, check, apiDeployment)
	celruletest.Expect(t, result, "spec.template.metadata.labels")
	celruletest.ExpectResource(t, result, "shop/api", "apps/v1/Deployment")
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, policies, check, report),
		"spec.jobTemplate.spec.template.metadata.labels")
	// The policy for web in shop does not cover web in billing.
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, policies, check, deployment("billing", "web", "web")),
		"spec.template.metadata.labels")

	// A default-deny selects every pod.
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, celruletest.Other{"policies": {shopDefaultDeny}}, check, report))

	// A Unit with no Target, or on a cluster that enforces no NetworkPolicy, passes.
	celruletest.Expect(t, celruletest.EvalCheckOther(t, map[string]any{}, nil, nil, check, apiDeployment))
	notEnforced := map[string]any{"TargetID": onCluster["TargetID"], "TargetFacts": map[string]any{netpol.EnforcerFact: ""}}
	celruletest.Expect(t, celruletest.EvalCheckOther(t, notEnforced, nil, nil, check, apiDeployment))
	enforcedBy := map[string]any{"TargetID": onCluster["TargetID"], "TargetFacts": map[string]any{netpol.EnforcerFact: "cilium"}}
	celruletest.Expect(t, celruletest.EvalCheckOther(t, enforcedBy, nil, nil, check, apiDeployment), "spec.template.metadata.labels")
}

// A policy, and each podSelector peer of its rules, selects some workload in its namespace.
func TestSelectsPods(t *testing.T) {
	check := celruletest.Check(t, Pack, "netpol-selects-pods")
	workloads := celruletest.Other{"workloads": {web, apiDeployment, pay}}

	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, workloads, check, webIngress))
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, workloads, check, shopDefaultDeny))

	renamed := strings.Replace(webIngress, "{app: web}", "{app: website}", 1)
	result := celruletest.EvalCheckOther(t, onCluster, nil, workloads, check, renamed)
	celruletest.Expect(t, result, "spec.podSelector")
	celruletest.ExpectResource(t, result, "shop/web-ingress", "networking.k8s.io/v1/NetworkPolicy")

	// pay is in billing, so the peer selects nothing in shop.
	peer := strings.Replace(webIngress, "{app: api}", "{app: pay}", 1)
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, workloads, check, peer), "spec.ingress.0.from.0.podSelector")
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, workloads, check,
		strings.Replace(apiEgress, "{app: web}", "{app: gone}", 1)), "spec.egress.0.to.0.podSelector")
	// A peer with a namespaceSelector may name workloads elsewhere, so it is not checked.
	elsewhere := strings.Replace(peer, "        - podSelector:\n", "        - namespaceSelector: {}\n          podSelector:\n", 1)
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, workloads, check, elsewhere))

	celruletest.Expect(t, celruletest.EvalCheckOther(t, map[string]any{}, nil, workloads, check, renamed))
}

// A Namespace holding workloads has a policy denying ingress by default.
func TestDefaultDeny(t *testing.T) {
	check := celruletest.Check(t, Pack, "netpol-default-deny")
	shop := namespaceOf("shop")
	workloads := []string{web, apiDeployment}

	result := celruletest.EvalCheckOther(t, onCluster, nil, celruletest.Other{"workloads": workloads, "policies": {webIngress}}, check, shop)
	celruletest.Expect(t, result, "metadata.name")
	celruletest.ExpectResource(t, result, "/shop", "v1/Namespace")
	// A rule limited to a port leaves the default-deny in place.
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil,
		celruletest.Other{"workloads": workloads, "policies": {webIngress, shopDefaultDeny}}, check, shop))
	// A rule with no ports and no peers admits everything.
	allowAll := strings.Replace(shopDefaultDeny, "    - ports:\n        - port: 53\n          protocol: UDP\n", "    - {}\n", 1)
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil,
		celruletest.Other{"workloads": workloads, "policies": {allowAll}}, check, shop), "metadata.name")
	// A default-deny elsewhere does not count.
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil,
		celruletest.Other{"workloads": workloads, "policies": {strings.Replace(shopDefaultDeny, "namespace: shop", "namespace: billing", 1)}}, check, shop),
		"metadata.name")
	// A Namespace with no workloads needs none.
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, celruletest.Other{"workloads": {pay}}, check, shop))
	celruletest.Expect(t, celruletest.EvalCheckOther(t, map[string]any{}, nil, celruletest.Other{"workloads": workloads}, check, shop))
}

// A rule allowing a flow is matched at the other end, where the pods are isolated in the other
// direction, in both directions.
func TestAllowedAtBothEnds(t *testing.T) {
	check := celruletest.Check(t, Pack, "netpol-allowed-at-both-ends")
	other := func(policies ...string) celruletest.Other {
		return celruletest.Other{"workloads": {web, apiDeployment, pay}, "policies": policies}
	}

	// api may reach web, and web admits api.
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, other(webIngress, apiEgress), check, apiEgress))
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, other(webIngress, apiEgress), check, webIngress))

	// web admits only pay: api's egress is dropped at web.
	webAdmitsPay := strings.Replace(webIngress, "{app: api}", "{app: pay}", 1)
	result := celruletest.EvalCheckOther(t, onCluster, nil, other(webAdmitsPay, apiEgress), check, apiEgress)
	celruletest.Expect(t, result, "spec.egress.0.to")
	if len(result.Details) != 1 || !strings.Contains(result.Details[0], "Deployment shop/web") {
		t.Errorf("details = %q, want one naming Deployment shop/web", result.Details)
	}
	// With web's ingress not isolated, nothing drops it.
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, other(apiEgress), check, apiEgress))

	// web admits api, but api's egress is isolated and lets it reach only pay.
	apiReachesPay := strings.Replace(apiEgress, "matchLabels: {app: web}", "matchLabels: {app: pay}\n          namespaceSelector:\n            matchLabels: {kubernetes.io/metadata.name: billing}", 1)
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, other(webIngress, apiReachesPay), check, webIngress), "spec.ingress.0.from")

	// pay in billing is reached through a namespaceSelector naming billing by its name label,
	// and admits shop's pods by a label billing's Namespace has.
	payIngress := `apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: pay-ingress
  namespace: billing
spec:
  podSelector: {}
  ingress:
    - from:
        - namespaceSelector:
            matchLabels: {team: storefront}
`
	withNamespaces := celruletest.Other{"workloads": {web, apiDeployment, pay}, "policies": {apiReachesPay, payIngress},
		"namespaces": {namespaceOf("shop", "team=storefront"), namespaceOf("billing")}}
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, withNamespaces, check, apiReachesPay))
	withNamespaces["namespaces"] = []string{namespaceOf("shop"), namespaceOf("billing")}
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, withNamespaces, check, apiReachesPay), "spec.egress.0.to")
}
