// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

// The workload pack's checks beyond its first: hardening, resources, reliability and images.

import (
	"strings"

	"github.com/confighub/sdk/managerkit/guardrails"

	"github.com/confighub/cub-manage/internal/celrule"
)

const podSpec = "r.spec.template.spec"

// celList is strs as a CEL list of string literals.
func celList(strs []string) string {
	lits := make([]string, len(strs))
	for i, s := range strs {
		lits[i] = celrule.Lit(s)
	}
	return "[" + strings.Join(lits, ", ") + "]"
}

// trimSlash is the path expr without a trailing slash, so /etc/ is /etc; / stays /.
func trimSlash(expr string) string {
	return "(" + expr + ".endsWith('/') && size(" + expr + ") > 1 ? " + expr + ".substring(0, size(" + expr + ") - 1) : " + expr + ")"
}

// volumeFailures fails each volume v of the pod template for which bad holds, at field.
func volumeFailures(bad, field, value, detail string) string {
	return "(has(" + podSpec + ".volumes) ? " + podSpec + ".volumes.filter(v, " + bad + ").map(v, " +
		celrule.Failure("'spec.template.spec.volumes.?name=' + v.name + '."+field+"'", value, detail) + ") : [])"
}

// eachContainer is the failures, a CEL list, of each container c in the pod template's list,
// containers or initContainers; none for a template without the list.
func eachContainer(list, failures string) string {
	return "(has(" + podSpec + "." + list + ") ? " + podSpec + "." + list + ".map(c, " + failures + ").flatten() : [])"
}

// failIf is a one-failure list when bad holds, and an empty one otherwise.
func failIf(bad, path, value, detail string) string {
	return "(" + bad + " ? [" + celrule.Failure(path, value, detail) + "] : [])"
}

// fieldPath is the path of field in container c of the pod template's list.
func fieldPath(list, field string) string {
	return "'spec.template.spec." + list + ".?name=' + c.name + '." + field + "'"
}

// hostPathIn holds for a hostPath volume v whose path, without a trailing slash, is one of paths.
func hostPathIn(paths []string) string {
	return "has(v.hostPath) && has(v.hostPath.path) && " + trimSlash("v.hostPath.path") + " in " + celList(paths)
}

// containerHas holds when container c sets the field at path, a dot path under c.
func containerHas(path string) string {
	parts := strings.Split(path, ".")
	var conds []string
	for i := range parts {
		conds = append(conds, "has(c."+strings.Join(parts[:i+1], ".")+")")
	}
	return strings.Join(conds, " && ")
}

// The container runtimes' sockets, and the directories holding them, which give whoever mounts
// them control of every container on the node.
var runtimeSockets = []string{
	"/var/run/docker.sock", "/run/docker.sock", "/var/run/docker", "/run/docker",
	"/var/run/containerd/containerd.sock", "/run/containerd/containerd.sock", "/var/run/containerd", "/run/containerd",
	"/var/run/crio/crio.sock", "/run/crio/crio.sock", "/var/run/crio", "/run/crio",
	"/var/run/cri-dockerd.sock", "/run/cri-dockerd.sock", "/var/run", "/run",
}

// The host directories whose contents control the node: its root, configuration, devices, kernel
// interfaces, binaries, root's home and the kubelet's and runtimes' state.
var sensitiveHostPaths = []string{
	"/", "/boot", "/dev", "/etc", "/etc/kubernetes", "/lib", "/proc", "/root", "/sys", "/usr",
	"/var/lib/containerd", "/var/lib/docker", "/var/lib/kubelet",
}

// spaceLabels are the labels of the Space of the Unit under test.
const spaceLabels = "(functionContext.?SpaceLabels.orValue(null) != null ? functionContext.SpaceLabels : {})"

// fullImage is the image of container c with the registry Docker assumes made explicit:
// nginx is docker.io/library/nginx, and bitnami/redis is docker.io/bitnami/redis.
const fullImage = "(c.image.contains('/') && (c.image.split('/')[0].contains('.') || c.image.split('/')[0].contains(':') || c.image.split('/')[0] == 'localhost') ? c.image : " +
	"'docker.io/' + (c.image.contains('/') ? c.image : 'library/' + c.image))"

