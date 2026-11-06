// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/confighub/sdk/managerkit"

	"github.com/confighub/cub-manage/internal/cub"
	"github.com/confighub/cub-manage/internal/kit"
	"github.com/confighub/cub-manage/internal/scheduling"
	"github.com/confighub/cub-manage/internal/scheduling/snapshot"
)

type clusterSpreadRow struct {
	Cluster      string   `json:"cluster"`
	Distribution string   `json:"distribution,omitempty"`
	Zones        []string `json:"zones,omitempty"`
	// DefaultSpread is the cluster's default constraints, in the
	// topologyKey=maxSkew:whenUnsatisfiable form; empty when defaulting is off.
	DefaultSpread []string `json:"defaultSpread"`
	DefaultSource string   `json:"defaultSource"`
	Error         string   `json:"error,omitempty"`
}

type workloadSpreadRow struct {
	Cluster   string `json:"cluster"`
	Namespace string `json:"namespace,omitempty"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Space     string `json:"space"`
	UnitSlug  string `json:"unitSlug"`
	Replicas  *int   `json:"replicas,omitempty"`
	// Spread is the constraints the scheduler applies, and SpreadSource where
	// they come from: explicit | cluster-default | none.
	Spread          []string `json:"spread,omitempty"`
	SpreadSource    string   `json:"spreadSource"`
	PodAntiAffinity []string `json:"podAntiAffinity,omitempty"`
}

type spreadReport struct {
	Clusters  []clusterSpreadRow  `json:"clusters"`
	Workloads []workloadSpreadRow `json:"workloads"`
	Totals    struct {
		Workloads      int `json:"workloads"`
		Explicit       int `json:"explicit"`
		ClusterDefault int `json:"clusterDefault"`
		None           int `json:"none"`
	} `json:"totals"`
	Filter string `json:"filter,omitempty"`
}

func newSpreadCmd() *cobra.Command {
	var output string
	var filter kit.FilterFlags
	var clusterFilter, namespaceFilter string
	var issuesOnly bool
	cmd := &cobra.Command{
		Use:   "spread",
		Short: "How each workload's replicas spread: its own constraints, or its cluster's defaults",
		Long: `spread reports, per cluster, the default topology spread constraints its
scheduler gives a Pod that has none of its own, and, per workload, the
constraints that apply to it and where they come from:

  explicit         the workload's own topologySpreadConstraints
  cluster-default  none of its own, so the cluster's defaults apply (to the Pods
                   of a Deployment, ReplicaSet or StatefulSet)
  none             no constraints apply

along with its pod anti-affinity terms (topologyKey:required|preferred).

A cluster's defaults are its kube-scheduler configuration, which cannot be read
from a cluster. They come from the Target's ` + scheduling.FactDefaultTopologySpread + ` fact
when an operator has recorded one ("builtin", "none", or
topologyKey=maxSkew:whenUnsatisfiable,...); otherwise Kubernetes' built-in defaults
are assumed (` + strings.Join(scheduling.FormatSpread(scheduling.BuiltinDefaultSpread()), ", ") + `), which is what the
managed services (EKS, GKE, AKS) run. defaultSource says which:
target-fact | managed-distribution | assumed. Record a cluster's defaults with:

  cub target update --space <space> --patch <target> --fact ` + scheduling.FactDefaultTopologySpread + `=topology.kubernetes.io/zone=1:ScheduleAnyway

The built-in defaults are soft and loose: a zone may hold five more replicas than
another. Give a workload that must survive a zone loss its own constraint with
'set-topology-spread'.

Filter with --cluster / --namespace; --issues-only shows the workloads with no
constraints of their own.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
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
			report := buildSpreadReport(snap, clusterFilter, namespaceFilter, issuesOnly)
			if done, err := managerkit.Render(cmd.OutOrStdout(), output, report); done || err != nil {
				return err
			}
			printSpreadTables(cmd, report)
			return nil
		},
	}
	kit.AddOutputFlag(cmd, &output)
	kit.AddFilterFlags(cmd, &filter)
	kit.AddClusterFlag(cmd, &clusterFilter)
	kit.AddNamespaceFlag(cmd, &namespaceFilter)
	kit.AddIssuesOnlyFlag(cmd, &issuesOnly, "workloads with no spread constraints of their own")
	return cmd
}

