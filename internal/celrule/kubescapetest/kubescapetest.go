// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

// Package kubescapetest runs the upstream tests of the Kubescape policies a pack adopts.
package kubescapetest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/confighub/sdk/configkit/k8skit"
	"github.com/confighub/sdk/core/function/api"
	"github.com/confighub/sdk/core/third_party/gaby"
	"github.com/confighub/sdk/function-impl/kubernetes"
	"github.com/confighub/sdk/managerkit/guardrails"
	"sigs.k8s.io/yaml"

	celadmission "github.com/confighub/cub-manage/third_party/kubescape/cel-admission-library"
)

// kubescapeTest is one of a control's upstream tests: a resource from test-resources with fields
// changed, the parameters it runs with, and whether the policy denies it.
type kubescapeTest struct {
	Name          string   `json:"name"`
	Template      string   `json:"template"`
	ParamTemplate string   `json:"param_template"`
	Changes       []string `json:"field_change_list"`
	Expected      string   `json:"expected"`
}

// RunUpstreamTests runs each check's control's upstream tests, from the library at dir: through
// vet-validating-admission-policy as its rule runs it, with the binding install writes, the
// check's policy passes the resources the tests say pass and fails those they say fail, so the
// check is as strict as the library. A check names its control in Corresponds as KS:<control>.
func RunUpstreamTests(t *testing.T, dir string, checks []guardrails.Check) {
	t.Helper()
	readKubescape := func(t *testing.T, path string) []byte {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(dir, path))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	for _, check := range checks {
		control := strings.TrimPrefix(check.Corresponds[0], "KS:")
		var tests []kubescapeTest
		if err := json.Unmarshal(readKubescape(t, "controls/"+control+"/tests.json"), &tests); err != nil {
			t.Fatal(err)
		}
		policy := celadmission.Policies(control)
		for _, tc := range tests {
			t.Run(check.Name+"/"+tc.Name, func(t *testing.T) {
				resource := map[string]any{}
				if err := yaml.Unmarshal(readKubescape(t, "test-resources/"+tc.Template), &resource); err != nil {
					t.Fatal(err)
				}
				for _, change := range tc.Changes {
					if err := applyChange(resource, change); err != nil {
						t.Fatalf("%s: %v", change, err)
					}
				}
				paramTemplate := tc.ParamTemplate
				if paramTemplate == "" {
					paramTemplate = "default-control-configuration.yaml"
				}
				params := map[string]any{}
				if err := yaml.Unmarshal(readKubescape(t, "test-resources/"+paramTemplate), &params); err != nil {
					t.Fatal(err)
				}
				params["metadata"] = map[string]any{"name": celadmission.ParamsName}
				result := vetKubescape(t, check.Policy, policy, resource, params)
				if want := tc.Expected == "pass"; result.Passed != want {
					t.Errorf("passed = %v, want %v: %+v", result.Passed, want, result)
				}
			})
		}
	}
}

// vetKubescape runs the policy named name, from policies, on resource with params, bound as
// install binds it: denying, with the library's parameters when the policy takes some.
func vetKubescape(t *testing.T, name, policies string, resource, params map[string]any) api.ValidationResult {
	t.Helper()
	spec := map[string]any{"policyName": name, "validationActions": []string{"Deny"}}
	if strings.Contains(policies, "paramKind:") {
		spec["paramRef"] = map[string]any{"name": celadmission.ParamsName, "parameterNotFoundAction": "Deny"}
	}
	binding := map[string]any{"apiVersion": "admissionregistration.k8s.io/v1", "kind": "ValidatingAdmissionPolicyBinding",
		"metadata": map[string]any{"name": name}, "spec": spec}
	policyDocs := parseYAML(t, policies, binding)
	paramDocs := parseYAML(t, "", params)
	docs := parseYAML(t, "", resource)
	_, output, err := kubernetes.VetValidatingAdmissionPolicyWith(k8skit.NewK8sResourceProvider(), api.NewFunctionOptions(), docs,
		kubernetes.AdmissionPolicyInputs{Policy: policyDocs, Params: paramDocs, Include: []string{name}})
	if err != nil {
		t.Fatal(err)
	}
	return output.(api.ValidationResult)
}

// parseYAML parses stream and objects as one YAML stream.
func parseYAML(t *testing.T, stream string, objects ...map[string]any) gaby.Container {
	t.Helper()
	docs := []string{}
	if stream != "" {
		docs = append(docs, stream)
	}
	for _, o := range objects {
		data, err := yaml.Marshal(o)
		if err != nil {
			t.Fatal(err)
		}
		docs = append(docs, string(data))
	}
	parsed, err := gaby.ParseAll([]byte(strings.Join(docs, "\n---\n")))
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

var listIndex = regexp.MustCompile(`^\[(\d+)\]$`)

// applyChange sets a field as the library's change-yaml-field.py does: path=value, the path's
// segments keys or [n] list indexes, creating what is missing, and the value JSON when it parses
// as JSON and a string otherwise.
func applyChange(doc map[string]any, change string) error {
	path, raw, _ := strings.Cut(change, "=")
	var value any = raw
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		value = raw
	}
	segments := strings.Split(path, ".")
	var parent any = doc
	set := func(container any, segment string, v any) (any, error) {
		if m := listIndex.FindStringSubmatch(segment); m != nil {
			i, _ := strconv.Atoi(m[1])
			list, _ := container.([]any)
			for len(list) <= i {
				list = append(list, nil)
			}
			list[i] = v
			return list, nil
		}
		obj, ok := container.(map[string]any)
		if !ok {
			return nil, &json.UnsupportedValueError{Str: "cannot set " + segment}
		}
		obj[segment] = v
		return obj, nil
	}
	get := func(container any, segment string) any {
		if m := listIndex.FindStringSubmatch(segment); m != nil {
			i, _ := strconv.Atoi(m[1])
			if list, ok := container.([]any); ok && i < len(list) {
				return list[i]
			}
			return nil
		}
		if obj, ok := container.(map[string]any); ok {
			return obj[segment]
		}
		return nil
	}
	// Walk down, creating each missing level as the next segment needs, then write each level
	// back into its parent, since growing a list makes a new one.
	chain := []any{parent}
	for i, segment := range segments[:len(segments)-1] {
		child := get(chain[i], segment)
		if child == nil {
			if listIndex.MatchString(segments[i+1]) {
				child = []any{}
			} else {
				child = map[string]any{}
			}
		}
		chain = append(chain, child)
	}
	v := value
	for i := len(segments) - 1; i >= 0; i-- {
		updated, err := set(chain[i], segments[i], v)
		if err != nil {
			return err
		}
		v = updated
	}
	return nil
}
