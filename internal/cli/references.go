// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

// The reference checks: a name one resource gives for another -- a pod's ServiceAccount,
// ConfigMap or claim, an Ingress's backend Service, a StatefulSet's Service -- names a resource
// on the cluster, and a Service's selector selects some pods. Each is a rule of its own, reading
// only the resources it names as OtherData, which its rule's Trigger selects for each Space from
// the Units released to the same Target. A Unit with no Target is on no known cluster, so the
// checks pass it.
//
// What the cluster makes itself is not looked for: the default ServiceAccount, and the
// kube-root-ca.crt ConfigMap in every namespace. A reference marked optional may name nothing.
// Secrets are not in ConfigHub, so references to them are not checked.

import (
	"github.com/confighub/sdk/managerkit/guardrails"

	"github.com/confighub/cub-manage/internal/celrule"
)

// The resources the reference checks read.
var (
	otherServiceAccounts = guardrails.OtherData{Key: "serviceaccounts", ResourceTypes: []string{"v1/ServiceAccount"}}
	otherConfigMaps      = guardrails.OtherData{Key: "configmaps", ResourceTypes: []string{"v1/ConfigMap"}}
	otherClaims          = guardrails.OtherData{Key: "claims", ResourceTypes: []string{"v1/PersistentVolumeClaim"}}
	otherServices        = guardrails.OtherData{Key: "services", ResourceTypes: []string{"v1/Service"}}
	otherWorkloads       = guardrails.OtherData{Key: "workloads", ResourceTypes: celrule.WorkloadTypes()}
)

// namedIn is whether a resource of the OtherData key in r's namespace has the name, a CEL string.
func namedIn(key, name string) string {
	return celrule.Other(key) + ".exists(o, " + celrule.NamespaceOf("o") + " == " + celrule.NamespaceOf("r") +
		" && o.metadata.name == " + name + ")"
}

// orEmptyList is the list at the CEL expression x's field, or an empty list when it is missing
// or null.
func orEmptyList(x, field string) string {
	return "(" + x + ".?" + field + ".orValue(null) == null ? [] : " + x + "." + field + ")"
}

var (
	podSpec     = celrule.PodSpecOf("r")
	podSpecPath = celrule.PodSpecPath()

	serviceAccount = celrule.ServiceAccountOf("r")

	celServiceAccountExists = celrule.When(
		celrule.OnACluster+" && podLabels(r) != null && "+serviceAccount+" != 'default' && !"+namedIn(otherServiceAccounts.Key, serviceAccount),
		"["+celrule.Failure(podSpecPath+" + '.serviceAccountName'", celrule.Some(serviceAccount),
			"'no ServiceAccount ' + "+serviceAccount+" + ' in namespace ' + "+celrule.NamespaceOf("r")+" + ' on the cluster'")+"]")

	// configMapExists is whether the ConfigMap name, a CEL string, is on the cluster in r's
	// namespace, or is the one the cluster makes in each.
	configMapExists = func(name string) string {
		return "(" + name + " == 'kube-root-ca.crt' || " + namedIn(otherConfigMaps.Key, name) + ")"
	}

	celConfigMapExists = celrule.When(celrule.OnACluster+" && podLabels(r) != null",
		orEmptyList(podSpec, "volumes")+".filter(v, v.?configMap.?name.orValue('') != '' && !v.?configMap.?optional.orValue(false) && !"+configMapExists("v.configMap.name")+").map(v, "+
			celrule.Failure(podSpecPath+" + '.volumes.?name=' + v.name + '.configMap.name'", celrule.Some("v.configMap.name"),
				"'volume ' + v.name + ' names no ConfigMap ' + v.configMap.name + ' in namespace ' + "+celrule.NamespaceOf("r"))+")"+
			" + "+containerConfigMapFailures("containers")+" + "+containerConfigMapFailures("initContainers"))

	celClaimExists = celrule.When(celrule.OnACluster+" && podLabels(r) != null",
		orEmptyList(podSpec, "volumes")+".filter(v, v.?persistentVolumeClaim.?claimName.orValue('') != '' && !"+namedIn(otherClaims.Key, "v.persistentVolumeClaim.claimName")+").map(v, "+
			celrule.Failure(podSpecPath+" + '.volumes.?name=' + v.name + '.persistentVolumeClaim.claimName'", celrule.Some("v.persistentVolumeClaim.claimName"),
				"'volume ' + v.name + ' names no PersistentVolumeClaim ' + v.persistentVolumeClaim.claimName + ' in namespace ' + "+celrule.NamespaceOf("r"))+")")

	celIngressBackendExists = celrule.When(celrule.OnACluster,
		"(r.?spec.?defaultBackend.?service.?name.orValue('') != '' ? "+backendFailures("r.spec.defaultBackend.service", "'spec.defaultBackend.service'")+" : []) + "+
			"lists.range(size("+orEmptyList("r.spec", "rules")+")).map(i, "+
			"lists.range(size("+orEmptyList("r.spec.rules[i].?http.orValue({})", "paths")+")).filter(j, r.spec.rules[i].http.paths[j].?backend.?service.?name.orValue('') != '').map(j, "+
			backendFailures("r.spec.rules[i].http.paths[j].backend.service", "'spec.rules.' + string(i) + '.http.paths.' + string(j) + '.backend.service'")+
			").flatten()).flatten()")

	celStatefulSetServiceExists = celrule.When(
		celrule.OnACluster+" && r.?spec.?serviceName.orValue('') != '' && !"+namedIn(otherServices.Key, "r.spec.serviceName"),
		"["+celrule.Failure("'spec.serviceName'", celrule.Some("r.spec.serviceName"),
			"'no Service ' + r.spec.serviceName + ' in namespace ' + "+celrule.NamespaceOf("r")+" + ' governs the StatefulSet'")+"]")

	// A Service's selector is a map of labels, all of which a pod must have. One with none, or
	// an ExternalName Service, selects no pods by design.
	serviceSelector       = "(r.?spec.?selector.orValue(null) == null ? {} : r.spec.selector)"
	celServiceSelectsPods = celrule.When(
		celrule.OnACluster+" && r.?spec.?type.orValue('') != 'ExternalName' && size("+serviceSelector+") > 0 && "+
			"!"+celrule.Other(otherWorkloads.Key)+".exists(w, "+celrule.NamespaceOf("w")+" == "+celrule.NamespaceOf("r")+" && podLabels(w) != null && "+
			serviceSelector+".all(k, k in podLabels(w) && string(podLabels(w)[k]) == string("+serviceSelector+"[k])))",
		"["+celrule.Failure("'spec.selector'", celrule.Some(serviceSelector),
			"'the selector selects no workload in namespace ' + "+celrule.NamespaceOf("r"))+"]")
)

