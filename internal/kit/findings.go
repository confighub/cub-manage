// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package kit

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	api "github.com/confighub/sdk/core/function/api"
	"github.com/confighub/sdk/managerkit"
)

// Finding is one issue an analyzer found, as every group's findings command
// reports it. Each group's engine keeps its own finding type, with whatever its
// other commands need; this is the shape they are all reported in.
type Finding struct {
	Severity  api.Score `json:"severity"`
	Analyzer  string    `json:"analyzer"`
	Cluster   string    `json:"cluster,omitempty"`
	Namespace string    `json:"namespace,omitempty"`
	Kind      string    `json:"kind,omitempty"`
	Name      string    `json:"name,omitempty"`
	// Component is set by the analyzers that compare a Component's variants.
	Component string `json:"component,omitempty"`
	Space     string `json:"space,omitempty"`
	Unit      string `json:"unit,omitempty"`
	Message   string `json:"message"`
}

// FindingTotals counts the findings a report kept.
type FindingTotals struct {
	Total      int               `json:"total"`
	BySeverity map[api.Score]int `json:"bySeverity"`
	ByAnalyzer map[string]int    `json:"byAnalyzer"`
}

// FindingsReport is what every findings command prints.
type FindingsReport struct {
	Findings []Finding     `json:"findings"`
	Totals   FindingTotals `json:"totals"`
	// Filter is the fleet scope the findings were read under.
	Filter string `json:"filter,omitempty"`
}

// FindingFilter is the flags every findings command takes to narrow what it
// reports, beyond the fleet scope: one severity, any of a list of analyzers, one
// cluster, one namespace.
type FindingFilter struct {
	Severity  string
	Analyzer  string
	Cluster   string
	Namespace string

	analyzers []string
	severity  api.Score
	only      []string
}

// AddFindingFlags binds --severity, --analyzer, --cluster and --namespace.
// analyzers is every analyzer the group has, which --analyzer must name one of.
func AddFindingFlags(cmd *cobra.Command, f *FindingFilter, analyzers ...string) {
	f.analyzers = analyzers
	cmd.Flags().StringVar(&f.Severity, "severity", "", "only findings of this severity: Critical | High | Medium | Low")
	cmd.Flags().StringVar(&f.Analyzer, "analyzer", "", "only findings from these analyzers, comma-separated: "+strings.Join(analyzers, " | "))
	AddClusterFlag(cmd, &f.Cluster)
	AddNamespaceFlag(cmd, &f.Namespace)
}

// Validate checks the flags before anything is read, so a typo fails fast
// rather than as an empty report.
func (f *FindingFilter) Validate() error {
	s, err := ParseScore(f.Severity)
	if err != nil {
		return err
	}
	f.severity = s
	f.only = nil
	for _, a := range strings.Split(f.Analyzer, ",") {
		if a = strings.TrimSpace(a); a == "" {
			continue
		}
		if len(f.analyzers) > 0 && !slices.Contains(f.analyzers, a) {
			return fmt.Errorf("unknown analyzer %q: want one of %s", a, strings.Join(f.analyzers, ", "))
		}
		f.only = append(f.only, a)
	}
	return nil
}

// Keep reports whether a finding passes the filter.
func (f FindingFilter) Keep(x Finding) bool {
	return (f.severity == api.ScoreNone || x.Severity == f.severity) &&
		(len(f.only) == 0 || slices.Contains(f.only, x.Analyzer)) &&
		(f.Cluster == "" || x.Cluster == f.Cluster) &&
		(f.Namespace == "" || x.Namespace == f.Namespace)
}

// NewFindingsReport keeps the findings that pass f, most severe first, and
// counts them.
func NewFindingsReport(all []Finding, f FindingFilter, filter string) FindingsReport {
	r := FindingsReport{
		Findings: []Finding{},
		Totals:   FindingTotals{BySeverity: map[api.Score]int{}, ByAnalyzer: map[string]int{}},
		Filter:   filter,
	}
	for _, x := range all {
		if !f.Keep(x) {
			continue
		}
		r.Findings = append(r.Findings, x)
		r.Totals.BySeverity[x.Severity]++
		r.Totals.ByAnalyzer[x.Analyzer]++
	}
	sort.SliceStable(r.Findings, func(i, j int) bool {
		return api.ScoreToNumber[r.Findings[i].Severity] > api.ScoreToNumber[r.Findings[j].Severity]
	})
	r.Totals.Total = len(r.Findings)
	return r
}

// severities are the severities a summary line counts, most severe first.
var severities = []api.Score{api.ScoreCritical, api.ScoreHigh, api.ScoreMedium, api.ScoreLow}

// RenderFindings prints a findings report: as -o asks, or as a table followed by
// a count per severity.
func RenderFindings(cmd *cobra.Command, output string, r FindingsReport) error {
	out := cmd.OutOrStdout()
	if done, err := managerkit.Render(out, output, r); done || err != nil {
		return err
	}
	if len(r.Findings) == 0 {
		Fprintln(out, "No findings.")
		return nil
	}
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "SEVERITY\tANALYZER\tCLUSTER\tNAMESPACE\tKIND\tNAME\tMESSAGE")
	for _, x := range r.Findings {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			x.Severity, x.Analyzer, Dash(x.Cluster), Dash(x.Namespace), Dash(x.Kind), Dash(x.Name), x.Message)
	}
	_ = tw.Flush()
	counts := make([]string, 0, len(severities))
	for _, s := range severities {
		if n := r.Totals.BySeverity[s]; n > 0 {
			counts = append(counts, fmt.Sprintf("%d %s", n, strings.ToLower(string(s))))
		}
	}
	noun := "findings"
	if r.Totals.Total == 1 {
		noun = "finding"
	}
	Fprintln(out, fmt.Sprintf("\n%d %s: %s", r.Totals.Total, noun, strings.Join(counts, ", ")))
	return nil
}
