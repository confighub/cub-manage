// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

// Package netpol is the Kubernetes NetworkPolicy analysis engine: it parses
// NetworkPolicies, Namespaces, pod-bearing workloads, and Services drawn from
// ConfigHub Units into a typed domain model, over which coverage.go and
// connectivity.go compute coverage and effective connectivity.
//
// Parsing is lenient: malformed documents are skipped, never errored on — a bad
// resource in one Unit must not take down fleet-wide analysis.
package netpol

import (
	"net/netip"
	"slices"
	"strconv"
	"strings"
)

// ResourceOrigin records where a resource came from in ConfigHub. Clusters are
// Targets: a Unit's Target identifies the cluster it deploys to, and Units from
// many Spaces can share one cluster Target. Cluster is the Target slug when the
// Unit is bound, falling back to the Space slug for unbound ("paper cluster")
// Units; Target is set only when actually bound.
type ResourceOrigin struct {
	Cluster      string `json:"cluster"`
	Target       string `json:"target,omitempty"`
	Space        string `json:"space"`
	SpaceID      string `json:"spaceId"`
	UnitID       string `json:"unitId"`
	UnitSlug     string `json:"unitSlug"`
	ResourceName string `json:"resourceName"`
	// Canonical is true for definitions in base/policy Spaces that aren't
	// deployed anywhere — shown in the explorer but excluded from cluster
	// analysis (coverage, connectivity, findings).
	Canonical bool `json:"canonical,omitempty"`
}

// FleetResource is a parsed resource document plus its ConfigHub origin. Doc is
// the decoded JSON body (typically a map[string]any).
type FleetResource struct {
	Origin ResourceOrigin
	Doc    any
}

// LabelSelector is a parsed Kubernetes label selector (matchLabels +
// matchExpressions). Present distinguishes an explicitly-set selector — even the
// empty selector `{}`, which matches every pod in scope — from an absent one.
type LabelSelector struct {
	Present          bool                       `json:"present"`
	MatchLabels      map[string]string          `json:"matchLabels,omitempty"`
	MatchExpressions []LabelSelectorRequirement `json:"matchExpressions,omitempty"`
}

// LabelSelectorRequirement is one matchExpressions clause.
type LabelSelectorRequirement struct {
	Key      string   `json:"key"`
	Operator string   `json:"operator"`
	Values   []string `json:"values,omitempty"`
}

// Empty reports whether the selector is present but constrains nothing (`{}`),
// i.e. it selects all pods in its scope.
func (s LabelSelector) Empty() bool {
	return s.Present && len(s.MatchLabels) == 0 && len(s.MatchExpressions) == 0
}

// NetworkPolicyEntity is a parsed networking.k8s.io/v1 NetworkPolicy, including
// its ingress/egress rules and the policy-type isolation it implies.
type NetworkPolicyEntity struct {
	Name        string              `json:"name"`
	Namespace   string              `json:"namespace"`
	PodSelector LabelSelector       `json:"podSelector"`
	PolicyTypes []string            `json:"policyTypes,omitempty"`
	Ingress     []NetworkPolicyRule `json:"ingress,omitempty"`
	Egress      []NetworkPolicyRule `json:"egress,omitempty"`
	Origin      ResourceOrigin      `json:"origin"`
}

// NetworkPolicyRule is one ingress or egress rule. Peers are the rule's `from`
// (ingress) or `to` (egress) entries; an empty Peers list means "all peers"
// (an allow-all rule). Ports empty means "all ports".
type NetworkPolicyRule struct {
	Peers []NetworkPolicyPeer `json:"peers,omitempty"`
	Ports []NetworkPolicyPort `json:"ports,omitempty"`
}

