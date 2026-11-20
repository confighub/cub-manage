// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"strings"
	"testing"

	"github.com/confighub/cub-manage/internal/celrule/celruletest"
)

// A Service exposing metrics is selected by a ServiceMonitor on its cluster, in its namespace
// or one the monitor's namespaceSelector names.
func TestServiceMonitorCoverage(t *testing.T) {
	check := celruletest.Check(t, Pack, "observability-servicemonitor-coverage")
	onCluster := map[string]any{"TargetID": "4d6b2a1e-0000-4000-8000-000000000001"}
	api := "apiVersion: v1\nkind: Service\nmetadata:\n  name: api\n  namespace: shop\n  labels:\n    app: api\n" +
		"spec:\n  ports:\n    - name: Metrics\n      port: 9090\n"
	monitor := func(namespace, extra string) string {
		return "apiVersion: monitoring.coreos.com/v1\nkind: ServiceMonitor\nmetadata:\n  name: api\n  namespace: " + namespace +
			"\nspec:\n  selector:\n    matchLabels:\n      app: api\n" + extra
	}
	monitors := func(docs ...string) celruletest.Other { return celruletest.Other{"servicemonitors": docs} }

	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, monitors(monitor("shop", "")), check, api))
	result := celruletest.EvalCheckOther(t, onCluster, nil, monitors(monitor("monitoring", "")), check, api)
	celruletest.Expect(t, result, "metadata.labels")
	celruletest.ExpectResource(t, result, "shop/api", "v1/Service")
	// A namespaceSelector names the namespaces a monitor selects in.
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil,
		monitors(monitor("monitoring", "  namespaceSelector:\n    matchNames: [shop]\n")), check, api))
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil,
		monitors(monitor("monitoring", "  namespaceSelector:\n    any: true\n")), check, api))
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil,
		monitors(strings.Replace(monitor("shop", ""), "app: api", "app: web", 1)), check, api), "metadata.labels")

	// The scrape annotation marks a metrics Service too; a Service with neither is not one.
	scraped := "apiVersion: v1\nkind: Service\nmetadata:\n  name: web\n  namespace: shop\n  annotations:\n    prometheus.io/scrape: \"true\"\n"
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, nil, check, scraped), "metadata.labels")
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, nil, check, strings.Replace(api, "name: Metrics", "name: http", 1)))
	celruletest.Expect(t, celruletest.EvalCheckOther(t, map[string]any{}, nil, nil, check, api))
}

func TestUnrelatedResourcesPass(t *testing.T) {
	celruletest.UnrelatedResourcesPass(t, Pack)
}

func TestServiceMonitorSelects(t *testing.T) {
	check := celruletest.Check(t, Pack, "servicemonitor-selects")
	onCluster := map[string]any{"TargetID": "4d6b2a1e-0000-4000-8000-000000000001"}
	monitor := "apiVersion: monitoring.coreos.com/v1\nkind: ServiceMonitor\nmetadata:\n  name: api\n  namespace: monitoring\n" +
		"spec:\n  namespaceSelector:\n    matchNames: [shop]\n  selector:\n    matchLabels:\n      app: api\n"
	service := func(namespace, app string) string {
		return "apiVersion: v1\nkind: Service\nmetadata:\n  name: api\n  namespace: " + namespace + "\n  labels:\n    app: " + app + "\n"
	}
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, celruletest.Other{"services": {service("shop", "api")}}, check, monitor))
	result := celruletest.EvalCheckOther(t, onCluster, nil, celruletest.Other{"services": {service("monitoring", "api"), service("shop", "web")}}, check, monitor)
	celruletest.Expect(t, result, "spec.selector")
	celruletest.ExpectResource(t, result, "monitoring/api", "monitoring.coreos.com/v1/ServiceMonitor")
	celruletest.Expect(t, celruletest.EvalCheckOther(t, map[string]any{}, nil, nil, check, monitor))
}
