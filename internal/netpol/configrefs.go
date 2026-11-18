// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package netpol

// configrefs derives flows from the Service addresses a workload's config
// names: in its containers' commands, arguments and env, and in the
// ConfigMaps it reads, whether it mounts them, reads them through env, or a
// sidecar finds them by label, as Grafana's finds its datasources. The text is
// read as text, so an address in a YAML or JSON document embedded in a
// ConfigMap value is found too.

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// serviceAddress is an address in config that may name a Service.
type serviceAddress struct {
	scheme    string
	service   string
	namespace string
	port      string
	// qualified is whether it names the Service's cluster domain, svc or
	// svc.cluster.local, so it can be nothing else.
	qualified bool
}

// addressPattern matches a host, with the scheme, the user, and the port around it. A host
// that is not the start of a word is left out, so a path or a longer name does
// not match its tail.
var addressPattern = regexp.MustCompile(`(?:^|[^a-zA-Z0-9.\-/])(?:([a-z][a-z0-9+.\-]*)://(?:[^@/\s]*@)?)?([a-z0-9](?:[a-z0-9\-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9\-]*[a-z0-9])?)*)(?::([0-9]{1,5}))?`)

// serviceAddresses are the addresses in text that name a Service:
// <service>.<namespace>.svc, optionally .cluster.local; or <service>, or
// <service>.<namespace>, with a scheme or a port, so that a bare word is not
// taken for one. namespace is the one an unqualified name resolves in, the
// reader's.
func serviceAddresses(text, namespace string) []serviceAddress {
	var out []serviceAddress
	for _, m := range addressPattern.FindAllStringSubmatch(text, -1) {
		scheme, host, port := m[1], m[2], m[3]
		labels := strings.Split(host, ".")
		a := serviceAddress{scheme: scheme, port: port}
		switch {
		case len(labels) == 3 && labels[2] == "svc",
			len(labels) == 5 && labels[2] == "svc" && labels[3] == "cluster" && labels[4] == "local":
			a.service, a.namespace, a.qualified = labels[0], labels[1], true
		case scheme == "" && port == "":
			continue
		case len(labels) == 1:
			a.service, a.namespace = labels[0], namespace
		case len(labels) == 2:
			a.service, a.namespace = labels[0], labels[1]
		default:
			continue
		}
		out = append(out, a)
	}
	return out
}

// apiServerService is the Service every cluster has for the Kubernetes API
// server, which runs outside the pod network, so no pod policy admits traffic
// to it.
const apiServerService = "default/kubernetes"

// schemePorts are the ports an address with a scheme and no port goes to.
var schemePorts = map[string]int{"http": 80, "https": 443, "ws": 80, "wss": 443}

// ConfigFlows are the flows the Service addresses in workloads' config say they
// make: from the workload whose config names the address to the pods of the
// Service, on the pod port the Service port goes to.
func ConfigFlows(c *ClusterNetpol) ([]Flow, []RefSkip) {
	var flows []Flow
	var skipped []RefSkip
	seen := map[string]bool{}
	skip := func(s RefSkip) {
		if key := s.Via + "|" + s.Reason; !seen[key] {
			seen[key] = true
			skipped = append(skipped, s)
		}
	}
	for _, w := range c.Workloads {
		ns := NamespaceOf(w.Namespace)
		for _, src := range configSources(c, w) {
			for _, a := range serviceAddresses(src.text, ns) {
				svc := findService(c, a.namespace, a.service)
				name := qualified(a.namespace, a.service)
				if svc == nil {
					if a.qualified && name != apiServerService {
						skip(RefSkip{Cluster: c.Cluster, Namespace: ns, Via: src.via,
							Reason: fmt.Sprintf("names Service %s, which is not in ConfigHub", name)})
					}
					continue
				}
				if len(svc.Selector) == 0 {
					continue
				}
				svcPorts, reason := addressPorts(svc, a)
				if len(svcPorts) == 0 {
					skip(RefSkip{Cluster: c.Cluster, Namespace: ns, Via: src.via, Reason: reason})
					continue
				}
				for _, dst := range selectedWorkloads(c, NamespaceOf(svc.Namespace), svc.Selector) {
					if dst == w {
						continue
					}
					ports := podPorts(dst, svcPorts)
					key := src.via + "|" + workloadKey(w) + "|" + workloadKey(dst) + "|" + portsKey(ports)
					if seen[key] {
						continue
					}
					seen[key] = true
					flows = append(flows, Flow{Source: w, Dest: dst, Ports: ports, Via: src.via})
				}
			}
		}
	}
	return flows, skipped
}

// configSource is text a workload's config holds, and where it comes from.
type configSource struct {
	text string
	via  string
}

// configSources are the texts of a workload's config: its containers' own, and
// the values of the ConfigMaps it reads or finds.
func configSources(c *ClusterNetpol, w *WorkloadEntity) []configSource {
	ns := NamespaceOf(w.Namespace)
	self := w.Kind + " " + WorkloadName(w)
	var out []configSource
	for _, t := range w.ConfigText {
		out = append(out, configSource{text: t, via: self})
	}
	for _, cm := range c.ConfigMaps {
		via := ""
		switch {
		case NamespaceOf(cm.Namespace) == ns && containsString(w.ConfigMaps, cm.Name):
			via = "ConfigMap " + qualified(NamespaceOf(cm.Namespace), cm.Name)
		default:
			for _, d := range w.Discovers {
				if d.finds(cm, ns) {
					via = "ConfigMap " + qualified(NamespaceOf(cm.Namespace), cm.Name) + ", found by " + d.Container
					break
				}
			}
		}
		if via == "" {
			continue
		}
		for _, v := range cm.Data {
			out = append(out, configSource{text: v, via: via})
		}
	}
	return out
}

// addressPorts are the Service's ports an address goes to: the one it names,
// the one its scheme implies, or all of them; or none, and why.
func addressPorts(svc *ServiceEntity, a serviceAddress) ([]ServicePort, string) {
	name := qualified(NamespaceOf(svc.Namespace), svc.Name)
	port := a.port
	if port == "" {
		if n, ok := schemePorts[a.scheme]; ok {
			port = strconv.Itoa(n)
		}
	}
	if port == "" {
		return svc.Ports, "Service " + name + " has no ports"
	}
	for _, sp := range svc.Ports {
		if strconv.Itoa(sp.Port) == port {
			return []ServicePort{sp}, ""
		}
	}
	return nil, "names Service " + name + " on port " + port + ", which it does not have"
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
