// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package netpol

// scrape derives flows from a metrics scraper's Kubernetes service discovery:
// the pods a Prometheus-format scrape_configs job, as Prometheus and
// VictoriaMetrics read it, or an Alloy discovery.kubernetes feeding a
// prometheus.scrape, finds through the Kubernetes API, filtered by its relabel
// rules, are pods the scraper connects to. No Service address names them, so
// only the discovery says so.
//
// Each target gets the labels discovery would give it -- its namespace, its
// pod's labels and annotations, the container port, and for an endpoints or
// service job the Service's -- and the job's keep, drop and replace rules are
// applied in order. A target that survives is scraped at the port its
// __address__ ends up with.

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"sigs.k8s.io/yaml"
)

// scrapeJob is one Kubernetes discovery a scraper's config sets up, with the
// relabel rules applied to what it finds.
type scrapeJob struct {
	name string
	role string
	// namespaces are the ones it looks in, every one when empty.
	namespaces []string
	rules      []relabelRule
}

type relabelRule struct {
	sourceLabels []string
	separator    string
	regex        *regexp.Regexp
	action       string
	targetLabel  string
	replacement  string
}

func newRelabelRule(source []string, separator, regex, action, target, replacement *string) (relabelRule, error) {
	r := relabelRule{separator: ";", action: "replace", replacement: "$1", sourceLabels: source}
	if separator != nil {
		r.separator = *separator
	}
	if action != nil && *action != "" {
		r.action = strings.ToLower(*action)
	}
	if target != nil {
		r.targetLabel = *target
	}
	if replacement != nil {
		r.replacement = *replacement
	}
	expr := "(.*)"
	if regex != nil {
		expr = *regex
	}
	re, err := regexp.Compile("^(?:" + expr + ")$")
	if err != nil {
		return r, err
	}
	r.regex = re
	return r, nil
}

// apply runs the rules over a target's labels, reporting whether it is kept.
// keep, drop and replace are applied; the actions that only rename or remove
// labels cannot change whether a target is scraped, and are skipped.
func applyRules(rules []relabelRule, labels map[string]string) bool {
	for _, r := range rules {
		values := make([]string, len(r.sourceLabels))
		for i, l := range r.sourceLabels {
			values[i] = labels[l]
		}
		value := strings.Join(values, r.separator)
		switch r.action {
		case "keep":
			if !r.regex.MatchString(value) {
				return false
			}
		case "drop":
			if r.regex.MatchString(value) {
				return false
			}
		case "replace":
			m := r.regex.FindStringSubmatchIndex(value)
			if m == nil || r.targetLabel == "" {
				continue
			}
			out := string(r.regex.ExpandString(nil, r.replacement, value, m))
			if out == "" {
				delete(labels, r.targetLabel)
			} else {
				labels[r.targetLabel] = out
			}
		}
	}
	return true
}

// ScrapeFlows are the flows the metrics scrapers in a cluster make, from the
// Kubernetes discovery in their config.
func ScrapeFlows(c *ClusterNetpol) ([]Flow, []RefSkip) {
	var flows []Flow
	var skipped []RefSkip
	seen := map[string]bool{}
	for _, w := range c.Workloads {
		for _, src := range configSources(c, w) {
			jobs, err := parseScrapeJobs(src.text)
			if err != nil {
				skipped = append(skipped, RefSkip{Cluster: c.Cluster, Namespace: NamespaceOf(w.Namespace), Via: src.via,
					Reason: "has scrape configuration that cannot be read: " + err.Error()})
				continue
			}
			for _, job := range jobs {
				via := src.via + ", scrape job " + job.name
				for _, t := range scrapeTargets(c, w, job) {
					if t.dest == w {
						continue
					}
					ports := []NetworkPolicyPort{{Protocol: "TCP", Port: t.port}}
					key := via + "|" + workloadKey(w) + "|" + workloadKey(t.dest) + "|" + portsKey(ports)
					if seen[key] {
						continue
					}
					seen[key] = true
					flows = append(flows, Flow{Source: w, Dest: t.dest, Ports: ports, Via: via})
				}
			}
		}
	}
	return flows, skipped
}

type scrapeTarget struct {
	dest *WorkloadEntity
	port string
}

