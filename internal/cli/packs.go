// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

// The tool-level packs no command group owns: exposure, secrets, metadata and storage. Each is
// installed with `cub manage guardrails <pack> install`, and a Space opts into each on its own.

import (
	"github.com/confighub/sdk/managerkit/guardrails"

	"github.com/confighub/cub-manage/internal/celrule"
	celadmission "github.com/confighub/cub-manage/third_party/kubescape/cel-admission-library"
)

func celCheck(control, name, priority, displayName, description, failures string, types []string, corresponds ...string) guardrails.Check {
	return guardrails.Check{Control: control, Name: name, Rule: name, DisplayName: displayName, Description: description,
		Engine: guardrails.EngineCEL, Priority: priority, ResourceTypes: types, Failures: failures, Corresponds: corresponds}
}

func optional(c guardrails.Check) guardrails.Check {
	c.Optional = true
	return c
}

func withParam(c guardrails.Check, p guardrails.Param) guardrails.Check {
	c.Params = append(c.Params, p)
	return c
}

// failIf is a one-failure list when bad holds, and an empty one otherwise.
func failIf(bad, path, value, detail string) string {
	return "(" + bad + " ? [" + celrule.Failure(path, value, detail) + "] : [])"
}

// param is the value of the check's parameter name, empty when it is not given.
func param(check, name string) string {
	return "params[?'" + check + "." + name + "'].orValue('')"
}

// paramList is the comma-separated parameter as a list of trimmed, non-empty values.
func paramList(check, name string) string {
	return param(check, name) + ".split(',').map(s, s.trim()).filter(s, s != '')"
}

// The types of resources the packs read.
var (
	serviceTypes  = []string{"v1/Service"}
	ingressTypes  = []string{"networking.k8s.io/v1/Ingress"}
	gatewayTypes  = []string{"gateway.networking.k8s.io/v1/Gateway"}
	routeTypes    = []string{"gateway.networking.k8s.io/v1/HTTPRoute"}
	podTypes      = []string{"v1/Pod", "apps/v1/Deployment", "apps/v1/ReplicaSet", "apps/v1/DaemonSet", "apps/v1/StatefulSet", "batch/v1/Job", "batch/v1/CronJob"}
	pvTypes       = []string{"v1/PersistentVolume"}
	claimTypes    = []string{"v1/PersistentVolumeClaim", "apps/v1/StatefulSet"}
	flexTypes     = append([]string{"v1/PersistentVolume"}, celrule.Controllers...)
	serviceOfType = func(t string) string { return "has(r.spec) && has(r.spec.type) && r.spec.type == '" + t + "'" }
)

// traefikRouteTypes are Traefik's HTTP routes, under its API group's current name and the one before.
var traefikRouteTypes = []string{"traefik.io/v1alpha1/IngressRoute", "traefik.containo.us/v1alpha1/IngressRoute"}

// concat is the lists' elements, in a new list.
func concat(lists ...[]string) []string {
	var out []string
	for _, l := range lists {
		out = append(out, l...)
	}
	return out
}

// listFailures fails each element of the list at path under r for which bad holds of x, at the
// element's field.
func listFailures(path, bad, field, value, detail string) string {
	return "(has(r.spec) && has(r." + path + ") ? lists.range(size(r." + path + ")).filter(i, [r." + path + "[i]].exists(x, " + bad + "))" +
		".map(i, " + celrule.Failure(celrule.Indexed(path, "i", field), value, detail) + ") : [])"
}

// mapKeyFailures fails each key k of the map at field of r's metadata, labels or annotations, for
// which bad holds.
func mapKeyFailures(field, bad, detail string) string {
	m := "r.metadata." + field
	return "(has(" + m + ") && " + m + " != null ? " + m + ".filter(k, " + bad + ").map(k, " +
		celrule.Failure("'metadata."+field+".' + k.replace('.', '~1')", celrule.Some(m+"[k]"), detail) + ") : [])"
}

