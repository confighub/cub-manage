// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	goclientnew "github.com/confighub/sdk/core/openapi/goclient-new"
	"github.com/confighub/sdk/managerkit/write"

	"github.com/confighub/cub-manage/internal/cub"
	"github.com/confighub/cub-manage/internal/kit"
	"github.com/confighub/cub-manage/internal/workload"
	"github.com/confighub/cub-manage/internal/workload/snapshot"
)

// unitManagedByLabel marks Units this tool authors (e.g. generated PDBs).
const unitManagedByLabel = "cub manage workload"

func newEnsurePDBCmd() *cobra.Command {
	var output string
	var minAvailable, maxUnavailable string
	var commit write.CommitFlags
	cmd := &cobra.Command{
		Use:   "ensure-pdb <space>/<unit>",
		Short: "Make a PodDisruptionBudget Unit whose selector matches a workload's pods",
		Long: `ensure-pdb reads a workload Unit's pod-template labels and namespace and makes a
PodDisruptionBudget Unit, <workload>-pdb, whose selector matches exactly, in the
same Space as the workload. It creates the Unit, or brings an existing one up to
date, so running it again changes nothing.

Set the policy with --min-available or --max-unavailable (default:
maxUnavailable 1, i.e. at most one pod down during a voluntary disruption).
Refuses to guess when the workload has no pod-template labels (an empty selector
would cover the whole namespace). Dry-run unless --commit --change-desc.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if minAvailable != "" && maxUnavailable != "" {
				return fmt.Errorf("set only one of --min-available / --max-unavailable")
			}
			policyKey, policyVal := "maxUnavailable", "1"
			switch {
			case minAvailable != "":
				policyKey, policyVal = "minAvailable", minAvailable
			case maxUnavailable != "":
				policyKey, policyVal = "maxUnavailable", maxUnavailable
			}
			ch, err := commit.Change(fmt.Sprintf("ensure PodDisruptionBudget for %s (%s: %s)", args[0], policyKey, policyVal))
			if err != nil {
				return err
			}
			dryRun := ch.DryRun()
			client, err := cub.Preflight(cmd.Context())
			if err != nil {
				return err
			}
			ref, err := write.ParseUnitRef(cmd.Context(), client, args[0])
			if err != nil {
				return err
			}

			// Find the workload to derive its selector, namespace, and name.
			snap, err := snapshot.Load(cmd.Context(), client, fmt.Sprintf("SpaceID = '%s'", ref.SpaceID.String()))
			if err != nil {
				return err
			}
			w := findWorkloadBySlug(snap, ref.UnitSlug)
			if w == nil {
				return fmt.Errorf("no workload found in Unit %s/%s", ref.SpaceSlug, ref.UnitSlug)
			}
			if len(w.PodLabels) == 0 {
				return fmt.Errorf("workload %s has no pod-template labels — cannot derive a safe PDB selector", w.Name)
			}

			pdbSlug := w.Name + "-pdb"
			manifest := renderPDB(w.Name+"-pdb", w.Namespace, policyKey, policyVal, w.PodLabels)

			res := cub.EnsureUnits(cmd.Context(), client, []cub.Creation{{
				SpaceSlug: ref.SpaceSlug,
				Unit: goclientnew.Unit{
					Slug:          pdbSlug,
					DisplayName:   pdbSlug,
					ToolchainType: "Kubernetes/YAML",
					SpaceID:       ref.SpaceID,
					Labels:        map[string]string{"managed-by": unitManagedByLabel},
				},
				Data: manifest,
			}}, ch)
			return write.ReportMutations(cmd, "ensure-pdb", ref.SpaceSlug, dryRun, output, res)
		},
	}
	kit.AddOutputFlag(cmd, &output)
	commit.Bind(cmd)
	cmd.Flags().StringVar(&minAvailable, "min-available", "", "PDB minAvailable (int or percentage, e.g. 2 or 80%)")
	cmd.Flags().StringVar(&maxUnavailable, "max-unavailable", "", "PDB maxUnavailable (int or percentage; default 1)")
	return cmd
}

// findWorkloadBySlug returns the first workload whose origin Unit slug matches.
func findWorkloadBySlug(snap *snapshot.Snapshot, unitSlug string) *workload.WorkloadEntity {
	for _, c := range snap.Clusters {
		for _, w := range c.Workloads {
			if w.Origin.UnitSlug == unitSlug {
				return w
			}
		}
	}
	return nil
}

// renderPDB produces literal PodDisruptionBudget YAML with the given selector.
func renderPDB(name, namespace, policyKey, policyVal string, labels map[string]string) string {
	var b strings.Builder
	b.WriteString("apiVersion: policy/v1\n")
	b.WriteString("kind: PodDisruptionBudget\n")
	b.WriteString("metadata:\n")
	b.WriteString("  name: " + name + "\n")
	if namespace != "" {
		b.WriteString("  namespace: " + namespace + "\n")
	}
	b.WriteString("spec:\n")
	b.WriteString("  " + policyKey + ": " + policyVal + "\n")
	b.WriteString("  selector:\n")
	b.WriteString("    matchLabels:\n")
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b.WriteString("      " + k + ": " + labels[k] + "\n")
	}
	return b.String()
}
