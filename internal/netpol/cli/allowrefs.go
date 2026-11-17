// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"

	"github.com/confighub/sdk/core/cubapi"
	api "github.com/confighub/sdk/core/function/api"
	goclientnew "github.com/confighub/sdk/core/openapi/goclient-new"
	"github.com/confighub/sdk/managerkit"
	"github.com/confighub/sdk/managerkit/write"

	"github.com/confighub/cub-manage/internal/cub"
	"github.com/confighub/cub-manage/internal/kit"
	"github.com/confighub/cub-manage/internal/netpol"
	"github.com/confighub/cub-manage/internal/netpol/snapshot"
)

func newAllowFromRefsCmd() *cobra.Command {
	var output, clusterFilter, namespaceFilter, controller string
	var noUpstream bool
	var filter kit.FilterFlags
	var commit write.CommitFlags
	cmd := &cobra.Command{
		Use:   "allow-from-refs",
		Short: "Admit to each workload only the traffic the references to it say it gets",
		Long: `allow-from-refs reads the references between resources that say one workload
connects to another, and changes NetworkPolicies so that each destination admits
those connections and no others: no Links have to be recorded first.

The references it reads are routes. An Ingress, or a Traefik IngressRoute,
IngressRouteTCP or IngressRouteUDP, names Services, and the ingress controller
serving it connects to the pods each Service selects, on the pod port the
Service port's targetPort names. The controller is Traefik for its own routes,
and for an Ingress the controller of its IngressClass (Traefik or ingress-nginx);
its workload is the one running that controller's image. Name it with
--ingress-controller <namespace>/<workload> when the images do not tell.

Policies add up, so a rule admitting a few sources does nothing beside a rule
admitting every source. For each destination, allow-from-refs:

  - takes the rules admitting every source (no 'from' peers) out of the
    policies isolating its ingress, and adds a rule admitting the controller on
    the route's ports to the first of them. Rules naming sources are kept.
  - creates a policy, allow-<workload>-ingress in the workload's Space, when no
    policy isolates its ingress, or none can be edited.
  - leaves a policy alone that also selects a workload whose routes differ,
    since the edit would apply to that workload too, and reports the
    destination as skipped when that policy admits every source.

Sources are selected by their namespace's name and their own pod selector, which
are the same in every cluster a workload is deployed to. So when every variant
of an upstream Unit -- each Unit cloned from it -- would take the same edit, and
the upstream's policies are the same as theirs, the edit is made to the upstream
instead, for the variants to take by an upgrade; when the upstream has the edit
already, nothing is written, and the report says the variants are to be
upgraded. --no-upstream edits the variants themselves. A Unit imported from elsewhere may need its write guarded,
as in --guard Override=External --clearance Override=External.

The report lists the connections found, the policy changes, who gains and loses
access to each destination as the model of the policies sees it (ports aside;
traffic from the pod's own node, such as kubelet probes, is always allowed), the
connections the source's own egress policies still block, and the references
that produced none, with why. A client no resource names -- one reaching a pod
by its IP, or pods made outside ConfigHub -- loses access, so read the list of
losses before committing.

This is a dry run unless you pass --commit --change-desc "…".`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ch, err := commit.Change("admit to each workload the traffic its routes send it")
			if err != nil {
				return err
			}
			if controller != "" && !strings.Contains(controller, "/") {
				return fmt.Errorf("--ingress-controller is <namespace>/<workload>, not %q", controller)
			}
			client, err := cub.Preflight(cmd.Context())
			if err != nil {
				return err
			}
			where, err := filter.Predicate()
			if err != nil {
				return err
			}
			snap, err := snapshot.Load(cmd.Context(), client, where)
			if err != nil {
				return err
			}
			plans := planRefs(snap, clusterFilter, namespaceFilter, controller)
			edits, err := planUnitEdits(cmd.Context(), client, plans, noUpstream)
			if err != nil {
				return err
			}
			creates, err := planCreates(snap, plans)
			if err != nil {
				return err
			}
			results, err := writeRefPlans(cmd.Context(), client, edits, creates, ch)
			if err != nil {
				return err
			}
			rep := newRefsReport(plans, edits, creates)
			if output != managerkit.OutputTable {
				result := write.Summarize("allow-from-refs", "", ch.DryRun(), results...)
				rep.Result = &result
				if done, err := managerkit.Render(cmd.OutOrStdout(), output, rep); done || err != nil {
					return err
				}
			}
			printRefsReport(cmd.OutOrStdout(), rep)
			return write.ReportMutations(cmd, "allow-from-refs", "", ch.DryRun(), managerkit.OutputTable, results...)
		},
	}
	kit.AddOutputFlag(cmd, &output)
	kit.AddFilterFlags(cmd, &filter)
	kit.AddClusterFlag(cmd, &clusterFilter)
	cmd.Flags().StringVar(&namespaceFilter, "namespace", "", "only destinations in this namespace")
	cmd.Flags().StringVar(&controller, "ingress-controller", "", "the workload serving every route, as <namespace>/<workload>, when its image does not say")
	cmd.Flags().BoolVar(&noUpstream, "no-upstream", false, "edit each variant's policies, even when every variant would take the same edit from its upstream Unit")
	commit.Bind(cmd)
	return cmd
}