// requiredKeys fails each entry of the check's parameter keys, key or key=pattern, that the map
// at field of r's metadata lacks, or holds with a value not matching the pattern.
func requiredKeys(check, field, noun string) string {
	m := "(has(r.metadata." + field + ") && r.metadata." + field + " != null ? r.metadata." + field + " : {})"
	key := "e.split('=')[0]"
	pattern := "(e.contains('=') ? e.substring(size(" + key + ") + 1) : '')"
	return paramList(check, "keys") + ".filter(e, !(" + key + " in " + m + " && (" + pattern + " == '' || string(" + m + "[" + key + "]).matches(" + pattern + "))))" +
		".map(e, " + celrule.Failure("'metadata."+field+".' + "+key+".replace('.', '~1')", "("+key+" in "+m+" ? optional.of(dyn("+m+"["+key+"])) : optional.none())",
		"'the resource has no "+noun+" ' + "+key+" + ("+pattern+" != '' ? ' matching ' + "+pattern+" : '')") + ")"
}

var exposurePack = guardrails.Pack{
	Group: "exposure",
	Checks: []guardrails.Check{
		celCheck("CH-0601", "no-external-ips", guardrails.PriorityHigh, "Services set no externalIPs",
			"Warns on a Service with externalIPs, which lets whoever can create a Service intercept traffic to those addresses from the whole cluster (CVE-2020-8554). Fix: use a LoadBalancer or an Ingress.",
			failIf("has(r.spec) && has(r.spec.externalIPs) && size(r.spec.externalIPs) > 0", celrule.Lit("spec.externalIPs"), celrule.Some("r.spec.externalIPs"),
				"'the Service claims the external addresses ' + r.spec.externalIPs.join(', ')"),
			serviceTypes, "GK:externalip", "KY:restrict-service-external-ips"),
		celCheck("CH-0602", "no-localhost-externalname", guardrails.PriorityHigh, "ExternalName Services do not point at localhost",
			"Warns on an ExternalName Service naming localhost or a loopback address, which can reach whatever listens on the node of the proxy resolving it. Fix: name the real host.",
			failIf(serviceOfType("ExternalName")+" && has(r.spec.externalName) && (r.spec.externalName == 'localhost' || r.spec.externalName.startsWith('localhost.') || r.spec.externalName.startsWith('127.') || r.spec.externalName in ['::1', '0.0.0.0'])",
				celrule.Lit("spec.externalName"), celrule.Some("r.spec.externalName"), "'the Service points at ' + r.spec.externalName + ', a loopback address'"),
			serviceTypes, "KY:disallow-localhost-services"),
		celCheck("CH-0603", "no-nginx-snippets", guardrails.PriorityHigh, "Ingresses carry no ingress-nginx snippets",
			"Warns on an Ingress with an nginx.ingress.kubernetes.io annotation ending in -snippet, which injects raw configuration into ingress-nginx and has let Ingress authors read the controller's credentials (CVE-2021-25742 and later). Fix: remove the snippet, and use the controller's own annotations.",
			mapKeyFailures("annotations", "k.startsWith('nginx.ingress.kubernetes.io/') && k.endsWith('-snippet')", "'the Ingress injects configuration into ingress-nginx with ' + k"),
			ingressTypes, "KY:check-ingress-nginx-controller-version-and-annotation-policy", "KS:C-0059"),
		celCheck("CH-0604", "no-nodeport", guardrails.PriorityMedium, "No NodePort Services",
			"Warns on a NodePort Service, which opens its port on every node, past whatever fronts the cluster. Fix: use ClusterIP behind an Ingress, or a LoadBalancer.",
			failIf(serviceOfType("NodePort"), celrule.Lit("spec.type"), celrule.Some("r.spec.type"), "'the Service opens a port on every node'"),
			serviceTypes, "SC:service-type", "GK:block-nodeport-services", "KY:restrict-node-port", "KL:exposed-services"),
		celCheck("CH-0605", "ingress-tls", guardrails.PriorityMedium, "Ingresses and HTTPS listeners have TLS",
			"Warns on an Ingress with no tls section, on a Gateway HTTPS listener with no tls, and on a route of a Traefik IngressRoute with no tls that uses no middleware, so traffic is served in the clear or not at all. A route redirects to HTTPS with a middleware, so one with any middleware passes. Fix: add the certificate, or redirect to HTTPS.",
			"(r.kind == 'Ingress' ? "+failIf("!(has(r.spec) && has(r.spec.tls) && size(r.spec.tls) > 0)", celrule.Lit("spec.tls"), celrule.None, "'the Ingress serves no TLS'")+
				" : r.kind == 'IngressRoute' ? "+listFailures("spec.routes", "!has(r.spec.tls) && !(has(x.middlewares) && size(x.middlewares) > 0)", "middlewares", celrule.None,
				"'route ' + string(i) + ' serves in the clear: the IngressRoute has no tls, and the route no middleware, such as one redirecting to HTTPS'")+" : "+
				listFailures("spec.listeners", "has(x.protocol) && x.protocol == 'HTTPS' && !has(x.tls)", "tls", celrule.None, "'HTTPS listener ' + string(i) + ' has no tls'")+")",
			concat(ingressTypes, gatewayTypes, traefikRouteTypes), "KS:C-0263", "GK:httpsonly", "KY:require-ingress-https"),
		celCheck("CH-0606", "ingress-host", guardrails.PriorityMedium, "Ingresses and HTTPRoutes name their hosts",
			"Warns on an Ingress rule with no host or the bare wildcard *, an HTTPRoute with no hostnames or *, and a route of a Traefik IngressRoute whose match has no Host or HostRegexp, which take traffic for every host the controller serves. Fix: name the host.",
			"(r.kind == 'Ingress' ? "+listFailures("spec.rules", "!has(x.host) || x.host == '*'", "host", "r.spec.rules[i].?host", "'rule ' + string(i) + ' takes traffic for every host'")+
				" : r.kind == 'IngressRoute' ? "+listFailures("spec.routes", "!has(x.match) || !x.match.matches('Host(Regexp)?\\\\(')", "match", "r.spec.routes[i].?match",
				"'route ' + string(i) + ' takes traffic for every host: its match names none'")+" : "+
				failIf("!(has(r.spec) && has(r.spec.hostnames) && size(r.spec.hostnames) > 0 && !r.spec.hostnames.exists(h, h == '*'))", celrule.Lit("spec.hostnames"), "r.?spec.?hostnames",
					"'the HTTPRoute takes traffic for every host'")+")",
			concat(ingressTypes, routeTypes, traefikRouteTypes), "GK:block-wildcard-ingress", "KY:disallow-empty-ingress-host", "KY:restrict-ingress-wildcard"),
		uniqueIngressHost,
		celCheck("CH-0608", "gateway-allowed-routes", guardrails.PriorityMedium, "Gateway listeners admit routes from chosen namespaces",
			"Warns on a Gateway listener whose allowedRoutes admit routes from All namespaces, so any namespace can take its traffic. Fix: allow Same, or a selector of the namespaces that serve it.",
			listFailures("spec.listeners", "has(x.allowedRoutes) && has(x.allowedRoutes.namespaces) && has(x.allowedRoutes.namespaces.from) && x.allowedRoutes.namespaces.from == 'All'",
				"allowedRoutes.namespaces.from", celrule.Some("'All'"), "'listener ' + string(i) + ' admits routes from every namespace'"),
			gatewayTypes),
		optional(celCheck("CH-0609", "no-loadbalancer", guardrails.PriorityLow, "No LoadBalancer Services",
			"Warns on a LoadBalancer Service, which provisions a cloud load balancer exposing it. Optional: for a cluster whose traffic all enters through an Ingress or Gateway. Fix: use ClusterIP behind the Ingress or Gateway.",
			failIf(serviceOfType("LoadBalancer"), celrule.Lit("spec.type"), celrule.Some("r.spec.type"), "'the Service provisions a load balancer'"),
			serviceTypes, "GK:block-loadbalancer-services", "KY:restrict-loadbalancer")),
		optional(withParam(celCheck("CH-0610", "ingress-class", guardrails.PriorityLow, "Ingresses use an allowed class",
			"Warns on an Ingress whose ingressClassName is unset, or not one of ingress-class.allowed. Optional. Fix: set an allowed ingressClassName.",
			failIf("!(has(r.spec) && has(r.spec.ingressClassName) && r.spec.ingressClassName in "+paramList("ingress-class", "allowed")+")",
				celrule.Lit("spec.ingressClassName"), "r.?spec.?ingressClassName", "'the Ingress does not use one of the allowed classes, ' + "+param("ingress-class", "allowed")),
			ingressTypes, "KY:restrict-ingress-classes"),
			guardrails.Param{Name: "allowed", Description: "the allowed IngressClasses, comma-separated", Required: true})),
	},
}

