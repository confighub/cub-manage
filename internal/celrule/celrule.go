// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

// Package celrule builds the vet-cel expressions of the guardrail rules.
//
// vet-cel evaluates an expression once per resource, with the resource as r. A
// rule's expression returns not just whether the resource passed but, when it
// did not, each attribute that failed -- the resource, the path and its value --
// with a sentence saying why. The ValidationResult then carries them as
// FailedAttributes and Details, so a reader can point at the field to fix, or at
// the resource to except, instead of parsing a message.
//
// A rule states its failures as a CEL list of Failure maps, empty when the
// resource passes, and Result turns that into the map vet-cel reads.
package celrule

import "strings"

const (
	// resourceName and resourceType are how ConfigHub names a Kubernetes
	// resource: <namespace>/<name>, with an empty namespace for one that has
	// none, and <apiVersion>/<kind>.
	resourceName = "(has(r.metadata.namespace) ? r.metadata.namespace : '') + '/' + r.metadata.name"
	resourceType = "r.apiVersion + '/' + r.kind"
)

// Failure is a CEL map naming one attribute of r that fails a rule. path and
// detail are CEL string expressions. value is a CEL optional: Some of the
// attribute's value, None for an attribute that is missing, or an optional
// field selection such as c.?securityContext.?runAsNonRoot for one that may be.
// A missing value is left out rather than null, which vet-cel would report as 0.
// Detail is not an attribute field: Result reads it into the result's Details,
// and vet-cel ignores it in FailedAttributes.
func Failure(path, value, detail string) string {
	return "{'ResourceName': " + resourceName +
		", 'ResourceType': " + resourceType +
		", 'Path': " + path +
		", ?'Value': " + value +
		", 'Detail': " + detail + "}"
}

// None is the value of a Failure whose attribute is missing.
const None = "optional.none()"

// Some is the value of a Failure whose attribute is expr.
func Some(expr string) string {
	return "optional.of(dyn(" + expr + "))"
}

// When is failures where applies holds, and no failure elsewhere.
func When(applies, failures string) string {
	return "(" + applies + " ? " + failures + " : [])"
}

// Result is the vet-cel expression for a rule whose failures, a CEL list of
// Failure maps, is empty when r passes. The list is bound once, as the element
// of a one-element list, since vet-cel's CEL has no variable binding and the
// result reads it three times.
func Result(failures string) string {
	return "[" + failures + "].map(f, {'passed': size(f) == 0, 'details': f.map(a, a.Detail), 'failed_attributes': f})[0]"
}

// Lit is s as a CEL string literal. s must not contain a single quote.
func Lit(s string) string {
	return "'" + s + "'"
}

// MapKeyPath is the path of key in the map at prefix. A dot in a key would split
// it into two segments, so it is written ~1.
func MapKeyPath(prefix, key string) string {
	return prefix + "." + strings.ReplaceAll(key, ".", "~1")
}

// AnnotationFinding fails r while it carries the annotation key, which a
// guardrails annotate command writes on a Unit to record what no single
// resource can show. The null check matters: a bare `annotations:` key is
// present with a null value, and `in` against null is an error.
func AnnotationFinding(key, detail string) string {
	present := "has(r.metadata.annotations) && r.metadata.annotations != null && " + Lit(key) + " in r.metadata.annotations"
	return Result(When(present, "["+Failure(Lit(MapKeyPath("metadata.annotations", key)), Some("r.metadata.annotations["+Lit(key)+"]"), Lit(detail))+"]"))
}

// ContainerPath is the path of field in the container c of a pod template, by
// the container's name, as ConfigHub's functions report it.
func ContainerPath(field string) string {
	return "'spec.template.spec.containers.?name=' + c.name + '." + field + "'"
}

// Indexed is the path of field in the element i of the list at prefix.
func Indexed(prefix, i, field string) string {
	path := Lit(prefix+".") + " + string(" + i + ")"
	if field != "" {
		path += " + " + Lit("."+field)
	}
	return path
}