// NetworkPolicyPeer is one entry in a rule's from/to list. Exactly one of
// PodSelector / NamespaceSelector / IPBlock is typically set (podSelector and
// namespaceSelector may be combined to mean "pods matching X in namespaces
// matching Y"). A nil selector means that dimension is unconstrained per the
// Kubernetes peer semantics.
type NetworkPolicyPeer struct {
	PodSelector       *LabelSelector `json:"podSelector,omitempty"`
	NamespaceSelector *LabelSelector `json:"namespaceSelector,omitempty"`
	IPBlock           *IPBlock       `json:"ipBlock,omitempty"`
}

// IPBlock is a CIDR peer with optional exceptions.
type IPBlock struct {
	CIDR   string   `json:"cidr"`
	Except []string `json:"except,omitempty"`
}

// NetworkPolicyPort is one port entry. Port is the numeric or named port as a
// string ("" = all ports); EndPort, when non-zero, makes [Port, EndPort] a range.
type NetworkPolicyPort struct {
	Protocol string `json:"protocol"`
	Port     string `json:"port,omitempty"`
	EndPort  int    `json:"endPort,omitempty"`
}

// Isolates reports which directions this policy isolates, applying the
// Kubernetes default: when policyTypes is set it is authoritative; when absent,
// Ingress is always implied and Egress is implied only if egress rules exist.
func (np *NetworkPolicyEntity) Isolates() (ingress, egress bool) {
	if len(np.PolicyTypes) > 0 {
		for _, t := range np.PolicyTypes {
			switch t {
			case "Ingress":
				ingress = true
			case "Egress":
				egress = true
			}
		}
		return ingress, egress
	}
	return true, len(np.Egress) > 0
}

// IsolatesIngress reports whether this policy makes its selected pods
// ingress-isolated (only explicitly-allowed ingress permitted).
func (np *NetworkPolicyEntity) IsolatesIngress() bool {
	ingress, _ := np.Isolates()
	return ingress
}

// IsolatesEgress reports whether this policy makes its selected pods
// egress-isolated.
func (np *NetworkPolicyEntity) IsolatesEgress() bool {
	_, egress := np.Isolates()
	return egress
}

// DeniesIngressByDefault reports whether this policy is a default-deny for
// ingress: it selects every pod in its namespace and isolates ingress, and no
// rule of it admits all traffic, which would make it an allow-all instead.
func (np *NetworkPolicyEntity) DeniesIngressByDefault() bool {
	return np.PodSelector.Empty() && np.IsolatesIngress() && !slices.ContainsFunc(np.Ingress, NetworkPolicyRule.admitsAll)
}

// DeniesEgressByDefault is DeniesIngressByDefault for egress.
func (np *NetworkPolicyEntity) DeniesEgressByDefault() bool {
	return np.PodSelector.Empty() && np.IsolatesEgress() && !slices.ContainsFunc(np.Egress, NetworkPolicyRule.admitsAll)
}

// admitsAll reports whether a rule admits all traffic: it names no ports, and
// either no peers or an ipBlock spanning every address. A rule limited to some
// ports, such as a DNS allowance, leaves a default-deny in place.
func (r NetworkPolicyRule) admitsAll() bool {
	if len(r.Ports) > 0 {
		return false
	}
	if len(r.Peers) == 0 {
		return true
	}
	for _, p := range r.Peers {
		if p.IPBlock == nil {
			continue
		}
		if prefix, err := netip.ParsePrefix(p.IPBlock.CIDR); err == nil && prefix.Bits() == 0 {
			return true
		}
	}
	return false
}

// allows reports whether the port entry admits traffic to port on protocol. An
// entry with no port admits every port of its protocol. A named port matches
// only a pod's container port of that name, which this cannot see, so it admits
// no numbered port.
func (p NetworkPolicyPort) allows(protocol string, port int) bool {
	if !strings.EqualFold(p.Protocol, protocol) {
		return false
	}
	if p.Port == "" {
		return true
	}
	n, err := strconv.Atoi(p.Port)
	if err != nil {
		return false
	}
	if p.EndPort != 0 {
		return n <= port && port <= p.EndPort
	}
	return n == port
}

