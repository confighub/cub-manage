// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package netpol

import (
	"encoding/json"
	"strings"
	"testing"
)

// scrapeFixture is a cluster with three scrape targets: an app annotated for
// scraping with a metrics port, one whose annotation names its port, and one
// behind a Service; and an app that is not annotated.
func scrapeFixture(t *testing.T, scraperConfig map[string]string) *ClusterNetpol {
	t.Helper()
	data, err := json.Marshal(scraperConfig)
	if err != nil {
		t.Fatal(err)
	}
	return buildFixture(t, []string{
		`{"apiVersion":"apps/v1","kind":"DaemonSet","metadata":{"name":"scraper","namespace":"obs"},"spec":{"selector":{"matchLabels":{"app":"scraper"}},"template":{"metadata":{"labels":{"app":"scraper"}},"spec":{"containers":[{"name":"scraper","image":"x"}],"volumes":[{"name":"c","configMap":{"name":"scraper-config"}}]}}}}`,
		`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"scraper-config","namespace":"obs"},"data":` + string(data) + `}`,
		`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"bot","namespace":"bot"},"spec":{"selector":{"matchLabels":{"app":"bot"}},"template":{"metadata":{"labels":{"app":"bot"},"annotations":{"prometheus.io/scrape":"true","prometheus.io/path":"/metrics"}},"spec":{"containers":[{"name":"bot","image":"x","ports":[{"name":"http","containerPort":8080},{"name":"metrics","containerPort":9090}]}]}}}}`,
		`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"web","namespace":"web"},"spec":{"selector":{"matchLabels":{"app":"web"}},"template":{"metadata":{"labels":{"app":"web"},"annotations":{"prometheus.io/scrape":"true","prometheus.io/port":"9102"}},"spec":{"containers":[{"name":"web","image":"x","ports":[{"name":"http","containerPort":8080}]}]}}}}`,
		`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"quiet","namespace":"bot"},"spec":{"selector":{"matchLabels":{"app":"quiet"}},"template":{"metadata":{"labels":{"app":"quiet"}},"spec":{"containers":[{"name":"quiet","image":"x","ports":[{"name":"metrics","containerPort":9090}]}]}}}}`,
		`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"ksm","namespace":"obs"},"spec":{"selector":{"matchLabels":{"app":"ksm"}},"template":{"metadata":{"labels":{"app":"ksm"}},"spec":{"containers":[{"name":"ksm","image":"x","ports":[{"name":"http","containerPort":8080},{"name":"telemetry","containerPort":8081}]}]}}}}`,
		`{"apiVersion":"v1","kind":"Service","metadata":{"name":"kube-state-metrics","namespace":"obs"},"spec":{"selector":{"app":"ksm"},"ports":[{"name":"http","port":8080,"targetPort":"http"},{"name":"telemetry","port":8081,"targetPort":"telemetry"}]}}`,
	})
}

func scrapeStrings(flows []Flow) string {
	var out []string
	for _, f := range flows {
		out = append(out, f.Via[strings.LastIndex(f.Via, "job ")+4:]+": "+WorkloadName(f.Source)+" -> "+WorkloadName(f.Dest)+" "+portsKey(f.Ports))
	}
	return strings.Join(out, "\n")
}

func TestScrapeFlowsPrometheus(t *testing.T) {
	c := scrapeFixture(t, map[string]string{"scrape.yml": `
scrape_configs:
  - job_name: annotated-pods
    kubernetes_sd_configs:
      - role: pod
    relabel_configs:
      - source_labels: [__meta_kubernetes_pod_annotation_prometheus_io_scrape]
        action: keep
        regex: "true"
      - source_labels: [__address__, __meta_kubernetes_pod_annotation_prometheus_io_port]
        action: replace
        regex: ([^:]+)(?::\d+)?;(\d+)
        replacement: $1:$2
        target_label: __address__
      - source_labels: [__meta_kubernetes_pod_container_port_name, __meta_kubernetes_pod_annotation_prometheus_io_port]
        regex: "metrics;|.*;.+"
        action: keep
  - job_name: kube-state-metrics
    kubernetes_sd_configs:
      - role: endpoints
        namespaces:
          names: [obs]
    relabel_configs:
      - source_labels: [__meta_kubernetes_service_name]
        action: keep
        regex: kube-state-metrics
      - source_labels: [__meta_kubernetes_endpoint_port_name]
        action: keep
        regex: http
`})
	flows, skipped := ScrapeFlows(c)
	want := strings.Join([]string{
		// bot's metrics port; its http port is dropped by the last keep.
		"annotated-pods: obs/scraper -> bot/bot TCP 9090",
		// web's annotation names its port, which the address is rewritten to.
		"annotated-pods: obs/scraper -> web/web TCP 9102",
		"kube-state-metrics: obs/scraper -> obs/ksm TCP 8080",
	}, "\n")
	if got := scrapeStrings(flows); got != want {
		t.Errorf("flows:\n%s\nwant:\n%s", got, want)
	}
	if len(skipped) != 0 {
		t.Errorf("skipped: %v", skipped)
	}
}

