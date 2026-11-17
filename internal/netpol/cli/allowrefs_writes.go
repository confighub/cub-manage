// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

// Where allow-from-refs writes its changes. A change every variant of an
// upstream Unit takes is made once, in the upstream, and the changes in each
// base are made in a ChangeSet and named by a ChangeOrder, which promotes them
// to the variants along the component's ChangeWorkflow. The rest are written to
// the Units that need them.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"sigs.k8s.io/yaml"

	"github.com/confighub/sdk/core/cubapi"
	api "github.com/confighub/sdk/core/function/api"
	goclientnew "github.com/confighub/sdk/core/openapi/goclient-new"

	"github.com/confighub/cub-manage/internal/cub"
	"github.com/confighub/cub-manage/internal/netpol"
	"github.com/confighub/cub-manage/internal/netpol/snapshot"
)

// The Triggers whose findings allow-from-refs fixes: an edit takes out rules
// admitting every source, and a created policy covers a workload no policy
// isolated.
const (
	allowAllTrigger = "netpol-no-allow-all-ingress"
	coverageTrigger = "netpol-ingress-coverage"
)

// lineage is what is known about the Units a plan writes, their upstreams, and
// the upstreams' variants.
type lineage struct {
	units    map[string]*goclientnew.ExtendedUnit
	variants map[string][]*goclientnew.ExtendedUnit
	parsed   map[string]*netpol.ClusterNetpol
}

const lineageSelect = "UnitID,SpaceID,Slug,UpstreamUnitID,TargetID"

func loadLineage(ctx context.Context, c *cubapi.Client, unitIDs []string) (*lineage, error) {
	l := &lineage{
		units:    map[string]*goclientnew.ExtendedUnit{},
		variants: map[string][]*goclientnew.ExtendedUnit{},
		parsed:   map[string]*netpol.ClusterNetpol{},
	}
	if len(unitIDs) == 0 {
		return l, nil
	}
	planned, err := listUnitsIn(ctx, c, "UnitID", unitIDs, lineageSelect, "SpaceID")
	if err != nil {
		return nil, err
	}
	var upstreamIDs []string
	for _, u := range planned {
		l.units[u.Unit.UnitID.String()] = u
		if u.Unit.UpstreamUnitID != nil && !containsString(upstreamIDs, u.Unit.UpstreamUnitID.String()) {
			upstreamIDs = append(upstreamIDs, u.Unit.UpstreamUnitID.String())
		}
	}
	if len(upstreamIDs) == 0 {
		return l, nil
	}
	upstreams, err := listUnitsIn(ctx, c, "UnitID", upstreamIDs, lineageSelect, "SpaceID")
	if err != nil {
		return nil, err
	}
	for _, u := range upstreams {
		l.units[u.Unit.UnitID.String()] = u
	}
	variants, err := listUnitsIn(ctx, c, "UpstreamUnitID", upstreamIDs, lineageSelect, "SpaceID")
	if err != nil {
		return nil, err
	}
	for _, v := range variants {
		l.units[v.Unit.UnitID.String()] = v
		up := v.Unit.UpstreamUnitID.String()
		l.variants[up] = append(l.variants[up], v)
	}
	return l, nil
}

// upstream is the Unit unitID was cloned from, nil when it has none in view.
func (l *lineage) upstream(unitID string) *goclientnew.ExtendedUnit {
	u := l.units[unitID]
	if u == nil || u.Unit.UpstreamUnitID == nil {
		return nil
	}
	return l.units[u.Unit.UpstreamUnitID.String()]
}

// deployed reports whether a Unit is released to a cluster, as a base often is
// not.
func deployed(u *goclientnew.ExtendedUnit) bool { return u.Unit.TargetID != nil }

func unitName(u *goclientnew.ExtendedUnit) string { return spaceSlugOf(u) + "/" + u.Unit.Slug }

