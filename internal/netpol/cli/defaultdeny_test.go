// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"encoding/json"
	"testing"

	"github.com/confighub/cub-manage/internal/netpol"
)

func TestPlanNamespace(t *testing.T) {
	const (
		denyIngress = `{"podSelector":{},"policyTypes":["Ingress"]}`
		denyBoth    = `{"podSelector":{},"policyTypes":["Ingress","Egress"]}`
		allowAll    = `{"podSelector":{},"policyTypes":["Ingress"],"ingress":[{}]}`
	)
	type policy struct{ unit, spec string }
	cases := []struct {
		name      string
		policies  []policy
		egress    bool
		wantUnit  string   // the Unit made or brought up to date, if any
		unchanged []string // the Units reported as already denying
	}{
		{"no policy", nil, false, "default-deny-shop", nil},
		{"denied under another name", []policy{{"lockdown", denyIngress}}, false, "", []string{"lockdown"}},
		{"allow-all is no deny", []policy{{"open", allowAll}}, false, "default-deny-shop", nil},
		{"egress beside another Unit's ingress deny", []policy{{"lockdown", denyIngress}}, true, "default-deny-egress-shop", nil},
		{"egress added to its own Unit", []policy{{"default-deny-shop", denyIngress}}, true, "default-deny-shop", nil},
		{"both denied by one policy", []policy{{"lockdown", denyIngress}, {"sealed", denyBoth}}, true, "", []string{"sealed"}},
	}
	for _, tc := range cases {
		var res []netpol.FleetResource
		for _, p := range tc.policies {
			var doc any
			body := `{"apiVersion":"networking.k8s.io/v1","kind":"NetworkPolicy","metadata":{"name":"p-` + p.unit + `","namespace":"shop"},"spec":` + p.spec + `}`
			if err := json.Unmarshal([]byte(body), &doc); err != nil {
				t.Fatal(err)
			}
			res = append(res, netpol.FleetResource{Origin: netpol.ResourceOrigin{Cluster: "c1", Space: "dev", UnitSlug: p.unit, UnitID: p.unit}, Doc: doc})
		}
		creation, unchanged := planNamespace(netpol.BuildFleet(res)["c1"], createDest{spaceSlug: "dev", cluster: "c1"}, "shop", tc.egress)
		gotUnit := ""
		if creation != nil {
			gotUnit = creation.Unit.Slug
		}
		if gotUnit != tc.wantUnit {
			t.Errorf("%s: makes %q, want %q", tc.name, gotUnit, tc.wantUnit)
		}
		var gotUnchanged []string
		for _, o := range unchanged {
			gotUnchanged = append(gotUnchanged, o.UnitSlug)
		}
		if len(gotUnchanged) != len(tc.unchanged) || (len(gotUnchanged) > 0 && gotUnchanged[0] != tc.unchanged[0]) {
			t.Errorf("%s: unchanged %v, want %v", tc.name, gotUnchanged, tc.unchanged)
		}
	}
}