// allowsPort reports whether the rule admits traffic to port on protocol: it
// names no ports, or one that admits it.
func (r NetworkPolicyRule) allowsPort(protocol string, port int) bool {
	return len(r.Ports) == 0 || slices.ContainsFunc(r.Ports, func(p NetworkPolicyPort) bool { return p.allows(protocol, port) })
}

// dnsPortName is the name the cluster DNS's pods give their UDP port 53.
const dnsPortName = "dns"

// allowsDNSPort reports whether the rule admits DNS: UDP port 53, or the port
// the cluster DNS names "dns". A port with no protocol is TCP, which most
// lookups do not use.
func (r NetworkPolicyRule) allowsDNSPort() bool {
	return r.allowsPort("UDP", 53) || slices.ContainsFunc(r.Ports, func(p NetworkPolicyPort) bool {
		return p.Port == dnsPortName && strings.EqualFold(p.Protocol, "UDP")
	})
}

// openEgress reports whether an egress rule allows every destination on every
// port: it names neither. A rule naming ports, such as one allowing HTTPS to
// anywhere, is a deliberate choice of what to allow, and is not.
func (r NetworkPolicyRule) openEgress() bool {
	return len(r.Peers) == 0 && len(r.Ports) == 0
}

// metadataPort is the port the cloud instance-metadata service listens on.
const metadataPort = 80

// reachesMetadata reports whether an egress rule admits the cloud metadata IP:
// it names no destinations and admits its HTTP port, or one of its ipBlocks
// contains the IP without excepting it.
func (r NetworkPolicyRule) reachesMetadata() bool {
	if len(r.Peers) == 0 {
		return r.allowsPort("TCP", metadataPort)
	}
	return slices.ContainsFunc(r.Peers, func(p NetworkPolicyPeer) bool {
		return p.IPBlock != nil && IPBlockExposesMetadata(p.IPBlock)
	})
}

// WorkloadEntity is a parsed pod-bearing resource (Deployment, StatefulSet,
// DaemonSet, ReplicaSet, Job, CronJob, or bare Pod). PodLabels holds the labels
// on the pods it manages — the labels a NetworkPolicy podSelector matches.
// SelectorLabels holds the workload's own equality-based pod selector; see
// PolicySelector for which of the two a generated policy should select on.
type WorkloadEntity struct {
	Kind           string            `json:"kind"`
	Name           string            `json:"name"`
	Namespace      string            `json:"namespace"`
	PodLabels      map[string]string `json:"podLabels,omitempty"`
	SelectorLabels map[string]string `json:"selectorLabels,omitempty"`
	// Images are the images of the pod's containers, which tell what a workload
	// runs: an ingress controller, say.
	Images []string `json:"images,omitempty"`
	// Ports are the ports the pod's containers declare, which a Service's named
	// targetPort resolves to.
	Ports  []ContainerPort `json:"ports,omitempty"`
	Origin ResourceOrigin  `json:"origin"`
}

// ContainerPort is a port a container declares.
type ContainerPort struct {
	Name     string `json:"name,omitempty"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
}

// PolicySelector returns the labels a generated NetworkPolicy should use to
// select this workload. It prefers the workload's own spec.selector.matchLabels,
// which Kubernetes requires to be immutable and which therefore cannot carry the
// release-varying labels (chart version, app version, managed-by) that Helm and
// similar renderers stamp onto the pod template. Selecting on those would make
// the policy stop matching at the next upgrade — it would still exist while
// protecting nothing, a silent fail-open. Falls back to the full pod labels for
// workloads with no equality-based selector: a bare Pod, or a selector written
// only as matchExpressions.
func (w *WorkloadEntity) PolicySelector() map[string]string {
	if len(w.SelectorLabels) > 0 {
		return w.SelectorLabels
	}
	return w.PodLabels
}

// NamespaceEntity is a parsed v1 Namespace.
type NamespaceEntity struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels,omitempty"`
	Origin ResourceOrigin    `json:"origin"`
}