// data is an upstream's NetworkPolicy-relevant resources, read from its configuration.
func (l *lineage) data(ctx context.Context, c *cubapi.Client, u *goclientnew.ExtendedUnit) (*netpol.ClusterNetpol, error) {
	id := u.Unit.UnitID.String()
	if cl, ok := l.parsed[id]; ok {
		return cl, nil
	}
	ref := cub.UnitRef{SpaceID: u.Unit.SpaceID, SpaceSlug: spaceSlugOf(u), UnitID: u.Unit.UnitID, UnitSlug: u.Unit.Slug}
	data, _, err := cub.GetUnitData(ctx, c, ref)
	if err != nil {
		return nil, err
	}
	var resources []netpol.FleetResource
	for _, doc := range cub.SplitDocuments(data) {
		var m map[string]any
		if yaml.Unmarshal([]byte(doc), &m) == nil && m != nil {
			resources = append(resources, netpol.FleetResource{Doc: m})
		}
	}
	cl := netpol.BuildFleet(resources)[""]
	if cl == nil {
		cl = &netpol.ClusterNetpol{}
	}
	l.parsed[id] = cl
	return cl, nil
}

// unitEdit is the policy edits one Unit takes.
type unitEdit struct {
	spaceID  string
	space    string
	unitID   string
	unit     string
	edits    []*netpol.PolicyEdit
	exprs    []string
	clusters []string
	// variants are the Units that take the edit from this one, their upstream,
	// rather than being edited.
	variants []string
	// note says why a variant is edited rather than its upstream.
	note string
	// done is whether the upstream has the edit already, and only its variants
	// lack it, until they take it.
	done bool
}

func (u *unitEdit) key() string { return strings.Join(u.exprs, "\n") }

func (u *unitEdit) inBase() bool { return len(u.variants) > 0 && !u.done }

// planUnitEdits groups the policy edits by the Unit holding the policy, and
// moves the edits of the variants of an upstream Unit to the upstream when they
// all take the same one.
func planUnitEdits(ctx context.Context, c *cubapi.Client, l *lineage, plans []netpol.IngressPlan, noUpstream bool) ([]*unitEdit, error) {
	byUnit := map[string]*unitEdit{}
	for _, p := range plans {
		for _, e := range p.Edits {
			o := e.Policy.Origin
			u := byUnit[o.UnitID]
			if u == nil {
				u = &unitEdit{spaceID: o.SpaceID, space: o.Space, unitID: o.UnitID, unit: o.UnitSlug}
				byUnit[o.UnitID] = u
			}
			u.edits = append(u.edits, e)
			u.exprs = append(u.exprs, e.YQ())
			if !containsString(u.clusters, p.Cluster) {
				u.clusters = append(u.clusters, p.Cluster)
			}
		}
	}
	if !noUpstream {
		if err := moveEditsToUpstreams(ctx, c, l, byUnit); err != nil {
			return nil, err
		}
	}
	out := make([]*unitEdit, 0, len(byUnit))
	for _, u := range byUnit {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].space+"/"+out[i].unit < out[j].space+"/"+out[j].unit })
	return out, nil
}

