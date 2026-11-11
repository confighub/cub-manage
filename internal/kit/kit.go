// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

// Package kit is what every 'cub manage' command group shares: how the tool is
// spelled in suggestions, how a group lays out its commands, and the spellings
// of the shared flag and output helpers from the SDK's cliutil and managerkit.
// A command group uses these rather than its own, so that every group reads the
// same.
package kit

import (
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/confighub/cub-manage/internal/cub"
	"github.com/confighub/sdk/cliutil"
	api "github.com/confighub/sdk/core/function/api"
	"github.com/confighub/sdk/managerkit"
)

// Command group IDs. Every domain lists its read commands, then its write
// commands, under these headings.
const (
	GroupRead  = "read"
	GroupWrite = "write"
)

// DomainHelpFooter ends every domain's long help: the conventions each of its
// commands follows.
const DomainHelpFooter = `Read commands print JSON unless -o says otherwise (-o table for people). Write
commands are dry runs unless --commit is given with --change-desc; they edit or
create Units and never apply them to a cluster, and never bypass a gate.`

// invokedAsPlugin reports whether cub launched the binary as a plugin. cub sets
// CUB_PLUGIN=1 when it does.
func invokedAsPlugin() bool {
	return os.Getenv("CUB_PLUGIN") == "1"
}

// RootName is how to spell the tool in a suggestion the user can paste back
// into a shell: "cub manage" when run as a cub plugin, otherwise the binary as
// it was invoked.
func RootName() string {
	if invokedAsPlugin() {
		return "cub manage"
	}
	if len(os.Args) > 0 && os.Args[0] != "" {
		return os.Args[0]
	}
	return "cub-manage"
}

// InvocationName is how to spell a domain's command group in a suggestion, e.g.
// "cub manage workload".
func InvocationName(domain string) string { return RootName() + " " + domain }

// AddGroups declares the read and write headings on a domain's command.
func AddGroups(cmd *cobra.Command) {
	cmd.AddGroup(
		&cobra.Group{ID: GroupRead, Title: "Read commands:"},
		&cobra.Group{ID: GroupWrite, Title: "Write commands (dry run unless --commit):"},
	)
}

// AddReadCommands adds commands under the read heading.
func AddReadCommands(cmd *cobra.Command, cmds ...*cobra.Command) { addInGroup(cmd, GroupRead, cmds) }

// AddWriteCommands adds commands under the write heading.
func AddWriteCommands(cmd *cobra.Command, cmds ...*cobra.Command) {
	addInGroup(cmd, GroupWrite, cmds)
}

func addInGroup(cmd *cobra.Command, group string, cmds []*cobra.Command) {
	for _, c := range cmds {
		c.GroupID = group
		cmd.AddCommand(c)
	}
}

// FilterFlags is the fleet scope every read command takes: --where and the
// standard Space-label shorthands.
type FilterFlags = cliutil.QueryFlags

// AddOutputFlag binds -o/--output.
func AddOutputFlag(cmd *cobra.Command, dest *string) { managerkit.AddOutputFlag(cmd, dest) }

// AddFilterFlags binds --where and the standard Space-label scopes. --select is
// deliberately not among them: the fleet snapshot pins the fields it reads, so a
// caller who narrowed the selection would get them back zeroed.
func AddFilterFlags(cmd *cobra.Command, f *FilterFlags) {
	f.BindWhere(cmd)
	f.BindSpaceLabels(cmd)
	managerkit.BindComponentResolver(cmd, f, cub.Preflight)
}

// AddClusterFlag binds --cluster, the display filter on a Target slug.
func AddClusterFlag(cmd *cobra.Command, dest *string) {
	cmd.Flags().StringVar(dest, "cluster", "", "only this cluster (a Target slug, or None for Units whose Space has no release Target)")
}

// AddNamespaceFlag binds --namespace, the display filter on a namespace.
func AddNamespaceFlag(cmd *cobra.Command, dest *string) {
	cmd.Flags().StringVar(dest, "namespace", "", "only this namespace")
}

// AddWhereResourceFlag binds --where-resource, which narrows a write to one Unit to the resources
// the expression selects, for a Unit holding more than the workload to change.
func AddWhereResourceFlag(cmd *cobra.Command, dest *string) {
	cmd.Flags().StringVar(dest, "where-resource", "",
		"only the resources of the Unit this selects, as in \"ConfigHub.ResourceName = 'kube-system/efs-csi-controller'\" or \"ConfigHub.ResourceType = 'apps/v1/Deployment'\" (default: every resource)")
}

// AddIssuesOnlyFlag binds --issues-only, which narrows a report to the rows
// with a problem. what says what a problem is in this report.
func AddIssuesOnlyFlag(cmd *cobra.Command, dest *bool, what string) {
	cmd.Flags().BoolVar(dest, "issues-only", false, "only "+what)
}

func Fprintln(w io.Writer, a ...any)         { cliutil.Fprintln(w, a...) }
func ParseScore(s string) (api.Score, error) { return api.ParseScore(s) }
func Dash(s string) string                   { return cliutil.Dash(s) }
func YesNo(b bool) string                    { return cliutil.YesNo(b) }