// scrapeTargets are the pods a job scrapes, and on which port.
func scrapeTargets(c *ClusterNetpol, scraper *WorkloadEntity, job scrapeJob) []scrapeTarget {
	inScope := func(ns string) bool {
		return len(job.namespaces) == 0 || containsString(job.namespaces, ns) ||
			(containsString(job.namespaces, ownNamespace) && ns == NamespaceOf(scraper.Namespace))
	}
	var out []scrapeTarget
	keep := func(dest *WorkloadEntity, labels map[string]string) {
		if !applyRules(job.rules, labels) {
			return
		}
		if _, port, ok := strings.Cut(labels["__address__"], ":"); ok && port != "" {
			out = append(out, scrapeTarget{dest: dest, port: port})
		}
	}
	switch job.role {
	case "pod":
		for _, w := range c.Workloads {
			ns := NamespaceOf(w.Namespace)
			if !inScope(ns) {
				continue
			}
			if len(w.Ports) == 0 {
				keep(w, podLabels(w, nil, ns+"-pod"))
			}
			for i := range w.Ports {
				keep(w, podLabels(w, &w.Ports[i], ns+"-pod"))
			}
		}
	case "endpoints", "endpointslice", "service":
		for _, s := range c.Services {
			ns := NamespaceOf(s.Namespace)
			if !inScope(ns) || len(s.Selector) == 0 {
				continue
			}
			for _, sp := range s.Ports {
				for _, w := range selectedWorkloads(c, ns, s.Selector) {
					target := podPorts(w, []ServicePort{sp})[0].Port
					var labels map[string]string
					if job.role == "service" {
						labels = serviceLabels(s, map[string]string{
							"__address__":                             s.Name + "." + ns + ".svc:" + strconv.Itoa(sp.Port),
							"__meta_kubernetes_service_port_name":     sp.Name,
							"__meta_kubernetes_service_port_number":   strconv.Itoa(sp.Port),
							"__meta_kubernetes_service_port_protocol": sp.Protocol,
						})
						if !applyRules(job.rules, labels) {
							continue
						}
						out = append(out, scrapeTarget{dest: w, port: target})
						continue
					}
					var cp *ContainerPort
					for i := range w.Ports {
						if strconv.Itoa(w.Ports[i].Port) == target {
							cp = &w.Ports[i]
						}
					}
					labels = podLabels(w, cp, ns+"-pod")
					labels = serviceLabels(s, labels)
					labels["__address__"] = "pod:" + target
					labels["__meta_kubernetes_endpoint_port_name"] = sp.Name
					labels["__meta_kubernetes_endpoint_port_protocol"] = sp.Protocol
					labels["__meta_kubernetes_endpoint_ready"] = "true"
					labels["__meta_kubernetes_endpointslice_port_name"] = sp.Name
					labels["__meta_kubernetes_endpointslice_port_protocol"] = sp.Protocol
					keep(w, labels)
				}
			}
		}
	}
	return out
}

// ownNamespace stands for the scraper's own namespace in a job's namespaces.
const ownNamespace = "\x00own"

// podLabels are the labels Kubernetes pod discovery gives a pod's target: one
// per container port, or one with no port when the pod declares none.
func podLabels(w *WorkloadEntity, cp *ContainerPort, podName string) map[string]string {
	ns := NamespaceOf(w.Namespace)
	labels := map[string]string{
		"__meta_kubernetes_namespace":           ns,
		"__meta_kubernetes_pod_name":            podName,
		"__meta_kubernetes_pod_phase":           "Running",
		"__meta_kubernetes_pod_ready":           "true",
		"__address__":                           "pod",
		"__meta_kubernetes_pod_controller_kind": w.Kind,
		"__meta_kubernetes_pod_controller_name": w.Name,
	}
	for k, v := range w.PodLabels {
		labels["__meta_kubernetes_pod_label_"+sanitizeLabel(k)] = v
		labels["__meta_kubernetes_pod_labelpresent_"+sanitizeLabel(k)] = "true"
	}
	for k, v := range w.PodAnnotations {
		labels["__meta_kubernetes_pod_annotation_"+sanitizeLabel(k)] = v
		labels["__meta_kubernetes_pod_annotationpresent_"+sanitizeLabel(k)] = "true"
	}
	if cp != nil {
		labels["__address__"] = "pod:" + strconv.Itoa(cp.Port)
		labels["__meta_kubernetes_pod_container_name"] = cp.Container
		labels["__meta_kubernetes_pod_container_port_name"] = cp.Name
		labels["__meta_kubernetes_pod_container_port_number"] = strconv.Itoa(cp.Port)
		labels["__meta_kubernetes_pod_container_port_protocol"] = cp.Protocol
	}
	return labels
}

