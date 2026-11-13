// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package netpol

import (
	"encoding/json"
	"slices"
	"testing"
)

// fleetFixture builds a single-cluster fleet exercising every analyzer and
// the connectivity model: a fully-covered namespace with an allow rule, a
// partially-covered namespace, an asymmetric egress/ingress pair, an allow-all
// policy, and metadata-egress (exposed vs. excepted).
func fleetFixture(t *testing.T) *ClusterNetpol {
	t.Helper()
	docs := []string{
		// covered ns: default-deny ingress + allow web from client.
		`{"apiVersion":"networking.k8s.io/v1","kind":"NetworkPolicy","metadata":{"name":"covered-default-deny","namespace":"covered"},"spec":{"podSelector":{},"policyTypes":["Ingress"]}}`,
		`{"apiVersion":"networking.k8s.io/v1","kind":"NetworkPolicy","metadata":{"name":"covered-allow-web","namespace":"covered"},"spec":{"podSelector":{"matchLabels":{"app":"web"}},"policyTypes":["Ingress"],"ingress":[{"from":[{"podSelector":{"matchLabels":{"app":"client"}}}]}]}}`,
		`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"cov-web","namespace":"covered"},"spec":{"template":{"metadata":{"labels":{"app":"web"}}}}}`,
		`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"cov-client","namespace":"covered"},"spec":{"template":{"metadata":{"labels":{"app":"client"}}}}}`,

		// partial ns: only a targeted policy; api is left uncovered, no default-deny.
		`{"apiVersion":"networking.k8s.io/v1","kind":"NetworkPolicy","metadata":{"name":"partial-web-only","namespace":"partial"},"spec":{"podSelector":{"matchLabels":{"app":"web"}},"policyTypes":["Ingress"],"ingress":[{"from":[{"podSelector":{"matchLabels":{"app":"client"}}}]}]}}`,
		`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"part-web","namespace":"partial"},"spec":{"template":{"metadata":{"labels":{"app":"web"}}}}}`,
		`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"part-api","namespace":"partial"},"spec":{"template":{"metadata":{"labels":{"app":"api"}}}}}`,

		// asym ns: frontend egress allows backend, but backend ingress admits only "other".
		`{"apiVersion":"networking.k8s.io/v1","kind":"NetworkPolicy","metadata":{"name":"asym-fe-egress","namespace":"asym"},"spec":{"podSelector":{"matchLabels":{"app":"frontend"}},"policyTypes":["Egress"],"egress":[{"to":[{"podSelector":{"matchLabels":{"app":"backend"}}}]}]}}`,
		`{"apiVersion":"networking.k8s.io/v1","kind":"NetworkPolicy","metadata":{"name":"asym-be-ingress","namespace":"asym"},"spec":{"podSelector":{"matchLabels":{"app":"backend"}},"policyTypes":["Ingress"],"ingress":[{"from":[{"podSelector":{"matchLabels":{"app":"other"}}}]}]}}`,
		`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"asym-fe","namespace":"asym"},"spec":{"template":{"metadata":{"labels":{"app":"frontend"}}}}}`,
		`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"asym-be","namespace":"asym"},"spec":{"template":{"metadata":{"labels":{"app":"backend"}}}}}`,

		// allowall ns: default-deny shape but with an allow-all ingress rule.
		`{"apiVersion":"networking.k8s.io/v1","kind":"NetworkPolicy","metadata":{"name":"allowall","namespace":"allowall"},"spec":{"podSelector":{},"policyTypes":["Ingress"],"ingress":[{}]}}`,
		`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"aa","namespace":"allowall"},"spec":{"template":{"metadata":{"labels":{"app":"x"}}}}}`,

		// meta ns: one egress exposes the metadata IP, one excepts it. No workloads.
		`{"apiVersion":"networking.k8s.io/v1","kind":"NetworkPolicy","metadata":{"name":"meta-open","namespace":"meta"},"spec":{"podSelector":{"matchLabels":{"app":"egressor"}},"policyTypes":["Egress"],"egress":[{"to":[{"ipBlock":{"cidr":"0.0.0.0/0"}}]}]}}`,
		`{"apiVersion":"networking.k8s.io/v1","kind":"NetworkPolicy","metadata":{"name":"meta-blocked","namespace":"meta"},"spec":{"podSelector":{"matchLabels":{"app":"egressor2"}},"policyTypes":["Egress"],"egress":[{"to":[{"ipBlock":{"cidr":"0.0.0.0/0","except":["169.254.169.254/32"]}}]}]}}`,
	}
	var res []FleetResource
	for _, d := range docs {
		var doc any
		if err := json.Unmarshal([]byte(d), &doc); err != nil {
			t.Fatalf("bad fixture: %v", err)
		}
		res = append(res, FleetResource{Origin: ResourceOrigin{Cluster: "c1"}, Doc: doc})
	}
	c := BuildFleet(res)["c1"]
	if c == nil {
		t.Fatal("cluster c1 missing")
	}
	return c
}

func findWorkload(c *ClusterNetpol, name string) *WorkloadEntity {
	for _, w := range c.Workloads {
		if w.Name == name {
			return w
		}
	}
	return nil
}