// moveEditsToUpstreams replaces the edits of an upstream Unit's variants with
// one edit to the upstream, when every variant takes the same edit -- and the
// upstream too, when it is deployed itself -- and the upstream's policies are
// the same as theirs, so that the edit means the same there.
func moveEditsToUpstreams(ctx context.Context, c *cubapi.Client, l *lineage, byUnit map[string]*unitEdit) error {
	ids := make([]string, 0, len(byUnit))
	for id := range byUnit {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	seen := map[string]bool{}
	for _, id := range ids {
		up := l.upstream(id)
		if up == nil || seen[up.Unit.UnitID.String()] {
			continue
		}
		upID := up.Unit.UnitID.String()
		seen[upID] = true
		group := l.variants[upID]
		if deployed(up) {
			group = append([]*goclientnew.ExtendedUnit{up}, group...)
		}
		note := sameEditNote(byUnit, group, unitName(up))
		done := false
		if note == "" {
			cl, err := l.data(ctx, c, up)
			if err != nil {
				return err
			}
			done, note = compareUpstream(unitName(up), policiesByName(cl), byUnit[group[0].Unit.UnitID.String()].edits)
		}
		if note != "" {
			for _, v := range group {
				if u := byUnit[v.Unit.UnitID.String()]; u != nil {
					u.note = note
				}
			}
			continue
		}
		first := byUnit[group[0].Unit.UnitID.String()]
		moved := &unitEdit{
			spaceID: up.Unit.SpaceID.String(), space: spaceSlugOf(up), unitID: upID, unit: up.Unit.Slug,
			edits: first.edits, exprs: first.exprs, done: done,
		}
		for _, v := range group {
			vid := v.Unit.UnitID.String()
			u := byUnit[vid]
			if vid != upID {
				moved.variants = append(moved.variants, u.space+"/"+u.unit)
			}
			moved.clusters = appendNew(moved.clusters, u.clusters...)
			delete(byUnit, vid)
		}
		sort.Strings(moved.variants)
		byUnit[upID] = moved
	}
	return nil
}

// sameEditNote says why the Units of a group -- an upstream's variants, and the
// upstream when it is deployed -- do not all take the same edit, empty when they do.
func sameEditNote(byUnit map[string]*unitEdit, group []*goclientnew.ExtendedUnit, upName string) string {
	var first *unitEdit
	for _, v := range group {
		u := byUnit[v.Unit.UnitID.String()]
		if u == nil {
			return fmt.Sprintf("%s takes no edit: it is out of scope, or its policies or routes differ", groupMember(v, upName))
		}
		if first == nil {
			first = u
			continue
		}
		if u.key() != first.key() {
			return fmt.Sprintf("variants of %s take different edits: %s/%s and %s", upName, first.space, first.unit, unitName(v))
		}
	}
	return ""
}

// groupMember names a Unit of an upstream's group: one of its variants, or the
// upstream itself, deployed too.
func groupMember(u *goclientnew.ExtendedUnit, upName string) string {
	if unitName(u) == upName {
		return "the upstream, " + upName + ", deployed too,"
	}
	return unitName(u) + ", a variant of " + upName + ","
}

func policiesByName(cl *netpol.ClusterNetpol) map[string]*netpol.NetworkPolicyEntity {
	out := map[string]*netpol.NetworkPolicyEntity{}
	for _, p := range cl.NetworkPolicies {
		out[p.Name] = p
	}
	return out
}

// compareUpstream compares the upstream's policies, by name, with a variant's
// edits. The edit means the same in the upstream when each of its policies has
// the variant's ingress rules; done is whether each has the rules the edit
// leaves instead, so that the variants take it with no edit. Otherwise note
// says why -- a policy the upstream lacks, or whose rules are neither.
func compareUpstream(name string, policies map[string]*netpol.NetworkPolicyEntity, edits []*netpol.PolicyEdit) (done bool, note string) {
	same, edited := 0, 0
	for _, e := range edits {
		p := policies[e.Policy.Name]
		switch {
		case p == nil:
			return false, fmt.Sprintf("the upstream, %s, has no policy %s", name, e.Policy.Name)
		case netpol.IngressKey(p) == netpol.IngressKey(e.Policy):
			same++
		case netpol.IngressKey(p) == e.ResultKey():
			edited++
		default:
			return false, fmt.Sprintf("the upstream, %s, has policy %s with other ingress rules than its variants", name, e.Policy.Name)
		}
	}
	if same > 0 && edited > 0 {
		return false, fmt.Sprintf("the upstream, %s, has some of the edits and not others", name)
	}
	return edited > 0, ""
}

// refCreate is a new policy Unit.
type refCreate struct {
	clusters []string
	create   *netpol.PolicyCreate
	dest     createDest
	// dstUnit is the Unit of the workload the policy selects.
	dstUnit string
	// variants are the Spaces that take the policy from the upstream's Space,
	// when it is created there.
	variants []string
	note     string
	done     bool
}

func (cr *refCreate) inBase() bool { return len(cr.variants) > 0 && !cr.done }

// planCreates places each new policy in the Space of the workload it selects,
// or, when every variant of that workload's upstream would get the same policy,
// once in the upstream's Space, from which promoting clones it to the variants.
func planCreates(ctx context.Context, c *cubapi.Client, l *lineage, snap *snapshot.Snapshot, plans []netpol.IngressPlan, noUpstream bool) ([]*refCreate, error) {
	var out []*refCreate
	for _, p := range plans {
		for _, cr := range p.Creates {
			meta, ok := snap.Units[cr.Dest.Origin.UnitID]
			if !ok {
				continue
			}
			dest, err := destFromUnitMeta(meta)
			if err != nil {
				return nil, err
			}
			out = append(out, &refCreate{clusters: []string{p.Cluster}, create: cr, dest: dest, dstUnit: cr.Dest.Origin.UnitID})
		}
	}
	if !noUpstream {
		var err error
		if out, err = moveCreatesToUpstreams(ctx, c, l, out); err != nil {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].dest.spaceSlug+"/"+out[i].create.Name < out[j].dest.spaceSlug+"/"+out[j].create.Name
	})
	return out, nil
}