// containerConfigMapFailures fails each ConfigMap a container of the list, containers or
// initContainers, names in env or envFrom that is not on the cluster.
func containerConfigMapFailures(list string) string {
	containers := orEmptyList(podSpec, list)
	containerPath := podSpecPath + " + '." + list + ".?name=' + c.name"
	env := containers + ".map(c, " + orEmptyList("c", "env") +
		".filter(e, e.?valueFrom.?configMapKeyRef.?name.orValue('') != '' && !e.valueFrom.configMapKeyRef.?optional.orValue(false) && !" +
		configMapExists("e.valueFrom.configMapKeyRef.name") + ").map(e, " +
		celrule.Failure(containerPath+" + '.env.?name=' + e.name + '.valueFrom.configMapKeyRef.name'", celrule.Some("e.valueFrom.configMapKeyRef.name"),
			"'container ' + c.name + ' reads ' + e.name + ' from no ConfigMap ' + e.valueFrom.configMapKeyRef.name + ' in namespace ' + "+celrule.NamespaceOf("r")) +
		")).flatten()"
	envFrom := containers + ".map(c, lists.range(size(" + orEmptyList("c", "envFrom") + ")).filter(j, " +
		"c.envFrom[j].?configMapRef.?name.orValue('') != '' && !c.envFrom[j].configMapRef.?optional.orValue(false) && !" +
		configMapExists("c.envFrom[j].configMapRef.name") + ").map(j, " +
		celrule.Failure(containerPath+" + '.envFrom.' + string(j) + '.configMapRef.name'", celrule.Some("c.envFrom[j].configMapRef.name"),
			"'container ' + c.name + ' reads its environment from no ConfigMap ' + c.envFrom[j].configMapRef.name + ' in namespace ' + "+celrule.NamespaceOf("r")) +
		")).flatten()"
	return env + " + " + envFrom
}

