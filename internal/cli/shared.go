// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

// The commands that are the same in every command group, so are the tool's
// rather than any group's: the profile library, which every group installs into
// the one shared Space, and the guardrail status report, which reports what any
// Trigger marked rather than one group's rules.

import (
	"slices"

	"github.com/spf13/cobra"

	"github.com/confighub/sdk/managerkit/guardrails"
	"github.com/confighub/sdk/managerkit/profiles"

	autoscale "github.com/confighub/cub-manage/internal/autoscale/cli"
	"github.com/confighub/cub-manage/internal/cub"
	"github.com/confighub/cub-manage/internal/kit"
	observability "github.com/confighub/cub-manage/internal/observability/cli"
	scheduling "github.com/confighub/cub-manage/internal/scheduling/cli"
	workload "github.com/confighub/cub-manage/internal/workload/cli"
)

// library is every command group's profiles as one library. 'profile install'
// seeds them all; each group's 'fleet-edit' applies one to the resource kinds
// that group manages.
var library = profiles.Library{
	Tool:           "cub-manage",
	Target:         "a Unit",
	ParamExample:   "--param container=web for the resource tiers",
	Preflight:      cub.Preflight,
	InvocationName: kit.RootName,
	Profiles: slices.Concat(
		workload.Library.Profiles,
		scheduling.Library.Profiles,
		autoscale.Library.Profiles,
		observability.Library.Profiles,
	),
}

func newProfileCmd() *cobra.Command { return library.Command() }

func newGuardrailsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "guardrails",
		Short: "Report the Units guardrails have marked",
		Long: `guardrails reports on the guardrail packs every command group installs. Each
group installs its own pack, so a Space can opt into some and not others:

  cub manage <group> guardrails install

'status' lists the Units any Trigger has marked, whichever pack it belongs to.`,
	}
	cmd.AddCommand(guardrails.Pack{}.StatusCmd(cub.Preflight))
	return cmd
}
