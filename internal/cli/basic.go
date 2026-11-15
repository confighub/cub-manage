// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"strings"

	"github.com/confighub/sdk/core/function/api"
	"github.com/confighub/sdk/managerkit/guardrails"

	"github.com/confighub/cub-manage/internal/celrule"
)

// servedTypeFacts are the Target facts listing the resource types the Target's cluster serves, as
// group/version/Kind, comma-separated: its built-in types and its CRDs', each by scope.
var servedTypeFacts = []string{
	"Cluster.NamespaceScopedResourceTypes", "Cluster.ClusterScopedResourceTypes",
	"Cluster.NamespaceScopedCRDs", "Cluster.ClusterScopedCRDs",
}

// celServedAPI fails a resource whose apiVersion and kind are not among the types the Target's
// cluster serves. A Target whose facts were never collected says nothing about what its cluster
// serves, so the resource passes.
var celServedAPI = func() string {
	facts := "functionContext.?TargetFacts.orValue({})"
	served := make([]string, len(servedTypeFacts))
	for i, fact := range servedTypeFacts {
		served[i] = facts + "[?" + celrule.Lit(fact) + "].orValue('')"
	}
	resourceType := "r.apiVersion + '/' + r.kind"
	return celrule.When(
		celrule.Lit(servedTypeFacts[0])+" in "+facts+" && has(r.apiVersion) && has(r.kind) && "+
			"!(',' + "+strings.Join(served, " + ',' + ")+" + ',').contains(',' + "+resourceType+" + ',')",
		"["+celrule.Failure(celrule.Lit("apiVersion"), celrule.Some("r.apiVersion"),
			"'the cluster of the Target does not serve ' + "+resourceType+" + ': the version was removed, or its CRD is not installed'")+"]")
}()

// basicPack is the tool-level guardrail group: rules about every resource or
// every Unit rather than any group's kinds, which a Space opts into on its own.
// The slugs are those of the Triggers the rules were first written as, so
// installing the pack where they exist updates them rather than adding a second
// copy. vet-namespace-required is numbered in the metadata block: it moves to
// the metadata pack, beside the other namespace checks, once that pack exists.
var basicPack = guardrails.Pack{
	Group: "basic",
	Checks: []guardrails.Check{
		{Control: "CH-0101", Name: "validate", Rule: "validate", DisplayName: "Resources match their schemas",
			Description: "Fails a resource that does not pass its Kubernetes or CRD schema, including the KEDA ScaledObjects `cub manage autoscale convert-keda` writes. Fix: correct the fields the result names.",
			Engine:      guardrails.EngineFunction, Priority: guardrails.PriorityHigh, Function: "vet-schemas"},
		{Control: "CH-0102", Name: "format", Rule: "format", DisplayName: "YAML has no ambiguous syntax",
			Description: "Fails YAML that anchors or aliases a value, leaves a value empty, repeats a key, writes a boolean as yes/no/on/off/y/n, or writes an octal number the old way (0755). Fix: write the value out, quote the string, or write 0o755.",
			Engine:      guardrails.EngineFunction, Priority: guardrails.PriorityMedium, Function: "vet-format"},
		{Control: "CH-0103", Name: "validate-dupes", Rule: "validate-dupes", DisplayName: "No duplicate keys in merged lists",
			Description: "Fails a list whose entries Kubernetes merges by a key, such as containers or env by name, when two entries share the key: applying it is an error. Fix: remove or rename the duplicate.",
			Engine:      guardrails.EngineFunction, Priority: guardrails.PriorityHigh, Function: "vet-merge-keys"},
		{Control: "CH-0801", Name: "vet-namespace-required", Rule: "vet-namespace-required", DisplayName: "Namespaced resources name their namespace",
			Description: "Fails a resource that does not set metadata.namespace, unless ConfigHub knows its type to be cluster-scoped -- Namespace, ClusterRole, StorageClass, CustomResourceDefinition, Karpenter's NodePool and the like -- so where it lands does not depend on the namespace it is applied with. Fix: `set-namespace`, or `ensure-namespaces` to add a placeholder to set later.",
			Engine:      guardrails.EngineFunction, Priority: guardrails.PriorityMedium, Function: "vet-namespace"},
		{Control: "CH-0104", Name: "no-merge-conflicts", Rule: "no-merge-conflicts", DisplayName: "No unresolved merge conflicts",
			Description: "Fails a Unit with merge conflicts left from an upgrade or merge: changes from upstream that could not be applied over its own. Fix: resolve or dismiss each conflict.",
			Engine:      guardrails.EngineFunction, Priority: guardrails.PriorityHigh, Function: "vet-no-merge-conflicts"},
		{Control: "CH-0105", Name: "no-link-findings", Rule: "no-link-findings", DisplayName: "Links find everything they name",
			Description: "Fails a Unit whose Links' last resolve could not do what they say: a path a Link names in this Unit or in its upstream Unit is not there -- typically the upstream resource was renamed -- or the resolve failed outright. Until it resolves cleanly, the Unit holds values that are not current. Fix: correct the Link or the path it names.",
			Engine:      guardrails.EngineFunction, Priority: guardrails.PriorityMedium, Function: "vet-no-link-findings"},
		{Control: "CH-0106", Name: "vet-no-placeholders", Rule: "vet-no-placeholders", DisplayName: "No placeholders left to fill",
			Description: "Fails a Unit released to a Target while a value is still the placeholder confighubplaceholder or 999999999. Fix: set the value, or link the Unit to one that provides it.",
			Engine:      guardrails.EngineFunction, Priority: guardrails.PriorityHigh, Function: "vet-placeholders", AppliedTo: guardrails.AppliedToTarget},
		{Control: "CH-0107", Name: "vet-immutable", Rule: "vet-immutable", DisplayName: "Immutable fields are unchanged",
			Description: "Fails a Unit released to a Target that changes a field Kubernetes does not let change once applied, such as a Deployment's selector, compared with what was last applied. Fix: restore the field, or replace the resource rather than updating it.",
			Engine:      guardrails.EngineFunction, Priority: guardrails.PriorityHigh, Function: "vet-immutable", AppliedTo: guardrails.AppliedToTarget},
		{Control: "CH-0110", Name: "served-api", Rule: "served-api", DisplayName: "The Target cluster serves each API version",
			Description: "Fails a Unit released to a Target whose cluster does not serve a resource's apiVersion and kind, as the Target's Cluster facts list them: the version was removed, as extensions/v1beta1 Ingress was, or the resource's CRD is not installed. A Target without the facts passes; `cub k8s collect` records them. Fix: move the resource to a version the cluster serves, or install its CRD first.",
			Engine:      guardrails.EngineCEL, Priority: guardrails.PriorityHigh, Failures: celServedAPI, AppliedTo: guardrails.AppliedToTarget,
			Corresponds: []string{"KL:no-extensions-v1beta", "KY:check-deprecated-apis", "GK:verifydeprecatedapi"}},
		{Control: "CH-0111", Name: "deprecated-api", Rule: "deprecated-api", DisplayName: "No deprecated API versions",
			Description: "Warns on a resource whose type, or the version of it, is deprecated, with the warning Kubernetes gives for it: a built-in type ConfigHub's resource type specs declare deprecated, such as v1/ComponentStatus, or a CRD version its CRD marks deprecated, for the CRDs in those specs. Fix: move the resource to the replacement the warning names.",
			Engine:      guardrails.EngineFunction, Priority: guardrails.PriorityMedium,
			Function: "vet-correctness", Arguments: []api.FunctionArgument{{ParameterName: "check", Value: "deprecated-api"}}},
	},
}
