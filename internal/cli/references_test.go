// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"strings"
	"testing"

	"github.com/confighub/cub-manage/internal/celrule/celruletest"
)

// onCluster is the FunctionContext of a Unit released to a Target.
var onCluster = map[string]any{"TargetID": "4d6b2a1e-0000-4000-8000-000000000001"}

const referencingDeployment = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: api
  namespace: shop
spec:
  template:
    metadata:
      labels: {app: api}
    spec:
      serviceAccountName: api
      containers:
        - name: app
          image: api:1.0
          env:
            - name: MODE
              valueFrom:
                configMapKeyRef: {name: settings, key: mode}
            - name: EXTRA
              valueFrom:
                configMapKeyRef: {name: extra, key: x, optional: true}
          envFrom:
            - configMapRef: {name: env}
      volumes:
        - name: cfg
          configMap: {name: files}
        - name: ca
          configMap: {name: kube-root-ca.crt}
        - name: data
          persistentVolumeClaim: {claimName: data}
`

func named(kind, apiVersion, namespace, name string) string {
	return "apiVersion: " + apiVersion + "\nkind: " + kind + "\nmetadata:\n  name: " + name + "\n  namespace: " + namespace + "\n"
}

func TestServiceAccountExists(t *testing.T) {
	check := celruletest.Check(t, basicPack, "service-account-exists")
	present := celruletest.Other{"serviceaccounts": {named("ServiceAccount", "v1", "shop", "api")}}
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, present, check, referencingDeployment))

	elsewhere := celruletest.Other{"serviceaccounts": {named("ServiceAccount", "v1", "billing", "api")}}
	result := celruletest.EvalCheckOther(t, onCluster, nil, elsewhere, check, referencingDeployment)
	celruletest.Expect(t, result, "spec.template.spec.serviceAccountName")
	celruletest.ExpectResource(t, result, "shop/api", "apps/v1/Deployment")

	cronJob := "apiVersion: batch/v1\nkind: CronJob\nmetadata:\n  name: report\n  namespace: shop\nspec:\n  jobTemplate:\n    spec:\n      template:\n" +
		"        metadata:\n          labels: {app: report}\n        spec:\n          serviceAccountName: report\n"
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, nil, check, cronJob),
		"spec.jobTemplate.spec.template.spec.serviceAccountName")
	// The default ServiceAccount is the cluster's, and a Unit with no Target is not checked.
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, nil, check,
		strings.Replace(referencingDeployment, "serviceAccountName: api", "serviceAccountName: default", 1)))
	celruletest.Expect(t, celruletest.EvalCheckOther(t, map[string]any{}, nil, nil, check, referencingDeployment))
}

func TestConfigMapExists(t *testing.T) {
	check := celruletest.Check(t, basicPack, "configmap-exists")
	all := celruletest.Other{"configmaps": {
		named("ConfigMap", "v1", "shop", "settings"), named("ConfigMap", "v1", "shop", "env"), named("ConfigMap", "v1", "shop", "files"),
	}}
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, all, check, referencingDeployment))

	// The optional reference and kube-root-ca.crt are not looked for.
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, nil, check, referencingDeployment),
		"spec.template.spec.volumes.?name=cfg.configMap.name",
		"spec.template.spec.containers.?name=app.env.?name=MODE.valueFrom.configMapKeyRef.name",
		"spec.template.spec.containers.?name=app.envFrom.0.configMapRef.name")
	celruletest.Expect(t, celruletest.EvalCheckOther(t, map[string]any{}, nil, nil, check, referencingDeployment))
}

func TestClaimExists(t *testing.T) {
	check := celruletest.Check(t, basicPack, "claim-exists")
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil,
		celruletest.Other{"claims": {named("PersistentVolumeClaim", "v1", "shop", "data")}}, check, referencingDeployment))
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, nil, check, referencingDeployment),
		"spec.template.spec.volumes.?name=data.persistentVolumeClaim.claimName")
}

func TestIngressBackendExists(t *testing.T) {
	check := celruletest.Check(t, basicPack, "ingress-backend-exists")
	ingress := `apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: shop
  namespace: shop
spec:
  defaultBackend:
    service: {name: web, port: {name: http}}
  rules:
    - host: shop.example.com
      http:
        paths:
          - path: /api
            pathType: Prefix
            backend:
              service: {name: api, port: {number: 8080}}
`
	service := func(name, ports string) string {
		return named("Service", "v1", "shop", name) + "spec:\n  ports: " + ports + "\n"
	}
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, celruletest.Other{"services": {
		service("web", "[{name: http, port: 80}]"), service("api", "[{name: http, port: 8080}]"),
	}}, check, ingress))

	result := celruletest.EvalCheckOther(t, onCluster, nil, celruletest.Other{"services": {
		service("web", "[{name: https, port: 443}]"),
	}}, check, ingress)
	celruletest.Expect(t, result, "spec.defaultBackend.service.port", "spec.rules.0.http.paths.0.backend.service.name")
}

func TestStatefulSetServiceExists(t *testing.T) {
	check := celruletest.Check(t, basicPack, "statefulset-service-exists")
	db := named("StatefulSet", "apps/v1", "shop", "db") + "spec:\n  serviceName: db\n"
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil,
		celruletest.Other{"services": {named("Service", "v1", "shop", "db")}}, check, db))
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, nil, check, db), "spec.serviceName")
	// A StatefulSet need not name one.
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, nil, check, named("StatefulSet", "apps/v1", "shop", "db")))
}

func TestServiceSelectsPods(t *testing.T) {
	check := celruletest.Check(t, basicPack, "service-selects-pods")
	workloads := celruletest.Other{"workloads": {referencingDeployment}}
	service := func(selector string) string {
		return named("Service", "v1", "shop", "api") + "spec:\n  selector: " + selector + "\n"
	}
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, workloads, check, service("{app: api}")))
	result := celruletest.EvalCheckOther(t, onCluster, nil, workloads, check, service("{app: api, tier: back}"))
	celruletest.Expect(t, result, "spec.selector")
	celruletest.ExpectResource(t, result, "shop/api", "v1/Service")
	// One with no selector, or an ExternalName, selects no pods by design.
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, workloads, check, named("Service", "v1", "shop", "api")))
	celruletest.Expect(t, celruletest.EvalCheckOther(t, onCluster, nil, workloads, check,
		named("Service", "v1", "shop", "api")+"spec:\n  type: ExternalName\n  selector: {app: gone}\n"))
}
