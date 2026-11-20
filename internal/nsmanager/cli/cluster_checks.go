// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"strings"

	"github.com/confighub/sdk/configkit/k8skit"
	"github.com/confighub/sdk/managerkit/guardrails"

	"github.com/confighub/cub-manage/internal/celrule"
)

// namespaceHas is a check that a Namespace has a resource of the type, read as OtherData under
// key, in it on its cluster. The namespaces every cluster has are left out, and so is a Unit with
// no Target, which is on no known cluster.
func namespaceHas(control, name, displayName, kind, resourceType, key, why, fix string, corresponds ...string) guardrails.Check {
	builtIn := []string{}
	for _, ns := range k8skit.BuiltInNamespaces() {
		builtIn = append(builtIn, celrule.Lit(ns))
	}
	failures := celrule.When(celrule.OnACluster+" && !(r.metadata.name in ["+strings.Join(builtIn, ", ")+"]) && "+
		"!"+celrule.Other(key)+".exists(o, "+celrule.NamespaceOf("o")+" == r.metadata.name)",
		"["+celrule.Failure("'metadata.name'", celrule.Some("r.metadata.name"),
			"'namespace ' + r.metadata.name + ' has no "+kind+" on the cluster, so "+why+"'")+"]")
	return guardrails.Check{Control: control, Name: name, Rule: name, DisplayName: displayName,
		Description: "Warns on a Namespace with no " + kind + " in it on its cluster, so " + why + ". Optional. The namespaces every cluster has, default and kube-system among them, are not checked, nor is a Unit with no Target. Fix: " + fix + ".",
		Engine:      guardrails.EngineCEL, Priority: guardrails.PriorityLow, Optional: true,
		ResourceTypes: []string{"v1/Namespace"}, Failures: failures,
		OtherData: []guardrails.OtherData{{Key: key, ResourceTypes: []string{resourceType}}}, Corresponds: corresponds}
}

var (
	namespaceResourceQuota = namespaceHas("CH-1004", "namespace-resource-quota", "Namespaces have a ResourceQuota",
		"ResourceQuota", "v1/ResourceQuota", "quotas", "its workloads can use all of the cluster",
		"add a ResourceQuota to the namespace", "KY:add-ns-quota")
	namespaceLimitRange = namespaceHas("CH-1005", "namespace-limit-range", "Namespaces have a LimitRange",
		"LimitRange", "v1/LimitRange", "limitranges", "a container that requests nothing gets no default",
		"add a LimitRange to the namespace", "KY:add-ns-quota")
)
