// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package netpol

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/confighub/sdk/configkit/yqkit"
	"sigs.k8s.io/yaml"
)

// refsFixture is a cluster with a Traefik controller, an nginx one, and the
// routes, Services and workloads behind them.
func refsFixture(t *testing.T, extra ...string) *ClusterNetpol {
	t.Helper()
	docs := []string{
		`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"traefik","namespace":"traefik"},"spec":{"selector":{"matchLabels":{"app.kubernetes.io/name":"traefik"}},"template":{"metadata":{"labels":{"app.kubernetes.io/name":"traefik","helm.sh/chart":"traefik-1"}},"spec":{"containers":[{"name":"traefik","image":"docker.io/traefik:v3.1@sha256:abc"}]}}}}`,
		`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"ingress-nginx-controller","namespace":"ingress-nginx"},"spec":{"selector":{"matchLabels":{"app":"nginx"}},"template":{"metadata":{"labels":{"app":"nginx"}},"spec":{"containers":[{"name":"controller","image":"registry.k8s.io/ingress-nginx/controller:v1.11.0"}]}}}}`,
		`{"apiVersion":"networking.k8s.io/v1","kind":"IngressClass","metadata":{"name":"nginx"},"spec":{"controller":"k8s.io/ingress-nginx"}}`,
		`{"apiVersion":"networking.k8s.io/v1","kind":"IngressClass","metadata":{"name":"traefik","annotations":{"ingressclass.kubernetes.io/is-default-class":"true"}},"spec":{"controller":"traefik.io/ingress-controller"}}`,

		// argocd: the server's Service names its targetPort, and a policy admits every source.
		`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"argocd-server","namespace":"argocd"},"spec":{"selector":{"matchLabels":{"app.kubernetes.io/name":"argocd-server"}},"template":{"metadata":{"labels":{"app.kubernetes.io/name":"argocd-server"}},"spec":{"containers":[{"name":"server","image":"quay.io/argoproj/argocd:v3","ports":[{"name":"server","containerPort":8080},{"name":"metrics","containerPort":8083}]}]}}}}`,
		`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"argocd-repo-server","namespace":"argocd"},"spec":{"template":{"metadata":{"labels":{"app.kubernetes.io/name":"argocd-repo-server"}}}}}`,
		`{"apiVersion":"v1","kind":"Service","metadata":{"name":"argocd-server","namespace":"argocd"},"spec":{"selector":{"app.kubernetes.io/name":"argocd-server"},"ports":[{"name":"http","port":80,"targetPort":"server"},{"name":"https","port":443,"targetPort":"server"}]}}`,
		`{"apiVersion":"networking.k8s.io/v1","kind":"NetworkPolicy","metadata":{"name":"argocd-server-network-policy","namespace":"argocd"},"spec":{"podSelector":{"matchLabels":{"app.kubernetes.io/name":"argocd-server"}},"policyTypes":["Ingress"],"ingress":[{}]}}`,
		`{"apiVersion":"traefik.io/v1alpha1","kind":"IngressRoute","metadata":{"name":"argocd-server","namespace":"argocd"},"spec":{"routes":[{"match":"Host(x)","services":[{"name":"argocd-server","port":80}]}]}}`,

		// web: an Ingress of the default class, to a Service with a numeric targetPort and no policy.
		`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"web","namespace":"web"},"spec":{"selector":{"matchLabels":{"app":"web"}},"template":{"metadata":{"labels":{"app":"web"}}}}}`,
		`{"apiVersion":"v1","kind":"Service","metadata":{"name":"web","namespace":"web"},"spec":{"selector":{"app":"web"},"ports":[{"name":"http","port":80,"targetPort":8000}]}}`,
		`{"apiVersion":"networking.k8s.io/v1","kind":"Ingress","metadata":{"name":"web","namespace":"web"},"spec":{"rules":[{"http":{"paths":[{"path":"/","backend":{"service":{"name":"web","port":{"name":"http"}}}}]}}]}}`,

		// api: an Ingress of class nginx.
		`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"api","namespace":"web"},"spec":{"selector":{"matchLabels":{"app":"api"}},"template":{"metadata":{"labels":{"app":"api"}}}}}`,
		`{"apiVersion":"v1","kind":"Service","metadata":{"name":"api","namespace":"web"},"spec":{"selector":{"app":"api"},"ports":[{"port":9090}]}}`,
		`{"apiVersion":"networking.k8s.io/v1","kind":"Ingress","metadata":{"name":"api","namespace":"web"},"spec":{"ingressClassName":"nginx","defaultBackend":{"service":{"name":"api","port":{"number":9090}}}}}`,
	}
	return buildFixture(t, append(docs, extra...))
}