// ServiceEntity is a parsed v1 Service. Selector is the equality-based pod
// selector (a plain string map, unlike a NetworkPolicy's LabelSelector).
type ServiceEntity struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace"`
	Selector  map[string]string `json:"selector,omitempty"`
	Ports     []ServicePort     `json:"ports,omitempty"`
	Origin    ResourceOrigin    `json:"origin"`
}

// ServicePort is one of a Service's ports. TargetPort is the pod port traffic to
// it goes to, a number or a container port's name; empty, it is Port.
type ServicePort struct {
	Name       string `json:"name,omitempty"`
	Port       int    `json:"port"`
	TargetPort string `json:"targetPort,omitempty"`
	Protocol   string `json:"protocol"`
}

// RouteEntity is a resource that tells an ingress controller to send traffic to
// Services: an Ingress, or a Traefik IngressRoute, IngressRouteTCP or
// IngressRouteUDP. The controller's pods are what connect to the Services' pods,
// so a route says which workloads the controller has to reach.
type RouteEntity struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	// Class is an Ingress's class: its ingressClassName, or the
	// kubernetes.io/ingress.class annotation. Empty, the cluster's default
	// IngressClass serves it.
	Class    string         `json:"class,omitempty"`
	Backends []RouteBackend `json:"backends,omitempty"`
	Origin   ResourceOrigin `json:"origin"`
}

// RouteBackend is a Service a route sends traffic to. Port is the Service port,
// as a number or a name, and empty when the route names none. Protocol is the
// protocol the route carries: UDP for an IngressRouteUDP, TCP otherwise.
type RouteBackend struct {
	Service   string `json:"service"`
	Namespace string `json:"namespace"`
	Port      string `json:"port,omitempty"`
	Protocol  string `json:"protocol"`
}

// IngressClassEntity is a networking.k8s.io/v1 IngressClass: which controller
// serves the Ingresses of the class, and whether it serves those naming none.
type IngressClassEntity struct {
	Name       string         `json:"name"`
	Controller string         `json:"controller"`
	Default    bool           `json:"default,omitempty"`
	Origin     ResourceOrigin `json:"origin"`
}

// EnforcerFact is the Target fact naming what enforces NetworkPolicy on the
// cluster: a network plugin, or empty for none. It is collected from the
// cluster by `cub k8s collect` and the bots that run in it, and absent when
// that could not be told.
const EnforcerFact = "Cluster.NetworkPolicyEnforcer"

// Enforcement is what a cluster's Target says enforces NetworkPolicy on it. A
// NetworkPolicy is accepted by every cluster, and has an effect only where a
// network plugin implements it.
type Enforcement struct {
	// Known is false when the cluster has no Target, or its Target no
	// EnforcerFact.
	Known bool `json:"known"`
	// Enforcer names the network plugin enforcing NetworkPolicy, empty when
	// nothing does.
	Enforcer string `json:"enforcer,omitempty"`
}

// EnforcementFromFacts reads a Target's facts.
func EnforcementFromFacts(facts map[string]string) Enforcement {
	enforcer, known := facts[EnforcerFact]
	return Enforcement{Known: known, Enforcer: enforcer}
}

// NotEnforced reports whether the cluster is known to enforce no NetworkPolicy.
func (e Enforcement) NotEnforced() bool { return e.Known && e.Enforcer == "" }

// String is the enforcement as a table shows it: the enforcer, "no", or
// "unknown".
func (e Enforcement) String() string {
	switch {
	case !e.Known:
		return "unknown"
	case e.Enforcer == "":
		return "no"
	}
	return e.Enforcer
}

