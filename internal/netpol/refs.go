// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package netpol

// refs derives the connections a cluster's own config says its workloads make,
// from the references between resources, so that policies can admit those and
// nothing else. A route names a Service, the Service selects pods, and the
// ingress controller serving the route is what connects to them: an Ingress or
// IngressRoute is a connection from the controller's pods to the Service's.

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Flow is a connection a workload makes to another, because a resource says it
// does.
type Flow struct {
	Source *WorkloadEntity
	Dest   *WorkloadEntity
	// Ports are the destination pod's ports the connection goes to.
	Ports []NetworkPolicyPort
	// Via names the resource that says so, such as "IngressRoute argocd/argocd-server".
	Via string
}

// RefSkip is a reference that produced no flow, and why.
type RefSkip struct {
	Cluster string `json:"cluster"`
	// Namespace is the namespace of the route, or of the destination.
	Namespace string `json:"namespace"`
	Via       string `json:"via"`
	Reason    string `json:"reason"`
}

// ingressController is an ingress controller allow-from-refs recognizes: by the
// IngressClass controller value that names it, the class names Ingresses gave it
// before IngressClass existed, and the images it runs.
type ingressController struct {
	name             string
	classControllers []string
	legacyClasses    []string
	// images are image repositories, matched whole or as the repository's last
	// path elements, so "traefik" matches docker.io/traefik but not traefik/whoami.
	images []string
}

var (
	traefikController = &ingressController{
		name:             "traefik",
		classControllers: []string{"traefik.io/ingress-controller"},
		legacyClasses:    []string{"traefik"},
		images:           []string{"traefik"},
	}
	ingressNginxController = &ingressController{
		name:             "ingress-nginx",
		classControllers: []string{"k8s.io/ingress-nginx"},
		legacyClasses:    []string{"nginx"},
		images:           []string{"ingress-nginx/controller", "nginx-ingress-controller"},
	}
	ingressControllers = []*ingressController{traefikController, ingressNginxController}
)

// runs reports whether a workload runs this controller, from its images.
func (ic *ingressController) runs(w *WorkloadEntity) bool {
	for _, image := range w.Images {
		repo := imageRepository(image)
		for _, want := range ic.images {
			if repo == want || strings.HasSuffix(repo, "/"+want) {
				return true
			}
		}
	}
	return false
}

// imageRepository is an image reference without its tag or digest.
func imageRepository(image string) string {
	if i := strings.Index(image, "@"); i >= 0 {
		image = image[:i]
	}
	if i := strings.LastIndex(image, ":"); i > strings.LastIndex(image, "/") {
		image = image[:i]
	}
	return image
}

// RouteFlows are the flows a cluster's routes say its ingress controllers make:
// from the controller serving each route to the pods of each Service it names,
// on the pod port the route's Service port goes to.
//
// controller, as "namespace/name", names the workload that serves every route in
// the cluster, for a controller the images and IngressClasses do not identify.
// Empty, the controller is the workload running the image of the controller the
// route's kind or IngressClass names.
func RouteFlows(c *ClusterNetpol, controller string) ([]Flow, []RefSkip) {
	var flows []Flow
	var skipped []RefSkip
	seen := map[string]bool{}
	var namespace string
	skip := func(via, format string, args ...any) {
		skipped = append(skipped, RefSkip{Cluster: c.Cluster, Namespace: namespace, Via: via, Reason: fmt.Sprintf(format, args...)})
	}
	for _, r := range c.Routes {
		namespace = NamespaceOf(r.Namespace)
		via := r.Kind + " " + qualified(namespace, r.Name)
		sources, reason := routeControllers(c, r, controller)
		if len(sources) == 0 {
			skip(via, "%s", reason)
			continue
		}
		if len(r.Backends) == 0 {
			skip(via, "names no Service")
			continue
		}
		for _, b := range r.Backends {
			svc := findService(c, b.Namespace, b.Service)
			svcName := qualified(b.Namespace, b.Service)
			switch {
			case svc == nil:
				skip(via, "Service %s is not in ConfigHub", svcName)
				continue
			case len(svc.Selector) == 0:
				skip(via, "Service %s selects no pods; its endpoints are set some other way", svcName)
				continue
			}
			svcPorts := servicePorts(svc, b.Port)
			if len(svcPorts) == 0 {
				skip(via, "Service %s has no port %s", svcName, b.Port)
				continue
			}
			dests := selectedWorkloads(c, NamespaceOf(svc.Namespace), svc.Selector)
			if len(dests) == 0 {
				skip(via, "Service %s selects no workload ConfigHub has", svcName)
				continue
			}
			for _, dst := range dests {
				ports := podPorts(dst, svcPorts)
				for _, src := range sources {
					if src == dst {
						continue
					}
					key := via + "|" + workloadKey(src) + "|" + workloadKey(dst) + "|" + portsKey(ports)
					if seen[key] {
						continue
					}
					seen[key] = true
					flows = append(flows, Flow{Source: src, Dest: dst, Ports: ports, Via: via})
				}
			}
		}
	}
	return flows, skipped
}