// backendFailures fails an Ingress backend service, the CEL expression svc at path, a CEL
// string, that names no Service in the namespace, or a port the Service does not have.
func backendFailures(svc, path string) string {
	named := celrule.Other(otherServices.Key) + ".filter(s, " + celrule.NamespaceOf("s") + " == " + celrule.NamespaceOf("r") +
		" && s.metadata.name == " + svc + ".name)"
	hasPort := named + ".exists(s, " + orEmptyList("s.spec", "ports") + ".exists(p, " +
		"(" + svc + ".?port.?number.orValue(0) != 0 && int(p.?port.orValue(0)) == int(" + svc + ".port.number)) || " +
		"(" + svc + ".?port.?name.orValue('') != '' && p.?name.orValue('') == " + svc + ".port.name)))"
	namesPort := "(" + svc + ".?port.?number.orValue(0) != 0 || " + svc + ".?port.?name.orValue('') != '')"
	return "(size(" + named + ") == 0 ? [" +
		celrule.Failure(path+" + '.name'", celrule.Some(svc+".name"),
			"'the backend names no Service ' + "+svc+".name + ' in namespace ' + "+celrule.NamespaceOf("r")) +
		"] : " + namesPort + " && !" + hasPort + " ? [" +
		celrule.Failure(path+" + '.port'", celrule.Some(svc+".port"),
			"'the Service ' + "+svc+".name + ' has no such port'") +
		"] : [])"
}

// referenceCheck is a reference check, a rule of its own, reading the other data it names.
func referenceCheck(control, name, displayName, description, failures string, types []string, reads guardrails.OtherData, corresponds ...string) guardrails.Check {
	return guardrails.Check{Control: control, Name: name, Rule: name, DisplayName: displayName,
		Description: description + " Not checked for a Unit with no Target.",
		Engine:      guardrails.EngineCEL, Priority: guardrails.PriorityMedium, ResourceTypes: types, Failures: failures,
		OtherData: []guardrails.OtherData{reads}, Corresponds: corresponds}
}

var referenceChecks = []guardrails.Check{
	referenceCheck("CH-0220", "service-account-exists", "Pods run as a ServiceAccount on the cluster",
		"Warns on a pod whose serviceAccountName names no ServiceAccount in its namespace on its cluster, so it is not created. The default ServiceAccount is taken to exist. Fix: add the ServiceAccount, or correct the name.",
		celServiceAccountExists, celrule.WorkloadTypes(), otherServiceAccounts, "KL:non-existent-service-account"),
	referenceCheck("CH-0221", "configmap-exists", "ConfigMaps a pod reads are on the cluster",
		"Warns on a pod whose ConfigMap volume, env configMapKeyRef or envFrom configMapRef names no ConfigMap in its namespace on its cluster, so it does not start. A reference marked optional, and kube-root-ca.crt, which the cluster makes, are not checked. Fix: add the ConfigMap, or correct the name.",
		celConfigMapExists, celrule.WorkloadTypes(), otherConfigMaps, "KL:env-value-from"),
	referenceCheck("CH-0222", "claim-exists", "PersistentVolumeClaims a pod mounts are on the cluster",
		"Warns on a pod volume whose persistentVolumeClaim names no PersistentVolumeClaim in its namespace on its cluster, so the pod is not scheduled. Fix: add the claim, or correct the name.",
		celClaimExists, celrule.WorkloadTypes(), otherClaims),
	referenceCheck("CH-0223", "ingress-backend-exists", "Ingress backends are Services on the cluster",
		"Warns on an Ingress backend that names no Service in its namespace on its cluster, or a port the Service does not have, so its traffic gets no answer. Fix: correct the Service or port.",
		celIngressBackendExists, []string{"networking.k8s.io/v1/Ingress"}, otherServices, "SC:ingress-targets-service", "KL:dangling-ingress"),
	referenceCheck("CH-0224", "statefulset-service-exists", "StatefulSets name a Service on the cluster",
		"Warns on a StatefulSet whose serviceName names no Service in its namespace on its cluster, so its pods get no stable network names. Fix: add the headless Service, or correct the name.",
		celStatefulSetServiceExists, []string{"apps/v1/StatefulSet"}, otherServices, "SC:statefulset-has-servicename"),
	referenceCheck("CH-0225", "service-selects-pods", "Services select pods",
		"Warns on a Service whose selector selects no workload's pods in its namespace on its cluster, so it has no endpoints: most often a label renamed or misspelled. A Service with no selector, or of type ExternalName, is not checked. Fix: correct the selector, or except a Service whose pods are made outside ConfigHub.",
		celServiceSelectsPods, []string{"v1/Service"}, otherWorkloads, "SC:service-targets-pod", "KL:dangling-service"),
}