func moveCreatesToUpstreams(ctx context.Context, c *cubapi.Client, l *lineage, creates []*refCreate) ([]*refCreate, error) {
	type groupKey struct{ upstream, name string }
	groups := map[groupKey][]*refCreate{}
	var keys []groupKey
	var out []*refCreate
	var unplaced []*refCreate
	for _, cr := range creates {
		up := l.upstream(cr.dstUnit)
		if up == nil {
			unplaced = append(unplaced, cr)
			continue
		}
		k := groupKey{up.Unit.UnitID.String(), cr.create.Name}
		if groups[k] == nil {
			keys = append(keys, k)
		}
		groups[k] = append(groups[k], cr)
	}
	// A deployed upstream's own policy belongs with its variants'.
	for _, cr := range unplaced {
		k := groupKey{cr.dstUnit, cr.create.Name}
		if groups[k] != nil {
			groups[k] = append(groups[k], cr)
		} else {
			out = append(out, cr)
		}
	}
	for _, k := range keys {
		members := groups[k]
		moved, note, err := createInUpstream(ctx, c, l, l.units[k.upstream], members)
		if err != nil {
			return nil, err
		}
		if moved == nil {
			for _, cr := range members {
				cr.note = note
			}
			out = append(out, members...)
			continue
		}
		out = append(out, moved)
	}
	return out, nil
}

// createInUpstream is the policy created once in the upstream's Space in place
// of members, a create for each of its variants -- and for itself, when it is
// deployed -- or, when the members differ or the upstream's workload is not
// theirs, nil and why.
func createInUpstream(ctx context.Context, c *cubapi.Client, l *lineage, up *goclientnew.ExtendedUnit, members []*refCreate) (*refCreate, string, error) {
	upID := up.Unit.UnitID.String()
	byUnit := map[string]*refCreate{}
	for _, cr := range members {
		byUnit[cr.dstUnit] = cr
	}
	group := l.variants[upID]
	if deployed(up) {
		group = append([]*goclientnew.ExtendedUnit{up}, group...)
	}
	want := members[0].create.YAML()
	for _, v := range group {
		cr := byUnit[v.Unit.UnitID.String()]
		switch {
		case cr == nil:
			return nil, fmt.Sprintf("%s gets no %s: it is out of scope, or its policies or routes differ", groupMember(v, unitName(up)), members[0].create.Name), nil
		case cr.create.YAML() != want:
			return nil, fmt.Sprintf("variants of %s would get different %s policies, in other namespaces or selecting other pods", unitName(up), members[0].create.Name), nil
		}
	}
	cl, err := l.data(ctx, c, up)
	if err != nil {
		return nil, "", err
	}
	dst := members[0].create.Dest
	var base *netpol.WorkloadEntity
	for _, w := range cl.Workloads {
		if w.Kind == dst.Kind && w.Name == dst.Name {
			base = w
		}
	}
	if base == nil {
		return nil, fmt.Sprintf("the upstream, %s, has no %s %s", unitName(up), dst.Kind, dst.Name), nil
	}
	create := &netpol.PolicyCreate{Dest: base, Name: members[0].create.Name, Rules: members[0].create.Rules}
	if create.YAML() != want {
		return nil, fmt.Sprintf("the upstream's %s %s is in another namespace or has other labels than its variants'", dst.Kind, dst.Name), nil
	}
	moved := &refCreate{
		create:  create,
		dest:    createDest{spaceID: up.Unit.SpaceID, spaceSlug: spaceSlugOf(up)},
		dstUnit: upID,
	}
	for _, v := range group {
		cr := byUnit[v.Unit.UnitID.String()]
		moved.clusters = appendNew(moved.clusters, cr.clusters...)
		if v.Unit.UnitID.String() != upID {
			moved.variants = append(moved.variants, spaceSlugOf(v))
		}
	}
	sort.Strings(moved.variants)
	// A policy the upstream's Space has already is waiting to be promoted.
	if _, err := cub.ResolveUnit(ctx, c, moved.dest.spaceSlug, create.Name); err == nil {
		moved.done = true
	}
	return moved, "", nil
}

