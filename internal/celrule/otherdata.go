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

// PodSpecOf is the pod spec of the workload x, a CEL expression, wherever its kind keeps it, such
// as a CronJob's spec.jobTemplate.spec.template.spec: an empty map when it has none.
func PodSpecOf(x string) string {
	byPath := map[string][]string{}
	for _, t := range k8skit.PodLabelsResourceTypes() {
		paths := k8skit.PodSpecPaths(t)
		if len(paths) == 0 {
			continue
		}
		byPath[paths[0]] = append(byPath[paths[0]], Lit(string(t)))
	}
	paths := make([]string, 0, len(byPath))
	for path := range byPath {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	expr := "{}"
	for _, path := range slices.Backward(paths) {
		types := byPath[path]
		slices.Sort(types)
		expr = "(" + x + ".apiVersion + '/' + " + x + ".kind in [" + strings.Join(types, ", ") + "] ? " +
			x + ".?" + strings.ReplaceAll(path, ".", ".?") + ".orValue({}) : " + expr + ")"
	}
	return expr
}

// AutoscalerTypes are the autoscalers that scale a workload: HorizontalPodAutoscalers and KEDA
// ScaledObjects.
var AutoscalerTypes = []string{
	"autoscaling/v1/HorizontalPodAutoscaler", "autoscaling/v2/HorizontalPodAutoscaler", "keda.sh/v1alpha1/ScaledObject",
}

// Scales is whether the autoscaler a scales the workload w: its scaleTargetRef names w's kind
// and name in its namespace. A ScaledObject's target kind defaults to Deployment.
func Scales(a, w string) string {
	kind := "(" + a + ".kind == 'ScaledObject' ? " + a + ".?spec.?scaleTargetRef.?kind.orValue('Deployment') : " +
		a + ".?spec.?scaleTargetRef.?kind.orValue(''))"
	return "(" + NamespaceOf(a) + " == " + NamespaceOf(w) + " && " + a + ".?spec.?scaleTargetRef.?name.orValue('') == " +
		w + ".metadata.name && " + kind + " == " + w + ".kind)"
}

// MinReplicas is the fewest replicas the autoscaler a scales to: an HPA's minReplicas, 1 when
// unset, or a ScaledObject's minReplicaCount, 0 when unset.
func MinReplicas(a string) string {
	return "(" + a + ".kind == 'ScaledObject' ? int(" + a + ".?spec.?minReplicaCount.orValue(0)) : int(" +
		a + ".?spec.?minReplicas.orValue(1)))"
}

// PodSpecPath is a CEL expression for the path of r's pod spec, wherever its kind keeps it, such
// as spec.jobTemplate.spec.template.spec for a CronJob.
func PodSpecPath() string {
	entries := []string{}
	for _, t := range k8skit.PodLabelsResourceTypes() {
		if paths := k8skit.PodSpecPaths(t); len(paths) > 0 {
			entries = append(entries, Lit(string(t))+": "+Lit(paths[0]))
		}
	}
	slices.Sort(entries)
	return "{" + strings.Join(entries, ", ") + "}[" + resourceType + "]"
}

// ServiceAccountOf is the ServiceAccount the pods of the workload x run as: its pod spec's
// serviceAccountName, the deprecated serviceAccount, or default.
func ServiceAccountOf(x string) string {
	spec := PodSpecOf(x)
	return "(" + spec + ".?serviceAccountName.orValue('') != '' ? " + spec + ".serviceAccountName : " +
		spec + ".?serviceAccount.orValue('') != '' ? " + spec + ".serviceAccount : 'default')"
}
