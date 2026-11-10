// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"strings"

	"github.com/confighub/sdk/managerkit/guardrails"

	"github.com/confighub/cub-manage/internal/celrule"
)

// clusterScopedKinds are the kinds namespace-required exempts: cluster-scoped,
// so they have no namespace to set.
var clusterScopedKinds = []string{
	"Namespace", "ClusterRole", "ClusterRoleBinding", "ClusterIssuer", "ClusterSecretStore",
	"CustomResourceDefinition", "StorageClass", "IngressClass", "PriorityClass", "RuntimeClass",
	"ValidatingWebhookConfiguration", "MutatingWebhookConfiguration",
	"ValidatingAdmissionPolicy", "ValidatingAdmissionPolicyBinding",
	"APIService", "PodSecurityPolicy", "PersistentVolume", "Node", "CSIDriver",
}

// celNamespaceRequired fails a namespaced resource that does not set its
// namespace, at metadata.namespace.
var celNamespaceRequired = func() string {
	kinds := make([]string, len(clusterScopedKinds))
	for i, kind := range clusterScopedKinds {
		kinds[i] = celrule.Lit(kind)
	}
	missing := "!has(r.metadata.namespace) && !(r.kind in [" + strings.Join(kinds, ", ") + "])"
	return celrule.Result(celrule.When(missing,
		"["+celrule.Failure(celrule.Lit("metadata.namespace"), celrule.None, "'the ' + r.kind + ' sets no metadata.namespace'")+"]"))
}()

// basicPack is the tool-level guardrail group: rules about every resource or
// every Unit rather than any group's kinds, which a Space opts into on its own.
// The slugs are those of the Triggers the rules were first written as, so
// installing the pack where they exist updates them rather than adding a second
// copy.
var basicPack = guardrails.Pack{
	Group: "basic",
	Rules: []guardrails.Rule{
		{Slug: "validate", DisplayName: "Resources match their schemas",
			Description: "Fails a resource that does not pass its Kubernetes or CRD schema, including the KEDA ScaledObjects `cub manage autoscale convert-keda` writes. Fix: correct the fields the result names.",
			Function:    "vet-schemas"},
		{Slug: "format", DisplayName: "YAML has no ambiguous syntax",
			Description: "Fails YAML that anchors or aliases a value, leaves a value empty, repeats a key, writes a boolean as yes/no/on/off/y/n, or writes an octal number the old way (0755). Fix: write the value out, quote the string, or write 0o755.",
			Function:    "vet-format"},
		{Slug: "validate-dupes", DisplayName: "No duplicate keys in merged lists",
			Description: "Fails a list whose entries Kubernetes merges by a key, such as containers or env by name, when two entries share the key: applying it is an error. Fix: remove or rename the duplicate.",
			Function:    "vet-merge-keys"},
		{Slug: "vet-namespace-required", DisplayName: "Namespaced resources name their namespace",
			Description: "Fails a namespaced resource that does not set metadata.namespace, so where it lands does not depend on the namespace it is applied with. Cluster-scoped kinds (Namespace, ClusterRole, StorageClass, CustomResourceDefinition and the like) are exempt. Fix: set metadata.namespace.",
			Expression:  celNamespaceRequired},
		{Slug: "no-merge-conflicts", DisplayName: "No unresolved merge conflicts",
			Description: "Fails a Unit with merge conflicts left from an upgrade or merge: changes from upstream that could not be applied over its own. Fix: resolve or dismiss each conflict.",
			Function:    "vet-no-merge-conflicts"},
		{Slug: "no-link-findings", DisplayName: "Links find everything they name",
			Description: "Fails a Unit whose Links' last resolve could not do what they say: a path a Link names in this Unit or in its upstream Unit is not there -- typically the upstream resource was renamed -- or the resolve failed outright. Until it resolves cleanly, the Unit holds values that are not current. Fix: correct the Link or the path it names.",
			Function:    "vet-no-link-findings"},
		{Slug: "vet-no-placeholders", DisplayName: "No placeholders left to fill",
			Description: "Fails a Unit released to a Target while a value is still the placeholder confighubplaceholder or 999999999. Fix: set the value, or link the Unit to one that provides it.",
			Function:    "vet-placeholders", AppliedTo: guardrails.AppliedToTarget},
		{Slug: "vet-immutable", DisplayName: "Immutable fields are unchanged",
			Description: "Fails a Unit released to a Target that changes a field Kubernetes does not let change once applied, such as a Deployment's selector, compared with what was last applied. Fix: restore the field, or replace the resource rather than updating it.",
			Function:    "vet-immutable", AppliedTo: guardrails.AppliedToTarget},
	},
}
