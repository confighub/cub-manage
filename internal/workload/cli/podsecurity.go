// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"github.com/confighub/sdk/configkit/k8skit"
	"github.com/confighub/sdk/managerkit/guardrails"
)

// The rules of the Pod Security Standards checks, one per level, so that baseline can be enforced
// long before restricted. Each check's name is the ID k8s.io/pod-security-admission gives it, in
// lowercase words, and its findings carry the ID itself as their Identifier. One is staged ahead
// of the rest of its level with `cub manage guardrails update <rule> --separate <check>`.
const (
	podSecurityBaselineRule   = "workload-pod-security-baseline"
	podSecurityRestrictedRule = "workload-pod-security-restricted"
)

const hardenFix = "cub manage workload harden <space>/<unit>"

var podSecurityRules = []guardrails.RuleInfo{
	{Slug: podSecurityBaselineRule, DisplayName: "Pods meet the baseline Pod Security Standard",
		Description: "Warns on a pod, or a resource with a pod template, that fails a check of the baseline Pod Security Standard: one that shares the host's namespaces, ports or filesystem, runs privileged, or adds capabilities or loosens isolation beyond the default. Each finding names the check it fails."},
	{Slug: podSecurityRestrictedRule, DisplayName: "Pods meet the restricted Pod Security Standard",
		Description: "Warns on a pod, or a resource with a pod template, that fails a check the restricted Pod Security Standard adds to baseline: privilege escalation, running as root, a missing seccomp profile, capabilities not dropped, or a volume type outside the restricted set. Each finding names the check it fails. Fix: `cub manage workload harden <space>/<unit>` sets what is unset."},
}

// podSecurityCheck is the check name: the Pod Security Standards check id at level, in the rule
// of its level.
func podSecurityCheck(control, name, id, level, displayName, description string) guardrails.Check {
	check := guardrails.Check{
		Control: control, Name: name, DisplayName: displayName, Description: description,
		Engine: guardrails.EnginePodSecurity, Level: level, PodSecurityCheck: id,
		ResourceTypes: podSpecTypes, Corresponds: append([]string{"PSS:" + id}, kubescapeControlsOf[id]...),
	}
	if level == "baseline" {
		check.Rule, check.Priority = podSecurityBaselineRule, guardrails.PriorityHigh
	} else {
		check.Rule, check.Priority = podSecurityRestrictedRule, guardrails.PriorityMedium
		// The restricted standard requires running as non-root, so a workload excepted from
		// workload-runs-nonroot cannot meet any of it: one that runs as root sets runAsUser: 0,
		// and usually needs the privileges the other restricted checks take away.
		check.ExceptFrom = []string{"workload-runs-nonroot"}
	}
	return check
}

// kubescapeControlsOf are the Kubescape controls each Pod Security Standards check corresponds to,
// by the check's ID, which is how a framework mapped to Kubescape's controls finds the check.
var kubescapeControlsOf = map[string][]string{
	"windowsHostProcess":        {"KS:C-0202"},
	"hostNamespaces":            {"KS:C-0038", "KS:C-0041", "KS:C-0194", "KS:C-0195", "KS:C-0196", "KS:C-0214", "KS:C-0215", "KS:C-0216", "KS:C-0275", "KS:C-0276"},
	"privileged":                {"KS:C-0057", "KS:C-0193", "KS:C-0213"},
	"capabilities_baseline":     {"KS:C-0046", "KS:C-0200", "KS:C-0219"},
	"hostPathVolumes":           {"KS:C-0048", "KS:C-0203"},
	"hostPorts":                 {"KS:C-0044", "KS:C-0204"},
	"allowPrivilegeEscalation":  {"KS:C-0016", "KS:C-0197", "KS:C-0217"},
	"runAsNonRoot":              {"KS:C-0013", "KS:C-0198", "KS:C-0218"},
	"seccompProfile_restricted": {"KS:C-0210"},
	"capabilities_restricted":   {"KS:C-0199", "KS:C-0201", "KS:C-0220"},
}

// podSpecTypes are the resource types holding a PodSpec, which vet-pod-security-standard checks:
// Pods, and the controllers, Jobs and CronJobs that hold pod templates.
var podSpecTypes = func() []string {
	var types []string
	for _, t := range k8skit.PodSpecResourceTypes() {
		types = append(types, string(t))
	}
	return types
}()

