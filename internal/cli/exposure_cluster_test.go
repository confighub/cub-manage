// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"strings"
	"testing"

	"github.com/confighub/cub-manage/internal/celrule/celruletest"
)

func TestUniqueIngressHost(t *testing.T) {
	check := celruletest.Check(t, exposurePack, "unique-ingress-host")
	ingress := func(namespace, name, host, path string) string {
		return "apiVersion: networking.k8s.io/v1\nkind: Ingress\nmetadata:\n  name: " + name + "\n  namespace: " + namespace + "\nspec:\n  rules:\n" +
			"    - host: " + host + "\n      http:\n        paths:\n          - path: " + path + "\n            pathType: Prefix\n" +
			"            backend: {service: {name: web, port: {number: 80}}}\n"
	}
	shop := ingress("shop", "shop", "shop.example.com", "/api")
	// The Unit's own Ingress is among those passed, and does not clash with itself.
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, celruletest.Other{"routes": {shop}}, check, shop))
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, celruletest.Other{"routes": {
		shop, ingress("billing", "pay", "shop.example.com", "/pay"), ingress("billing", "api", "api.example.com", "/api"),
	}}, check, shop))

	result := celruletest.EvalCheckOther(t, onCluster, nil, celruletest.Other{"routes": {
		shop, ingress("billing", "legacy", "shop.example.com", "/api"),
	}}, check, shop)
	celruletest.Expect(t, result, "spec.rules.0.http.paths.0.path")
	if len(result.Details) != 1 || !strings.Contains(result.Details[0], "Ingress billing/legacy") {
		t.Errorf("details = %q, want one naming Ingress billing/legacy", result.Details)
	}
	celruletest.Expect(t, celruletest.EvalCheckOther(t, map[string]any{}, nil, celruletest.Other{"routes": {
		shop, ingress("billing", "legacy", "shop.example.com", "/api"),
	}}, check, shop))
}

// Two Traefik IngressRoutes conflict when a route of each matches a host and path in common, on
// an entry point they share, however their rules are written, as long as the rules are made of
// Host, Path and PathPrefix alone.
func TestUniqueIngressRouteHost(t *testing.T) {
	check := celruletest.Check(t, exposurePack, "unique-ingress-host")
	route := func(namespace, name, entryPoints, match string) string {
		doc := "apiVersion: traefik.io/v1alpha1\nkind: IngressRoute\nmetadata:\n  name: " + name + "\n  namespace: " + namespace + "\nspec:\n"
		if entryPoints != "" {
			doc += "  entryPoints: " + entryPoints + "\n"
		}
		return doc + "  routes:\n    - kind: Rule\n      match: '" + match + "'\n      services: [{name: web, port: 80}]\n"
	}
	shop := route("shop", "shop", "[websecure]", "Host(`shop.example.com`) && PathPrefix(`/api`)")
	conflicts := func(other string) celruletest.Result {
		return celruletest.EvalCheckOther(t, onCluster, nil, celruletest.Other{"routes": {shop, other}}, check, shop)
	}

	result := conflicts(route("billing", "legacy", "[websecure]", "PathPrefix(`/api`) && Host(`shop.example.com`)"))
	celruletest.Expect(t, result, "spec.routes.0.match")
	if len(result.Details) != 1 || !strings.Contains(result.Details[0], "IngressRoute billing/legacy") {
		t.Errorf("details = %q, want one naming IngressRoute billing/legacy", result.Details)
	}
	// Traefik v2's several arguments, quotes, and an IngressRoute on every entry point.
	celruletest.Expect(t, conflicts(route("billing", "legacy", "", `Host("a.example.com", "shop.example.com") && PathPrefix("/api")`)),
		"spec.routes.0.match")
	// Another entry point, host or path, or an exact Path for a prefix, is no conflict.
	celruletest.Expect(t, conflicts(route("billing", "legacy", "[web]", "Host(`shop.example.com`) && PathPrefix(`/api`)")))
	celruletest.Expect(t, conflicts(route("billing", "legacy", "[websecure]", "Host(`pay.example.com`) && PathPrefix(`/api`)")))
	celruletest.Expect(t, conflicts(route("billing", "legacy", "[websecure]", "Host(`shop.example.com`) && Path(`/api`)")))
	// A rule matching more than a host and path can split the traffic, so it is not compared.
	celruletest.Expect(t, conflicts(route("billing", "legacy", "[websecure]", "Host(`shop.example.com`) && PathPrefix(`/api`) && Header(`X-Canary`, `true`)")))
	celruletest.Expect(t, conflicts(route("billing", "legacy", "[websecure]", "Host(`shop.example.com`) || Host(`pay.example.com`)")))
	// A rule naming no path matches every path, as PathPrefix(`/`) does.
	root := route("shop", "shop", "[websecure]", "Host(`shop.example.com`)")
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, celruletest.Other{"routes": {
		root, route("billing", "legacy", "[websecure]", "Host(`shop.example.com`) && PathPrefix(`/`)"),
	}}, check, root), "spec.routes.0.match")
	// An Ingress is not compared with an IngressRoute.
	ingress := "apiVersion: networking.k8s.io/v1\nkind: Ingress\nmetadata:\n  name: legacy\n  namespace: billing\nspec:\n  rules:\n" +
		"    - host: shop.example.com\n      http:\n        paths:\n          - path: /api\n            pathType: Prefix\n" +
		"            backend: {service: {name: web, port: {number: 80}}}\n"
	celruletest.Expect(t, conflicts(ingress))
	celruletest.Expect(t, celruletest.EvalCheckOther(t, map[string]any{}, nil, nil, check, shop))
}