// kubescapeSecrets is the policy the secrets pack adopts from Kubescape, C-0012, which applies one
// list of credential names and values to containers' env and to ConfigMaps' data.
var kubescapeSecrets = guardrails.PolicyLibrary{
	Name: "kubescape", Source: celadmission.Source, License: "Apache-2.0",
	Policies: celadmission.Policies("C-0012"), Params: celadmission.Params(), ParamRef: celadmission.ParamsName,
}

const credentialsPolicy = "kubescape-c-0012-deny-resources-with-sensitive-information-in-environment-variables"

func credentialsCheck(control, name, priority, displayName, description string, types []string) guardrails.Check {
	return guardrails.Check{Control: control, Name: name, Rule: name, DisplayName: displayName, Description: description,
		Engine: guardrails.EngineAdmissionPolicy, Priority: priority, Library: kubescapeSecrets.Name, Policy: credentialsPolicy,
		ResourceTypes: types, Corresponds: []string{"KS:C-0012"}}
}

var secretsPack = guardrails.Pack{
	Group:     "secrets",
	Libraries: []guardrails.PolicyLibrary{kubescapeSecrets},
	Checks: []guardrails.Check{
		celCheck("CH-0701", "no-secret-resources", guardrails.PriorityHigh, "No Secrets in configuration",
			"Warns on a Secret in configuration: secrets come from the secret store, through an ExternalSecret or the like, rather than being written into Units. Fix: move the values to the secret store and reference them.",
			"["+celrule.Failure(celrule.Lit("kind"), celrule.Some("r.kind"), "'a Secret is in configuration rather than the secret store'")+"]",
			[]string{"v1/Secret"}, "KS:C-0234"),
		credentialsCheck("CH-0702", "no-credentials-in-env", guardrails.PriorityHigh, "No credentials in environment variables",
			"Warns on a container with a literal environment variable whose name or value looks like a credential, by Kubescape's lists: names containing password, secret, token, key and the like, values like private keys and bearer tokens. A value from a secretKeyRef passes. The lists are settings in the Unit guardrails-kubescape-params in the policy Space, which no-credentials-in-configmaps reads too. Fix: move the value to a Secret from the secret store.",
			podTypes),
		credentialsCheck("CH-0703", "no-credentials-in-configmaps", guardrails.PriorityMedium, "No credentials in ConfigMaps",
			"Warns on a ConfigMap with a key or value that looks like a credential, by the lists no-credentials-in-env uses, in the Unit guardrails-kubescape-params in the policy Space. Fix: move the value to a Secret from the secret store.",
			[]string{"v1/ConfigMap"}),
		optional(celCheck("CH-0704", "secrets-files-not-env", guardrails.PriorityLow, "Secrets are mounted as files",
			"Warns on a container that reads a Secret into an environment variable or with envFrom, where it shows in the process environment, crash dumps and child processes. Optional. Fix: mount the Secret as a volume and read the file.",
			"(has(r.spec.template.spec.containers) ? r.spec.template.spec.containers.map(c, "+
				"(has(c.env) ? c.env.filter(e, has(e.valueFrom) && has(e.valueFrom.secretKeyRef)).map(e, "+
				celrule.Failure("'spec.template.spec.containers.?name=' + c.name + '.env.?name=' + e.name + '.valueFrom.secretKeyRef'", celrule.None, "'container ' + c.name + ' reads a Secret into ' + e.name")+") : []) + "+
				"(has(c.envFrom) ? lists.range(size(c.envFrom)).filter(i, has(c.envFrom[i].secretRef)).map(i, "+
				celrule.Failure("'spec.template.spec.containers.?name=' + c.name + '.envFrom.' + string(i) + '.secretRef'", celrule.None, "'container ' + c.name + ' reads a whole Secret into its environment'")+") : [])"+
				").flatten() : [])",
			celrule.Controllers, "KS:C-0207", "KL:read-secret-from-env-var", "KY:disallow-secrets-from-env-vars", "CIS:5.4.1")),
	},
}