func serviceLabels(s *ServiceEntity, labels map[string]string) map[string]string {
	labels["__meta_kubernetes_namespace"] = NamespaceOf(s.Namespace)
	labels["__meta_kubernetes_service_name"] = s.Name
	for k, v := range s.Labels {
		labels["__meta_kubernetes_service_label_"+sanitizeLabel(k)] = v
		labels["__meta_kubernetes_service_labelpresent_"+sanitizeLabel(k)] = "true"
	}
	for k, v := range s.Annotations {
		labels["__meta_kubernetes_service_annotation_"+sanitizeLabel(k)] = v
		labels["__meta_kubernetes_service_annotationpresent_"+sanitizeLabel(k)] = "true"
	}
	return labels
}

var invalidLabelChars = regexp.MustCompile(`[^a-zA-Z0-9_]`)

// sanitizeLabel is a Kubernetes label or annotation name as discovery writes it
// into a label name.
func sanitizeLabel(name string) string { return invalidLabelChars.ReplaceAllString(name, "_") }

// parseScrapeJobs reads the Kubernetes discovery jobs in a scraper's config:
// Prometheus-format YAML, or Alloy's configuration language. Other text has
// none.
func parseScrapeJobs(text string) ([]scrapeJob, error) {
	switch {
	case strings.Contains(text, "kubernetes_sd_configs"):
		return parsePrometheusJobs(text)
	case strings.Contains(text, "discovery.kubernetes"):
		return parseAlloyJobs(text)
	}
	return nil, nil
}

type promConfig struct {
	ScrapeConfigs []struct {
		JobName string `json:"job_name"`
		SD      []struct {
			Role       string `json:"role"`
			Namespaces *struct {
				Names        []string `json:"names"`
				OwnNamespace bool     `json:"own_namespace"`
			} `json:"namespaces"`
		} `json:"kubernetes_sd_configs"`
		Relabel []struct {
			SourceLabels []string `json:"source_labels"`
			Separator    *string  `json:"separator"`
			Regex        *string  `json:"regex"`
			Action       *string  `json:"action"`
			TargetLabel  *string  `json:"target_label"`
			Replacement  *string  `json:"replacement"`
		} `json:"relabel_configs"`
	} `json:"scrape_configs"`
}

func parsePrometheusJobs(text string) ([]scrapeJob, error) {
	var cfg promConfig
	if err := yaml.Unmarshal([]byte(text), &cfg); err != nil {
		return nil, err
	}
	var out []scrapeJob
	for _, sc := range cfg.ScrapeConfigs {
		var rules []relabelRule
		for _, r := range sc.Relabel {
			rule, err := newRelabelRule(r.SourceLabels, r.Separator, r.Regex, r.Action, r.TargetLabel, r.Replacement)
			if err != nil {
				return nil, fmt.Errorf("job %s: %w", sc.JobName, err)
			}
			rules = append(rules, rule)
		}
		for _, sd := range sc.SD {
			job := scrapeJob{name: sc.JobName, role: sd.Role, rules: rules}
			if sd.Namespaces != nil {
				job.namespaces = append(job.namespaces, sd.Namespaces.Names...)
				if sd.Namespaces.OwnNamespace {
					job.namespaces = append(job.namespaces, ownNamespace)
				}
			}
			out = append(out, job)
		}
	}
	return out, nil
}

// alloyBlock matches the header of an Alloy component block, as in
// discovery.kubernetes "pods" {.
var alloyBlock = regexp.MustCompile(`(?m)^\s*([a-z_]+(?:\.[a-z_]+)+)\s+"([^"]+)"\s*\{`)

