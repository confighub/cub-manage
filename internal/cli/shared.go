// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

// The commands that are the same in every command group, so are the tool's
// rather than any group's: the profile library, which every group installs into
// the one shared Space, the guardrail status report, which reports what any
// Trigger marked rather than one group's rules, and guardrail exceptions, which
// name a rule of any group.

import (
	"slices"

	"github.com/spf13/cobra"

	"github.com/confighub/sdk/managerkit/guardrails"
	"github.com/confighub/sdk/managerkit/profiles"

	autoscale "github.com/confighub/cub-manage/internal/autoscale/cli"
	"github.com/confighub/cub-manage/internal/cub"
	"github.com/confighub/cub-manage/internal/kit"
	netpol "github.com/confighub/cub-manage/internal/netpol/cli"
	nsmanager "github.com/confighub/cub-manage/internal/nsmanager/cli"
	observability "github.com/confighub/cub-manage/internal/observability/cli"
	rbac "github.com/confighub/cub-manage/internal/rbac/cli"
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
		rbac.Library.Profiles,
		netpol.Library.Profiles,
		nsmanager.Library.Profiles,
	),
}

func newProfileCmd() *cobra.Command { return library.Command() }

func newGuardrailsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "guardrails",
		Short: "Install the tool-level basic guardrail group; report marked Units; manage exceptions and enforcement",
		Long: `guardrails manages what is the tool's rather than any command group's. Each
group installs its own pack, so a Space can opt into some and not others:

  cub manage <group> guardrails install

'install' installs the tool-level basic group, whose rules are about every
resource or every Unit rather than one group's kinds:

  validate                 Resources match their schemas (vet-schemas)
  format                   YAML has no ambiguous syntax (vet-format)
  validate-dupes           No duplicate keys in merged lists (vet-merge-keys)
  vet-namespace-required   Namespaced resources name their namespace (vet-cel)
  no-merge-conflicts       No unresolved merge conflicts (vet-no-merge-conflicts)
  no-link-findings         Links find everything they name (vet-no-link-findings)
  vet-no-placeholders      No placeholders left to fill (vet-placeholders),
                           on the Units released to a Target
  vet-immutable            Immutable fields are unchanged (vet-immutable),
                           on the Units released to a Target

'status' lists the Units any Trigger has marked, whichever group it belongs to.
'status --by-rule' reports where each rule stands instead -- Created, Audited,
Enforced or Disabled -- with its priority, the date it is due to be enforced,
and how many Units it passes, warns on and blocks.

'exception' excepts a resource from one rule of any group, removes an exception,
and lists every resource excepted and why.

'update' sets a rule's priority and the date it is due to be enforced, and
enforces it once its warnings are worked down.`,
	}
	cmd.AddCommand(basicPack.InstallCmd(cub.Preflight), guardrails.Pack{}.StatusCmd(cub.Preflight),
		guardrails.ExceptionCmd(cub.Preflight, basicPack, workload.Pack, scheduling.Pack, autoscale.Pack,
			observability.Pack, rbac.Pack, netpol.Pack, nsmanager.Pack),
		guardrails.UpdateCmd(cub.Preflight))
	return cmd
}