func buildFixture(t *testing.T, docs []string) *ClusterNetpol {
	t.Helper()
	var res []FleetResource
	for _, d := range docs {
		var doc any
		if err := json.Unmarshal([]byte(d), &doc); err != nil {
			t.Fatalf("bad fixture %s: %v", d, err)
		}
		res = append(res, FleetResource{Origin: ResourceOrigin{Cluster: "c1"}, Doc: doc})
	}
	c := BuildFleet(res)["c1"]
	if c == nil {
		t.Fatal("cluster c1 missing")
	}
	return c
}

func flowStrings(flows []Flow) []string {
	var out []string
	for _, f := range flows {
		out = append(out, WorkloadName(f.Source)+" -> "+WorkloadName(f.Dest)+" "+portsKey(f.Ports))
	}
	return out
}

func TestRouteFlows(t *testing.T) {
	c := refsFixture(t,
		// A Traefik route to a TraefikService, to a Service with no selector, and to one that is not there.
		`{"apiVersion":"traefik.io/v1alpha1","kind":"IngressRoute","metadata":{"name":"odd","namespace":"web"},"spec":{"routes":[{"services":[{"name":"weighted","kind":"TraefikService"},{"name":"external","port":80},{"name":"missing","port":80}]}]}}`,
		`{"apiVersion":"v1","kind":"Service","metadata":{"name":"external","namespace":"web"},"spec":{"ports":[{"port":80}]}}`,
		// An Ingress of a class whose controller is not recognized.
		`{"apiVersion":"networking.k8s.io/v1","kind":"IngressClass","metadata":{"name":"haproxy"},"spec":{"controller":"haproxy.org/ingress-controller"}}`,
		`{"apiVersion":"networking.k8s.io/v1","kind":"Ingress","metadata":{"name":"legacy","namespace":"web"},"spec":{"ingressClassName":"haproxy","defaultBackend":{"service":{"name":"web","port":{"number":80}}}}}`,
	)
	flows, skipped := RouteFlows(c, "")
	got := strings.Join(flowStrings(flows), "\n")
	want := strings.Join([]string{
		"traefik/traefik -> argocd/argocd-server TCP 8080", // the named targetPort, as the container's port number
		"traefik/traefik -> web/web TCP 8000",              // the default IngressClass, and the Service port by name
		"ingress-nginx/ingress-nginx-controller -> web/api TCP 9090",
	}, "\n")
	if got != want {
		t.Errorf("flows:\n%s\nwant:\n%s", got, want)
	}
	reasons := map[string]bool{}
	for _, s := range skipped {
		reasons[s.Via+": "+s.Reason] = true
	}
	for _, r := range []string{
		"IngressRoute web/odd: Service web/external selects no pods; its endpoints are set some other way",
		"IngressRoute web/odd: Service web/missing is not in ConfigHub",
		"Ingress web/legacy: IngressClass haproxy's controller, haproxy.org/ingress-controller, is not one allow-from-refs recognizes; name its workload with --ingress-controller",
	} {
		if !reasons[r] {
			t.Errorf("missing skip %q; got %v", r, skipped)
		}
	}
	if len(skipped) != 3 {
		t.Errorf("skipped %d references, want 3 (a TraefikService is not a Kubernetes Service): %v", len(skipped), skipped)
	}

	// --ingress-controller names the controller for every route.
	flows, _ = RouteFlows(c, "web/api")
	for _, f := range flows {
		if f.Source.Name != "api" {
			t.Errorf("flow from %s, want every flow from the named controller", WorkloadName(f.Source))
		}
	}
	if _, skipped := RouteFlows(c, "nowhere/nothing"); len(skipped) == 0 || !strings.Contains(skipped[0].Reason, "no workload nowhere/nothing") {
		t.Errorf("an unknown --ingress-controller should skip each route, got %v", skipped)
	}
}

