// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"strings"
	"testing"

	"github.com/confighub/cub-manage/internal/celrule/celruletest"
)

const twoContainers = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  namespace: shop
spec:
  template:
    spec:
      containers:
        - name: web
          image: postgres:16
          resources:
            limits:
              cpu: "1"
        - name: sidecar
          image: busybox:1.36
          terminationMessagePolicy: FallbackToLogsOnError
          securityContext:
            runAsNonRoot: true
            readOnlyRootFilesystem: true
          resources:
            limits:
              memory: 64Mi
`

// Each workload rule names the container that fails it, at the field to fix.
func TestWorkloadRulesNameTheFailingContainer(t *testing.T) {
	for _, tc := range []struct {
		name       string
		expression string
		path       string
		value      any
	}{
		{"has-limits", celHasLimits, "spec.template.spec.containers.?name=web.resources.limits.memory", nil},
		{"runs-nonroot", celRunsNonRoot, "spec.template.spec.containers.?name=web.securityContext.runAsNonRoot", nil},
		{"readonly-root-filesystem", celReadOnlyRootFS, "spec.template.spec.containers.?name=web.securityContext.readOnlyRootFilesystem", nil},
		{"termination-message-policy", celTerminationMsg, "spec.template.spec.containers.?name=web.terminationMessagePolicy", nil},
		{"database-statefulset", celDatabaseStateful, "spec.template.spec.containers.?name=web.image", "postgres:16"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := celruletest.Eval(t, tc.expression, twoContainers)
			celruletest.Expect(t, result, tc.path)
			celruletest.ExpectResource(t, result, "shop/web", "apps/v1/Deployment")
			if len(result.FailedAttributes) == 1 && result.FailedAttributes[0].Value != tc.value {
				t.Errorf("value = %v, want %v", result.FailedAttributes[0].Value, tc.value)
			}
			if len(result.Details) == 1 && !strings.Contains(result.Details[0], "container web") {
				t.Errorf("detail %q does not name the container", result.Details[0])
			}
		})
	}
}

func TestWorkloadRulesPass(t *testing.T) {
	compliant := `apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: db
spec:
  template:
    spec:
      securityContext:
        runAsNonRoot: true
      containers:
        - name: db
          image: postgres:16
          terminationMessagePolicy: FallbackToLogsOnError
          securityContext:
            readOnlyRootFilesystem: true
          resources:
            limits:
              memory: 1Gi
`
	for name, expression := range map[string]string{
		"has-limits": celHasLimits, "runs-nonroot": celRunsNonRoot, "readonly-root-filesystem": celReadOnlyRootFS,
		"termination-message-policy": celTerminationMsg, "database-statefulset": celDatabaseStateful,
		"pdb-coverage": celNoPDBFinding,
	} {
		t.Run(name, func(t *testing.T) {
			celruletest.Expect(t, celruletest.Eval(t, expression, compliant))
		})
	}
	// Not a controller: nothing to check.
	celruletest.Expect(t, celruletest.Eval(t, celHasLimits, "apiVersion: v1\nkind: Service\nmetadata:\n  name: web\n"))
}

// A controller with no containers has none to pass.
func TestWorkloadRuleWithoutContainers(t *testing.T) {
	result := celruletest.Eval(t, celHasLimits, "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\nspec:\n  template:\n    spec: {}\n")
	celruletest.Expect(t, result, "spec.template.spec.containers")
	celruletest.ExpectResource(t, result, "/web", "apps/v1/Deployment")
}

// A container that sets readOnlyRootFilesystem: false fails, with the value it
// set; the pod's securityContext has no such field to cover it.
func TestReadOnlyRootFilesystemExplicitFalse(t *testing.T) {
	writable := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  namespace: shop
spec:
  template:
    spec:
      securityContext:
        runAsNonRoot: true
      containers:
        - name: web
          image: nginx:1.27
          securityContext:
            readOnlyRootFilesystem: false
`
	result := celruletest.Eval(t, celReadOnlyRootFS, writable)
	celruletest.Expect(t, result, "spec.template.spec.containers.?name=web.securityContext.readOnlyRootFilesystem")
	if len(result.FailedAttributes) == 1 && result.FailedAttributes[0].Value != false {
		t.Errorf("value = %v, want false", result.FailedAttributes[0].Value)
	}
}

func TestPDBCoverageFinding(t *testing.T) {
	annotated := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  namespace: shop
  annotations:
    workload.confighub.com/pdb-coverage: uncovered
`
	result := celruletest.Eval(t, celNoPDBFinding, annotated)
	celruletest.Expect(t, result, "metadata.annotations.workload~1confighub~1com/pdb-coverage")
	if len(result.FailedAttributes) == 1 && result.FailedAttributes[0].Value != "uncovered" {
		t.Errorf("value = %v, want the annotation's", result.FailedAttributes[0].Value)
	}
	// A bare annotations key is null, and passes.
	celruletest.Expect(t, celruletest.Eval(t, celNoPDBFinding, "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\n  annotations:\n"))
}

func TestUnrelatedResourcesPass(t *testing.T) {
	celruletest.UnrelatedResourcesPass(t, Pack.Rules)
}
