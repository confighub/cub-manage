// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package netpol

import (
	"fmt"
	"strings"
	"testing"
)

func TestServiceAddresses(t *testing.T) {
	for text, want := range map[string]string{
		"url: http://victoriametrics.observability.svc:8428":            "http victoriametrics observability 8428 qualified",
		"url: http://loki.observability.svc.cluster.local:3100/loki":    "http loki observability 3100 qualified",
		"--server=temporal-frontend.temporal:7233":                      "- temporal-frontend temporal 7233",
		"endpoint = \"http://loki:3100/loki/api/v1/push\"":              "http loki own 3100",
		"postgres://user@db.app.svc/app":                                "postgres db app - qualified",
		"https://hub.confighub.com/api":                                 "",
		"uid: victoriametrics":                                          "",
		"name: VictoriaMetrics":                                         "",
		"see /docs/loki.observability.svc for details":                  "",
		"example.com:443 and redis:6379":                                "- example com 443; - redis own 6379",
		"two: http://a.ns.svc:1, http://b.ns.svc.cluster.local:2 done.": "http a ns 1 qualified; http b ns 2 qualified",
	} {
		var got []string
		for _, a := range serviceAddresses(text, "own") {
			s := fmt.Sprintf("%s %s %s %s", dash(a.scheme), a.service, a.namespace, dash(a.port))
			if a.qualified {
				s += " qualified"
			}
			got = append(got, s)
		}
		if g := strings.Join(got, "; "); g != want {
			t.Errorf("serviceAddresses(%q) = %q, want %q", text, g, want)
		}
	}
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func TestConfigFlows(t *testing.T) {
	c := buildFixture(t, []string{
		// Grafana finds datasources by label with a k8s-sidecar, and mounts its own config.
		`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"grafana","namespace":"obs"},"spec":{"selector":{"matchLabels":{"app":"grafana"}},"template":{"metadata":{"labels":{"app":"grafana"}},"spec":{"containers":[{"name":"grafana","image":"grafana/grafana:11"},{"name":"sc-datasources","image":"kiwigrid/k8s-sidecar:1.27.5","env":[{"name":"LABEL","value":"grafana_datasource"},{"name":"NAMESPACE","value":"ALL"}]}]}}}}`,
		`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"ds-metrics","namespace":"other","labels":{"grafana_datasource":"true"}},"data":{"ds.yaml":"datasources:\n  - url: http://metrics.obs.svc:8428\n"}}`,
		`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"unlabeled","namespace":"obs"},"data":{"ds.yaml":"url: http://logs.obs.svc:3100\n"}}`,
		// A log shipper mounting its config, and an app naming a Service in its env.
		`{"apiVersion":"apps/v1","kind":"DaemonSet","metadata":{"name":"shipper","namespace":"obs"},"spec":{"selector":{"matchLabels":{"app":"shipper"}},"template":{"metadata":{"labels":{"app":"shipper"}},"spec":{"containers":[{"name":"shipper","image":"x"}],"volumes":[{"name":"c","configMap":{"name":"shipper-config"}}]}}}}`,
		`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"shipper-config","namespace":"obs"},"data":{"config.alloy":"loki.write \"x\" { endpoint { url = \"http://logs:3100/push\" } }\nremote = \"https://kubernetes.default.svc\"\nother = \"http://gone.obs.svc:80\"\n"}}`,
		`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"app","namespace":"app"},"spec":{"selector":{"matchLabels":{"app":"app"}},"template":{"metadata":{"labels":{"app":"app"}},"spec":{"containers":[{"name":"app","image":"x","env":[{"name":"METRICS","value":"metrics.obs:9999"},{"name":"LOGS","value":"logs.obs.svc"}]}]}}}}`,
		// The Services, and the workloads behind them.
		`{"apiVersion":"apps/v1","kind":"StatefulSet","metadata":{"name":"metrics","namespace":"obs"},"spec":{"selector":{"matchLabels":{"app":"metrics"}},"template":{"metadata":{"labels":{"app":"metrics"}}}}}`,
		`{"apiVersion":"v1","kind":"Service","metadata":{"name":"metrics","namespace":"obs"},"spec":{"selector":{"app":"metrics"},"ports":[{"port":8428}]}}`,
		`{"apiVersion":"apps/v1","kind":"StatefulSet","metadata":{"name":"logs","namespace":"obs"},"spec":{"selector":{"matchLabels":{"app":"logs"}},"template":{"metadata":{"labels":{"app":"logs"}},"spec":{"containers":[{"name":"logs","image":"x","ports":[{"name":"http","containerPort":3100}]}]}}}}`,
		`{"apiVersion":"v1","kind":"Service","metadata":{"name":"logs","namespace":"obs"},"spec":{"selector":{"app":"logs"},"ports":[{"name":"http","port":3100,"targetPort":"http"},{"name":"grpc","port":9095}]}}`,
	})
	flows, skipped := ConfigFlows(c)
	var got []string
	for _, f := range flows {
		got = append(got, f.Via+": "+WorkloadName(f.Source)+" -> "+WorkloadName(f.Dest)+" "+portsKey(f.Ports))
	}
	want := []string{
		// grafana finds ds-metrics in another namespace; the unlabeled ConfigMap is not its.
		"ConfigMap other/ds-metrics, found by sc-datasources: obs/grafana -> obs/metrics TCP 8428",
		// The shipper's mounted config names logs without a namespace: its own.
		"ConfigMap obs/shipper-config: obs/shipper -> obs/logs TCP 3100",
		// An address with no port or scheme goes to every port.
		"Deployment app/app: app/app -> obs/logs TCP 3100,TCP 9095",
	}
	if g, w := strings.Join(got, "\n"), strings.Join(want, "\n"); g != w {
		t.Errorf("flows:\n%s\nwant:\n%s", g, w)
	}
	var reasons []string
	for _, s := range skipped {
		reasons = append(reasons, s.Via+": "+s.Reason)
	}
	// The API server's Service is never reported; a Service ConfigHub lacks is, once.
	wantSkips := []string{
		"ConfigMap obs/shipper-config: names Service obs/gone, which is not in ConfigHub",
		"Deployment app/app: names Service obs/metrics on port 9999, which it does not have",
	}
	if g, w := strings.Join(reasons, "\n"), strings.Join(wantSkips, "\n"); g != w {
		t.Errorf("skipped:\n%s\nwant:\n%s", g, w)
	}
}