// ClusterNetpol holds the NetworkPolicy-relevant entities of one cluster.
type ClusterNetpol struct {
	Cluster         string                 `json:"cluster"`
	Enforcement     Enforcement            `json:"enforcement"`
	NetworkPolicies []*NetworkPolicyEntity `json:"networkPolicies"`
	Workloads       []*WorkloadEntity      `json:"workloads"`
	Namespaces      []*NamespaceEntity     `json:"namespaces"`
	Services        []*ServiceEntity       `json:"services"`
	Routes          []*RouteEntity         `json:"routes,omitempty"`
	IngressClasses  []*IngressClassEntity  `json:"ingressClasses,omitempty"`
}

// workloadKinds is the set of pod-bearing kinds whose pod-template labels a
// NetworkPolicy can select.
var workloadKinds = map[string]bool{
	"Deployment":  true,
	"StatefulSet": true,
	"DaemonSet":   true,
	"ReplicaSet":  true,
	"Job":         true,
	"CronJob":     true,
	"Pod":         true,
}

// BuildFleet indexes parsed fleet resources into per-cluster entity sets.
// Unrecognized kinds and unparseable docs are ignored. Entities within a cluster
// preserve input order.
func BuildFleet(resources []FleetResource) map[string]*ClusterNetpol {
	clusters := make(map[string]*ClusterNetpol)
	forCluster := func(name string) *ClusterNetpol {
		c, ok := clusters[name]
		if !ok {
			c = &ClusterNetpol{Cluster: name}
			clusters[name] = c
		}
		return c
	}

	for _, fr := range resources {
		rec, ok := asRecord(fr.Doc)
		if !ok {
			continue
		}
		kind, hasKind := asString(rec["kind"])
		apiVersion, _ := asString(rec["apiVersion"])
		metadata, _ := asRecord(rec["metadata"])
		name, hasName := asString(metadata["name"])
		if !hasKind || !hasName {
			continue
		}
		namespace, _ := asString(metadata["namespace"])
		labels := asStringMap(metadata["labels"])
		cluster := forCluster(fr.Origin.Cluster)
		spec, _ := asRecord(rec["spec"])

		switch {
		case kind == "NetworkPolicy" && strings.HasPrefix(apiVersion, "networking.k8s.io/"):
			cluster.NetworkPolicies = append(cluster.NetworkPolicies, &NetworkPolicyEntity{
				Name:        name,
				Namespace:   namespace,
				PodSelector: parseLabelSelector(spec["podSelector"]),
				PolicyTypes: asStringArray(spec["policyTypes"]),
				Ingress:     parseNetpolRules(spec["ingress"], "from"),
				Egress:      parseNetpolRules(spec["egress"], "to"),
				Origin:      fr.Origin,
			})
		case kind == "Namespace" && apiVersion == "v1":
			cluster.Namespaces = append(cluster.Namespaces, &NamespaceEntity{
				Name: name, Labels: labels, Origin: fr.Origin,
			})
		case kind == "Service" && apiVersion == "v1":
			cluster.Services = append(cluster.Services, &ServiceEntity{
				Name: name, Namespace: namespace,
				Selector: asStringMap(spec["selector"]),
				Ports:    parseServicePorts(spec["ports"]),
				Origin:   fr.Origin,
			})
		case kind == "Ingress" && apiVersion == "networking.k8s.io/v1":
			class, _ := asString(spec["ingressClassName"])
			if class == "" {
				class = asStringMap(metadata["annotations"])[ingressClassAnnotation]
			}
			cluster.Routes = append(cluster.Routes, &RouteEntity{
				Kind: kind, Name: name, Namespace: namespace, Class: class,
				Backends: ingressBackends(spec, namespace), Origin: fr.Origin,
			})
		case traefikRouteKinds[kind] && isTraefikAPIVersion(apiVersion):
			cluster.Routes = append(cluster.Routes, &RouteEntity{
				Kind: kind, Name: name, Namespace: namespace,
				Backends: traefikBackends(kind, spec, namespace), Origin: fr.Origin,
			})
		case kind == "IngressClass" && apiVersion == "networking.k8s.io/v1":
			controller, _ := asString(spec["controller"])
			cluster.IngressClasses = append(cluster.IngressClasses, &IngressClassEntity{
				Name: name, Controller: controller,
				Default: asStringMap(metadata["annotations"])[defaultIngressClassAnnotation] == "true",
				Origin:  fr.Origin,
			})
		case workloadKinds[kind]:
			podSpec := podTemplateSpec(kind, rec)
			cluster.Workloads = append(cluster.Workloads, &WorkloadEntity{
				Kind: kind, Name: name, Namespace: namespace,
				PodLabels:      podTemplateLabels(kind, rec),
				SelectorLabels: workloadSelectorLabels(kind, rec),
				Images:         containerImages(podSpec),
				Ports:          containerPorts(podSpec),
				Origin:         fr.Origin,
			})
		}
	}
	return clusters
}

