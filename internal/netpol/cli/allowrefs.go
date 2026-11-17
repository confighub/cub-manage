// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/confighub/sdk/managerkit"
	"github.com/confighub/sdk/managerkit/write"

	"github.com/confighub/cub-manage/internal/cub"
	"github.com/confighub/cub-manage/internal/kit"
	"github.com/confighub/cub-manage/internal/netpol"
	"github.com/confighub/cub-manage/internal/netpol/snapshot"
)

func newAllowFromRefsCmd() *cobra.Command {
	var output, clusterFilter, namespaceFilter, controller, rolloutSlug, policySpace string
	var refKinds []string
	var noUpstream bool
	var filter kit.FilterFlags
	var commit write.CommitFlags
	cmd := &cobra.Command{
		Use:   "allow-from-refs",
		Short: "Admit to each workload only the traffic the references to it say it gets",
		Long: `allow-from-refs reads the references between resources that say one workload
connects to another, and changes NetworkPolicies so that each destination admits
those connections and no others: no Links have to be recorded first.

It reads three kinds of reference, chosen with --refs (all by default):

  routes   An Ingress, or a Traefik IngressRoute, IngressRouteTCP or
           IngressRouteUDP, names Services, and the ingress controller serving
           it connects to the pods each Service selects, on the pod port the
           Service port's targetPort names. The controller is Traefik for its
           own routes, and for an Ingress the controller of its IngressClass
           (Traefik or ingress-nginx); its workload is the one running that
           controller's image. Name it with --ingress-controller
           <namespace>/<workload> when the images do not tell.
  config   A Service address in a workload's config is a connection from the
           workload to the Service's pods: in its containers' commands,
           arguments and env values, and in the ConfigMaps it reads -- by a
           volume, by env, or found by label by a kiwigrid k8s-sidecar, as
           Grafana's datasources are. A ConfigMap value is read as text, so an
           address in YAML or JSON inside it counts. An address is
           <service>.<namespace>.svc[.cluster.local], or <service> or
           <service>.<namespace> with a scheme or a port, as in
           http://loki:3100; the Service has to be in ConfigHub, and the port
           is the one the address names or its scheme implies (http 80, https
           443), or every port.
  scrape   A metrics scraper's Kubernetes discovery is a connection from the
           scraper to the pods it finds: a scrape_configs job with
           kubernetes_sd_configs, as Prometheus and VictoriaMetrics read it, or
           an Alloy discovery.kubernetes feeding a prometheus.scrape, in the
           scraper's config as above. Each pod, endpoint or Service the role
           finds in the job's namespaces is given the labels discovery would
           give it, and the job's keep, drop and replace rules are applied; a
           target that survives is scraped at the port its __address__ ends
           up with.

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
instead; and when every variant would get the same new policy, it is created in
the upstream's Space. --no-upstream changes the variants themselves.

The changes in each base, the upstream's Space, are made in a ChangeSet, which is
then closed, and named by a ChangeOrder adopting its end Tag, to be promoted to
the variants stage by stage with 'cub variant promote <variant> --change-order
<base>/<rollout>'. Both are named --rollout and annotated Trigger=<space>/<slug>
with the guardrail Triggers whose findings the changes fix
(netpol-no-allow-all-ingress for an edit, netpol-ingress-coverage for a new
policy). The ChangeOrder follows the ChangeWorkflow of the base's Component:
the one kept in the base, or the only one it allows. A base with none, or with
a ChangeSet or ChangeOrder of that name already, is reported and not changed.
When the upstream has the change already, nothing is written, and the report
says to promote it. --changeset applies to the changes made directly. A Unit
imported from elsewhere may need its write guarded, as in --guard
Override=External --clearance Override=External.

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
			kinds, err := parseRefKinds(refKinds)
			if err != nil {
				return err
			}
			load := snapshot.Load
			if kinds[refsConfig] || kinds[refsScrape] {
				load = snapshot.LoadWithConfigMaps
			}
			snap, err := load(cmd.Context(), client, where)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			plans := planRefs(snap, kinds, clusterFilter, namespaceFilter, controller)
			l, err := loadLineage(ctx, client, plannedUnitIDs(plans))
			if err != nil {
				return err
			}
			edits, err := planUnitEdits(ctx, client, l, plans, noUpstream)
			if err != nil {
				return err
			}
			creates, err := planCreates(ctx, client, l, snap, plans, noUpstream)
			if err != nil {
				return err
			}
			rollouts, err := planRollouts(ctx, client, edits, creates, rolloutSlug, policySpace)
			if err != nil {
				return err
			}
			results, err := writeRefPlans(ctx, client, edits, creates, rollouts, rolloutSlug, ch)
			if err != nil {
				return err
			}
			rep := newRefsReport(plans, edits, creates, rollouts, rolloutSlug, ch.DryRun())
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
	cmd.Flags().StringSliceVar(&refKinds, "refs", []string{refsRoutes, refsConfig, refsScrape}, "the references to read: routes, config and scrape, or some of them")
	cmd.Flags().StringVar(&controller, "ingress-controller", "", "the workload serving every route, as <namespace>/<workload>, when its image does not say")
	cmd.Flags().BoolVar(&noUpstream, "no-upstream", false, "change each variant's policies, even when every variant would take the same change from its upstream Unit")
	cmd.Flags().StringVar(&rolloutSlug, "rollout", "netpol-allow-from-refs", "name of the ChangeSet and ChangeOrder each base's changes are made in")
	cmd.Flags().StringVar(&policySpace, "policy-space", "", "Space of the netpol guardrail Triggers the rollouts are annotated with (default: wherever the organization has them)")
	commit.Bind(cmd)
	return cmd
}

// The kinds of reference allow-from-refs reads.
const (
	refsRoutes = "routes"
	refsConfig = "config"
	refsScrape = "scrape"
)

func parseRefKinds(kinds []string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, k := range kinds {
		if k != refsRoutes && k != refsConfig && k != refsScrape {
			return nil, fmt.Errorf("--refs takes %s, %s and %s, not %q", refsRoutes, refsConfig, refsScrape, k)
		}
		out[k] = true
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("--refs names no references to read")
	}
	return out, nil
}

// planRefs plans each cluster's policy changes from the references it reads.
func planRefs(snap *snapshot.Snapshot, kinds map[string]bool, clusterFilter, namespaceFilter, controller string) []netpol.IngressPlan {
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
		var flows []netpol.Flow
		var skipped []netpol.RefSkip
		if kinds[refsRoutes] {
			f, s := netpol.RouteFlows(c, controller)
			flows, skipped = append(flows, f...), append(skipped, s...)
		}
		if kinds[refsConfig] {
			f, s := netpol.ConfigFlows(c)
			flows, skipped = append(flows, f...), append(skipped, s...)
		}
		if kinds[refsScrape] {
			f, s := netpol.ScrapeFlows(c)
			flows, skipped = append(flows, f...), append(skipped, s...)
		}
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

// refsReport is what allow-from-refs found and did.
type refsReport struct {
	Flows    []refFlow        `json:"flows"`
	Policies []refPolicy      `json:"policies"`
	Writes   []refWrite       `json:"writes"`
	Rollouts []refRollout     `json:"rollouts,omitempty"`
	Access   []refAccess      `json:"access"`
	Blocked  []refFlow        `json:"blocked,omitempty"`
	Skipped  []netpol.RefSkip `json:"skipped,omitempty"`
	Result   *write.Report    `json:"result,omitempty"`
	dryRun   bool
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
	Action   string   `json:"action"`
	Clusters []string `json:"clusters"`
	Policies []string `json:"policies"`
	// Variants are the Spaces that take the change from the Unit, an upstream,
	// by promotion.
	Variants []string `json:"variants,omitempty"`
	// Rollout is the ChangeOrder the change is made in, as space/slug, empty
	// for a change made directly.
	Rollout string `json:"rollout,omitempty"`
	Note    string `json:"note,omitempty"`
	// Done is whether the Unit, an upstream, has the change already, and is
	// not written: its variants are yet to take it.
	Done bool `json:"done,omitempty"`
}

type refRollout struct {
	Base      string   `json:"base"`
	Component string   `json:"component,omitempty"`
	Workflow  string   `json:"workflow,omitempty"`
	Rollout   string   `json:"rollout"`
	Triggers  []string `json:"triggers,omitempty"`
	// ChangeOrder is the ChangeOrder made, as space/slug.
	ChangeOrder string `json:"changeOrder,omitempty"`
	Problem     string `json:"problem,omitempty"`
	Error       string `json:"error,omitempty"`
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

func newRefsReport(plans []netpol.IngressPlan, edits []*unitEdit, creates []*refCreate, rollouts []*baseRollout, slug string, dryRun bool) refsReport {
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
	for _, p := range plans {
		for _, cr := range p.Creates {
			rep.Policies = append(rep.Policies, refPolicy{
				Cluster: p.Cluster, Action: "create", Policy: netpol.NamespaceOf(cr.Dest.Namespace) + "/" + cr.Name,
				Unit: cr.Dest.Origin.Space + "/" + cr.Name, Added: len(cr.Rules),
			})
		}
	}
	rolloutOf := func(spaceSlug string, inBase bool) string {
		if !inBase {
			return ""
		}
		return spaceSlug + "/" + slug
	}
	for _, u := range edits {
		w := refWrite{
			Unit: u.space + "/" + u.unit, Action: "edit", Clusters: u.clusters, Variants: u.variants,
			Rollout: rolloutOf(u.space, u.inBase()), Note: u.note, Done: u.done,
		}
		for _, e := range u.edits {
			w.Policies = append(w.Policies, e.Policy.Name)
		}
		rep.Writes = append(rep.Writes, w)
	}
	for _, cr := range creates {
		rep.Writes = append(rep.Writes, refWrite{
			Unit: cr.dest.spaceSlug + "/" + cr.create.Name, Action: "create", Clusters: cr.clusters,
			Policies: []string{cr.create.Name}, Variants: cr.variants,
			Rollout: rolloutOf(cr.dest.spaceSlug, cr.inBase()), Note: cr.note, Done: cr.done,
		})
	}
	for _, r := range rollouts {
		rep.Rollouts = append(rep.Rollouts, refRollout{
			Base: r.base.SpaceSlug, Component: r.base.Component, Workflow: r.base.Workflow, Rollout: slug,
			Triggers: r.triggers, ChangeOrder: r.changeOrder, Problem: r.base.Problem, Error: r.err,
		})
	}
	rep.dryRun = dryRun
	return rep
}

// plannedUnitIDs are the Units a plan edits, and those of the workloads it
// creates policies for.
func plannedUnitIDs(plans []netpol.IngressPlan) []string {
	var ids []string
	for _, p := range plans {
		for _, e := range p.Edits {
			ids = appendNew(ids, e.Policy.Origin.UnitID)
		}
		for _, cr := range p.Creates {
			ids = appendNew(ids, cr.Dest.Origin.UnitID)
		}
	}
	sort.Strings(ids)
	return ids
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
		section("Units the changes are made in:")
		fmt.Fprintln(tw, "UNIT\tACTION\tPOLICIES\tFOR\tIN")
		for _, w := range rep.Writes {
			forWhat := strings.Join(w.Clusters, ", ")
			if len(w.Variants) > 0 {
				forWhat = "variants " + strings.Join(w.Variants, ", ")
			}
			in := "directly"
			switch {
			case w.Done:
				in = "made already: promote it"
			case w.Rollout != "":
				in = "ChangeSet and ChangeOrder " + w.Rollout
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", w.Unit, w.Action, strings.Join(w.Policies, ", "), forWhat, in)
		}
		flush()
		for _, w := range rep.Writes {
			if w.Note != "" {
				kit.Fprintln(out, fmt.Sprintf("  %s is changed itself, not its upstream: %s", w.Unit, w.Note))
			}
		}
	}

	if len(rep.Rollouts) > 0 {
		section("Rollouts from bases:")
		fmt.Fprintln(tw, "BASE\tCOMPONENT\tCHANGEWORKFLOW\tTRIGGER\tSTATUS")
		for _, r := range rep.Rollouts {
			status := "ChangeOrder " + r.ChangeOrder + " created"
			switch {
			case r.Problem != "":
				status = "not changed: " + r.Problem
			case r.Error != "":
				status = "failed: " + r.Error
			case rep.dryRun:
				status = "would make ChangeSet and ChangeOrder " + r.Base + "/" + r.Rollout
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.Base, kit.Dash(r.Component), kit.Dash(r.Workflow), kit.Dash(strings.Join(r.Triggers, ",")), status)
		}
		flush()
		if !rep.dryRun {
			for _, r := range rep.Rollouts {
				if r.ChangeOrder != "" {
					kit.Fprintln(out, fmt.Sprintf("  promote it stage by stage: cub variant promote <variant> --change-order %s", r.ChangeOrder))
				}
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
