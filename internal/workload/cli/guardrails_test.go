// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/confighub/sdk/managerkit/guardrails"

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
		check string
		path  string
		value any
	}{
		{"workload-has-limits", "spec.template.spec.containers.?name=web.resources.limits.memory", nil},
		{"workload-runs-nonroot", "spec.template.spec.containers.?name=web.securityContext.runAsNonRoot", nil},
		{"workload-readonly-root-filesystem", "spec.template.spec.containers.?name=web.securityContext.readOnlyRootFilesystem", nil},
		{"workload-termination-message-policy", "spec.template.spec.containers.?name=web.terminationMessagePolicy", nil},
		{"workload-database-statefulset", "spec.template.spec.containers.?name=web.image", "postgres:16"},
	} {
		t.Run(tc.check, func(t *testing.T) {
			result := celruletest.EvalCheck(t, celruletest.Check(t, Pack, tc.check), twoContainers)
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
	for _, check := range Pack.Checks {
		// The database check's rule does not select StatefulSets.
		if check.Engine != guardrails.EngineCEL || len(check.ResourceTypes) > 0 && !slices.Contains(check.ResourceTypes, "apps/v1/StatefulSet") {
			continue
		}
		t.Run(check.Name, func(t *testing.T) {
			celruletest.Expect(t, celruletest.EvalCheck(t, check, compliant))
		})
	}
}

// A controller with no containers has none to pass.
func TestWorkloadRuleWithoutContainers(t *testing.T) {
	result := celruletest.EvalCheck(t, celruletest.Check(t, Pack, "workload-has-limits"), "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\nspec:\n  template:\n    spec: {}\n")
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
	result := celruletest.EvalCheck(t, celruletest.Check(t, Pack, "workload-readonly-root-filesystem"), writable)
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
	result := celruletest.EvalCheck(t, celruletest.Check(t, Pack, "workload-pdb-coverage"), annotated)
	celruletest.Expect(t, result, "metadata.annotations.workload~1confighub~1com/pdb-coverage")
	if len(result.FailedAttributes) == 1 && result.FailedAttributes[0].Value != "uncovered" {
		t.Errorf("value = %v, want the annotation's", result.FailedAttributes[0].Value)
	}
	// A bare annotations key is null, and passes.
	celruletest.Expect(t, celruletest.EvalCheck(t, celruletest.Check(t, Pack, "workload-pdb-coverage"), "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\n  annotations:\n"))
}

func TestUnrelatedResourcesPass(t *testing.T) {
	celruletest.UnrelatedResourcesPass(t, Pack)
}

// The Pod Security checks are the 17 of the standard, 11 in the baseline rule and 6 in the
// restricted one, each its own ID; run-as-non-root is left out unless asked for, since
// workload-runs-nonroot checks the same.
func TestPodSecurityChecks(t *testing.T) {
	rules, ids := map[string]int{}, map[string]bool{}
	for _, check := range podSecurityChecks {
		if check.DisplayName == "" || check.Description == "" {
			t.Errorf("check %s has no DisplayName or Description", check.Name)
		}
		if ids[check.PodSecurityCheck] {
			t.Errorf("Pod Security check %s is in two checks", check.PodSecurityCheck)
		}
		ids[check.PodSecurityCheck] = true
		rules[check.Rule+"/"+check.Level+"/"+check.Priority]++
		if check.Optional != (check.Name == "run-as-non-root") {
			t.Errorf("check %s: Optional = %v", check.Name, check.Optional)
		}
		if restricted := check.Level == "restricted"; restricted != slices.Equal(check.ExceptFrom, []string{"workload-runs-nonroot"}) {
			t.Errorf("check %s: ExceptFrom = %v", check.Name, check.ExceptFrom)
		}
		if !slices.Contains(check.ResourceTypes, "v1/Pod") || !slices.Contains(check.ResourceTypes, "batch/v1/CronJob") {
			t.Errorf("check %s selects %v, not every type with a pod spec", check.Name, check.ResourceTypes)
		}
	}
	want := map[string]int{
		podSecurityBaselineRule + "/baseline/" + guardrails.PriorityHigh:       11,
		podSecurityRestrictedRule + "/restricted/" + guardrails.PriorityMedium: 6,
	}
	if !maps.Equal(rules, want) {
		t.Errorf("rules = %v, want %v", rules, want)
	}
}
