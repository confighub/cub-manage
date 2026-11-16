// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"github.com/confighub/sdk/managerkit/guardrails"

	celadmission "github.com/confighub/cub-manage/third_party/kubescape/cel-admission-library"
)

// kubescapeLibrary are the ValidatingAdmissionPolicies the pack adopts from Kubescape, the same
// policies a cluster can enforce at admission. Install writes them into the policy Space as the
// Unit guardrails-workload-kubescape, and the library's parameters as guardrails-kubescape-params,
// which a team edits to set them.
var kubescapeLibrary = guardrails.PolicyLibrary{
	Name: "kubescape", Source: celadmission.Source, License: "Apache-2.0",
	Policies: celadmission.Policies("C-0020", "C-0062", "C-0081", "C-0292"),
	Params:   celadmission.Params(), ParamRef: celadmission.ParamsName,
}

// kubescapeTypes are the types Kubescape's workload policies read: Pods, and the controllers,
// Jobs and CronJobs holding pod templates.
var kubescapeTypes = []string{"v1/Pod", "apps/v1/Deployment", "apps/v1/ReplicaSet", "apps/v1/DaemonSet",
	"apps/v1/StatefulSet", "batch/v1/Job", "batch/v1/CronJob"}

// vulnerableImageTypes are the types the policies of known-vulnerable-images read.
var vulnerableImageTypes = []string{"apps/v1/Deployment", "apps/v1/DaemonSet", "apps/v1/StatefulSet"}

const knownVulnerableImagesRule = "known-vulnerable-images"

var kubescapeRules = []guardrails.RuleInfo{
	{Slug: knownVulnerableImagesRule, DisplayName: "No known vulnerable or end of life images",
		Description: "Warns on a workload running an image version with a known critical vulnerability, or one past its end of life, from Kubescape's lists. Each finding names the check it fails. Fix: move to a supported version."},
}

// kubescapeCheck is the check name running the policy of Kubescape control, in rule.
func kubescapeCheck(control, name, rule, priority, policy, ksControl string, types []string, displayName, description string) guardrails.Check {
	return guardrails.Check{Control: control, Name: name, Rule: rule, DisplayName: displayName, Description: description,
		Engine: guardrails.EngineAdmissionPolicy, Priority: priority, Library: kubescapeLibrary.Name, Policy: policy,
		ResourceTypes: types, Corresponds: []string{"KS:" + ksControl}}
}

var kubescapeChecks = []guardrails.Check{
	kubescapeCheck("CH-0324", "no-cloud-credential-mounts", "no-cloud-credential-mounts", guardrails.PriorityHigh,
		"kubescape-c-0020-deny-resources-having-volumes-with-potential-access-to-known-cloud-credentials", "C-0020", kubescapeTypes,
		"No volumes with access to cloud credentials",
		"Warns on a pod that mounts a host path holding a cloud's credentials, such as /.aws/credentials on EKS or /etc/kubernetes/azure.json on AKS. It checks the paths of the cloud set as settings.cloudProvider (eks, aks or gke) in the Unit guardrails-kubescape-params in the policy Space, and none until it is set. Fix: remove the volume, and use the cloud's workload identity."),
	kubescapeCheck("CH-0330", "no-sudo-entrypoint", "no-sudo-entrypoint", guardrails.PriorityLow,
		"kubescape-c-0062-deny-resources-having-containers-with-sudo-in-entrypoint", "C-0062", kubescapeTypes,
		"No sudo in container entrypoints",
		"Warns on a container whose command or args run sudo, which means it expects to gain root. Fix: run the process as the user it needs, without sudo."),
	kubescapeCheck("CH-0385", "vulnerable-argocd", knownVulnerableImagesRule, guardrails.PriorityHigh,
		"kubescape-c-0081-deny-vulnerable-argocd-versions", "C-0081", vulnerableImageTypes,
		"No Argo CD versions with CVE-2022-24348",
		"Warns on a Deployment running an Argo CD version affected by CVE-2022-24348, a path traversal through Helm values files: any 1.x or 2.0, 2.1 before 2.1.9, or 2.2 before 2.2.4. Fix: upgrade Argo CD."),
	kubescapeCheck("CH-0386", "eol-ingress-nginx", knownVulnerableImagesRule, guardrails.PriorityHigh,
		"kubescape-c-0292-deny-nginx-ingress-controller-eol", "C-0292", vulnerableImageTypes,
		"No end of life ingress-nginx controller",
		"Warns on a workload running the community ingress-nginx controller, which reached end of life in March 2026 and gets no more security fixes. NGINX Inc's own images are not matched. Fix: move to a supported ingress or Gateway API controller."),
}