func TestCoverage(t *testing.T) {
	c := fleetFixture(t)
	nss, _ := c.Coverage()
	byNS := map[string]NamespaceCoverage{}
	for _, nc := range nss {
		byNS[nc.Namespace] = nc
	}

	if !byNS["covered"].DefaultDenyIngress {
		t.Error("covered namespace should have default-deny ingress")
	}
	if len(byNS["covered"].UncoveredIngress) != 0 {
		t.Errorf("covered namespace should have no uncovered ingress, got %v", byNS["covered"].UncoveredIngress)
	}
	if byNS["partial"].DefaultDenyIngress {
		t.Error("partial namespace should NOT have default-deny ingress")
	}
	if got := byNS["partial"].UncoveredIngress; len(got) != 1 || got[0] != "part-api" {
		t.Errorf("partial uncovered ingress = %v, want [part-api]", got)
	}
}

func TestConnectivity(t *testing.T) {
	c := fleetFixture(t)

	// In the covered namespace, the client is allowed to reach web; nothing else is.
	web := findWorkload(c, "cov-web")
	sources := WhoCanReach(c, web)
	if len(sources) != 1 || sources[0].Name != "cov-client" {
		t.Fatalf("who-can-reach cov-web = %v, want [cov-client]", names(sources))
	}

	// The asymmetric flow is actually blocked: frontend cannot reach backend
	// because backend's ingress does not admit it.
	be := findWorkload(c, "asym-be")
	if got := names(WhoCanReach(c, be)); contains(got, "asym-fe") {
		t.Errorf("asym-fe should NOT be able to reach asym-be, got sources %v", got)
	}
	fe := findWorkload(c, "asym-fe")
	if got := names(ReachableFrom(c, fe)); contains(got, "asym-be") {
		t.Errorf("asym-fe should not reach asym-be, got dests %v", got)
	}
}

func TestFindings(t *testing.T) {
	c := fleetFixture(t)
	byAnalyzer := map[string]int{}
	for _, f := range AnalyzeFindings(c) {
		byAnalyzer[f.Analyzer]++
	}
	want := map[string]int{
		"missing-default-deny-ingress": 3, // partial, asym, and allowall: an allow-all denies nothing
		"uncovered-ingress":            2, // part-api, asym-fe
		"allow-all":                    1, // allowall
		"metadata-egress":              1, // meta-open only (meta-blocked is excepted)
		"ingress-egress-asymmetry":     1, // frontend egress vs backend ingress
		"egress-blocks-dns":            1, // asym-fe: egress-isolated, and nothing allows DNS
		"selects-nothing":              4, // meta-open and meta-blocked select no workload; no app=client in partial, no app=other in asym
		"not-enforced":                 0, // enforcement unknown
	}
	for analyzer, n := range want {
		if byAnalyzer[analyzer] != n {
			t.Errorf("analyzer %q = %d findings, want %d", analyzer, byAnalyzer[analyzer], n)
		}
	}
}

func TestDefaultDenies(t *testing.T) {
	c := fleetFixture(t)
	if ingress, _ := c.DefaultDenies("allowall"); len(ingress) != 0 {
		t.Errorf("allowall: an allow-all policy counted as a default-deny: %v", ingress)
	}
	ingress, egress := c.DefaultDenies("covered")
	if len(ingress) != 1 || len(egress) != 0 {
		t.Errorf("covered: default-denies = %d ingress, %d egress, want 1, 0", len(ingress), len(egress))
	}
}

func names(ws []*WorkloadEntity) []string {
	out := make([]string, 0, len(ws))
	for _, w := range ws {
		out = append(out, w.Name)
	}
	return out
}

func clusterOf(t *testing.T, docs ...string) *ClusterNetpol {
	t.Helper()
	var res []FleetResource
	for _, d := range docs {
		var doc any
		if err := json.Unmarshal([]byte(d), &doc); err != nil {
			t.Fatalf("bad fixture: %v", err)
		}
		res = append(res, FleetResource{Origin: ResourceOrigin{Cluster: "c1"}, Doc: doc})
	}
	return BuildFleet(res)["c1"]
}