func TestImageRepository(t *testing.T) {
	for image, want := range map[string]string{
		"traefik":                       "traefik",
		"traefik:v3.1":                  "traefik",
		"docker.io/traefik:v3.1":        "docker.io/traefik",
		"localhost:5000/traefik:v3":     "localhost:5000/traefik",
		"localhost:5000/traefik":        "localhost:5000/traefik",
		"ghcr.io/x/traefik@sha256:abcd": "ghcr.io/x/traefik",
	} {
		if got := imageRepository(image); got != want {
			t.Errorf("imageRepository(%q) = %q, want %q", image, got, want)
		}
	}
	w := &WorkloadEntity{Images: []string{"traefik/whoami:v1"}}
	if traefikController.runs(w) {
		t.Error("traefik/whoami is not Traefik")
	}
}

func TestPlanIngress(t *testing.T) {
	c := refsFixture(t)
	flows, _ := RouteFlows(c, "")
	plan := PlanIngress(c, flows)

	if len(plan.Edits) != 1 {
		t.Fatalf("edits = %d, want 1 (argocd-server's policy)", len(plan.Edits))
	}
	e := plan.Edits[0]
	if e.Policy.Name != "argocd-server-network-policy" || e.Removed != 1 || len(e.Added) != 1 {
		t.Errorf("edit = %s removed %d added %d, want argocd-server-network-policy removing 1 and adding 1", e.Policy.Name, e.Removed, len(e.Added))
	}

	var created []string
	for _, cr := range plan.Creates {
		created = append(created, NamespaceOf(cr.Dest.Namespace)+"/"+cr.Name)
	}
	if got := strings.Join(created, ","); got != "web/allow-api-ingress,web/allow-web-ingress" {
		t.Errorf("creates = %s", got)
	}

	// argocd-server was open to every workload, and now admits only Traefik,
	// which it admitted already.
	var argocd *AccessChange
	for i := range plan.Access {
		if plan.Access[i].Dest.Name == "argocd-server" {
			argocd = &plan.Access[i]
		}
	}
	if argocd == nil || !argocd.WasOpen || len(argocd.Gains) != 0 || contains(names(argocd.Loses), "traefik") || !contains(names(argocd.Loses), "argocd-repo-server") {
		t.Errorf("argocd-server access change = %+v", argocd)
	}
	if len(plan.Blocked) != 0 {
		t.Errorf("blocked = %v, want none", flowStrings(plan.Blocked))
	}
}

// TestPlanIngressEditApplies runs the edit's expression through yq, as set-yq
// does, and checks that re-planning the result changes nothing.
func TestPlanIngressEditApplies(t *testing.T) {
	c := refsFixture(t)
	flows, _ := RouteFlows(c, "")
	e := PlanIngress(c, flows).Edits[0]

	unit := "apiVersion: v1\nkind: Service\nmetadata:\n  name: argocd-server-network-policy\n---\n" +
		"apiVersion: networking.k8s.io/v1\nkind: NetworkPolicy\nmetadata:\n  name: argocd-server-network-policy\n  namespace: argocd\n" +
		"spec:\n  podSelector:\n    matchLabels:\n      app.kubernetes.io/name: argocd-server\n  policyTypes:\n  - Ingress\n  ingress:\n  - {}\n" +
		"  - from:\n    - podSelector:\n        matchLabels:\n          app: prometheus\n    ports:\n    - port: 8083\n"
	out, err := yqkit.EvalYQExpression(e.YQ(), unit)
	if err != nil {
		t.Fatalf("eval %s: %v", e.YQ(), err)
	}
	docs := strings.Split(out, "\n---\n")
	if len(docs) != 2 || strings.Contains(docs[0], "ingress") {
		t.Fatalf("the expression changed a resource other than the policy:\n%s", out)
	}
	var policy map[string]any
	if err := yaml.Unmarshal([]byte(docs[1]), &policy); err != nil {
		t.Fatal(err)
	}
	ingress, _ := json.Marshal(nestedRecord(policy, "spec")["ingress"])
	want := `[{"from":[{"podSelector":{"matchLabels":{"app":"prometheus"}}}],"ports":[{"port":8083}]},` +
		`{"from":[{"namespaceSelector":{"matchLabels":{"kubernetes.io/metadata.name":"traefik"}},"podSelector":{"matchLabels":{"app.kubernetes.io/name":"traefik"}}}],"ports":[{"port":8080,"protocol":"TCP"}]}]`
	if string(ingress) != want {
		t.Errorf("ingress after the edit:\n%s\nwant:\n%s", ingress, want)
	}

	// The edited policy in the cluster: planning again changes nothing.
	for i, p := range c.NetworkPolicies {
		if p.Name == "argocd-server-network-policy" {
			edited := *p
			edited.Ingress = parseNetpolRules(nestedRecord(policy, "spec")["ingress"], "from")
			c.NetworkPolicies[i] = &edited
		}
	}
	if again := PlanIngress(c, flows); len(again.Edits) != 0 {
		t.Errorf("re-planning after the edit made %d edits, want none", len(again.Edits))
	}
}