// baseRollout is the ChangeSet and ChangeOrder a base's changes are made in.
type baseRollout struct {
	base     cub.RolloutBase
	edits    []*unitEdit
	creates  []*refCreate
	triggers []string
	// changeOrder is the ChangeOrder made, as space/slug, and err why the
	// rollout failed part way.
	changeOrder string
	err         string
}

// planRollouts groups the changes made in upstreams by base Space, and finds
// each base's ChangeWorkflow and the Triggers to annotate its rollout with.
func planRollouts(ctx context.Context, c *cubapi.Client, edits []*unitEdit, creates []*refCreate, slug, policySpace string) ([]*baseRollout, error) {
	bySpace := map[string]*baseRollout{}
	var order []string
	get := func(spaceID string) *baseRollout {
		if r := bySpace[spaceID]; r != nil {
			return r
		}
		r := &baseRollout{}
		bySpace[spaceID] = r
		order = append(order, spaceID)
		return r
	}
	slugs := map[string]bool{}
	for _, u := range edits {
		if u.inBase() {
			r := get(u.spaceID)
			r.edits = append(r.edits, u)
			for _, e := range u.edits {
				if e.Removed > 0 {
					slugs[allowAllTrigger] = true
					r.triggers = appendNew(r.triggers, allowAllTrigger)
				}
			}
		}
	}
	for _, cr := range creates {
		if cr.inBase() {
			r := get(cr.dest.spaceID.String())
			r.creates = append(r.creates, cr)
			slugs[coverageTrigger] = true
			r.triggers = appendNew(r.triggers, coverageTrigger)
		}
	}
	if len(order) == 0 {
		return nil, nil
	}
	refs, err := cub.TriggerRefs(ctx, c, policySpace, sortedKeys(slugs))
	if err != nil {
		return nil, err
	}
	var out []*baseRollout
	for _, id := range order {
		r := bySpace[id]
		spaceID, err := uuid.Parse(id)
		if err != nil {
			return nil, fmt.Errorf("space ID %q: %w", id, err)
		}
		if r.base, err = cub.PrepareRolloutBase(ctx, c, spaceID, slug); err != nil {
			return nil, err
		}
		var named []string
		for _, t := range r.triggers {
			if ref, ok := refs[t]; ok {
				named = append(named, ref)
			}
		}
		sort.Strings(named)
		r.triggers = named
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].base.SpaceSlug < out[j].base.SpaceSlug })
	return out, nil
}

func (r *baseRollout) rollout(slug string, ch cubapi.Change) cub.Rollout {
	ro := cub.Rollout{Slug: slug, Description: ch.Description}
	if len(r.triggers) > 0 {
		ro.Annotations = map[string]string{cub.TriggerAnnotation: strings.Join(r.triggers, ",")}
	}
	return ro
}

// writeRefPlans makes the changes: the ones in bases in each base's ChangeSet,
// closed and named by a ChangeOrder, and the rest directly, each Unit's edits in
// one write.
func writeRefPlans(ctx context.Context, c *cubapi.Client, edits []*unitEdit, creates []*refCreate, rollouts []*baseRollout, slug string, ch cubapi.Change) ([]*cubapi.Result, error) {
	var direct []*unitEdit
	for _, u := range edits {
		if !u.done && !u.inBase() {
			direct = append(direct, u)
		}
	}
	var directCreates []*refCreate
	for _, cr := range creates {
		if !cr.done && !cr.inBase() {
			directCreates = append(directCreates, cr)
		}
	}
	ids := make([]string, 0, len(direct))
	for _, u := range direct {
		ids = append(ids, u.unitID)
	}
	if err := cub.OpenChangeSetFirst(ctx, c, ch, ids); err != nil {
		return nil, err
	}
	results, err := writeEditsAndCreates(ctx, c, direct, directCreates, ch)
	if err != nil {
		return nil, err
	}
	for _, r := range rollouts {
		if r.base.Problem != "" {
			continue
		}
		res, err := writeRollout(ctx, c, r, slug, ch)
		results = append(results, res...)
		if err != nil {
			r.err = err.Error()
		}
	}
	return results, nil
}