var metadataPack = guardrails.Pack{
	Group: "metadata",
	Checks: []guardrails.Check{
		celCheck("CH-0802", "not-default-namespace", guardrails.PriorityMedium, "Resources are not in the default namespace",
			"Warns on a resource in the default namespace, which is shared by everything that names none and has no policy of its own. Fix: `set-namespace` to the application's namespace.",
			failIf("has(r.metadata.namespace) && r.metadata.namespace == 'default'", celrule.Lit("metadata.namespace"), celrule.Some("r.metadata.namespace"),
				"'the resource is in the default namespace'"),
			nil, "KS:C-0061", "KS:C-0212", "KL:use-namespace", "KY:disallow-default-namespace", "CIS:5.7.4"),
		withParam(celCheck("CH-0803", "required-labels", guardrails.PriorityMedium, "Resources carry the required labels",
			"Warns on a resource missing a label of required-labels.keys, a comma-separated list of keys, each optionally key=pattern for a regular expression its value has to match, such as owner,tier=^(web|batch)$. Installed once given the keys. Fix: add the labels.",
			requiredKeys("required-labels", "labels", "label"), nil, "KS:C-0076", "KS:C-0077", "GK:requiredlabels", "KY:require-labels", "KL:required-label-owner"),
			guardrails.Param{Name: "keys", Description: "required label keys, comma-separated, each optionally key=pattern", Required: true}),
		withParam(celCheck("CH-0804", "required-annotations", guardrails.PriorityLow, "Resources carry the required annotations",
			"Warns on a resource missing an annotation of required-annotations.keys, a comma-separated list of keys, each optionally key=pattern for a regular expression its value has to match. Installed once given the keys. Fix: add the annotations.",
			requiredKeys("required-annotations", "annotations", "annotation"), nil, "GK:requiredannotations", "KY:require-annotations", "KL:required-annotation-email"),
			guardrails.Param{Name: "keys", Description: "required annotation keys, comma-separated, each optionally key=pattern", Required: true}),
		optional(withParam(celCheck("CH-0805", "allowed-annotations", guardrails.PriorityLow, "Annotations have allowed prefixes",
			"Warns on an annotation whose key starts with none of allowed-annotations.prefixes, comma-separated. Guardrail exceptions are always allowed. Optional. Fix: remove the annotation, or allow its prefix.",
			mapKeyFailures("annotations", "!k.startsWith('"+guardrails.ExceptionAnnotationPrefix+"') && !"+paramList("allowed-annotations", "prefixes")+".exists(p, k.startsWith(p))",
				"'the annotation ' + k + ' has a prefix not allowed'"),
			nil, "KY:allowed-annotations", "KY:restrict-annotations"),
			guardrails.Param{Name: "prefixes", Description: "allowed annotation key prefixes, comma-separated", Required: true})),
	},
}