// planRefs plans each cluster's policy changes from its routes.
func planRefs(snap *snapshot.Snapshot, clusterFilter, namespaceFilter, controller string) []netpol.IngressPlan {
	names := make([]string, 0, len(snap.Clusters))
	for name := range snap.Clusters {
		if clusterFilter == "" || name == clusterFilter {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	var plans []netpol.IngressPlan
	for _, name := range names {
		c := snap.Clusters[name]
		flows, skipped := netpol.RouteFlows(c, controller)
		if namespaceFilter != "" {
			var kept []netpol.Flow
			for _, f := range flows {
				if netpol.NamespaceOf(f.Dest.Namespace) == namespaceFilter {
					kept = append(kept, f)
				}
			}
			flows = kept
		}
		p := netpol.PlanIngress(c, flows)
		for _, s := range skipped {
			if namespaceFilter == "" || s.Namespace == namespaceFilter {
				p.Skipped = append(p.Skipped, s)
			}
		}
		if len(p.Flows) > 0 || len(p.Skipped) > 0 {
			plans = append(plans, p)
		}
	}
	return plans
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
	// by an upgrade rather than being edited.
	variants []string
	// note says why a variant is edited rather than its upstream.
	note string
	// done is whether the upstream has the edit already, and only its variants
	// lack it, until they are upgraded.
	done bool
}

func (u *unitEdit) key() string { return strings.Join(u.exprs, "\n") }

// planUnitEdits groups the policy edits by the Unit holding the policy, and
// moves the edits of the variants of an upstream Unit to the upstream when they
// all take the same one.
func planUnitEdits(ctx context.Context, c *cubapi.Client, plans []netpol.IngressPlan, noUpstream bool) ([]*unitEdit, error) {
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
	if !noUpstream && len(byUnit) > 0 {
		if err := moveToUpstreams(ctx, c, byUnit); err != nil {
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

// moveToUpstreams replaces the edits of an upstream Unit's variants with one
// edit to the upstream, when every variant takes the same edit and the
// upstream's policies are the same as theirs, so the edit means the same there.
func moveToUpstreams(ctx context.Context, c *cubapi.Client, byUnit map[string]*unitEdit) error {
	ids := make([]string, 0, len(byUnit))
	for id := range byUnit {
		ids = append(ids, id)
	}
	edited, err := listUnitsIn(ctx, c, "UnitID", ids, "UnitID,SpaceID,Slug,UpstreamUnitID", "")
	if err != nil {
		return err
	}
	var upstreamIDs []string
	for _, u := range edited {
		if u.Unit.UpstreamUnitID != nil && !containsString(upstreamIDs, u.Unit.UpstreamUnitID.String()) {
			upstreamIDs = append(upstreamIDs, u.Unit.UpstreamUnitID.String())
		}
	}
	if len(upstreamIDs) == 0 {
		return nil
	}
	sort.Strings(upstreamIDs)
	upstreams, err := listUnitsIn(ctx, c, "UnitID", upstreamIDs, "UnitID,SpaceID,Slug", "SpaceID")
	if err != nil {
		return err
	}
	variants, err := listUnitsIn(ctx, c, "UpstreamUnitID", upstreamIDs, "UnitID,SpaceID,Slug,UpstreamUnitID", "SpaceID")
	if err != nil {
		return err
	}
	variantsOf := map[string][]*goclientnew.ExtendedUnit{}
	for _, v := range variants {
		up := v.Unit.UpstreamUnitID.String()
		variantsOf[up] = append(variantsOf[up], v)
	}
	for _, up := range upstreams {
		upID := up.Unit.UnitID.String()
		vs := variantsOf[upID]
		if len(vs) == 0 || byUnit[upID] != nil {
			// An upstream with policy edits of its own is deployed too; its
			// variants are edited themselves.
			continue
		}
		upName := spaceSlugOf(up) + "/" + up.Unit.Slug
		note := sameEditNote(byUnit, vs, upName)
		done := false
		if note == "" {
			first := byUnit[vs[0].Unit.UnitID.String()]
			if done, note, err = upstreamState(ctx, c, up, first); err != nil {
				return err
			}
		}
		if note != "" {
			for _, v := range vs {
				if u := byUnit[v.Unit.UnitID.String()]; u != nil {
					u.note = note
				}
			}
			continue
		}
		first := byUnit[vs[0].Unit.UnitID.String()]
		moved := &unitEdit{
			spaceID: up.Unit.SpaceID.String(), space: spaceSlugOf(up), unitID: upID, unit: up.Unit.Slug,
			edits: first.edits, exprs: first.exprs, done: done,
		}
		for _, v := range vs {
			u := byUnit[v.Unit.UnitID.String()]
			moved.variants = append(moved.variants, u.space+"/"+u.unit)
			for _, cl := range u.clusters {
				if !containsString(moved.clusters, cl) {
					moved.clusters = append(moved.clusters, cl)
				}
			}
			delete(byUnit, v.Unit.UnitID.String())
		}
		sort.Strings(moved.variants)
		byUnit[upID] = moved
	}
	return nil
}

// sameEditNote says why the variants of an upstream do not all take the same
// edit, empty when they do.
func sameEditNote(byUnit map[string]*unitEdit, variants []*goclientnew.ExtendedUnit, upName string) string {
	var first *unitEdit
	for _, v := range variants {
		u := byUnit[v.Unit.UnitID.String()]
		name := spaceSlugOf(v) + "/" + v.Unit.Slug
		if u == nil {
			return fmt.Sprintf("%s, another variant of %s, takes no edit: it is out of scope, or its policies or routes differ", name, upName)
		}
		if first == nil {
			first = u
			continue
		}
		if u.key() != first.key() {
			return fmt.Sprintf("variants of %s take different edits: %s/%s and %s", upName, first.space, first.unit, name)
		}
	}
	return ""
}

// upstreamState compares the upstream's policies with a variant's. The edit
// means the same in the upstream when each of its policies has the variant's
// ingress rules; done is whether each has the rules the edit leaves instead, so
// that the variants take it by an upgrade with no edit. Otherwise note says why
// -- a policy the upstream lacks, or whose rules are neither.
func upstreamState(ctx context.Context, c *cubapi.Client, up *goclientnew.ExtendedUnit, variant *unitEdit) (done bool, note string, err error) {
	ref := cub.UnitRef{SpaceID: up.Unit.SpaceID, SpaceSlug: spaceSlugOf(up), UnitID: up.Unit.UnitID, UnitSlug: up.Unit.Slug}
	data, _, err := cub.GetUnitData(ctx, c, ref)
	if err != nil {
		return false, "", err
	}
	var resources []netpol.FleetResource
	for _, doc := range cub.SplitDocuments(data) {
		var m map[string]any
		if yaml.Unmarshal([]byte(doc), &m) == nil && m != nil {
			resources = append(resources, netpol.FleetResource{Doc: m})
		}
	}
	policies := map[string]*netpol.NetworkPolicyEntity{}
	for _, cl := range netpol.BuildFleet(resources) {
		for _, p := range cl.NetworkPolicies {
			policies[p.Name] = p
		}
	}
	done, note = compareUpstream(ref.SpaceSlug+"/"+ref.UnitSlug, policies, variant.edits)
	return done, note, nil
}

// compareUpstream is upstreamState's comparison of the upstream's policies,
// by name, with a variant's edits.
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
	sort.Slice(out, func(i, j int) bool {
		return spaceSlugOf(out[i])+"/"+out[i].Unit.Slug < spaceSlugOf(out[j])+"/"+out[j].Unit.Slug
	})
	return out, nil
}

func spaceSlugOf(u *goclientnew.ExtendedUnit) string {
	if u.Space != nil {
		return u.Space.Slug
	}
	return u.Unit.SpaceID.String()
}

// refCreate is a new policy Unit, in the Space of the workload it selects.
type refCreate struct {
	cluster string
	create  *netpol.PolicyCreate
	dest    createDest
}

func planCreates(snap *snapshot.Snapshot, plans []netpol.IngressPlan) ([]refCreate, error) {
	var out []refCreate
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
			out = append(out, refCreate{cluster: p.Cluster, create: cr, dest: dest})
		}
	}
	return out, nil
}

// writeRefPlans makes the edits, each Unit's in one write, and the creates.
func writeRefPlans(ctx context.Context, c *cubapi.Client, edits []*unitEdit, creates []refCreate, ch cubapi.Change) ([]*cubapi.Result, error) {
	ids := make([]string, 0, len(edits))
	for _, u := range edits {
		if !u.done {
			ids = append(ids, u.unitID)
		}
	}
	if err := cub.OpenChangeSetFirst(ctx, c, ch, ids); err != nil {
		return nil, err
	}
	var results []*cubapi.Result
	for _, u := range edits {
		if u.done {
			continue
		}
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

// refsReport is what allow-from-refs found and did.
type refsReport struct {
	Flows    []refFlow        `json:"flows"`
	Policies []refPolicy      `json:"policies"`
	Writes   []refWrite       `json:"writes"`
	Access   []refAccess      `json:"access"`
	Blocked  []refFlow        `json:"blocked,omitempty"`
	Skipped  []netpol.RefSkip `json:"skipped,omitempty"`
	Result   *write.Report    `json:"result,omitempty"`
}

type refFlow struct {
	Cluster     string   `json:"cluster"`
	Via         string   `json:"via"`
	Source      string   `json:"source"`
	Destination string   `json:"destination"`
	Ports       []string `json:"ports"`
}

type refPolicy struct {
	Cluster string `json:"cluster"`
	Action  string `json:"action"`
	Policy  string `json:"policy"`
	Unit    string `json:"unit"`
	// Removed is how many rules admitting every source an edit takes out.
	Removed int `json:"removed,omitempty"`
	// Added is how many rules admitting the flows it adds.
	Added int `json:"added"`
}

type refWrite struct {
	Unit     string   `json:"unit"`
	Clusters []string `json:"clusters"`
	Policies []string `json:"policies"`
	Variants []string `json:"variants,omitempty"`
	Note     string   `json:"note,omitempty"`
	// Done is whether the Unit, an upstream, has the edit already, and is not
	// written: its variants take it by an upgrade.
	Done bool `json:"done,omitempty"`
}

type refAccess struct {
	Cluster     string   `json:"cluster"`
	Destination string   `json:"destination"`
	Gains       []string `json:"gains,omitempty"`
	Loses       []string `json:"loses,omitempty"`
	// WasOpen is whether every other workload in the cluster could reach the
	// destination before.
	WasOpen bool `json:"wasOpen"`
}

func newRefsReport(plans []netpol.IngressPlan, edits []*unitEdit, creates []refCreate) refsReport {
	rep := refsReport{Flows: []refFlow{}, Policies: []refPolicy{}, Writes: []refWrite{}, Access: []refAccess{}}
	for _, p := range plans {
		for _, f := range p.Flows {
			rep.Flows = append(rep.Flows, newRefFlow(p.Cluster, f))
		}
		for _, f := range p.Blocked {
			rep.Blocked = append(rep.Blocked, newRefFlow(p.Cluster, f))
		}
		for _, e := range p.Edits {
			o := e.Policy.Origin
			rep.Policies = append(rep.Policies, refPolicy{
				Cluster: p.Cluster, Action: "edit", Policy: netpol.NamespaceOf(e.Policy.Namespace) + "/" + e.Policy.Name,
				Unit: o.Space + "/" + o.UnitSlug, Removed: e.Removed, Added: len(e.Added),
			})
		}
		for _, a := range p.Access {
			rep.Access = append(rep.Access, refAccess{
				Cluster: p.Cluster, Destination: netpol.WorkloadName(a.Dest),
				Gains: workloadNames(a.Gains), Loses: workloadNames(a.Loses), WasOpen: a.WasOpen,
			})
		}
		rep.Skipped = append(rep.Skipped, p.Skipped...)
	}
	for _, cr := range creates {
		rep.Policies = append(rep.Policies, refPolicy{
			Cluster: cr.cluster, Action: "create", Policy: netpol.NamespaceOf(cr.create.Dest.Namespace) + "/" + cr.create.Name,
			Unit: cr.dest.spaceSlug + "/" + cr.create.Name, Added: len(cr.create.Rules),
		})
	}
	for _, u := range edits {
		w := refWrite{Unit: u.space + "/" + u.unit, Clusters: u.clusters, Variants: u.variants, Note: u.note, Done: u.done}
		for _, e := range u.edits {
			w.Policies = append(w.Policies, e.Policy.Name)
		}
		rep.Writes = append(rep.Writes, w)
	}
	return rep
}

func newRefFlow(cluster string, f netpol.Flow) refFlow {
	ports := make([]string, 0, len(f.Ports))
	for _, p := range f.Ports {
		ports = append(ports, p.String())
	}
	return refFlow{Cluster: cluster, Via: f.Via, Source: netpol.WorkloadName(f.Source), Destination: netpol.WorkloadName(f.Dest), Ports: ports}
}

func workloadNames(ws []*netpol.WorkloadEntity) []string {
	out := make([]string, 0, len(ws))
	for _, w := range ws {
		out = append(out, netpol.WorkloadName(w))
	}
	return out
}

// maxListed is how many workloads a table names before counting the rest.
const maxListed = 6

func printRefsReport(out io.Writer, rep refsReport) {
	section := func(title string) { kit.Fprintln(out, "\n"+title) }
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	flush := func() { _ = tw.Flush() }

	section("Connections the references say are made:")
	if len(rep.Flows) == 0 {
		kit.Fprintln(out, "  none")
	} else {
		fmt.Fprintln(tw, "CLUSTER\tVIA\tSOURCE\tDESTINATION\tPORTS")
		for _, f := range rep.Flows {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", f.Cluster, f.Via, f.Source, f.Destination, kit.Dash(strings.Join(f.Ports, ", ")))
		}
		flush()
	}

	if len(rep.Policies) > 0 {
		section("Policy changes:")
		fmt.Fprintln(tw, "CLUSTER\tACTION\tPOLICY\tUNIT\tRULES REMOVED\tRULES ADDED")
		for _, p := range rep.Policies {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%d\n", p.Cluster, p.Action, p.Policy, p.Unit, p.Removed, p.Added)
		}
		flush()
	}

	if len(rep.Writes) > 0 {
		section("Units the edits are made in:")
		fmt.Fprintln(tw, "UNIT\tPOLICIES\tFOR")
		for _, w := range rep.Writes {
			forWhat := strings.Join(w.Clusters, ", ")
			switch {
			case w.Done:
				forWhat = "has the edit already: upgrade variants " + strings.Join(w.Variants, ", ")
			case len(w.Variants) > 0:
				forWhat = "variants " + strings.Join(w.Variants, ", ") + ", by an upgrade"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\n", w.Unit, strings.Join(w.Policies, ", "), forWhat)
		}
		flush()
		for _, w := range rep.Writes {
			if w.Note != "" {
				kit.Fprintln(out, fmt.Sprintf("  %s is edited itself: %s", w.Unit, w.Note))
			}
		}
	}

	if len(rep.Access) > 0 {
		section("Access changes (ports aside):")
		for _, a := range rep.Access {
			kit.Fprintln(out, fmt.Sprintf("  %s %s", a.Cluster, a.Destination))
			if len(a.Gains) > 0 {
				kit.Fprintln(out, "    gains: "+listed(a.Gains))
			}
			switch {
			case a.WasOpen && len(a.Loses) > 0:
				kit.Fprintln(out, fmt.Sprintf("    was open to every workload; %d others lose access (-o json lists them)", len(a.Loses)))
			case len(a.Loses) > 0:
				kit.Fprintln(out, "    loses: "+listed(a.Loses))
			}
		}
	}

	if len(rep.Blocked) > 0 {
		section("Connections the source's egress policies still block:")
		fmt.Fprintln(tw, "CLUSTER\tSOURCE\tDESTINATION\tPORTS")
		for _, f := range rep.Blocked {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", f.Cluster, f.Source, f.Destination, kit.Dash(strings.Join(f.Ports, ", ")))
		}
		flush()
	}

	if len(rep.Skipped) > 0 {
		section("References that admit nothing:")
		fmt.Fprintln(tw, "CLUSTER\tREFERENCE\tWHY")
		for _, s := range rep.Skipped {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", s.Cluster, s.Via, s.Reason)
		}
		flush()
	}
	kit.Fprintln(out, "")
}

func listed(names []string) string {
	if len(names) <= maxListed {
		return fmt.Sprintf("%d (%s)", len(names), strings.Join(names, ", "))
	}
	return fmt.Sprintf("%d (%s, and %d more)", len(names), strings.Join(names[:maxListed], ", "), len(names)-maxListed)
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
