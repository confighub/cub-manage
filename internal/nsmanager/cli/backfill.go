// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/confighub/sdk/cliutil"
	"github.com/confighub/sdk/core/cubapi"
	api "github.com/confighub/sdk/core/function/api"
	"github.com/confighub/sdk/managerkit/write"

	"github.com/confighub/cub-manage/internal/cub"
	"github.com/confighub/cub-manage/internal/kit"
	"github.com/confighub/cub-manage/internal/nsmanager"
	"github.com/confighub/cub-manage/internal/nsmanager/snapshot"
)

func newBackfillCmd() *cobra.Command {
	var output, spaceSlug, fromSpace, namespace string
	var commit cliutil.CommitFlags
	cmd := &cobra.Command{
		Use:   "backfill --space <dest> --from <base-space> --namespace <ns>",
		Short: "Clone a base Space's namespace policy Units into an existing Space and re-home them with set-namespace",
		Long: `backfill adds a namespace's policy Units to an existing Space -- one that
already holds workloads (e.g. a Helm/Kustomize-ingested app Space) but none of
the policy Units a base Space defines, such as its Namespace. It clones every deployable Unit
from the base Space (--from) into the destination Space (--space) via the
BulkCreateUnits API, then runs set-namespace to re-home the clones to the target
namespace (--namespace) — set-namespace also rewrites RBAC subject namespaces and
Service DNS references, so whatever the base defines lands correctly.

What the base Space contains is the base Space's business: backfill clones and
re-homes it without reading the policies themselves.

This is the half the installer's 'new'/'upload' doesn't cover (greenfield only).
Re-runs are idempotent (already-present clones are reported, not duplicated).
Dry-run unless --commit --change-desc. A dry run checks each clone, but cannot
show what set-namespace would change in Units that do not exist yet.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if spaceSlug == "" || fromSpace == "" || namespace == "" {
				return fmt.Errorf("--space, --from, and --namespace are all required")
			}
			changeDesc, dryRun, err := commit.Validate(
				fmt.Sprintf("backfill namespace policy Units from %s into %s (namespace %s)", fromSpace, spaceSlug, namespace))
			if err != nil {
				return err
			}
			client, err := cub.Preflight(cmd.Context())
			if err != nil {
				return err
			}
			base, err := cubapi.ResolveSpace(cmd.Context(), client, cubapi.ParseRef(fromSpace), cubapi.ResolveOpts{})
			if err != nil {
				return fmt.Errorf("resolve --from space %q: %w", fromSpace, err)
			}
			dest, err := cubapi.ResolveSpace(cmd.Context(), client, cubapi.ParseRef(spaceSlug), cubapi.ResolveOpts{})
			if err != nil {
				return fmt.Errorf("resolve --space %q: %w", spaceSlug, err)
			}

			// Clone every Kubernetes Unit in the base, except its Namespace when the
			// dest already has one: cloning that would collide with the dest's
			// (findings reports duplicate namespaces). The base's Units are listed
			// directly rather than read from the snapshot, which loads only the
			// kinds this group analyzes and so would not see a base's NetworkPolicies
			// or RBAC.
			baseUnits, err := cubapi.ListUnits(cmd.Context(), client,
				cubapi.NewWhere(fmt.Sprintf("SpaceID = '%s' AND ToolchainType = '%s' AND Slug != 'installer-record'",
					base.Space.SpaceID.String(), cubapi.DefaultToolchainType)),
				cubapi.ListOpts{Select: "Slug,SpaceID,UnitID"})
			if err != nil {
				return fmt.Errorf("list base Units: %w", err)
			}
			baseSlugs := map[string]bool{}
			for _, eu := range baseUnits {
				if eu.Unit != nil {
					baseSlugs[eu.Unit.Slug] = true
				}
			}
			scopeWhere := fmt.Sprintf("SpaceID IN ('%s', '%s')", base.Space.SpaceID.String(), dest.Space.SpaceID.String())
			snap, err := snapshot.Load(cmd.Context(), client, scopeWhere)
			if err != nil {
				return fmt.Errorf("load base + dest snapshot: %w", err)
			}
			baseNamespaceSlugs := map[string]bool{}
			destHasNamespace := false
			for _, r := range snap.Resources {
				kind, _, _, ok := nsmanager.ResourceMeta(r.Doc)
				if !ok || kind != "Namespace" {
					continue
				}
				switch r.Origin.SpaceID {
				case base.Space.SpaceID.String():
					baseNamespaceSlugs[r.Origin.UnitSlug] = true
				case dest.Space.SpaceID.String():
					destHasNamespace = true
				}
			}
			var cloneSlugs []string
			for s := range baseSlugs {
				if destHasNamespace && baseNamespaceSlugs[s] {
					continue // dest already has a Namespace; don't clone another
				}
				cloneSlugs = append(cloneSlugs, s)
			}
			sort.Strings(cloneSlugs)

			if len(cloneSlugs) == 0 {
				return fmt.Errorf("no Units to backfill from base Space %q (nothing missing)", fromSpace)
			}
			srcWhere := fmt.Sprintf("SpaceID = '%s' AND Slug IN (%s)", base.Space.SpaceID.String(), inList(cloneSlugs))
			destWhere := fmt.Sprintf("SpaceID = '%s'", dest.Space.SpaceID.String())
			cloned, err := cub.BulkCloneUnits(cmd.Context(), client, srcWhere, destWhere, true, dryRun)
			if err != nil {
				return fmt.Errorf("clone Units into %q: %w", spaceSlug, err)
			}
			clones := &cubapi.Result{DryRun: dryRun}
			var clonedSlugs []string
			for _, r := range cloned {
				o := cubapi.UnitOutcome{SpaceSlug: spaceSlug, HasMutations: true, Success: r.Error == nil}
				if r.Unit != nil {
					o.UnitID, o.UnitSlug = r.Unit.UnitID.String(), r.Unit.Slug
					clonedSlugs = append(clonedSlugs, r.Unit.Slug)
				}
				if r.Error != nil {
					o.Error = r.Error.Message
				}
				clones.Outcomes = append(clones.Outcomes, o)
			}
			results := []*cubapi.Result{clones}
			if !dryRun && len(clonedSlugs) > 0 {
				sel := cubapi.Selector{Where: fmt.Sprintf("SpaceID = '%s' AND Slug IN (%s)", dest.Space.SpaceID.String(), inList(clonedSlugs))}
				res, err := cub.InvokeMutation(cmd.Context(), client, "set-namespace",
					[]api.FunctionArgument{{ParameterName: "namespace-name", Value: namespace}},
					sel, write.Change(changeDesc, false))
				if err != nil {
					return fmt.Errorf("re-home cloned Units to namespace %q: %w", namespace, err)
				}
				results = append(results, res)
			}
			return write.ReportMutations(cmd, "backfill", spaceSlug, dryRun, output, results...)
		},
	}
	kit.AddOutputFlag(cmd, &output)
	commit.Bind(cmd)
	cmd.Flags().StringVar(&spaceSlug, "space", "", "destination Space to backfill the Units into (required)")
	cmd.Flags().StringVar(&fromSpace, "from", "", "base Space holding the Units to clone (required)")
	cmd.Flags().StringVar(&namespace, "namespace", "", "namespace to re-home the cloned Units to (required)")
	return cmd
}

// inList renders slugs as a quoted, comma-separated list for a ConfigHub
// `Slug IN (...)` predicate.
func inList(slugs []string) string {
	quoted := make([]string, len(slugs))
	for i, s := range slugs {
		quoted[i] = "'" + strings.ReplaceAll(s, "'", "''") + "'"
	}
	return strings.Join(quoted, ", ")
}