// writeRollout makes a base's changes in its ChangeSet, closes it, and names the
// change with a ChangeOrder. A dry run makes the changes as a dry run, and
// creates neither.
func writeRollout(ctx context.Context, c *cubapi.Client, r *baseRollout, slug string, ch cubapi.Change) ([]*cubapi.Result, error) {
	in := ch
	in.ChangeSet = ""
	ro := r.rollout(slug, ch)
	if !ch.DryRun() {
		cs, err := cub.CreateChangeSet(ctx, c, r.base.SpaceID, ro)
		if err != nil {
			return nil, fmt.Errorf("create ChangeSet %s/%s: %w", r.base.SpaceSlug, slug, err)
		}
		in.ChangeSet = cs.ChangeSetID.String()
		ids := make([]string, 0, len(r.edits))
		for _, u := range r.edits {
			ids = append(ids, u.unitID)
		}
		if err := cub.OpenChangeSetFirst(ctx, c, in, ids); err != nil {
			return nil, err
		}
	}
	results, err := writeEditsAndCreates(ctx, c, r.edits, r.creates, in)
	if err != nil || ch.DryRun() {
		return results, err
	}
	for _, res := range results {
		for _, o := range res.Outcomes {
			if !o.Success {
				return results, fmt.Errorf("ChangeSet %s/%s is left open: %s/%s was not written: %s", r.base.SpaceSlug, slug, o.SpaceSlug, o.UnitSlug, o.Error)
			}
		}
	}
	csID, err := uuid.Parse(in.ChangeSet)
	if err != nil {
		return results, err
	}
	if err := cub.CloseChangeSet(ctx, c, csID); err != nil {
		return results, fmt.Errorf("close ChangeSet %s/%s: %w", r.base.SpaceSlug, slug, err)
	}
	if _, err := cub.CreateChangeOrder(ctx, c, r.base, csID, ro); err != nil {
		return results, fmt.Errorf("create ChangeOrder %s/%s: %w", r.base.SpaceSlug, slug, err)
	}
	r.changeOrder = r.base.SpaceSlug + "/" + slug
	return results, nil
}

// writeEditsAndCreates makes each Unit's edits in one write, and the creates.
func writeEditsAndCreates(ctx context.Context, c *cubapi.Client, edits []*unitEdit, creates []*refCreate, ch cubapi.Change) ([]*cubapi.Result, error) {
	var results []*cubapi.Result
	for _, u := range edits {
		spaceID, err := uuid.Parse(u.spaceID)
		if err != nil {
			return nil, fmt.Errorf("space ID %q: %w", u.spaceID, err)
		}
		fns := make([]api.FunctionInvocation, 0, len(u.exprs))
		for _, expr := range u.exprs {
			fns = append(fns, api.FunctionInvocation{
				FunctionName:  "set-yq",
				Arguments:     []api.FunctionArgument{{ParameterName: "yq-expression", Value: expr}},
				WhereResource: cub.WhereResourceTypes(cub.NetworkPolicyResourceType),
			})
		}
		res, err := cub.InvokeMutations(ctx, c, fns, cub.UnitSelector(spaceID, u.unit), ch)
		if err != nil {
			return nil, fmt.Errorf("edit the policies of %s/%s: %w", u.space, u.unit, err)
		}
		results = append(results, res)
	}
	if len(creates) > 0 {
		creations := make([]cub.Creation, 0, len(creates))
		for _, cr := range creates {
			creations = append(creations, policyCreation(cr.dest, cr.create.Name, cr.create.YAML()))
		}
		results = append(results, cub.EnsureUnits(ctx, c, creations, ch))
	}
	return results, nil
}

// listUnitsIn lists the Units whose field is one of values, a hundred values to
// a request.
func listUnitsIn(ctx context.Context, c *cubapi.Client, field string, values []string, sel, include string) ([]*goclientnew.ExtendedUnit, error) {
	var out []*goclientnew.ExtendedUnit
	for start := 0; start < len(values); start += 100 {
		units, err := cubapi.ListUnits(ctx, c, cubapi.Where{}.InStrings(field, values[start:min(start+100, len(values))]),
			cubapi.ListOpts{Select: sel, Include: include})
		if err != nil {
			return nil, err
		}
		for _, u := range units {
			if u.Unit != nil {
				out = append(out, u)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return unitName(out[i]) < unitName(out[j]) })
	return out, nil
}

func spaceSlugOf(u *goclientnew.ExtendedUnit) string {
	if u.Space != nil {
		return u.Space.Slug
	}
	return u.Unit.SpaceID.String()
}

func appendNew(list []string, items ...string) []string {
	for _, s := range items {
		if !containsString(list, s) {
			list = append(list, s)
		}
	}
	return list
}
