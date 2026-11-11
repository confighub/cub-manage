// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/confighub/sdk/core/cubapi"
	"github.com/confighub/sdk/managerkit/write"

	"github.com/confighub/cub-manage/internal/autoscale/localexec"
	"github.com/confighub/cub-manage/internal/cub"
	"github.com/confighub/cub-manage/internal/kit"
)

func newConvertCmd() *cobra.Command {
	var output string
	var commit write.CommitFlags
	cmd := &cobra.Command{
		Use:   "convert-keda <space>/<unit>",
		Short: "Convert a HorizontalPodAutoscaler Unit to a KEDA ScaledObject",
		Long: `convert-keda rewrites a Unit's HorizontalPodAutoscaler into an equivalent KEDA
ScaledObject, preserving min/max replicas and cpu/memory metrics.

The conversion runs the convert-hpa-to-keda ConfigHub function in an embedded
executor in-process: the command fetches the Unit's data, runs the function on
it locally (no server-side function), and writes the result back as a
new Unit revision. cpu/memory Resource metrics become KEDA cpu/memory triggers;
Pods/Object/External metrics are not converted (KEDA needs a matching scaler).

Dry-run by default (shows what the conversion would change); --commit
--change-desc writes the revision. Deploying KEDA ScaledObjects requires the KEDA
operator installed in the target cluster.

To schema-validate the generated ScaledObject automatically, install the
tool-level basic group ('cub manage guardrails install --commit'): its
validate rule runs vet-schemas on every change, and the KEDA schemas are in
its catalog.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			space, unit, ok := strings.Cut(args[0], "/")
			if !ok || space == "" || unit == "" {
				return fmt.Errorf("target must be <space>/<unit>, got %q", args[0])
			}
			ch, err := commit.Change(fmt.Sprintf("convert HPA to KEDA ScaledObject in %s", args[0]))
			if err != nil {
				return err
			}
			dryRun := ch.DryRun()
			ctx := cmd.Context()
			client, err := cub.Preflight(ctx)
			if err != nil {
				return err
			}
			ref, err := cub.ResolveUnit(ctx, client, space, unit)
			if err != nil {
				return err
			}
			unitData, etag, err := cub.GetUnitData(ctx, client, ref)
			if err != nil {
				return err
			}
			mutated, changed, err := localexec.ConvertHPAToKEDA(ctx, unitData)
			if err != nil {
				return err
			}
			// With no HorizontalPodAutoscaler to convert there is nothing to write,
			// and the report says the Unit is unchanged, as any edit's does.
			res := &cubapi.Result{DryRun: dryRun, Outcomes: []cubapi.UnitOutcome{{
				UnitID: ref.UnitID.String(), SpaceSlug: space, UnitSlug: unit, Success: true,
			}}}
			if changed {
				if res, err = cub.UpdateUnitData(ctx, client, ref, mutated, etag, ch); err != nil {
					return err
				}
			}
			return write.ReportMutations(cmd, "convert-keda", space, dryRun, output, res)
		},
	}
	kit.AddOutputFlag(cmd, &output)
	commit.Bind(cmd)
	return cmd
}