func TestScrapeFlowsAlloy(t *testing.T) {
	c := scrapeFixture(t, map[string]string{"config.alloy": `
// Logs: tailed from files, which connects to no pod.
discovery.kubernetes "pods" {
  role = "pod"
}

discovery.relabel "pods" {
  targets = discovery.kubernetes.pods.targets
  rule {
    source_labels = ["__meta_kubernetes_namespace"]
    target_label  = "namespace"
  }
}

local.file_match "pods" {
  path_targets = discovery.relabel.pods.output
}

discovery.kubernetes "metrics_pods" {
  role = "pod"
  namespaces {
    names = ["bot", "web"]
  }
  selectors {
    role  = "pod"
    field = "spec.nodeName=" + env("HOSTNAME")
  }
}

discovery.relabel "metrics_pods" {
  targets = discovery.kubernetes.metrics_pods.targets

  rule {
    source_labels = ["__meta_kubernetes_pod_annotation_prometheus_io_scrape"]
    regex         = "true"
    action        = "keep"
  }

  // only the port named metrics
  rule {
    source_labels = ["__meta_kubernetes_pod_container_port_name"]
    regex         = "metrics"
    action        = "keep"
  }
}

prometheus.scrape "metrics_pods" {
  targets         = discovery.relabel.metrics_pods.output
  scrape_interval = "30s"
  forward_to      = [prometheus.remote_write.prod.receiver]
}

prometheus.remote_write "prod" {
  endpoint {
    url = "https://metrics.example.com/api/v1/write"
  }
}
`})
	flows, skipped := ScrapeFlows(c)
	// web has no port named metrics, and quiet is not annotated.
	if got, want := scrapeStrings(flows), "metrics_pods: obs/scraper -> bot/bot TCP 9090"; got != want {
		t.Errorf("flows:\n%s\nwant:\n%s", got, want)
	}
	if len(skipped) != 0 {
		t.Errorf("skipped: %v", skipped)
	}
}

func TestSamePolicy(t *testing.T) {
	c := refsFixture(t)
	flows, _ := RouteFlows(c, "")
	var create *PolicyCreate
	for _, cr := range PlanIngress(c, flows).Creates {
		if cr.Name == "allow-web-ingress" {
			create = cr
		}
	}
	if create == nil {
		t.Fatal("no create for web")
	}
	parse := func(yaml string) *NetworkPolicyEntity {
		var doc any
		if err := json.Unmarshal([]byte(yaml), &doc); err != nil {
			t.Fatal(err)
		}
		return BuildFleet([]FleetResource{{Doc: doc}})[""].NetworkPolicies[0]
	}
	same := parse(`{"apiVersion":"networking.k8s.io/v1","kind":"NetworkPolicy","metadata":{"name":"allow-web-ingress","namespace":"web"},"spec":{"podSelector":{"matchLabels":{"app":"web"}},"policyTypes":["Ingress"],"ingress":[{"from":[{"namespaceSelector":{"matchLabels":{"kubernetes.io/metadata.name":"traefik"}},"podSelector":{"matchLabels":{"app.kubernetes.io/name":"traefik"}}}],"ports":[{"port":8000,"protocol":"TCP"}]}]}}`)
	if !SamePolicy(same, create) {
		t.Error("the policy as planned is not the same")
	}
	fewer := parse(`{"apiVersion":"networking.k8s.io/v1","kind":"NetworkPolicy","metadata":{"name":"allow-web-ingress","namespace":"web"},"spec":{"podSelector":{"matchLabels":{"app":"web"}},"policyTypes":["Ingress"],"ingress":[]}}`)
	if SamePolicy(fewer, create) || SamePolicy(nil, create) {
		t.Error("a policy with other rules, or none, is the same")
	}
}