// imageFailures fails each image of a container or init container for which bad holds.
func imageFailures(bad, detail string) string {
	each := func(list string) string {
		return eachContainer(list, failIf("has(c.image) && "+bad, fieldPath(list, "image"), celrule.Some("c.image"), detail))
	}
	return each("containers") + " + " + each("initContainers")
}

var (
	celNoRuntimeSocket = volumeFailures(hostPathIn(runtimeSockets), "hostPath.path", celrule.Some("v.hostPath.path"),
		"'volume ' + v.name + ' mounts the container runtime socket, or the directory holding it, ' + v.hostPath.path")
	celNoSensitiveHostPaths = volumeFailures(hostPathIn(sensitiveHostPaths), "hostPath.path", celrule.Some("v.hostPath.path"),
		"'volume ' + v.name + ' mounts the node directory ' + v.hostPath.path")
	celReadOnlyHostPaths = eachContainer("containers",
		"has(c.volumeMounts) && has("+podSpec+".volumes) ? c.volumeMounts.filter(m, "+podSpec+".volumes.exists(v, v.name == m.name && has(v.hostPath)) && !(has(m.readOnly) && m.readOnly))"+
			".map(m, "+celrule.Failure("'spec.template.spec.containers.?name=' + c.name + '.volumeMounts.?mountPath=' + m.mountPath.replace('.', '~1') + '.readOnly'",
			"m.?readOnly", "'container ' + c.name + ' mounts the hostPath volume ' + m.name + ' writable at ' + m.mountPath")+") : []")
	celDropNetRaw = containerFailures(
		containerHas("securityContext.capabilities.drop")+" && c.securityContext.capabilities.drop.exists(d, d == 'NET_RAW' || d == 'ALL')",
		"securityContext.capabilities.drop", "c.?securityContext.?capabilities.?drop",
		"'container ' + c.name + ' does not drop NET_RAW, or ALL'")
	celNonRootGroups = failIf("has("+podSpec+".securityContext) && has("+podSpec+".securityContext.runAsGroup) && "+podSpec+".securityContext.runAsGroup == 0",
		celrule.Lit("spec.template.spec.securityContext.runAsGroup"), celrule.Some("0"), "'the pod runs as group 0'") + " + " +
		failIf("has("+podSpec+".securityContext) && has("+podSpec+".securityContext.fsGroup) && "+podSpec+".securityContext.fsGroup == 0",
			celrule.Lit("spec.template.spec.securityContext.fsGroup"), celrule.Some("0"), "'the pod owns its volumes as group 0'") + " + " +
		failIf("has("+podSpec+".securityContext) && has("+podSpec+".securityContext.supplementalGroups) && "+podSpec+".securityContext.supplementalGroups.exists(g, g == 0)",
			celrule.Lit("spec.template.spec.securityContext.supplementalGroups"), celrule.Some(podSpec+".securityContext.supplementalGroups"), "'the pod adds group 0'") + " + " +
		eachContainer("containers", failIf(containerHas("securityContext.runAsGroup")+" && c.securityContext.runAsGroup == 0",
			fieldPath("containers", "securityContext.runAsGroup"), celrule.Some("0"), "'container ' + c.name + ' runs as group 0'"))
	celDedicatedServiceAccount = failIf("!has("+podSpec+".serviceAccountName) || "+podSpec+".serviceAccountName == 'default'",
		celrule.Lit("spec.template.spec.serviceAccountName"), podSpec+".?serviceAccountName", "'the pod runs as its namespace\\'s default ServiceAccount'")
	celNoSSHPort = eachContainer("containers", "has(c.ports) ? c.ports.filter(p, has(p.containerPort) && p.containerPort == 22).map(p, "+
		celrule.Failure(fieldPath("containers", "ports.?containerPort=22.containerPort"), celrule.Some("22"), "'container ' + c.name + ' exposes port 22, for SSH'")+") : []")
	celHighUID = eachContainer("containers", highID("runAsUser", "user")+" + "+highID("runAsGroup", "group"))

	celRequests          = eachContainer("containers", request("cpu")+" + "+request("memory"))
	celEmptyDirSizeLimit = volumeFailures("has(v.emptyDir) && (v.emptyDir == null || !has(v.emptyDir.sizeLimit))", "emptyDir.sizeLimit", celrule.None,
		"'emptyDir volume ' + v.name + (v.emptyDir != null && has(v.emptyDir.medium) && v.emptyDir.medium == 'Memory' ? ', which is in memory and counts against the memory limit,' : '') + ' sets no sizeLimit'")
	celEphemeralStorage         = eachContainer("containers", resourceSet("requests", "ephemeral-storage")+" + "+resourceSet("limits", "ephemeral-storage"))
	celMemoryRequestEqualsLimit = eachContainer("containers", failIf(
		"!("+containerHas("resources.requests")+" && "+containerHas("resources.limits")+" && 'memory' in c.resources.requests && 'memory' in c.resources.limits && "+
			"string(c.resources.requests.memory) == string(c.resources.limits.memory))",
		fieldPath("containers", "resources.requests.memory"), "c.?resources.?requests.?memory",
		"'container ' + c.name + ' does not request the memory it is limited to, as written'"))

	celReadinessProbe = eachContainer("containers", failIf("has(c.ports) && size(c.ports) > 0 && !has(c.readinessProbe)",
		fieldPath("containers", "readinessProbe"), celrule.None, "'container ' + c.name + ' exposes a port but has no readiness probe'"))
	celProbesDiffer = eachContainer("containers", failIf("has(c.livenessProbe) && has(c.readinessProbe) && c.livenessProbe == c.readinessProbe",
		fieldPath("containers", "livenessProbe"), celrule.None, "'container ' + c.name + ' has the same liveness and readiness probes, so a slow container is restarted as soon as it stops taking traffic'"))
	celLivenessProbe = eachContainer("containers", failIf("!has(c.livenessProbe)",
		fieldPath("containers", "livenessProbe"), celrule.None, "'container ' + c.name + ' has no liveness probe'"))
	celPDBUnhealthyEviction = failIf("!(has(r.spec) && has(r.spec.unhealthyPodEvictionPolicy) && r.spec.unhealthyPodEvictionPolicy == 'AlwaysAllow')",
		celrule.Lit("spec.unhealthyPodEvictionPolicy"), "r.spec.?unhealthyPodEvictionPolicy",
		"'the PodDisruptionBudget counts unhealthy pods, so they can block draining a node'")
	celReplicas = failIf(
		"has(r.spec) && has(r.spec.replicas) && params['replicas.environments'].split(',').exists(e, e.trim().lowerAscii() == "+spaceLabels+"[?'Environment'].orValue('').lowerAscii()) && "+
			"r.spec.replicas < int(params['replicas.min'])",
		celrule.Lit("spec.replicas"), celrule.Some("r.spec.replicas"),
		"'the workload runs ' + string(r.spec.replicas) + ' replicas in ' + "+spaceLabels+"['Environment'] + ', fewer than ' + params['replicas.min']")
	celRollingUpdate = failIf("has(r.spec) && has(r.spec.strategy) && has(r.spec.strategy.type) && r.spec.strategy.type != 'RollingUpdate'",
		celrule.Lit("spec.strategy.type"), celrule.Some("r.spec.strategy.type"), "'the Deployment replaces all its pods at once rather than rolling'")
	celNoBarePods = "[" + celrule.Failure(celrule.Lit("kind"), celrule.Some("r.kind"), "'a bare Pod is not replaced when it or its node fails'") + "]"
	celJobCleanup = failIf("r.kind == 'Job' && !(has(r.spec) && has(r.spec.ttlSecondsAfterFinished)) && !(has(r.metadata.ownerReferences) && r.metadata.ownerReferences.exists(o, o.kind == 'CronJob'))",
		celrule.Lit("spec.ttlSecondsAfterFinished"), celrule.None, "'the Job sets no ttlSecondsAfterFinished, so it and its pods stay after it finishes'") + " + " +
		failIf("r.kind == 'CronJob' && !(has(r.spec) && has(r.spec.startingDeadlineSeconds))",
			celrule.Lit("spec.startingDeadlineSeconds"), celrule.None, "'the CronJob sets no startingDeadlineSeconds, so a run missed while the controller was down starts late'") + " + " +
		failIf("r.kind == 'CronJob' && !(has(r.spec) && has(r.spec.concurrencyPolicy))",
			celrule.Lit("spec.concurrencyPolicy"), celrule.None, "'the CronJob sets no concurrencyPolicy, so a slow run overlaps the next'")
	celPriorityClass = failIf("!has("+podSpec+".priorityClassName) || !params['priority-class.allowed'].split(',').exists(p, p.trim() == "+podSpec+".priorityClassName)",
		celrule.Lit("spec.template.spec.priorityClassName"), podSpec+".?priorityClassName", "'the pod does not use one of the allowed priority classes, ' + params['priority-class.allowed']")

	celAllowedRegistries = imageFailures("!params['allowed-registries.registries'].split(',').exists(reg, reg.trim() != '' && "+fullImage+".startsWith(reg.trim().endsWith('/') ? reg.trim() : reg.trim() + '/'))",
		"'container ' + c.name + ' pulls ' + c.image + ' from a registry not allowed, which are ' + params['allowed-registries.registries']")
	celDeprecatedRegistries = imageFailures("(c.image.startsWith('k8s.gcr.io/') || c.image.startsWith('gcr.io/google_containers/') || c.image.startsWith('gcr.io/google-containers/'))",
		"'container ' + c.name + ' pulls ' + c.image + ' from a registry frozen since 2023; pull it from registry.k8s.io'")
	celImageDigest = imageFailures("!c.image.contains('@sha256:')", "'container ' + c.name + ' pulls ' + c.image + ' by image tag, which can change, rather than by digest'")
)

