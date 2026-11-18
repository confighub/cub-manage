// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/confighub/cub-manage/internal/netpol"
)

func policyDoc(t *testing.T, ingress string) netpol.FleetResource {
	t.Helper()
	var doc any
	if err := json.Unmarshal([]byte(`{"apiVersion":"networking.k8s.io/v1","kind":"NetworkPolicy","metadata":{"name":"server","namespace":"app"},"spec":{"podSelector":{"matchLabels":{"app":"server"}},"policyTypes":["Ingress"],"ingress":`+ingress+`}}`), &doc); err != nil {
		t.Fatal(err)
	}
	return netpol.FleetResource{Origin: netpol.ResourceOrigin{Cluster: "c1"}, Doc: doc}
}

func TestCompareUpstream(t *testing.T) {
	var docs []netpol.FleetResource
	for _, d := range []string{
		`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"traefik","namespace":"traefik"},"spec":{"selector":{"matchLabels":{"app":"traefik"}},"template":{"metadata":{"labels":{"app":"traefik"}},"spec":{"containers":[{"image":"traefik:v3"}]}}}}`,
		`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"server","namespace":"app"},"spec":{"selector":{"matchLabels":{"app":"server"}},"template":{"metadata":{"labels":{"app":"server"}}}}}`,
		`{"apiVersion":"v1","kind":"Service","metadata":{"name":"server","namespace":"app"},"spec":{"selector":{"app":"server"},"ports":[{"port":80,"targetPort":8080}]}}`,
		`{"apiVersion":"traefik.io/v1alpha1","kind":"IngressRoute","metadata":{"name":"server","namespace":"app"},"spec":{"routes":[{"services":[{"name":"server","port":80}]}]}}`,
	} {
		var doc any
		if err := json.Unmarshal([]byte(d), &doc); err != nil {
			t.Fatal(err)
		}
		docs = append(docs, netpol.FleetResource{Origin: netpol.ResourceOrigin{Cluster: "c1"}, Doc: doc})
	}
	c := netpol.BuildFleet(append(docs, policyDoc(t, `[{}]`)))["c1"]
	flows, _ := netpol.RouteFlows(c, "")
	edits := netpol.PlanIngress(c, flows).Edits
	if len(edits) != 1 {
		t.Fatalf("edits = %d, want 1", len(edits))
	}

	upstream := func(ingress string) map[string]*netpol.NetworkPolicyEntity {
		p := netpol.BuildFleet([]netpol.FleetResource{policyDoc(t, ingress)})["c1"].NetworkPolicies[0]
		return map[string]*netpol.NetworkPolicyEntity{p.Name: p}
	}
	edited := `[{"from":[{"namespaceSelector":{"matchLabels":{"kubernetes.io/metadata.name":"traefik"}},"podSelector":{"matchLabels":{"app":"traefik"}}}],"ports":[{"protocol":"TCP","port":8080}]}]`

	if done, note := compareUpstream("base/app", upstream(`[{}]`), edits); done || note != "" {
		t.Errorf("the same policy as the variant's: done %v, note %q; want the edit made to the upstream", done, note)
	}
	if done, note := compareUpstream("base/app", upstream(edited), edits); !done || note != "" {
		t.Errorf("the edited policy: done %v, note %q; want the upstream to have it already", done, note)
	}
	if _, note := compareUpstream("base/app", upstream(`[{"from":[{"podSelector":{}}]}]`), edits); !strings.Contains(note, "other ingress rules") {
		t.Errorf("another policy: note %q", note)
	}
	if _, note := compareUpstream("base/app", map[string]*netpol.NetworkPolicyEntity{}, edits); !strings.Contains(note, "has no policy server") {
		t.Errorf("no policy: note %q", note)
	}
}
