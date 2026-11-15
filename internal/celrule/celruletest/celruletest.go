// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

// Package celruletest evaluates a rule's expression on a resource the way vet-cel
// does, for the packs' tests: the same CEL environment and variables, the
// resource read from YAML as a map, and the result read the way vet-cel reads it.
package celruletest

import (
	"fmt"
	"reflect"
	"slices"
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
	// Checks are the Identifiers of the attribute's Issues: the checks it fails.
	Checks []string
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
	return EvalIn(t, map[string]any{}, expression, resourceYAML)
}

// EvalIn evaluates expression on the resource in resourceYAML with functionContext, the
// FunctionContext as vet-cel sees it: a map of its JSON fields, such as TargetFacts.
func EvalIn(t *testing.T, functionContext map[string]any, expression, resourceYAML string) Result {
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
		"r": resource, "object": resource, "params": map[string]any{}, "functionContext": functionContext,
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
		attribute := Attribute{
			ResourceName: fmt.Sprintf("%v", am["ResourceName"]),
			ResourceType: fmt.Sprintf("%v", am["ResourceType"]),
			Path:         fmt.Sprintf("%v", am["Path"]),
			Value:        am["Value"],
		}
		issues, _ := am["Issues"].([]any)
		for _, issue := range issues {
			if im, ok := issue.(map[string]any); ok {
				attribute.Checks = append(attribute.Checks, fmt.Sprintf("%v", im["Identifier"]))
			}
		}
		r.FailedAttributes = append(r.FailedAttributes, attribute)
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

// EvalCheck evaluates check on the resource in resourceYAML as its rule runs it, and checks
// that each failure names the check.
func EvalCheck(t *testing.T, check guardrails.Check, resourceYAML string) Result {
	t.Helper()
	return EvalCheckIn(t, map[string]any{}, check, resourceYAML)
}

// EvalCheckIn is EvalCheck with functionContext, as EvalIn takes it.
func EvalCheckIn(t *testing.T, functionContext map[string]any, check guardrails.Check, resourceYAML string) Result {
	t.Helper()
	result := EvalIn(t, functionContext, guardrails.CheckExpression([]guardrails.Check{check}), resourceYAML)
	for _, a := range result.FailedAttributes {
		if !reflect.DeepEqual(a.Checks, []string{check.Name}) {
			t.Errorf("the failure at %s names checks %q, want %s", a.Path, a.Checks, check.Name)
		}
	}
	return result
}

// Check is the check of pack named name.
func Check(t *testing.T, pack guardrails.Pack, name string) guardrails.Check {
	t.Helper()
	for _, c := range pack.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("the %s pack has no check %s", pack.Group, name)
	return guardrails.Check{}
}

// unrelated are resources none of the checks is about, with as little in them as
// a resource can have, and their types.
var unrelated = map[string]string{
	"v1/ConfigMap": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: settings\n  namespace: shop\n",
	"v1/Secret":    "apiVersion: v1\nkind: Secret\nmetadata:\n  name: token\n  annotations:\n",
}

// UnrelatedResourcesPass checks that the pack's checks are installable, and that each cel check
// of the pack leaves alone a resource it is not about: its rule does not select the resource's
// type, or, for a check of every type, it passes the resource without an error.
func UnrelatedResourcesPass(t *testing.T, pack guardrails.Pack) {
	t.Helper()
	if err := pack.Validate(); err != nil {
		t.Fatalf("the %s pack: %v", pack.Group, err)
	}
	for _, check := range pack.Checks {
		if check.Engine != guardrails.EngineCEL {
			continue
		}
		for resourceType, doc := range unrelated {
			if len(check.ResourceTypes) == 0 {
				Expect(t, EvalCheck(t, check, doc))
			} else if slices.Contains(check.ResourceTypes, resourceType) {
				t.Errorf("check %s selects %s", check.Name, resourceType)
			}
		}
	}
}