// podTemplateLabels extracts the labels on the pods a workload manages, from the
// pod-template path appropriate to its kind.
func podTemplateLabels(kind string, rec map[string]any) map[string]string {
	switch kind {
	case "Pod":
		md := nestedRecord(rec, "metadata")
		return asStringMap(md["labels"])
	case "CronJob":
		md := nestedRecord(rec, "spec", "jobTemplate", "spec", "template", "metadata")
		return asStringMap(md["labels"])
	default: // Deployment, StatefulSet, DaemonSet, ReplicaSet, Job
		md := nestedRecord(rec, "spec", "template", "metadata")
		return asStringMap(md["labels"])
	}
}

// podTemplateSpec is the spec of the pods a workload manages, from the path
// appropriate to its kind.
func podTemplateSpec(kind string, rec map[string]any) map[string]any {
	switch kind {
	case "Pod":
		return nestedRecord(rec, "spec")
	case "CronJob":
		return nestedRecord(rec, "spec", "jobTemplate", "spec", "template", "spec")
	default: // Deployment, StatefulSet, DaemonSet, ReplicaSet, Job
		return nestedRecord(rec, "spec", "template", "spec")
	}
}

// containerImages are the images of a pod spec's containers. Init containers
// are left out: they finish before the pod serves anything.
func containerImages(podSpec map[string]any) []string {
	var out []string
	for _, c := range asRecords(podSpec["containers"]) {
		if image, ok := asString(c["image"]); ok && image != "" {
			out = append(out, image)
		}
	}
	return out
}

// containerPorts are the ports a pod spec's containers declare. A port with no
// protocol is TCP.
func containerPorts(podSpec map[string]any) []ContainerPort {
	var out []ContainerPort
	for _, c := range asRecords(podSpec["containers"]) {
		for _, p := range asRecords(c["ports"]) {
			port := intOf(p["containerPort"])
			if port == 0 {
				continue
			}
			out = append(out, ContainerPort{Name: firstString(p["name"]), Port: port, Protocol: protocolOf(p["protocol"])})
		}
	}
	return out
}

// parseServicePorts parses a Service's spec.ports.
func parseServicePorts(v any) []ServicePort {
	var out []ServicePort
	for _, p := range asRecords(v) {
		port := intOf(p["port"])
		if port == 0 {
			continue
		}
		out = append(out, ServicePort{
			Name: firstString(p["name"]), Port: port,
			TargetPort: scalarString(p["targetPort"]), Protocol: protocolOf(p["protocol"]),
		})
	}
	return out
}

// protocolOf is a port's protocol, TCP when it names none.
func protocolOf(v any) string {
	if s, _ := asString(v); s != "" {
		return s
	}
	return "TCP"
}

const (
	// ingressClassAnnotation names an Ingress's class on Ingresses written
	// before ingressClassName, and is still read by most controllers.
	ingressClassAnnotation = "kubernetes.io/ingress.class"
	// defaultIngressClassAnnotation marks the IngressClass that serves the
	// Ingresses naming no class.
	defaultIngressClassAnnotation = "ingressclass.kubernetes.io/is-default-class"
)

