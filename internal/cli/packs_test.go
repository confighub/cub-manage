// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"slices"
	"testing"

	"github.com/confighub/sdk/managerkit/guardrails"

	"github.com/confighub/cub-manage/internal/celrule/celruletest"
	"github.com/confighub/cub-manage/internal/celrule/kubescapetest"
)

// failing is a check, a resource that fails it with the given parameters, and the paths at which
// it fails.
type failing struct {
	check, resource string
	params          map[string]string
	paths           []string
}

func expectFailures(t *testing.T, pack guardrails.Pack, cases []failing) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.check, func(t *testing.T) {
			celruletest.Expect(t, celruletest.EvalCheckWith(t, map[string]any{}, tc.params, celruletest.Check(t, pack, tc.check), tc.resource), tc.paths...)
		})
	}
}

func expectPasses(t *testing.T, pack guardrails.Pack, cases []failing) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.check, func(t *testing.T) {
			celruletest.Expect(t, celruletest.EvalCheckWith(t, map[string]any{}, tc.params, celruletest.Check(t, pack, tc.check), tc.resource))
		})
	}
}

const (
	nodePort     = "apiVersion: v1\nkind: Service\nmetadata:\n  name: web\nspec:\n  type: NodePort\n  externalIPs: [203.0.113.7]\n"
	loopback     = "apiVersion: v1\nkind: Service\nmetadata:\n  name: local\nspec:\n  type: ExternalName\n  externalName: localhost\n"
	openIngress  = "apiVersion: networking.k8s.io/v1\nkind: Ingress\nmetadata:\n  name: web\n  annotations:\n    nginx.ingress.kubernetes.io/server-snippet: 'location /x {}'\n    nginx.ingress.kubernetes.io/ssl-redirect: 'true'\nspec:\n  rules:\n    - http: {}\n    - host: '*'\n    - host: shop.example.com\n"
	gateway      = "apiVersion: gateway.networking.k8s.io/v1\nkind: Gateway\nmetadata:\n  name: edge\nspec:\n  listeners:\n    - name: http\n      protocol: HTTP\n      allowedRoutes:\n        namespaces:\n          from: All\n    - name: https\n      protocol: HTTPS\n"
	route        = "apiVersion: gateway.networking.k8s.io/v1\nkind: HTTPRoute\nmetadata:\n  name: web\nspec:\n  hostnames: ['*']\n"
	openRoute    = "apiVersion: traefik.io/v1alpha1\nkind: IngressRoute\nmetadata:\n  name: web\nspec:\n  entryPoints: [web]\n  routes:\n    - match: Host(`shop.example.com`)\n      middlewares: [{name: redirect-to-https}]\n    - match: PathPrefix(`/`)\n"
	tidyRoute    = "apiVersion: traefik.io/v1alpha1\nkind: IngressRoute\nmetadata:\n  name: web\nspec:\n  entryPoints: [websecure]\n  routes:\n    - match: Host(`shop.example.com`) && PathPrefix(`/api`)\n    - match: HostRegexp(`^.+\\.example\\.com$`)\n  tls:\n    secretName: shop-tls\n"
	tidyIngress  = "apiVersion: networking.k8s.io/v1\nkind: Ingress\nmetadata:\n  name: web\nspec:\n  ingressClassName: nginx\n  tls:\n    - hosts: [shop.example.com]\n  rules:\n    - host: shop.example.com\n"
	envSecret    = "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\nspec:\n  template:\n    spec:\n      containers:\n        - name: web\n          env:\n            - name: DB_PASSWORD\n              valueFrom:\n                secretKeyRef: {name: db, key: password}\n          envFrom:\n            - configMapRef: {name: settings}\n            - secretRef: {name: api}\n"
	labeled      = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: settings\n  namespace: default\n  labels:\n    owner: web\n    tier: frontend\n  annotations:\n    example.com/team: web\n    exceptions.guardrails.confighub.com/no-credentials-in-configmaps: test data\n"
	hostPathPV   = "apiVersion: v1\nkind: PersistentVolume\nmetadata:\n  name: local\nspec:\n  hostPath:\n    path: /data\n  flexVolume:\n    driver: example.com/lvm\n"
	statefulSet  = "apiVersion: apps/v1\nkind: StatefulSet\nmetadata:\n  name: db\nspec:\n  volumeClaimTemplates:\n    - metadata: {name: data}\n      spec: {storageClassName: gp2}\n    - metadata: {name: logs}\n      spec: {}\n  template:\n    spec:\n      volumes:\n        - name: legacy\n          flexVolume: {driver: example.com/lvm}\n"
	unclassified = "apiVersion: v1\nkind: PersistentVolumeClaim\nmetadata:\n  name: data\nspec: {}\n"
)

