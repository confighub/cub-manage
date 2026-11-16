// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"testing"

	"github.com/confighub/cub-manage/internal/celrule/celruletest"
)

const violating = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: bad
  namespace: shop
spec:
  replicas: 1
  strategy:
    type: Recreate
  template:
    spec:
      securityContext:
        runAsGroup: 0
        fsGroup: 0
        supplementalGroups: [0]
      volumes:
        - name: sock
          hostPath:
            path: /var/run/docker.sock
        - name: etc
          hostPath:
            path: /etc/
        - name: cache
          emptyDir:
            medium: Memory
      initContainers:
        - name: init
          image: k8s.gcr.io/pause:3.9
      containers:
        - name: app
          image: nginx
          ports:
            - containerPort: 22
            - containerPort: 8080
          livenessProbe:
            httpGet:
              path: /
              port: 8080
          readinessProbe:
            httpGet:
              path: /
              port: 8080
          volumeMounts:
            - name: etc
              mountPath: /host/etc
          securityContext:
            runAsGroup: 0
          resources:
            requests:
              memory: 1Gi
            limits:
              memory: 2Gi
`

const (
	app   = "spec.template.spec.containers.?name=app."
	initC = "spec.template.spec.initContainers.?name=init."
)

// Each check fails the resource at the fields that violate it, and names the resource.
func TestMoreWorkloadChecksFail(t *testing.T) {
	prod := map[string]any{"SpaceLabels": map[string]any{"Environment": "Prod"}}
	for _, tc := range []struct {
		check    string
		resource string
		context  map[string]any
		params   map[string]string
		paths    []string
	}{
		{"no-runtime-socket", violating, nil, nil, []string{"spec.template.spec.volumes.?name=sock.hostPath.path"}},
		{"no-sensitive-host-paths", violating, nil, nil, []string{"spec.template.spec.volumes.?name=etc.hostPath.path"}},
		{"readonly-host-paths", violating, nil, nil, []string{app + "volumeMounts.?mountPath=/host/etc.readOnly"}},
		{"drop-net-raw", violating, nil, nil, []string{app + "securityContext.capabilities.drop"}},
		{"non-root-groups", violating, nil, nil, []string{"spec.template.spec.securityContext.runAsGroup", "spec.template.spec.securityContext.fsGroup",
			"spec.template.spec.securityContext.supplementalGroups", app + "securityContext.runAsGroup"}},
		{"dedicated-service-account", violating, nil, nil, []string{"spec.template.spec.serviceAccountName"}},
		{"no-ssh-port", violating, nil, nil, []string{app + "ports.?containerPort=22.containerPort"}},
		{"high-uid", violating, nil, nil, []string{app + "securityContext.runAsUser", app + "securityContext.runAsGroup"}},
		{"requests", violating, nil, nil, []string{app + "resources.requests.cpu"}},
		{"emptydir-size-limit", violating, nil, nil, []string{"spec.template.spec.volumes.?name=cache.emptyDir.sizeLimit"}},
		{"ephemeral-storage", violating, nil, nil, []string{app + "resources.requests.ephemeral-storage", app + "resources.limits.ephemeral-storage"}},
		{"memory-request-equals-limit", violating, nil, nil, []string{app + "resources.requests.memory"}},
		{"probes-differ", violating, nil, nil, []string{app + "livenessProbe"}},
		{"replicas", violating, prod, nil, []string{"spec.replicas"}},
		{"rolling-update", violating, nil, nil, []string{"spec.strategy.type"}},
		{"priority-class", violating, nil, map[string]string{"allowed": "high"}, []string{"spec.template.spec.priorityClassName"}},
		{"allowed-registries", violating, nil, map[string]string{"registries": "ghcr.io, registry.k8s.io"}, []string{app + "image", initC + "image"}},
		{"deprecated-registries", violating, nil, nil, []string{initC + "image"}},
		{"image-digest", violating, nil, nil, []string{app + "image", initC + "image"}},
		{"readiness-probe", "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\nspec:\n  template:\n    spec:\n      containers:\n        - name: web\n          ports:\n            - containerPort: 80\n", nil, nil,
			[]string{"spec.template.spec.containers.?name=web.readinessProbe"}},
		{"no-bare-pods", "apiVersion: v1\nkind: Pod\nmetadata:\n  name: web\n", nil, nil, []string{"kind"}},
		{"job-cleanup", "apiVersion: batch/v1\nkind: Job\nmetadata:\n  name: once\n", nil, nil, []string{"spec.ttlSecondsAfterFinished"}},
		{"job-cleanup", "apiVersion: batch/v1\nkind: CronJob\nmetadata:\n  name: nightly\nspec:\n  schedule: '0 0 * * *'\n", nil, nil,
			[]string{"spec.startingDeadlineSeconds", "spec.concurrencyPolicy"}},
		{"pdb-unhealthy-eviction", "apiVersion: policy/v1\nkind: PodDisruptionBudget\nmetadata:\n  name: web\nspec:\n  maxUnavailable: 1\n", nil, nil,
			[]string{"spec.unhealthyPodEvictionPolicy"}},
	} {
		t.Run(tc.check, func(t *testing.T) {
			context := tc.context
			if context == nil {
				context = map[string]any{}
			}
			celruletest.Expect(t, celruletest.EvalCheckWith(t, context, tc.params, celruletest.Check(t, Pack, tc.check), tc.resource), tc.paths...)
		})
	}
}

// Some resources pass checks that might seem to apply to them.
func TestMoreWorkloadChecksPass(t *testing.T) {
	for _, tc := range []struct {
		check, resource string
		context         map[string]any
	}{
		// A Job a CronJob owns is cleaned up with it.
		{"job-cleanup", "apiVersion: batch/v1\nkind: Job\nmetadata:\n  name: run\n  ownerReferences:\n    - kind: CronJob\n      name: nightly\n", nil},
		// The minimum applies in the listed environments only, and a workload setting no replicas is scaled by an autoscaler.
		{"replicas", violating, map[string]any{"SpaceLabels": map[string]any{"Environment": "dev"}}},
		{"replicas", "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\n", map[string]any{"SpaceLabels": map[string]any{"Environment": "prod"}}},
		{"replicas", violating, map[string]any{"SpaceLabels": nil}},
		// A subdirectory of a sensitive one, mounted read-only, passes.
		{"no-sensitive-host-paths", "apiVersion: apps/v1\nkind: DaemonSet\nmetadata:\n  name: agent\nspec:\n  template:\n    spec:\n      volumes:\n        - name: certs\n          hostPath:\n            path: /etc/ssl/certs\n", nil},
		// An emptyDir written as a bare key is unlimited, and fails; one with a sizeLimit passes.
		{"emptydir-size-limit", "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\nspec:\n  template:\n    spec:\n      volumes:\n        - name: tmp\n          emptyDir:\n            sizeLimit: 64Mi\n", nil},
	} {
		t.Run(tc.check, func(t *testing.T) {
			context := tc.context
			if context == nil {
				context = map[string]any{}
			}
			celruletest.Expect(t, celruletest.EvalCheckWith(t, context, nil, celruletest.Check(t, Pack, tc.check), tc.resource))
		})
	}
	// A bare emptyDir key is null, and fails without an error.
	bare := "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\nspec:\n  template:\n    spec:\n      volumes:\n        - name: tmp\n          emptyDir:\n"
	celruletest.Expect(t, celruletest.EvalCheck(t, celruletest.Check(t, Pack, "emptydir-size-limit"), bare), "spec.template.spec.volumes.?name=tmp.emptyDir.sizeLimit")
}