// traefikRouteKinds are Traefik's route kinds, all of which name Services the
// same way.
var traefikRouteKinds = map[string]bool{"IngressRoute": true, "IngressRouteTCP": true, "IngressRouteUDP": true}

// isTraefikAPIVersion reports whether apiVersion is Traefik's API group, under
// its current name or the one before.
func isTraefikAPIVersion(apiVersion string) bool {
	return strings.HasPrefix(apiVersion, "traefik.io/") || strings.HasPrefix(apiVersion, "traefik.containo.us/")
}

// ingressBackends are the Services an Ingress sends traffic to: its default
// backend and each path's. A backend that is a resource rather than a Service is
// left out.
func ingressBackends(spec map[string]any, namespace string) []RouteBackend {
	var out []RouteBackend
	add := func(backend map[string]any) {
		svc := nestedRecord(backend, "service")
		name := firstString(svc["name"])
		if name == "" {
			return
		}
		port := nestedRecord(svc, "port")
		p := firstString(port["name"])
		if n := intOf(port["number"]); n != 0 {
			p = strconv.Itoa(n)
		}
		out = append(out, RouteBackend{Service: name, Namespace: namespace, Port: p, Protocol: "TCP"})
	}
	if b := nestedRecord(spec, "defaultBackend"); b != nil {
		add(b)
	}
	for _, rule := range asRecords(spec["rules"]) {
		for _, path := range asRecords(nestedRecord(rule, "http")["paths"]) {
			add(nestedRecord(path, "backend"))
		}
	}
	return out
}

// traefikBackends are the Services a Traefik route sends traffic to. A service
// of kind TraefikService is Traefik's own load balancer over other services,
// not a Kubernetes Service, and is left out.
func traefikBackends(kind string, spec map[string]any, namespace string) []RouteBackend {
	protocol := "TCP"
	if kind == "IngressRouteUDP" {
		protocol = "UDP"
	}
	var out []RouteBackend
	for _, route := range asRecords(spec["routes"]) {
		for _, svc := range asRecords(route["services"]) {
			if k := firstString(svc["kind"]); k != "" && k != "Service" {
				continue
			}
			name := firstString(svc["name"])
			if name == "" {
				continue
			}
			ns := firstString(svc["namespace"])
			if ns == "" {
				ns = namespace
			}
			out = append(out, RouteBackend{Service: name, Namespace: ns, Port: scalarString(svc["port"]), Protocol: protocol})
		}
	}
	return out
}

// workloadSelectorLabels extracts the workload's own equality-based pod
// selector, from the path appropriate to its kind. A bare Pod manages no pods
// and so has no selector.
func workloadSelectorLabels(kind string, rec map[string]any) map[string]string {
	switch kind {
	case "Pod":
		return nil
	case "CronJob":
		sel := nestedRecord(rec, "spec", "jobTemplate", "spec", "selector")
		return asStringMap(sel["matchLabels"])
	default: // Deployment, StatefulSet, DaemonSet, ReplicaSet, Job
		sel := nestedRecord(rec, "spec", "selector")
		return asStringMap(sel["matchLabels"])
	}
}

// ResourceMeta extracts the kind, name, and namespace from a decoded resource
// document. ok is false when the doc is not an object or lacks a kind/name.
func ResourceMeta(doc any) (kind, name, namespace string, ok bool) {
	rec, isRec := asRecord(doc)
	if !isRec {
		return "", "", "", false
	}
	kind, _ = asString(rec["kind"])
	md, _ := asRecord(rec["metadata"])
	name, _ = asString(md["name"])
	namespace, _ = asString(md["namespace"])
	return kind, name, namespace, kind != "" && name != ""
}

// --- lenient decoding helpers (shared with later analysis files) ---

