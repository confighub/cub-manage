// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"strconv"

	"github.com/confighub/sdk/managerkit/guardrails"

	"github.com/confighub/cub-manage/internal/celrule"
)

// otherRoutes are the Ingresses and Traefik IngressRoutes on a route's cluster, which
// unique-ingress-host reads.
var otherRoutes = guardrails.OtherData{Key: "routes", ResourceTypes: concat(ingressTypes, traefikRouteTypes)}

// sameResource: o is r, as the Unit's own resources are passed among the others.
const sameResource = "(o.metadata.name == r.metadata.name && o.kind == r.kind && " +
	"(o.?metadata.?namespace.orValue('') == r.?metadata.?namespace.orValue('')))"

// ingressConflicts fails each path of an Ingress rule whose host and path another Ingress on the
// cluster claims too. A rule naming no host takes every host, which ingress-host warns on, and is
// not compared. A path with none is /.
var ingressConflicts = func() string {
	rules := orEmptyList("r.spec", "rules")
	paths := orEmptyList(rules+"[i].?http.orValue({})", "paths")
	host := rules + "[i].host"
	path := paths + "[j].?path.orValue('/')"
	claimants := celrule.Other(otherRoutes.Key) + ".filter(o, o.kind == 'Ingress' && !" + sameResource + " && " + orEmptyList("o.spec", "rules") +
		".exists(or, or.?host.orValue('') == " + host + " && " + orEmptyList("or.?http.orValue({})", "paths") +
		".exists(op, op.?path.orValue('/') == " + path + "))).map(o, " + celrule.NamespaceOf("o") + " + '/' + o.metadata.name)"
	// Each path's claimants are found once, then named.
	xHost := orEmptyList("r.spec", "rules") + "[x.i].host"
	xPath := orEmptyList(orEmptyList("r.spec", "rules")+"[x.i].?http.orValue({})", "paths") + "[x.j].?path.orValue('/')"
	return "lists.range(size(" + rules + ")).filter(i, " + rules + "[i].?host.orValue('') != '').map(i, " +
		"lists.range(size(" + paths + ")).map(j, {'i': i, 'j': j, 'by': " + claimants + "})).flatten()" +
		".filter(x, size(x.by) > 0).map(x, " +
		celrule.Failure("'spec.rules.' + string(x.i) + '.http.paths.' + string(x.j) + '.path'", celrule.Some(xPath),
			"'host ' + "+xHost+" + ' path ' + "+xPath+" + ' is also claimed by Ingress ' + x.by[0]") + ")"
}()

// A Traefik route's match is a rule such as Host(`shop.example.com`) && PathPrefix(`/api`). The
// conflict check compares the rules made only of Host, Path and PathPrefix terms ANDed together,
// each with one or more arguments in backticks or quotes; a rule with any other matcher, such as
// Header or HostRegexp, or with || or !, can split the same host and path between routes, and is
// not compared. v is a name for the comprehension variables, unique within an expression.

// traefikTerms are the ANDed terms of the match rule m, trimmed.
func traefikTerms(m, v string) string {
	return "(" + m + ".split('&&').map(" + v + ", " + v + ".trim()))"
}

// traefikSimple is whether the match rule m is made only of Host, Path and PathPrefix terms.
func traefikSimple(m, v string) string {
	return "(!" + m + ".contains('||') && !" + m + ".contains('!') && " + traefikTerms(m, v) + ".all(" + v +
		", " + v + ".startsWith('Host(') || " + v + ".startsWith('Path(') || " + v + ".startsWith('PathPrefix(')))"
}

// traefikArgs are the arguments of the terms of m starting with matcher, without their quotes,
// each prefixed with prefix.
func traefikArgs(m, v, matcher, prefix string) string {
	open := strconv.Itoa(len(matcher) + 1)
	return traefikTerms(m, v) + ".filter(" + v + ", " + v + ".startsWith('" + matcher + "(')).map(" + v + ", " +
		v + ".substring(" + open + ", size(" + v + ") - 1).split(',').map(" + v + "a, " + v + "a.trim()).map(" + v + "a, '" +
		prefix + "' + " + v + "a.substring(1, size(" + v + "a) - 1))).flatten()"
}