// Policies add up: DNS allowed by one policy counts for a workload another
// policy isolates.
func TestDNSFindingsUnion(t *testing.T) {
	const (
		web      = `{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"web","namespace":"shop"},"spec":{"template":{"metadata":{"labels":{"app":"web"}}}}}`
		denyAll  = `{"apiVersion":"networking.k8s.io/v1","kind":"NetworkPolicy","metadata":{"name":"deny","namespace":"shop"},"spec":{"podSelector":{},"policyTypes":["Egress"]}}`
		allowDB  = `{"apiVersion":"networking.k8s.io/v1","kind":"NetworkPolicy","metadata":{"name":"db","namespace":"shop"},"spec":{"podSelector":{"matchLabels":{"app":"web"}},"policyTypes":["Egress"],"egress":[{"to":[{"ipBlock":{"cidr":"10.0.0.0/8"}}],"ports":[{"port":5432}]}]}}`
		dnsTCP   = `{"apiVersion":"networking.k8s.io/v1","kind":"NetworkPolicy","metadata":{"name":"dns","namespace":"shop"},"spec":{"podSelector":{},"policyTypes":["Egress"],"egress":[{"ports":[{"port":53}]}]}}`
		dnsUDP   = `{"apiVersion":"networking.k8s.io/v1","kind":"NetworkPolicy","metadata":{"name":"dns","namespace":"shop"},"spec":{"podSelector":{},"policyTypes":["Egress"],"egress":[{"to":[{"namespaceSelector":{"matchLabels":{"kubernetes.io/metadata.name":"kube-system"}}}],"ports":[{"port":53,"protocol":"UDP"}]}]}}`
		dnsLocal = `{"apiVersion":"networking.k8s.io/v1","kind":"NetworkPolicy","metadata":{"name":"dns","namespace":"shop"},"spec":{"podSelector":{},"policyTypes":["Egress"],"egress":[{"to":[{"podSelector":{}}],"ports":[{"port":53,"protocol":"UDP"}]}]}}`
	)
	cases := []struct {
		name string
		docs []string
		want int
	}{
		{"deny all", []string{web, denyAll}, 1},
		{"database only", []string{web, denyAll, allowDB}, 1},
		{"TCP 53 only", []string{web, denyAll, allowDB, dnsTCP}, 1},
		{"UDP 53 to kube-system", []string{web, denyAll, allowDB, dnsUDP}, 0},
		{"UDP 53 within its namespace", []string{web, denyAll, dnsLocal}, 1},
		{"not isolated", []string{web}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := 0
			for _, f := range AnalyzeFindings(clusterOf(t, c.docs...)) {
				if f.Analyzer == "egress-blocks-dns" {
					got++
				}
			}
			if got != c.want {
				t.Errorf("egress-blocks-dns findings = %d, want %d", got, c.want)
			}
		})
	}
}

func TestEgressRuleFindings(t *testing.T) {
	c := clusterOf(t,
		`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"run","namespace":"agents"},"spec":{"template":{"metadata":{"labels":{"app":"run"}}}}}`,
		// Every destination, but only on the ports named: neither allow-all
		// nor, without TCP 80, a way to the metadata IP.
		`{"apiVersion":"networking.k8s.io/v1","kind":"NetworkPolicy","metadata":{"name":"ports","namespace":"agents"},"spec":{"podSelector":{"matchLabels":{"app":"run"}},"policyTypes":["Egress"],"egress":[{"to":[],"ports":[{"port":53,"protocol":"UDP"}]},{"to":[],"ports":[{"port":443}]}]}}`,
		// TCP 80 to anywhere reaches the metadata service.
		`{"apiVersion":"networking.k8s.io/v1","kind":"NetworkPolicy","metadata":{"name":"http","namespace":"agents"},"spec":{"podSelector":{"matchLabels":{"app":"run"}},"policyTypes":["Egress"],"egress":[{"ports":[{"port":80}]}]}}`,
		// A rule naming nothing allows everything, the metadata IP included.
		`{"apiVersion":"networking.k8s.io/v1","kind":"NetworkPolicy","metadata":{"name":"open","namespace":"agents"},"spec":{"podSelector":{"matchLabels":{"app":"run"}},"policyTypes":["Egress"],"egress":[{}]}}`,
		// A peer whose podSelector matches nothing in the namespace.
		`{"apiVersion":"networking.k8s.io/v1","kind":"NetworkPolicy","metadata":{"name":"typo","namespace":"agents"},"spec":{"podSelector":{"matchLabels":{"app":"run"}},"policyTypes":["Ingress"],"ingress":[{"from":[{"podSelector":{"matchLabels":{"app":"contorlplane"}}}]}]}}`,
	)
	c.Enforcement = Enforcement{Known: true}
	got := map[string][]string{}
	fs := AnalyzeFindings(c)
	for _, f := range fs {
		got[f.Analyzer] = append(got[f.Analyzer], f.Resource)
	}
	want := map[string][]string{
		"allow-all":       {"open"},
		"metadata-egress": {"http", "open"},
		"selects-nothing": {"typo"},
		"not-enforced":    {""},
	}
	for analyzer, resources := range want {
		if !slices.Equal(got[analyzer], resources) {
			t.Errorf("%s findings on %v, want %v", analyzer, got[analyzer], resources)
		}
	}
	if fs[0].Analyzer != "not-enforced" {
		t.Errorf("first finding is %s, want not-enforced", fs[0].Analyzer)
	}
}

func TestEnforcementFromFacts(t *testing.T) {
	for _, c := range []struct {
		facts map[string]string
		want  string
	}{
		{nil, "unknown"},
		{map[string]string{EnforcerFact: ""}, "no"},
		{map[string]string{EnforcerFact: "cilium"}, "cilium"},
	} {
		if got := EnforcementFromFacts(c.facts).String(); got != c.want {
			t.Errorf("EnforcementFromFacts(%v) = %s, want %s", c.facts, got, c.want)
		}
	}
}