func asRecord(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

func asString(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

// asRecords is the objects in an array, skipping anything else.
func asRecords(v any) []map[string]any {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(arr))
	for _, x := range arr {
		if rec, ok := asRecord(x); ok {
			out = append(out, rec)
		}
	}
	return out
}

func asStringArray(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func asStringMap(v any) map[string]string {
	rec, ok := asRecord(v)
	if !ok {
		return map[string]string{}
	}
	out := make(map[string]string, len(rec))
	for k, val := range rec {
		if s, ok := val.(string); ok {
			out[k] = s
		}
	}
	return out
}

// nestedRecord walks a chain of map keys, returning the nested object or a nil
// map if any segment is missing or not an object. Reading from the returned nil
// map is safe and yields zero values.
func nestedRecord(rec map[string]any, keys ...string) map[string]any {
	cur := rec
	for _, k := range keys {
		next, ok := asRecord(cur[k])
		if !ok {
			return nil
		}
		cur = next
	}
	return cur
}

func firstString(v any) string {
	s, _ := asString(v)
	return s
}

// scalarString renders a string or number as a string. Port values arrive as a
// string (named port) or a number — float64 from JSON (get-resources) or int
// from YAML — so all numeric kinds are handled.
func scalarString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	}
	return ""
}

func intOf(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case int64:
		return int(t)
	}
	return 0
}

// parseNetpolRules parses a spec.ingress or spec.egress array. peerKey is "from"
// for ingress, "to" for egress. A null or non-object entry still counts as a
// rule with no constraints (allow-all).
func parseNetpolRules(v any, peerKey string) []NetworkPolicyRule {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	rules := make([]NetworkPolicyRule, 0, len(arr))
	for _, item := range arr {
		rec, ok := asRecord(item)
		if !ok {
			rules = append(rules, NetworkPolicyRule{})
			continue
		}
		rules = append(rules, NetworkPolicyRule{
			Peers: parsePeers(rec[peerKey]),
			Ports: parsePorts(rec["ports"]),
		})
	}
	return rules
}

func parsePeers(v any) []NetworkPolicyPeer {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	peers := make([]NetworkPolicyPeer, 0, len(arr))
	for _, item := range arr {
		rec, ok := asRecord(item)
		if !ok {
			continue
		}
		var peer NetworkPolicyPeer
		if ps, ok := rec["podSelector"]; ok {
			sel := parseLabelSelector(ps)
			peer.PodSelector = &sel
		}
		if nsSel, ok := rec["namespaceSelector"]; ok {
			sel := parseLabelSelector(nsSel)
			peer.NamespaceSelector = &sel
		}
		if ib, ok := asRecord(rec["ipBlock"]); ok {
			peer.IPBlock = &IPBlock{CIDR: firstString(ib["cidr"]), Except: asStringArray(ib["except"])}
		}
		peers = append(peers, peer)
	}
	return peers
}

func parsePorts(v any) []NetworkPolicyPort {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	ports := make([]NetworkPolicyPort, 0, len(arr))
	for _, item := range arr {
		rec, ok := asRecord(item)
		if !ok {
			continue
		}
		proto := firstString(rec["protocol"])
		if proto == "" {
			proto = "TCP"
		}
		ports = append(ports, NetworkPolicyPort{
			Protocol: proto,
			Port:     scalarString(rec["port"]),
			EndPort:  intOf(rec["endPort"]),
		})
	}
	return ports
}

func parseLabelSelector(v any) LabelSelector {
	rec, ok := asRecord(v)
	if !ok {
		return LabelSelector{}
	}
	sel := LabelSelector{Present: true, MatchLabels: asStringMap(rec["matchLabels"])}
	if exprs, ok := rec["matchExpressions"].([]any); ok {
		for _, e := range exprs {
			er, ok := asRecord(e)
			if !ok {
				continue
			}
			key, _ := asString(er["key"])
			op, _ := asString(er["operator"])
			sel.MatchExpressions = append(sel.MatchExpressions, LabelSelectorRequirement{
				Key: key, Operator: op, Values: asStringArray(er["values"]),
			})
		}
	}
	return sel
}