// traefikHosts are the hosts the match rule m names.
func traefikHosts(m, v string) string { return traefikArgs(m, v, "Host", "") }

// traefikPaths are the paths the match rule m names, E: for an exact Path and P: for a
// PathPrefix; a rule naming neither matches every path, as PathPrefix(`/`) does.
func traefikPaths(m, v string) string {
	paths := traefikArgs(m, v, "Path", "E:") + " + " + traefikArgs(m, v+"p", "PathPrefix", "P:")
	return "(size(" + paths + ") == 0 ? ['P:/'] : " + paths + ")"
}

// traefikConflict is whether the match rules m1 and m2 match a host and path in common.
func traefikConflict(m1, m2 string) string {
	return "(" + traefikSimple(m1, "t1") + " && " + traefikSimple(m2, "t2") + " && size(" + traefikHosts(m1, "h1") + ") > 0 && " +
		"sets.intersects(" + traefikHosts(m1, "h1") + ", " + traefikHosts(m2, "h2") + ") && " +
		"sets.intersects(" + traefikPaths(m1, "q1") + ", " + traefikPaths(m2, "q2") + "))"
}

// traefikConflicts fails each route of an IngressRoute whose rule matches a host and path that a
// route of another IngressRoute on the cluster also matches, on an entry point they share. A
// router's rule applies across namespaces, and an IngressRoute naming no entry points is on
// every one.
var traefikConflicts = func() string {
	routes := orEmptyList("r.spec", "routes")
	match := routes + "[i].?match.orValue('')"
	entryPoints := func(x string) string { return orEmptyList(x+".spec", "entryPoints") }
	shareEntryPoint := "(size(" + entryPoints("r") + ") == 0 || size(" + entryPoints("o") + ") == 0 || sets.intersects(" +
		entryPoints("r") + ", " + entryPoints("o") + "))"
	claimants := celrule.Other(otherRoutes.Key) + ".filter(o, o.kind == 'IngressRoute' && !" + sameResource + " && " + shareEntryPoint +
		" && " + orEmptyList("o.spec", "routes") + ".exists(or, " + traefikConflict(match, "or.?match.orValue('')") + "))" +
		".map(o, " + celrule.NamespaceOf("o") + " + '/' + o.metadata.name)"
	// Each route's claimants are found once, then named.
	return "lists.range(size(" + routes + ")).map(i, {'i': i, 'by': " + claimants + "}).filter(x, size(x.by) > 0).map(x, " +
		celrule.Failure("'spec.routes.' + string(x.i) + '.match'", celrule.Some(routes+"[x.i].?match.orValue('')"),
			"'route ' + string(x.i) + ' matches a host and path that IngressRoute ' + x.by[0] + ' matches too'") +
		")"
}()

// celUniqueIngressHost fails a route whose host and path another route of its kind on the cluster
// claims too, so which gets the traffic is up to the controller. A Unit with no Target is on no
// known cluster, so it passes.
var celUniqueIngressHost = celrule.When(celrule.OnACluster,
	"(r.kind == 'Ingress' ? "+ingressConflicts+" : "+traefikConflicts+")")

var uniqueIngressHost = guardrails.Check{Control: "CH-0607", Name: "unique-ingress-host", Rule: "unique-ingress-host",
	DisplayName: "Routes claim their hosts and paths alone",
	Description: "Warns on an Ingress path whose host and path another Ingress on its cluster claims too, and on a Traefik IngressRoute route whose rule matches a host and path a route of another IngressRoute matches, on an entry point they share, so which gets the traffic is up to the controller. An Ingress rule naming no host is not compared, nor is a Traefik rule with matchers other than Host, Path and PathPrefix, or with || or !, which can split the traffic between routes. Not checked for a Unit with no Target. Fix: give one of them another host or path.",
	Engine:      guardrails.EngineCEL, Priority: guardrails.PriorityHigh, ResourceTypes: concat(ingressTypes, traefikRouteTypes), Failures: celUniqueIngressHost,
	OtherData: []guardrails.OtherData{otherRoutes}, Corresponds: []string{"GK:uniqueingresshost", "KY:unique-ingress-paths"}}