// routeControllers are the workloads serving a route, or why there are none.
func routeControllers(c *ClusterNetpol, r *RouteEntity, override string) ([]*WorkloadEntity, string) {
	if override != "" {
		ns, name, _ := strings.Cut(override, "/")
		for _, w := range c.Workloads {
			if NamespaceOf(w.Namespace) == ns && w.Name == name {
				return []*WorkloadEntity{w}, ""
			}
		}
		return nil, "no workload " + override + " in the cluster"
	}
	ic, reason := routeController(c, r)
	if ic == nil {
		return nil, reason
	}
	var out []*WorkloadEntity
	for _, w := range c.Workloads {
		if ic.runs(w) {
			out = append(out, w)
		}
	}
	if len(out) == 0 {
		return nil, "no workload in the cluster runs " + ic.name + "; name it with --ingress-controller"
	}
	return out, ""
}

// routeController is the controller serving a route: Traefik for its own
// routes, and for an Ingress, the controller of its IngressClass.
func routeController(c *ClusterNetpol, r *RouteEntity) (*ingressController, string) {
	if traefikRouteKinds[r.Kind] {
		return traefikController, ""
	}
	class := r.Class
	var ingressClass *IngressClassEntity
	for _, ic := range c.IngressClasses {
		if (class != "" && ic.Name == class) || (class == "" && ic.Default) {
			ingressClass = ic
			break
		}
	}
	if ingressClass != nil {
		for _, ic := range ingressControllers {
			if slices.Contains(ic.classControllers, ingressClass.Controller) {
				return ic, ""
			}
		}
		return nil, "IngressClass " + ingressClass.Name + "'s controller, " + ingressClass.Controller + ", is not one allow-from-refs recognizes; name its workload with --ingress-controller"
	}
	if class == "" {
		return nil, "names no IngressClass, and the cluster has no default IngressClass in ConfigHub"
	}
	for _, ic := range ingressControllers {
		if slices.Contains(ic.legacyClasses, class) {
			return ic, ""
		}
	}
	return nil, "IngressClass " + class + " is not in ConfigHub; name its controller's workload with --ingress-controller"
}

func findService(c *ClusterNetpol, namespace, name string) *ServiceEntity {
	for _, s := range c.Services {
		if NamespaceOf(s.Namespace) == NamespaceOf(namespace) && s.Name == name {
			return s
		}
	}
	return nil
}

// servicePorts are the Service's ports a route's port names: by number or by
// name, and all of them when the route names none.
func servicePorts(svc *ServiceEntity, port string) []ServicePort {
	if port == "" {
		return svc.Ports
	}
	var out []ServicePort
	for _, sp := range svc.Ports {
		if strconv.Itoa(sp.Port) == port || (sp.Name != "" && sp.Name == port) {
			out = append(out, sp)
		}
	}
	return out
}

// podPorts are the destination pod's ports that traffic to the Service ports
// goes to. A named targetPort becomes the number of the container port of that
// name, and stays a name, which a NetworkPolicy port can also be, when the
// workload declares none by it.
func podPorts(dst *WorkloadEntity, svcPorts []ServicePort) []NetworkPolicyPort {
	var out []NetworkPolicyPort
	for _, sp := range svcPorts {
		target := sp.TargetPort
		if target == "" {
			target = strconv.Itoa(sp.Port)
		}
		if _, err := strconv.Atoi(target); err != nil {
			for _, cp := range dst.Ports {
				if cp.Name == target {
					target = strconv.Itoa(cp.Port)
					break
				}
			}
		}
		p := NetworkPolicyPort{Protocol: sp.Protocol, Port: target}
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	sortPorts(out)
	return out
}

// selectedWorkloads are the workloads in namespace whose pods have every label
// of selector.
func selectedWorkloads(c *ClusterNetpol, namespace string, selector map[string]string) []*WorkloadEntity {
	var out []*WorkloadEntity
	for _, w := range c.Workloads {
		if NamespaceOf(w.Namespace) != namespace {
			continue
		}
		if labelsMatch(selector, w.PodLabels) {
			out = append(out, w)
		}
	}
	return out
}

func labelsMatch(selector, labels map[string]string) bool {
	for k, v := range selector {
		if labels[k] != v {
			return false
		}
	}
	return true
}

func sortPorts(ports []NetworkPolicyPort) {
	sort.Slice(ports, func(i, j int) bool {
		a, b := ports[i], ports[j]
		if a.Protocol != b.Protocol {
			return a.Protocol < b.Protocol
		}
		an, aerr := strconv.Atoi(a.Port)
		bn, berr := strconv.Atoi(b.Port)
		if aerr == nil && berr == nil {
			return an < bn
		}
		return a.Port < b.Port
	})
}

func portsKey(ports []NetworkPolicyPort) string {
	parts := make([]string, len(ports))
	for i, p := range ports {
		parts[i] = p.String()
	}
	return strings.Join(parts, ",")
}

// String renders a port as "TCP 8080", or "TCP" for every port.
func (p NetworkPolicyPort) String() string {
	s := p.Protocol
	if p.Port != "" {
		s += " " + p.Port
		if p.EndPort != 0 {
			s += "-" + strconv.Itoa(p.EndPort)
		}
	}
	return s
}

// WorkloadName is a workload as reports name it: namespace/name.
func WorkloadName(w *WorkloadEntity) string { return qualified(NamespaceOf(w.Namespace), w.Name) }

func workloadKey(w *WorkloadEntity) string {
	return w.Origin.UnitID + "|" + w.Kind + "|" + WorkloadName(w)
}

func qualified(namespace, name string) string {
	if namespace == "" {
		return name
	}
	return namespace + "/" + name
}