func TestExposureChecks(t *testing.T) {
	expectFailures(t, exposurePack, []failing{
		{"no-external-ips", nodePort, nil, []string{"spec.externalIPs"}},
		{"no-nodeport", nodePort, nil, []string{"spec.type"}},
		{"no-localhost-externalname", loopback, nil, []string{"spec.externalName"}},
		{"no-nginx-snippets", openIngress, nil, []string{"metadata.annotations.nginx~1ingress~1kubernetes~1io/server-snippet"}},
		{"ingress-tls", openIngress, nil, []string{"spec.tls"}},
		{"ingress-tls", gateway, nil, []string{"spec.listeners.1.tls"}},
		{"ingress-host", openIngress, nil, []string{"spec.rules.0.host", "spec.rules.1.host"}},
		{"ingress-host", route, nil, []string{"spec.hostnames"}},
		// The redirecting route passes; the one with no middleware and no host fails both.
		{"ingress-tls", openRoute, nil, []string{"spec.routes.1.middlewares"}},
		{"ingress-host", openRoute, nil, []string{"spec.routes.1.match"}},
		{"gateway-allowed-routes", gateway, nil, []string{"spec.listeners.0.allowedRoutes.namespaces.from"}},
		{"no-loadbalancer", "apiVersion: v1\nkind: Service\nmetadata:\n  name: lb\nspec:\n  type: LoadBalancer\n", nil, []string{"spec.type"}},
		{"ingress-class", openIngress, map[string]string{"allowed": "nginx"}, []string{"spec.ingressClassName"}},
	})
	expectPasses(t, exposurePack, []failing{
		{"ingress-tls", tidyIngress, nil, nil},
		{"ingress-host", tidyIngress, nil, nil},
		{"ingress-tls", tidyRoute, nil, nil},
		{"ingress-host", tidyRoute, nil, nil},
		{"ingress-class", tidyIngress, map[string]string{"allowed": "nginx, traefik"}, nil},
		{"no-nginx-snippets", tidyIngress, nil, nil},
		{"no-nodeport", loopback, nil, nil},
	})
}

func TestSecretsChecks(t *testing.T) {
	expectFailures(t, secretsPack, []failing{
		{"no-secret-resources", "apiVersion: v1\nkind: Secret\nmetadata:\n  name: db\n", nil, []string{"kind"}},
		{"secrets-files-not-env", envSecret, nil, []string{
			"spec.template.spec.containers.?name=web.env.?name=DB_PASSWORD.valueFrom.secretKeyRef",
			"spec.template.spec.containers.?name=web.envFrom.1.secretRef"}},
	})
	// Both credential checks run Kubescape's C-0012, on env and on ConfigMaps.
	kubescapetest.RunUpstreamTests(t, "../../third_party/kubescape/cel-admission-library",
		[]guardrails.Check{celruletest.Check(t, secretsPack, "no-credentials-in-env")})
}

func TestMetadataChecks(t *testing.T) {
	expectFailures(t, metadataPack, []failing{
		{"not-default-namespace", labeled, nil, []string{"metadata.namespace"}},
		{"required-labels", labeled, map[string]string{"keys": "owner, tier=^(web|batch)$, app.kubernetes.io/name"},
			[]string{"metadata.labels.tier", "metadata.labels.app~1kubernetes~1io/name"}},
		{"required-annotations", labeled, map[string]string{"keys": "example.com/team=^web$,example.com/oncall"},
			[]string{"metadata.annotations.example~1com/oncall"}},
		{"allowed-annotations", labeled, map[string]string{"prefixes": "kubernetes.io/"}, []string{"metadata.annotations.example~1com/team"}},
	})
	expectPasses(t, metadataPack, []failing{
		{"required-labels", labeled, map[string]string{"keys": "owner,tier=^front"}, nil},
		{"allowed-annotations", labeled, map[string]string{"prefixes": "example.com/"}, nil},
		// With no parameters, nothing is required.
		{"required-labels", labeled, nil, nil},
		{"not-default-namespace", "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: default\n", nil, nil},
	})
}

func TestStorageChecks(t *testing.T) {
	expectFailures(t, storagePack, []failing{
		{"no-hostpath-volumes", hostPathPV, nil, []string{"spec.hostPath"}},
		{"no-flexvolume", hostPathPV, nil, []string{"spec.flexVolume"}},
		{"no-flexvolume", statefulSet, nil, []string{"spec.template.spec.volumes.?name=legacy.flexVolume"}},
		{"storage-class-set", unclassified, nil, []string{"spec.storageClassName"}},
		{"storage-class-set", statefulSet, nil, []string{"spec.volumeClaimTemplates.1.spec.storageClassName"}},
		{"allowed-storage-classes", statefulSet, map[string]string{"classes": "gp3"}, []string{"spec.volumeClaimTemplates.0.spec.storageClassName"}},
	})
	expectPasses(t, storagePack, []failing{
		{"allowed-storage-classes", statefulSet, map[string]string{"classes": "gp2,gp3"}, nil},
		{"allowed-storage-classes", unclassified, map[string]string{"classes": "gp3"}, nil},
	})
}

// Each check leaves alone resources it is not about; the test's are a ConfigMap and a Secret, so
// no-secret-resources, which is about Secrets, is left out.
func TestNewPacksLeaveUnrelatedResourcesAlone(t *testing.T) {
	for _, tp := range toolPacks {
		p := tp.pack
		p.Checks = slices.DeleteFunc(slices.Clone(p.Checks), func(c guardrails.Check) bool { return c.Name == "no-secret-resources" })
		celruletest.UnrelatedResourcesPass(t, p)
	}
}
