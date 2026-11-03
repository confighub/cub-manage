// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package kit

import (
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/confighub/sdk/managerkit"
)

// ResourceRow is one resource a list command reports: what it is, where it runs,
// and the Unit it came from.
type ResourceRow struct {
	Cluster   string `json:"cluster"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
	Space     string `json:"space"`
	Unit      string `json:"unit"`
	// Canonical marks a definition in a base or policy Space, which is listed but
	// deployed nowhere and so left out of analysis.
	Canonical bool `json:"canonical,omitempty"`
}

// ResourceFilter is the flags every list command takes: --kind, --cluster and
// --namespace.
type ResourceFilter struct {
	Kind      string
	Cluster   string
	Namespace string
}

// AddResourceFlags binds --kind, --cluster and --namespace. kinds are the kinds
// the group lists, for the help.
func AddResourceFlags(cmd *cobra.Command, f *ResourceFilter, kinds ...string) {
	cmd.Flags().StringVar(&f.Kind, "kind", "", "only this kind: "+strings.Join(kinds, " | "))
	AddClusterFlag(cmd, &f.Cluster)
	AddNamespaceFlag(cmd, &f.Namespace)
}

// Keep reports whether a row passes the filter. Kinds match without regard to
// case.
func (f ResourceFilter) Keep(r ResourceRow) bool {
	return (f.Kind == "" || strings.EqualFold(r.Kind, f.Kind)) &&
		(f.Cluster == "" || r.Cluster == f.Cluster) &&
		(f.Namespace == "" || r.Namespace == f.Namespace)
}

// SortResources orders rows by cluster, kind, namespace and name.
func SortResources[T any](rows []T, row func(T) ResourceRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := row(rows[i]), row(rows[j])
		if a.Cluster != b.Cluster {
			return a.Cluster < b.Cluster
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Namespace != b.Namespace {
			return a.Namespace < b.Namespace
		}
		return a.Name < b.Name
	})
}

// RenderResources prints the rows a list command kept, sorted: as -o asks, or as
// a table.
func RenderResources(cmd *cobra.Command, output string, rows []ResourceRow) error {
	if rows == nil {
		rows = []ResourceRow{}
	}
	SortResources(rows, func(r ResourceRow) ResourceRow { return r })
	out := cmd.OutOrStdout()
	if done, err := managerkit.Render(out, output, rows); done || err != nil {
		return err
	}
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "CLUSTER\tKIND\tNAMESPACE\tNAME\tUNIT")
	for _, r := range rows {
		name := r.Name
		if r.Canonical {
			name += " (canonical)"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.Cluster, r.Kind, Dash(r.Namespace), name, UnitLabel(r.Space, r.Unit))
	}
	_ = tw.Flush()
	Fprintln(out, fmt.Sprintf("\n%d resources", len(rows)))
	return nil
}

// UnitLabel names a Unit as <space>/<unit>, the form a write command takes it in.
func UnitLabel(space, unit string) string {
	if space == "" {
		return Dash(unit)
	}
	return space + "/" + unit
}