// highID fails container c whose user or group, its own or the pod's, is unset or 10000 or below.
func highID(field, noun string) string {
	id := "(" + containerHas("securityContext."+field) + " ? c.securityContext." + field + " : (has(" + podSpec + ".securityContext) && has(" + podSpec + ".securityContext." + field + ") ? " + podSpec + ".securityContext." + field + " : -1))"
	return failIf(id+" <= 10000", fieldPath("containers", "securityContext."+field), "("+id+" >= 0 ? optional.of(dyn("+id+")) : optional.none())",
		"'container ' + c.name + ' runs as "+noun+" ' + ("+id+" >= 0 ? string("+id+") : 'unset') + ', not above 10000'")
}

// request fails container c that requests none of resource.
func request(resource string) string {
	return failIf("!("+containerHas("resources.requests")+" && "+celrule.Lit(resource)+" in c.resources.requests)",
		fieldPath("containers", "resources.requests."+resource), celrule.None, "'container ' + c.name + ' requests no "+resource+"'")
}

// resourceSet fails container c that sets none of resource under kind, requests or limits.
func resourceSet(kind, resource string) string {
	return failIf("!("+containerHas("resources."+kind)+" && "+celrule.Lit(resource)+" in c.resources."+kind+")",
		fieldPath("containers", "resources."+kind+"."+resource), celrule.None, "'container ' + c.name + ' sets no "+resource+" in its "+kind+"'")
}