// parseAlloyJobs reads Alloy's configuration: each prometheus.scrape whose
// targets are a discovery.kubernetes's, directly or through discovery.relabel
// components, is a job with the rules of the relabels between them, in order.
func parseAlloyJobs(text string) ([]scrapeJob, error) {
	type component struct{ kind, body string }
	components := map[string]component{}
	var scrapes []string
	for _, m := range alloyBlock.FindAllStringSubmatchIndex(text, -1) {
		kind, label := text[m[2]:m[3]], text[m[4]:m[5]]
		body, ok := blockBody(text, m[1]-1)
		if !ok {
			return nil, fmt.Errorf("%s %q is not closed", kind, label)
		}
		components[kind+"."+label] = component{kind: kind, body: body}
		if kind == "prometheus.scrape" {
			scrapes = append(scrapes, label)
		}
	}
	sort.Strings(scrapes)
	var out []scrapeJob
	for _, label := range scrapes {
		ref := alloyTargets(components["prometheus.scrape."+label].body)
		var rules []relabelRule
		for strings.HasPrefix(ref, "discovery.relabel.") {
			name := strings.TrimSuffix(ref, ".output")
			rel, ok := components[name]
			if !ok {
				break
			}
			rs, err := alloyRules(rel.body)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", name, err)
			}
			rules = append(rs, rules...)
			ref = alloyTargets(rel.body)
		}
		name := strings.TrimSuffix(ref, ".targets")
		disc, ok := components[name]
		if !ok || disc.kind != "discovery.kubernetes" {
			continue
		}
		job := scrapeJob{name: label, role: alloyString(disc.body, "role"), rules: rules}
		if ns, ok := alloyNestedBody(disc.body, "namespaces"); ok {
			job.namespaces = alloyStrings(ns, "names")
			if regexp.MustCompile(`own_namespace\s*=\s*true`).MatchString(ns) {
				job.namespaces = append(job.namespaces, ownNamespace)
			}
		}
		out = append(out, job)
	}
	return out, nil
}

// blockBody is the text between the brace at open and the one that closes it,
// skipping braces in strings and comments.
func blockBody(text string, open int) (string, bool) {
	depth := 0
	for i := open; i < len(text); i++ {
		switch text[i] {
		case '"':
			for i++; i < len(text) && text[i] != '"'; i++ {
				if text[i] == '\\' {
					i++
				}
			}
		case '/':
			if i+1 < len(text) && text[i+1] == '/' {
				for i < len(text) && text[i] != '\n' {
					i++
				}
			}
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return text[open+1 : i], true
			}
		}
	}
	return "", false
}

var alloyTargetsAttr = regexp.MustCompile(`(?m)^\s*targets\s*=\s*([a-zA-Z0-9_.]+)\s*$`)

// alloyTargets is the component a block's targets come from, as
// discovery.relabel.pods.output.
func alloyTargets(body string) string {
	if m := alloyTargetsAttr.FindStringSubmatch(body); m != nil {
		return m[1]
	}
	return ""
}

func alloyNestedBody(body, block string) (string, bool) {
	loc := regexp.MustCompile(`(?m)^\s*` + block + `\s*\{`).FindStringIndex(body)
	if loc == nil {
		return "", false
	}
	return blockBody(body, loc[1]-1)
}

var alloyRuleBlock = regexp.MustCompile(`(?m)^\s*rule\s*\{`)

func alloyRules(body string) ([]relabelRule, error) {
	var out []relabelRule
	for _, loc := range alloyRuleBlock.FindAllStringIndex(body, -1) {
		rb, ok := blockBody(body, loc[1]-1)
		if !ok {
			return nil, fmt.Errorf("a rule is not closed")
		}
		opt := func(attr string) *string {
			if v, ok := alloyStringOK(rb, attr); ok {
				return &v
			}
			return nil
		}
		rule, err := newRelabelRule(alloyStrings(rb, "source_labels"), opt("separator"), opt("regex"), opt("action"), opt("target_label"), opt("replacement"))
		if err != nil {
			return nil, err
		}
		out = append(out, rule)
	}
	return out, nil
}

func alloyString(body, attr string) string {
	v, _ := alloyStringOK(body, attr)
	return v
}

func alloyStringOK(body, attr string) (string, bool) {
	m := regexp.MustCompile(`(?m)^\s*` + attr + `\s*=\s*("(?:[^"\\]|\\.)*")`).FindStringSubmatch(body)
	if m == nil {
		return "", false
	}
	v, err := strconv.Unquote(m[1])
	return v, err == nil
}

func alloyStrings(body, attr string) []string {
	m := regexp.MustCompile(`(?ms)^\s*` + attr + `\s*=\s*\[(.*?)\]`).FindStringSubmatch(body)
	if m == nil {
		return nil
	}
	var out []string
	for _, q := range regexp.MustCompile(`"(?:[^"\\]|\\.)*"`).FindAllString(m[1], -1) {
		if v, err := strconv.Unquote(q); err == nil {
			out = append(out, v)
		}
	}
	return out
}
