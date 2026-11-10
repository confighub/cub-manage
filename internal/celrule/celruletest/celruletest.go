// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

// Package celruletest evaluates a rule's expression on a resource the way vet-cel
// does, for the packs' tests: the same CEL environment and variables, the
// resource read from YAML as a map, and the result read the way vet-cel reads it.
package celruletest

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/confighub/sdk/managerkit/guardrails"
	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/ext"
	"google.golang.org/protobuf/types/known/structpb"
	"sigs.k8s.io/yaml"
)

// Attribute is one entry of a result's FailedAttributes, with the fields a rule sets.
type Attribute struct {
	ResourceName string
	ResourceType string
	Path         string
	Value        any
}

// Result is what vet-cel reports for one resource.
type Result struct {
	Passed           bool
	Details          []string
	FailedAttributes []Attribute
}

// Eval evaluates expression on the resource in resourceYAML.
func Eval(t *testing.T, expression, resourceYAML string) Result {
	t.Helper()
	env, err := cel.NewEnv(
		cel.DefaultUTCTimeZone(true),
		cel.CrossTypeNumericComparisons(true),
		cel.OptionalTypes(),
		ext.Strings(),
		ext.Sets(),
		ext.Lists(),
		ext.Encoders(),
		cel.Variable("r", cel.DynType),
		cel.Variable("object", cel.DynType),
		cel.Variable("params", cel.DynType),
		cel.Variable("functionContext", cel.DynType),
	)
	if err != nil {
		t.Fatalf("CEL environment: %v", err)
	}
	ast, issues := env.Compile(expression)
	if issues != nil && issues.Err() != nil {
		t.Fatalf("compile: %v", issues.Err())
	}
	program, err := env.Program(ast)
	if err != nil {
		t.Fatalf("program: %v", err)
	}
	var resource map[string]any
	if err := yaml.Unmarshal([]byte(resourceYAML), &resource); err != nil {
		t.Fatalf("resource: %v", err)
	}
	val, _, err := program.Eval(map[string]any{
		"r": resource, "object": resource, "params": map[string]any{}, "functionContext": map[string]any{},
	})
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	native, err := val.ConvertToNative(reflect.TypeFor[*structpb.Value]())
	if err != nil {
		t.Fatalf("result: %v", err)
	}
	return readResult(t, native.(*structpb.Value).AsInterface())
}

// readResult reads a result as vet-cel does: a bool, or a map of passed, details
// and failed_attributes.
func readResult(t *testing.T, v any) Result {
	t.Helper()
	if b, ok := v.(bool); ok {
		return Result{Passed: b}
	}
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("the expression returned %T, not a bool or a map", v)
	}
	r := Result{Passed: true}
	if passed, ok := m["passed"].(bool); ok {
		r.Passed = passed
	}
	details, _ := m["details"].([]any)
	for _, d := range details {
		r.Details = append(r.Details, fmt.Sprintf("%v", d))
	}
	attributes, _ := m["failed_attributes"].([]any)
	for _, a := range attributes {
		am, ok := a.(map[string]any)
		if !ok {
			t.Fatalf("a failed attribute is %T, not a map", a)
		}
		// vet-cel reads a null as protobuf's NullValue, which it reports as 0.
		if v, present := am["Value"]; present && v == nil {
			t.Errorf("the failed attribute at %v has a null Value; leave a missing value out", am["Path"])
		}
		r.FailedAttributes = append(r.FailedAttributes, Attribute{
			ResourceName: fmt.Sprintf("%v", am["ResourceName"]),
			ResourceType: fmt.Sprintf("%v", am["ResourceType"]),
			Path:         fmt.Sprintf("%v", am["Path"]),
			Value:        am["Value"],
		})
	}
	return r
}

// Paths are the paths of a result's failed attributes, in order.
func (r Result) Paths() []string {
	paths := []string{}
	for _, a := range r.FailedAttributes {
		paths = append(paths, a.Path)
	}
	return paths
}

// Expect checks that result failed at exactly paths, with one detail for each,
// and passed when there are none.
func Expect(t *testing.T, result Result, paths ...string) {
	t.Helper()
	if paths == nil {
		paths = []string{}
	}
	if result.Passed != (len(paths) == 0) {
		t.Errorf("passed = %v, want %v", result.Passed, len(paths) == 0)
	}
	if got := result.Paths(); !reflect.DeepEqual(got, paths) {
		t.Errorf("failed paths = %q, want %q", got, paths)
	}
	if len(result.Details) != len(paths) {
		t.Errorf("details = %q, want one for each failed path", result.Details)
	}
}

// ExpectResource checks that every failed attribute of result names the resource.
func ExpectResource(t *testing.T, result Result, name, resourceType string) {
	t.Helper()
	for _, a := range result.FailedAttributes {
		if a.ResourceName != name || a.ResourceType != resourceType {
			t.Errorf("failed attribute names %s %s, want %s %s", a.ResourceType, a.ResourceName, resourceType, name)
		}
	}
}

// unrelated are resources none of the rules is about, with as little in them as
// a resource can have.
var unrelated = []string{
	"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: settings\n  namespace: shop\n",
	"apiVersion: v1\nkind: Secret\nmetadata:\n  name: token\n  annotations:\n",
}

// UnrelatedResourcesPass checks that each vet-cel rule of rules passes a
// resource it is not about, without an error: a rule reads fields only of the
// resources it checks.
func UnrelatedResourcesPass(t *testing.T, rules []guardrails.Rule) {
	t.Helper()
	for _, rule := range rules {
		if rule.Function != "" {
			continue
		}
		for _, doc := range unrelated {
			Expect(t, Eval(t, rule.Expression, doc))
		}
	}
}