var storagePack = guardrails.Pack{
	Group: "storage",
	Checks: []guardrails.Check{
		celCheck("CH-0901", "no-hostpath-volumes", guardrails.PriorityHigh, "No hostPath PersistentVolumes",
			"Warns on a PersistentVolume backed by a hostPath, which gives whatever claims it a directory of one node. Fix: use a storage class.",
			failIf("has(r.spec) && has(r.spec.hostPath)", celrule.Lit("spec.hostPath"), celrule.Some("r.spec.hostPath"), "'the PersistentVolume is a directory of a node'"),
			pvTypes, "KY:limit-hostpath-type-pv"),
		celCheck("CH-0902", "storage-class-set", guardrails.PriorityLow, "Claims name a storage class",
			"Warns on a PersistentVolumeClaim, or a StatefulSet volumeClaimTemplate, with no storageClassName, which gets whatever the cluster's default class is. Fix: name the class.",
			"(r.kind == 'PersistentVolumeClaim' ? "+failIf("!(has(r.spec) && has(r.spec.storageClassName))", celrule.Lit("spec.storageClassName"), celrule.None, "'the claim names no storage class'")+" : "+
				listFailures("spec.volumeClaimTemplates", "!(has(x.spec) && has(x.spec.storageClassName))", "spec.storageClassName", celrule.None,
					"'volumeClaimTemplate ' + string(i) + ' names no storage class'")+")",
			claimTypes, "KY:require-storageclass"),
		optional(withParam(celCheck("CH-0903", "allowed-storage-classes", guardrails.PriorityMedium, "Claims use an allowed storage class",
			"Warns on a PersistentVolumeClaim, or a StatefulSet volumeClaimTemplate, whose storageClassName is not one of allowed-storage-classes.classes. One naming no class is storage-class-set's. Optional. Fix: use an allowed class.",
			"(r.kind == 'PersistentVolumeClaim' ? "+failIf("has(r.spec) && has(r.spec.storageClassName) && !(r.spec.storageClassName in "+paramList("allowed-storage-classes", "classes")+")",
				celrule.Lit("spec.storageClassName"), celrule.Some("r.spec.storageClassName"), "'the claim uses the storage class ' + r.spec.storageClassName + ', which is not allowed'")+" : "+
				listFailures("spec.volumeClaimTemplates", "has(x.spec) && has(x.spec.storageClassName) && !(x.spec.storageClassName in "+paramList("allowed-storage-classes", "classes")+")",
					"spec.storageClassName", celrule.Some("r.spec.volumeClaimTemplates[i].spec.storageClassName"), "'volumeClaimTemplate ' + string(i) + ' uses a storage class not allowed'")+")",
			claimTypes, "GK:storageclass", "KY:restrict-storageclass"),
			guardrails.Param{Name: "classes", Description: "the allowed storage classes, comma-separated", Required: true})),
		celCheck("CH-0904", "no-flexvolume", guardrails.PriorityLow, "No FlexVolumes",
			"Warns on a PersistentVolume or pod volume using FlexVolume, deprecated since Kubernetes 1.23 in favor of CSI. Fix: use the driver's CSI equivalent.",
			"(r.kind == 'PersistentVolume' ? "+failIf("has(r.spec) && has(r.spec.flexVolume)", celrule.Lit("spec.flexVolume"), celrule.None, "'the PersistentVolume uses FlexVolume'")+" : "+
				"(has(r.spec.template.spec.volumes) ? r.spec.template.spec.volumes.filter(v, has(v.flexVolume)).map(v, "+
				celrule.Failure("'spec.template.spec.volumes.?name=' + v.name + '.flexVolume'", celrule.None, "'volume ' + v.name + ' uses FlexVolume'")+") : []))",
			flexTypes, "GK:flexvolume-drivers"),
	},
}

// toolPacks are the packs installed with `cub manage guardrails <pack> install`, each with what
// it checks.
var toolPacks = []struct {
	pack  guardrails.Pack
	short string
}{
	{exposurePack, "Services, Ingresses, Gateways and Traefik IngressRoutes: no externalIPs, NodePorts or nginx snippets; TLS; named hosts"},
	{secretsPack, "No Secrets in configuration, and no credentials in env or ConfigMaps"},
	{metadataPack, "Not in the default namespace; required labels and annotations"},
	{storagePack, "No hostPath or FlexVolume PersistentVolumes; claims name a storage class"},
}