func TestPlanIngressSharedPolicy(t *testing.T) {
	// A policy admitting every source selects both argocd-server and the repo
	// server, which no route reaches: editing it would cut the repo server off
	// too, so argocd-server is skipped.
	c := refsFixture(t,
		`{"apiVersion":"networking.k8s.io/v1","kind":"NetworkPolicy","metadata":{"name":"argocd-all","namespace":"argocd"},"spec":{"podSelector":{},"policyTypes":["Ingress"],"ingress":[{}]}}`,
	)
	flows, _ := RouteFlows(c, "")
	plan := PlanIngress(c, flows)
	for _, e := range plan.Edits {
		if NamespaceOf(e.Policy.Namespace) == "argocd" {
			t.Errorf("edited %s, want argocd left alone", e.Policy.Name)
		}
	}
	var reason string
	for _, s := range plan.Skipped {
		if s.Via == "argocd/argocd-server" {
			reason = s.Reason
		}
	}
	if !strings.Contains(reason, "argocd/argocd-all, which also selects argocd/argocd-repo-server") {
		t.Errorf("skip reason = %q", reason)
	}
}

func TestPlanIngressBlockedByEgress(t *testing.T) {
	c := refsFixture(t,
		`{"apiVersion":"networking.k8s.io/v1","kind":"NetworkPolicy","metadata":{"name":"traefik-egress","namespace":"traefik"},"spec":{"podSelector":{},"policyTypes":["Egress"],"egress":[{"to":[{"namespaceSelector":{"matchLabels":{"kubernetes.io/metadata.name":"argocd"}}}]}]}}`,
	)
	flows, _ := RouteFlows(c, "")
	plan := PlanIngress(c, flows)
	got := strings.Join(flowStrings(plan.Blocked), "\n")
	if got != "traefik/traefik -> web/web TCP 8000" {
		t.Errorf("blocked = %q, want only Traefik's flow to web, which its egress does not allow", got)
	}
}

func TestPlanIngressMergesPorts(t *testing.T) {
	// Two routes to two ports of one Service: one rule naming both ports.
	c := refsFixture(t,
		`{"apiVersion":"v1","kind":"Service","metadata":{"name":"web-admin","namespace":"web"},"spec":{"selector":{"app":"web"},"ports":[{"port":9000}]}}`,
		`{"apiVersion":"traefik.io/v1alpha1","kind":"IngressRoute","metadata":{"name":"admin","namespace":"web"},"spec":{"routes":[{"services":[{"name":"web-admin","port":9000}]}]}}`,
	)
	flows, _ := RouteFlows(c, "")
	for _, cr := range PlanIngress(c, flows).Creates {
		if cr.Name != "allow-web-ingress" {
			continue
		}
		if len(cr.Rules) != 1 || portsKey(cr.Rules[0].Ports) != "TCP 8000,TCP 9000" {
			t.Errorf("rules = %+v, want one rule on TCP 8000 and 9000", cr.Rules)
		}
		if !strings.Contains(cr.YAML(), "name: allow-web-ingress") {
			t.Errorf("YAML:\n%s", cr.YAML())
		}
		return
	}
	t.Error("no policy created for web")
}