var (
	readinessTypes = []string{"apps/v1/Deployment", "apps/v1/StatefulSet", "apps/v1/DaemonSet"}
	jobTypes       = []string{"batch/v1/Job", "batch/v1/CronJob"}
	pdbTypes       = []string{"policy/v1/PodDisruptionBudget"}
	scaledTypes    = []string{"apps/v1/Deployment", "apps/v1/StatefulSet"}
)

func celCheck(control, name, priority, displayName, description, failures string, types []string, corresponds ...string) guardrails.Check {
	return guardrails.Check{Control: control, Name: name, Rule: name, DisplayName: displayName, Description: description,
		Engine: guardrails.EngineCEL, Priority: priority, ResourceTypes: types, Failures: failures, Corresponds: corresponds}
}

func optionalCheck(c guardrails.Check) guardrails.Check {
	c.Optional = true
	return c
}

func withParams(c guardrails.Check, params ...guardrails.Param) guardrails.Check {
	c.Params = params
	return c
}

func appliedToTarget(c guardrails.Check) guardrails.Check {
	c.AppliedTo = guardrails.AppliedToTarget
	return c
}

var moreChecks = []guardrails.Check{
	// Hardening.
	celCheck("CH-0321", "no-runtime-socket", guardrails.PriorityHigh, "No container runtime socket mounts",
		"Warns on a hostPath volume of the Docker, containerd or CRI-O socket, or a directory holding one such as /var/run, which gives the pod control of every container on its node. Fix: remove the volume; a node agent that needs it is excepted.",
		celNoRuntimeSocket, celrule.Controllers, "KS:C-0074", "KL:docker-sock", "KY:disallow-cri-sock-mount"),
	celCheck("CH-0322", "no-sensitive-host-paths", guardrails.PriorityHigh, "No mounts of sensitive node directories",
		"Warns on a hostPath volume of a node directory that controls the node: /, /etc, /proc, /sys, /dev, /boot, /usr, /lib, /root, or the kubelet's or a runtime's state under /var/lib. A subdirectory such as /etc/ssl/certs passes. Fix: mount only the subdirectory the pod needs, read-only, or except a node agent.",
		celNoSensitiveHostPaths, celrule.Controllers, "KL:sensitive-host-mounts"),
	celCheck("CH-0323", "readonly-host-paths", guardrails.PriorityMedium, "hostPath mounts are read-only",
		"Warns on a container that mounts a hostPath volume without readOnly: true, so it can write to the node. Fix: set readOnly: true on the mount, or except a pod that has to write there.",
		celReadOnlyHostPaths, celrule.Controllers, "KS:C-0045", "KL:writable-host-mount", "KY:ensure-readonly-hostpath"),
	withFix(celCheck("CH-0325", "drop-net-raw", guardrails.PriorityMedium, "Containers drop NET_RAW",
		"Warns on a container that does not drop the NET_RAW capability, or ALL, with which it can craft packets and spoof traffic on the node's network. Fix: `cub manage workload harden <space>/<unit>` drops ALL.",
		celDropNetRaw, celrule.Controllers, "KS:C-0199", "KL:drop-net-raw-capability", "KY:require-drop-cap-net-raw"), hardenFix),
	celCheck("CH-0326", "non-root-groups", guardrails.PriorityMedium, "Pods do not run as group 0",
		"Warns on a pod or container that sets runAsGroup, fsGroup or supplementalGroups to 0, the root group, which owns files root owns. Unset passes. Fix: use a group other than 0; `cub manage workload harden <space>/<unit>` sets 3000 and 2000 where unset.",
		celNonRootGroups, celrule.Controllers, "KY:require-non-root-groups", "GK:users", "GK:fsgroup"),
	celCheck("CH-0328", "dedicated-service-account", guardrails.PriorityLow, "Pods run as a ServiceAccount of their own",
		"Warns on a pod that sets no serviceAccountName, or default, so it shares its namespace's default ServiceAccount, and whatever is bound to it, with every other such pod. Fix: create a ServiceAccount for the workload and name it.",
		celDedicatedServiceAccount, celrule.Controllers, "KL:default-service-account", "KS:C-0189", "KS:C-0225"),
	celCheck("CH-0329", "no-ssh-port", guardrails.PriorityLow, "No SSH port",
		"Warns on a container exposing port 22, which means it runs an SSH server, a way in that bypasses the cluster's access control. Fix: remove the SSH server, and use kubectl exec.",
		celNoSSHPort, celrule.Controllers, "KS:C-0042", "KL:ssh-port"),
	optionalCheck(celCheck("CH-0331", "high-uid", guardrails.PriorityLow, "Containers run as users and groups above 10000",
		"Warns on a container whose user or group, its own or its pod's, is unset or 10000 or below, where it could share an ID with a user of the node. Optional. Fix: set runAsUser and runAsGroup above 10000.",
		celHighUID, celrule.Controllers, "SC:container-security-context-user-group-id")),

	// Resources.
	withFix(celCheck("CH-0341", "requests", guardrails.PriorityHigh, "Containers request CPU and memory",
		"Warns on a container that requests no CPU or no memory, so the scheduler places it as if it needed none and it is the first evicted. Fix: `cub manage workload set-resources <space>/<unit>`.",
		celRequests, celrule.Controllers, "KS:C-0268", "KS:C-0269", "SC:container-resources", "GK:containerrequests", "KY:require-pod-requests-limits"),
		"cub manage workload set-resources <space>/<unit>"),
	celCheck("CH-0342", "emptydir-size-limit", guardrails.PriorityMedium, "emptyDir volumes set a sizeLimit",
		"Warns on an emptyDir volume with no sizeLimit, which can fill the node's disk, or, with medium: Memory, use memory the container's limit counts. Fix: set sizeLimit.",
		celEmptyDirSizeLimit, celrule.Controllers, "KY:require-emptydir-requests-limits"),
	optionalCheck(celCheck("CH-0343", "ephemeral-storage", guardrails.PriorityLow, "Containers request and limit ephemeral storage",
		"Warns on a container that does not set both a request and a limit of ephemeral-storage, the node disk its logs and writable layer use. Optional. Fix: set resources.requests and resources.limits ephemeral-storage.",
		celEphemeralStorage, celrule.Controllers, "SC:container-ephemeral-storage", "GK:ephemeralstoragelimit")),
	optionalCheck(celCheck("CH-0346", "memory-request-equals-limit", guardrails.PriorityLow, "Containers request the memory they are limited to",
		"Warns on a container whose memory request is not its memory limit, as written, so the node can run out of memory before the container reaches its limit. Optional. Fix: set the request to the limit.",
		celMemoryRequestEqualsLimit, celrule.Controllers, "KY:memory-requests-equal-limits")),

	// Reliability.
	withFix(celCheck("CH-0363", "readiness-probe", guardrails.PriorityMedium, "Containers serving a port have a readiness probe",
		"Warns on a container of a Deployment, StatefulSet or DaemonSet that exposes a port and has no readiness probe, so it gets traffic before it can serve it. Fix: `cub manage workload set-probes <space>/<unit>`.",
		celReadinessProbe, readinessTypes, "KS:C-0018", "SC:pod-probes", "KL:no-readiness-probe", "GK:requiredprobes", "KY:require-probes"),
		"cub manage workload set-probes <space>/<unit>"),
	celCheck("CH-0364", "probes-differ", guardrails.PriorityMedium, "Liveness and readiness probes differ",
		"Warns on a container whose liveness probe is its readiness probe, so a container that is merely busy is restarted when it should only be taken out of service. Fix: make the liveness probe more tolerant, or check something else.",
		celProbesDiffer, celrule.Controllers, "SC:pod-probes-identical", "KY:ensure-probes-different"),
	optionalCheck(celCheck("CH-0365", "liveness-probe", guardrails.PriorityLow, "Containers have a liveness probe",
		"Warns on a container with no liveness probe, so one that hangs is not restarted. Optional: a bad liveness probe does more harm than none. Fix: `cub manage workload set-probes <space>/<unit>`.",
		celLivenessProbe, celrule.Controllers, "KS:C-0056", "KL:no-liveness-probe")),
	celCheck("CH-0367", "pdb-unhealthy-eviction", guardrails.PriorityLow, "PodDisruptionBudgets let unhealthy pods go",
		"Warns on a PodDisruptionBudget without unhealthyPodEvictionPolicy: AlwaysAllow, so pods that are not ready count against it and can stop a node from draining. Fix: set unhealthyPodEvictionPolicy: AlwaysAllow.",
		celPDBUnhealthyEviction, pdbTypes, "KL:pdb-unhealthy-pod-eviction-policy"),
	withParams(celCheck("CH-0368", "replicas", guardrails.PriorityMedium, "Workloads run enough replicas where it matters",
		"Warns on a Deployment or StatefulSet that sets replicas below replicas.min in a Space whose Environment label is one of replicas.environments, so one pod's loss is an outage. A workload that sets no replicas is taken to be scaled by an autoscaler, and passes. Fix: raise replicas, or add an autoscaler.",
		celReplicas, scaledTypes, "SC:deployment-replicas", "KL:minimum-three-replicas", "KY:require-deployments-have-multiple-replicas"),
		guardrails.Param{Name: "min", Description: "the fewest replicas", Default: "2"},
		guardrails.Param{Name: "environments", Description: "the Environment label values, comma-separated, where the minimum applies", Default: "prod"}),
	celCheck("CH-0369", "rolling-update", guardrails.PriorityLow, "Deployments roll their updates",
		"Warns on a Deployment whose strategy is Recreate, which stops every pod before starting the new ones. Fix: use RollingUpdate, or except a workload that cannot run two versions at once.",
		celRollingUpdate, []string{"apps/v1/Deployment"}, "SC:deployment-strategy", "KL:no-rolling-update-strategy"),
	celCheck("CH-0370", "no-bare-pods", guardrails.PriorityMedium, "No bare Pods",
		"Warns on a Pod in configuration rather than a controller's template: nothing replaces it when it or its node fails. Fix: run it as a Deployment, StatefulSet, DaemonSet or Job.",
		celNoBarePods, []string{"v1/Pod"}, "KS:C-0073", "KY:prevent-bare-pods"),
	celCheck("CH-0371", "job-cleanup", guardrails.PriorityLow, "Jobs and CronJobs clean up after themselves",
		"Warns on a Job, not owned by a CronJob, with no ttlSecondsAfterFinished, and on a CronJob with no startingDeadlineSeconds or concurrencyPolicy. Fix: set them.",
		celJobCleanup, jobTypes, "KL:job-ttl-seconds-after-finished", "SC:cronjob-has-deadline"),
	optionalCheck(withParams(celCheck("CH-0372", "priority-class", guardrails.PriorityLow, "Pods use an allowed priority class",
		"Warns on a pod that sets no priorityClassName, or one not in priority-class.allowed. Optional. Fix: set an allowed priorityClassName.",
		celPriorityClass, celrule.Controllers, "KL:priority-class-name", "KY:require-pod-priorityclassname"),
		guardrails.Param{Name: "allowed", Description: "the allowed priority classes, comma-separated", Required: true})),

	// Images.
	withParams(celCheck("CH-0381", "allowed-registries", guardrails.PriorityHigh, "Images come from allowed registries",
		"Warns on a container or init container whose image is not under one of allowed-registries.registries, comma-separated registries or repository prefixes such as ghcr.io/my-org. An image naming no registry is from docker.io. Installed once it is given the registries. Fix: pull the image from an allowed registry.",
		celAllowedRegistries, celrule.Controllers, "KS:C-0001", "KS:C-0078", "GK:allowedrepos", "KY:restrict-image-registries"),
		guardrails.Param{Name: "registries", Description: "allowed registries or repository prefixes, comma-separated", Required: true}),
	celCheck("CH-0382", "deprecated-registries", guardrails.PriorityHigh, "No images from frozen registries",
		"Warns on an image from k8s.gcr.io or gcr.io/google_containers, frozen since 2023, so its images get no updates. Fix: pull it from registry.k8s.io.",
		celDeprecatedRegistries, celrule.Controllers, "KY:restrict-deprecated-registry", "KS:C-0253"),
	optionalCheck(appliedToTarget(celCheck("CH-0384", "image-digest", guardrails.PriorityLow, "Released images are pinned by digest",
		"Warns on a Unit released to a Target with an image pulled by image tag rather than by digest, which a push to the tag can change under it. Optional. Fix: pin the image by digest.",
		celImageDigest, celrule.Controllers, "GK:imagedigests", "KY:require-image-checksum", "KS:C-0312"))),
}