var podSecurityChecks = []guardrails.Check{
	podSecurityCheck("CH-0301", "windows-host-process", "windowsHostProcess", "baseline", "Pods do not run Windows HostProcess containers",
		"Fails a pod or container that sets windowsOptions.hostProcess: true, which runs it on the Windows node as a host process."),
	podSecurityCheck("CH-0302", "host-namespaces", "hostNamespaces", "baseline", "Pods do not share host namespaces",
		"Fails a pod that sets hostNetwork, hostPID or hostIPC, which share the node's network, processes or IPC with it."),
	podSecurityCheck("CH-0303", "privileged", "privileged", "baseline", "Containers are not privileged",
		"Fails a container that sets securityContext.privileged: true, which gives it every capability of the node. Fix: remove the setting; a container that needs host access, such as a CSI node plugin, is excepted or separated into a rule of its own."),
	podSecurityCheck("CH-0304", "capabilities-baseline", "capabilities_baseline", "baseline", "Containers add no capabilities beyond the default set",
		"Fails a container that adds a capability other than AUDIT_WRITE, CHOWN, DAC_OVERRIDE, FOWNER, FSETID, KILL, MKNOD, NET_BIND_SERVICE, SETFCAP, SETGID, SETPCAP, SETUID and SYS_CHROOT, such as NET_ADMIN or SYS_ADMIN."),
	podSecurityCheck("CH-0305", "host-path-volumes", "hostPathVolumes", "baseline", "Pods mount no hostPath volumes",
		"Fails a pod with a hostPath volume, which reads or writes the node's filesystem."),
	podSecurityCheck("CH-0306", "host-ports", "hostPorts", "baseline", "Containers use no host ports",
		"Fails a container port that sets a hostPort, which binds it on the node."),
	podSecurityCheck("CH-0307", "apparmor-profile", "appArmorProfile", "baseline", "AppArmor is not unconfined",
		"Fails a pod or container whose AppArmor profile, set in its securityContext or by the legacy annotation, is anything but RuntimeDefault or a Localhost profile."),
	podSecurityCheck("CH-0308", "selinux-options", "seLinuxOptions", "baseline", "SELinux options stay within the container types",
		"Fails a pod or container that sets an SELinux user or role, or a type other than container_t, container_init_t, container_kvm_t or container_engine_t."),
	podSecurityCheck("CH-0309", "proc-mount", "procMount", "baseline", "Containers use the default proc mount",
		"Fails a container that sets securityContext.procMount to anything but Default, which unmasks parts of /proc."),
	podSecurityCheck("CH-0310", "seccomp-profile-baseline", "seccompProfile_baseline", "baseline", "Seccomp is not unconfined",
		"Fails a pod or container that sets its seccomp profile type to Unconfined."),
	podSecurityCheck("CH-0311", "sysctls", "sysctls", "baseline", "Pods set only safe sysctls",
		"Fails a pod that sets a sysctl outside the set Kubernetes considers safe and namespaced, such as net.core.somaxconn."),

	podSecurityCheck("CH-0312", "restricted-volumes", "restrictedVolumes", "restricted", "Pods use only restricted volume types",
		"Fails a pod with a volume other than configMap, csi, downwardAPI, emptyDir, ephemeral, image, persistentVolumeClaim, projected or secret."),
	withFix(podSecurityCheck("CH-0313", "allow-privilege-escalation", "allowPrivilegeEscalation", "restricted", "Containers disallow privilege escalation",
		"Fails a container that does not set securityContext.allowPrivilegeEscalation: false. Fix: `cub manage workload harden <space>/<unit>`."), hardenFix),
	// workload-runs-nonroot checks the same, and is installed by default, with exceptions of its
	// own; this check is for a Space that wants all of restricted under one rule.
	optional(withFix(podSecurityCheck("CH-0314", "run-as-non-root", "runAsNonRoot", "restricted", "Pods run as non-root",
		"Fails a pod that does not set runAsNonRoot: true, on the pod or on every container. Optional: workload-runs-nonroot checks the same. Fix: `cub manage workload harden <space>/<unit>`."), hardenFix)),
	withFix(podSecurityCheck("CH-0315", "run-as-user", "runAsUser", "restricted", "Pods do not run as UID 0",
		"Fails a pod or container that sets runAsUser: 0. Fix: set a non-zero runAsUser, or remove it and let `cub manage workload harden <space>/<unit>` set one."), hardenFix),
	withFix(podSecurityCheck("CH-0316", "seccomp-profile-restricted", "seccompProfile_restricted", "restricted", "Seccomp is RuntimeDefault or Localhost",
		"Fails a pod whose containers do not all have a seccomp profile of RuntimeDefault or Localhost, set on the pod or on each container. Fix: `cub manage workload harden <space>/<unit>`."), hardenFix),
	withFix(podSecurityCheck("CH-0317", "capabilities-restricted", "capabilities_restricted", "restricted", "Containers drop all capabilities",
		"Fails a container that does not drop ALL capabilities, or adds one other than NET_BIND_SERVICE. Fix: `cub manage workload harden <space>/<unit>`."), hardenFix),
}

func withFix(c guardrails.Check, fix string) guardrails.Check {
	c.Fix = fix
	return c
}

func optional(c guardrails.Check) guardrails.Check {
	c.Optional = true
	return c
}