// antiAffinityTerms names each distinct topologyKey:strength the workload's
// pod anti-affinity has; terms that differ only in selector or weight are one.
func antiAffinityTerms(w *scheduling.WorkloadPlacement) []string {
	var out []string
	seen := map[string]bool{}
	for _, t := range w.PodAntiAffinity {
		strength := "preferred"
		if t.Required {
			strength = "required"
		}
		term := t.TopologyKey + ":" + strength
		if !seen[term] {
			seen[term] = true
			out = append(out, term)
		}
	}
	return out
}

func buildSpreadReport(snap *snapshot.Snapshot, clusterFilter, namespaceFilter string, issuesOnly bool) spreadReport {
	report := spreadReport{Clusters: []clusterSpreadRow{}, Workloads: []workloadSpreadRow{}, Filter: snap.Filter}
	for name, c := range snap.Clusters {
		if clusterFilter != "" && name != clusterFilter {
			continue
		}
		facts := c.FactsOrUnknown()
		report.Clusters = append(report.Clusters, clusterSpreadRow{
			Cluster:       name,
			Distribution:  facts.Distribution,
			Zones:         facts.Zones,
			DefaultSpread: scheduling.FormatSpread(facts.DefaultSpread.Constraints),
			DefaultSource: facts.DefaultSpread.Source,
			Error:         facts.DefaultSpread.Error,
		})
		for _, w := range c.Workloads {
			if namespaceFilter != "" && w.Namespace != namespaceFilter {
				continue
			}
			constraints, source := scheduling.EffectiveSpread(w, facts)
			report.Totals.Workloads++
			switch source {
			case scheduling.SpreadExplicit:
				report.Totals.Explicit++
			case scheduling.SpreadClusterDefault:
				report.Totals.ClusterDefault++
			default:
				report.Totals.None++
			}
			if issuesOnly && source == scheduling.SpreadExplicit {
				continue
			}
			report.Workloads = append(report.Workloads, workloadSpreadRow{
				Cluster: w.Origin.Cluster, Namespace: w.Namespace, Kind: w.Kind, Name: w.Name,
				Space: w.Origin.Space, UnitSlug: w.Origin.UnitSlug, Replicas: w.Replicas,
				Spread: scheduling.FormatSpread(constraints), SpreadSource: source,
				PodAntiAffinity: antiAffinityTerms(w),
			})
		}
	}
	sort.Slice(report.Clusters, func(i, j int) bool { return report.Clusters[i].Cluster < report.Clusters[j].Cluster })
	sort.Slice(report.Workloads, func(i, j int) bool {
		a, b := report.Workloads[i], report.Workloads[j]
		if a.Cluster != b.Cluster {
			return a.Cluster < b.Cluster
		}
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Name < b.Name
	})
	return report
}

func printSpreadTables(cmd *cobra.Command, r spreadReport) {
	out := cmd.OutOrStdout()
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "CLUSTER\tDISTRIBUTION\tZONES\tDEFAULT-SPREAD\tSOURCE")
	for _, c := range r.Clusters {
		def := dashList(c.DefaultSpread)
		if len(c.DefaultSpread) == 0 {
			def = "none"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", c.Cluster, kit.Dash(c.Distribution), dashList(c.Zones), def, c.DefaultSource)
	}
	_ = tw.Flush()
	for _, c := range r.Clusters {
		if c.Error != "" {
			kit.Fprintln(out, fmt.Sprintf("%s: %s (assuming the built-in defaults)", c.Cluster, c.Error))
		}
	}
	kit.Fprintln(out, "")
	tw = tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "CLUSTER\tNAMESPACE\tKIND\tNAME\tREPLICAS\tSPREAD\tANTI-AFFINITY")
	for _, w := range r.Workloads {
		replicas := "-"
		if w.Replicas != nil {
			replicas = fmt.Sprintf("%d", *w.Replicas)
		}
		// The cluster table shows the defaults, so a row names only its source.
		spread := strings.Join(w.Spread, ",")
		switch w.SpreadSource {
		case scheduling.SpreadClusterDefault:
			spread = "cluster default"
		case scheduling.SpreadNone:
			spread = "none"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			w.Cluster, kit.Dash(w.Namespace), w.Kind, w.Name, replicas, spread, dashList(w.PodAntiAffinity))
	}
	_ = tw.Flush()
	kit.Fprintln(out, fmt.Sprintf("\n%d workloads (%d explicit, %d cluster default, %d none)",
		r.Totals.Workloads, r.Totals.Explicit, r.Totals.ClusterDefault, r.Totals.None))
}
