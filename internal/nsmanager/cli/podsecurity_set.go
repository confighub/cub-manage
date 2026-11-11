// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"context"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/confighub/sdk/core/cubapi"
	api "github.com/confighub/sdk/core/function/api"
	"github.com/confighub/sdk/managerkit"
	"github.com/confighub/sdk/managerkit/write"

	"github.com/confighub/cub-manage/internal/cub"
	"github.com/confighub/cub-manage/internal/kit"
	"github.com/confighub/cub-manage/internal/nsmanager"
)

type podSecuritySetReport struct {
	Level      string                       `json:"level"`
	Version    string                       `json:"version"`
	Namespaces []nsmanager.PodSecurityApply `json:"namespaces"`
	// Edits are the labels each Unit gets.
	Edits  []nsmanager.UnitLabelEdits `json:"edits"`
	Result write.Report               `json:"result"`
}

func newPodSecuritySetCmd() *cobra.Command {
	var output, clusterFilter, namespaceFilter, level, version string
	var bases bool
	var filter kit.FilterFlags
	var commit write.CommitFlags
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Set each namespace's Pod Security Standard to the most restrictive level its workloads meet",
		Long: `set labels each selected namespace with the most restrictive Pod Security
Standard level all of its workloads meet -- the ACHIEVABLE column of
'cub manage namespace pod-security' -- or with --level, a level you choose:

  pod-security.kubernetes.io/enforce          the level
  pod-security.kubernetes.io/enforce-version  --version (default latest)
  pod-security.kubernetes.io/warn             restricted, below restricted, unless a warn level is set

The Namespace label is what admission enforces. The enforce labels also go on
each of the namespace's workloads, wherever its Unit is, so that the
namespace-pod-security-standard guardrail ('cub manage namespace guardrails install')
can hold each workload to its namespace's level in ConfigHub, before it reaches
a cluster. Labeling a workload does not touch its pod template, so nothing
restarts.

A typical loop: 'pod-security' to see each namespace's achievable level and
what blocks the next one; 'cub manage workload harden' (or other fixes) on the
blockers; 'pod-security' again to confirm; then 'pod-security set'.

set never lowers a namespace's level. A Namespace that already
enforces the level, or a stricter one, keeps its labels, version included
("keep"), and its workloads get copies -- so where a stricter label is already
in place, the guardrail flags the workloads it rejects. A namespace is left
alone, with the reason, when the cluster owns it (kube-system, default, ...),
when some of its workloads could not be checked, when it has no workloads in
ConfigHub and no --level was given, or when its workloads do not meet --level.

The namespaces are selected as every command selects what it reports: the fleet
scope (--where, --component and the label shorthands) picks the Namespace Units,
and --cluster and --namespace narrow them further. A namespace's level is judged
from all of its workloads, wherever their Units are, so the scope selects which
namespaces are labeled, not which workloads count. With --bases it labels base
Spaces instead -- Spaces with no release Target, which deploy nowhere -- each
judged by its own workloads (and listed under its slug as the cluster); the
variants made from a base pick the labels up when they are upgraded from it.

Dry-run unless --commit --change-desc; the dry run shows each Unit's diff.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			switch level {
			case "", nsmanager.LevelPrivileged, nsmanager.LevelBaseline, nsmanager.LevelRestricted:
			default:
				return fmt.Errorf("--level must be privileged, baseline or restricted")
			}
			if version == "" {
				version = "latest"
			}
			ch, err := commit.Change("apply the Pod Security Standard level their workloads meet to the selected namespaces")
			if err != nil {
				return err
			}
			dryRun := ch.DryRun()
			client, err := cub.Preflight(cmd.Context())
			if err != nil {
				return err
			}
			where, err := filter.Predicate()
			if err != nil {
				return err
			}
			inScope, err := unitsInScope(cmd.Context(), client, where)
			if err != nil {
				return err
			}
			// A base is judged by its own workloads, so the scope can narrow what
			// is read; a deployed namespace is judged by all of them.
			readScope := ""
			if bases {
				readScope = where
			}
			fleet, err := loadPodSecurity(cmd.Context(), client, readScope, version, bases)
			if err != nil {
				return err
			}
			selected := func(n *nsmanager.NamespaceEntity) bool {
				return (inScope == nil || inScope[n.Origin.UnitID]) &&
					(clusterFilter == "" || n.Origin.Cluster == clusterFilter) &&
					(namespaceFilter == "" || n.Name == namespaceFilter)
			}
			plans, edits := nsmanager.PlanPodSecurity(fleet.clusters, fleet.postures, level, version, selected)

			var results []*cubapi.Result
			for _, u := range edits {
				sel := cubapi.Selector{Where: cubapi.Where{}.Eq("UnitID", u.UnitID).String()}
				res, err := cub.InvokeMutations(cmd.Context(), client, labelInvocations(u.Edits), sel, ch)
				if err != nil {
					return fmt.Errorf("label %s/%s: %w", u.Space, u.UnitSlug, err)
				}
				results = append(results, res)
			}

			report := podSecuritySetReport{
				Level: level, Version: version, Namespaces: plans, Edits: edits,
				Result: write.Summarize("pod-security set", "", dryRun, results...),
			}
			if report.Level == "" {
				report.Level = "achievable"
			}
			if done, err := managerkit.Render(cmd.OutOrStdout(), output, report); done || err != nil {
				return err
			}
			printPodSecuritySet(cmd, report)
			return write.ReportMutations(cmd, "pod-security set", "", dryRun, output, results...)
		},
	}
	kit.AddOutputFlag(cmd, &output)
	kit.AddFilterFlags(cmd, &filter)
	commit.Bind(cmd)
	cmd.Flags().BoolVar(&bases, "bases", false, "label base Spaces, those with no release Target, instead of deployed ones, each judged by its own workloads; variants then pick the labels up by upgrade")
	kit.AddClusterFlag(cmd, &clusterFilter)
	kit.AddNamespaceFlag(cmd, &namespaceFilter)
	cmd.Flags().StringVar(&level, "level", "", "level to apply: privileged | baseline | restricted (default: the most restrictive one each namespace's workloads meet)")
	cmd.Flags().StringVar(&version, "version", "latest", `Pod Security Standards version to check against and label: "latest" or a Kubernetes minor version such as "v1.30"`)
	return cmd
}

// labelInvocations turns label edits into set-label invocations, each on the
// resources its WhereResource selects.
func labelInvocations(edits []nsmanager.LabelEdit) []api.FunctionInvocation {
	invocations := make([]api.FunctionInvocation, 0, len(edits))
	for _, e := range edits {
		invocations = append(invocations, api.FunctionInvocation{
			FunctionName: "set-label",
			Arguments: []api.FunctionArgument{
				{ParameterName: "label-key", Value: e.Key},
				{ParameterName: "label-value", Value: e.Value},
			},
			WhereResource: e.WhereResource,
		})
	}
	return invocations
}

// unitsInScope is the IDs of the Units the fleet scope selects, or nil when there
// is no scope and every Unit is selected.
func unitsInScope(ctx context.Context, client *cubapi.Client, where string) (map[string]bool, error) {
	if where == "" {
		return nil, nil
	}
	units, err := cubapi.ListUnits(ctx, client, cubapi.NewWhere(where), cubapi.ListOpts{Select: "UnitID"})
	if err != nil {
		return nil, err
	}
	ids := make(map[string]bool, len(units))
	for _, u := range units {
		if u.Unit != nil {
			ids[u.Unit.UnitID.String()] = true
		}
	}
	return ids, nil
}

// printPodSecuritySet prints the level each selected namespace gets, ahead of the
// write report.
func printPodSecuritySet(cmd *cobra.Command, r podSecuritySetReport) {
	out := cmd.OutOrStdout()
	if len(r.Namespaces) == 0 {
		kit.Fprintln(out, "No Namespace Units match the selection.")
		return
	}
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "CLUSTER\tNAMESPACE\tWORKLOADS\tENFORCE\tACHIEVABLE\tAPPLY")
	for _, p := range r.Namespaces {
		apply := p.Level
		switch {
		case p.Skip != "":
			apply = "skip: " + p.Skip
		case p.Kept:
			apply = "keep " + p.Level
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\t%s\n", p.Cluster, p.Namespace, p.Workloads, kit.Dash(p.Enforce), kit.Dash(p.Achievable), apply)
	}
	_ = tw.Flush()
	for _, p := range r.Namespaces {
		if p.Note != "" {
			kit.Fprintln(out, fmt.Sprintf("  %s/%s: %s", p.Cluster, p.Namespace, p.Note))
		}
	}
	kit.Fprintln(out, "")
}
