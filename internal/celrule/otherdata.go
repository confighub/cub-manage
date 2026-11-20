// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package celrule

import (
	"slices"
	"strings"

	"github.com/confighub/sdk/configkit/k8skit"
)

// A check that reads other resources -- the NetworkPolicies selecting a workload, the
// PodDisruptionBudgets guarding it -- declares them as the check's OtherData. The rule's
// Trigger then selects, for each Space, the resources of those types released to the same
// Target, and vet-cel passes them as other.<key>, along with the Unit's own.

// Other is the list of resources passed under key, empty when none were.
func Other(key string) string {
	return "other.?" + key + ".orValue([])"
}

// OnACluster says the Unit is released to a Target, and so is on a cluster whose other
// resources a selection picked. A Unit with no Target is passed only its own Unit's
// resources, so a check of what the cluster holds cannot tell what is missing there; it
// passes, until a Space without a Target can select its own Units in place of the cluster's.
const OnACluster = "functionContext.?TargetID.orValue('') != ''"

// NamespaceOf is the namespace of the resource x, a CEL expression: its own, or default for
// one that names none, as the API server places it.
func NamespaceOf(x string) string {
	return "(" + x + ".?metadata.?namespace.orValue('') != '' ? " + x + ".metadata.namespace : 'default')"
}

// WorkloadTypes are the resource types that run pods, whose labels podLabels reads: those
// with a pod template in the Kubernetes resource-type specs, and a bare Pod.
func WorkloadTypes() []string {
	types := []string{}
	for _, t := range k8skit.PodLabelsResourceTypes() {
		types = append(types, string(t))
	}
	return types
}

// PodLabelsPath is a CEL expression for the path of the labels of r's pods, wherever its kind
// keeps them, such as a CronJob's spec.jobTemplate.spec.template.metadata.labels.
func PodLabelsPath() string {
	entries := []string{}
	for _, t := range k8skit.PodLabelsResourceTypes() {
		path, ok := k8skit.PodLabelsPath(t)
		if !ok {
			continue
		}
		entries = append(entries, Lit(string(t))+": "+Lit(strings.Join(path, ".")))
	}
	slices.Sort(entries)
	return "{" + strings.Join(entries, ", ") + "}[" + resourceType + "]"
}
