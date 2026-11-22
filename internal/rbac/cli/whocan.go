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
	"github.com/confighub/cub-manage/internal/rbac"
	"github.com/confighub/cub-manage/internal/rbac/reduce"
)

// grantRow is a who-can grant, as the server's reduce returns it.
type grantRow = reduce.Grant

func newWhoCanCmd() *cobra.Command {
	var output, apiGroup, cluster, namespace, name string
	var filter kit.FilterFlags
	cmd := &cobra.Command{
		Use:   "who-can <verb> <resource>",
		Short: "Find every subject that can perform an action, fleet-wide",
		Long: `who-can answers "who can VERB RESOURCE?" across the fleet, with provenance:
the cluster, the granting binding and role, and the namespace scope.

RESOURCE may include a subresource (e.g. pods/log). Use --api-group for
non-core groups (e.g. --api-group apps for deployments), --cluster to one cluster,
--namespace to the grants effective in a namespace (its own and the cluster-wide
ones), and --name to honor resourceNames.

Only cluster-admin is credited among Kubernetes builtins (its rules are known);
bindings to admin/edit/view/system:* are reported by 'findings', not here.`,
		Example: `  cub manage rbac who-can get secrets
  cub manage rbac who-can create pods/exec --namespace payments
  cub manage rbac who-can update deployments --api-group apps`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := cub.Preflight(cmd.Context())
			if err != nil {
				return err
			}
			where, err := filter.Predicate()
			if err != nil {
				return err
			}
			query := rbac.AccessQuery{
				Verb:      args[0],
				Resource:  args[1],
				APIGroup:  apiGroup,
				Namespace: namespace,
				Name:      name,
			}
			// The server reduces each cluster's roles and bindings to the grants, so only those come
			// back, not every RBAC resource in the fleet.
			grants, err := reduce.WhoCan(cmd.Context(), client, where, cluster, query)
			if err != nil {
				return err
			}
			rows := sortGrantRows(grants)
			if done, err := managerkit.Render(cmd.OutOrStdout(), output, rows); done || err != nil {
				return err
			}
			printGrantTable(cmd, rows)
			return nil
		},
	}
	kit.AddOutputFlag(cmd, &output)
	kit.AddFilterFlags(cmd, &filter)
	cmd.Flags().StringVar(&apiGroup, "api-group", "", "API group of the resource (default core)")
	kit.AddClusterFlag(cmd, &cluster)
	cmd.Flags().StringVar(&namespace, "namespace", "", "only grants effective in this namespace: its own and the cluster-wide ones")
	cmd.Flags().StringVar(&name, "name", "", "specific object name (honors resourceNames)")
	return cmd
}

// sortGrantRows orders grants by cluster, then subject.
func sortGrantRows(rows []grantRow) []grantRow {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Cluster != rows[j].Cluster {
			return rows[i].Cluster < rows[j].Cluster
		}
		return rows[i].Subject < rows[j].Subject
	})
	return rows
}

func printGrantTable(cmd *cobra.Command, rows []grantRow) {
	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "CLUSTER\tSUBJECT\tSCOPE\tROLE\tBINDING\tUNIT")
	for _, r := range rows {
		scope := r.Scope
		if scope == "" {
			scope = "cluster-wide"
		}
		role := r.RoleRef
		if r.Builtin {
			role += " (builtin)"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", r.Cluster, r.Subject, scope, role, r.Binding, r.Unit)
	}
	_ = tw.Flush()
	kit.Fprintln(cmd.OutOrStdout(), fmt.Sprintf("\n%d grants", len(rows)))
}

// --- access (inverse query) ---

// subjectGrantRow is a binding naming the subject of access, as the server's reduce returns it.
type subjectGrantRow = reduce.SubjectGrant

func newAccessCmd() *cobra.Command {
	var output, cluster, namespace string
	var filter kit.FilterFlags
	cmd := &cobra.Command{
		Use:   "access <subject>",
		Short: "List every role a subject holds, fleet-wide (inverse of who-can)",
		Long: `access lists every role a subject holds across the fleet.

SUBJECT is "Kind:Name", or "ServiceAccount:namespace/name" for a ServiceAccount:
  User:alice@example.com
  Group:oidc:developers
  ServiceAccount:apps/ci-deployer

--cluster narrows the list to one cluster, and --namespace to the grants effective
in a namespace: its own and the cluster-wide ones.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			subject, err := parseSubjectRef(args[0])
			if err != nil {
				return err
			}
			client, err := cub.Preflight(cmd.Context())
			if err != nil {
				return err
			}
			where, err := filter.Predicate()
			if err != nil {
				return err
			}
			grants, err := reduce.Access(cmd.Context(), client, where, cluster, subject)
			if err != nil {
				return err
			}
			rows := subjectGrantRows(grants, namespace)
			if done, err := managerkit.Render(cmd.OutOrStdout(), output, rows); done || err != nil {
				return err
			}
			printSubjectGrantTable(cmd, rows)
			return nil
		},
	}
	kit.AddOutputFlag(cmd, &output)
	kit.AddFilterFlags(cmd, &filter)
	kit.AddClusterFlag(cmd, &cluster)
	cmd.Flags().StringVar(&namespace, "namespace", "", "only grants effective in this namespace: its own and the cluster-wide ones")
	return cmd
}

// parseSubjectRef parses "Kind:Name" (or "ServiceAccount:ns/name").
func parseSubjectRef(s string) (rbac.SubjectRef, error) {
	kindStr, rest, ok := strings.Cut(s, ":")
	if !ok || kindStr == "" || rest == "" {
		return rbac.SubjectRef{}, fmt.Errorf("invalid subject %q: expected Kind:Name (e.g. User:alice, Group:devs, ServiceAccount:ns/name)", s)
	}
	kind, err := normalizeSubjectKind(kindStr)
	if err != nil {
		return rbac.SubjectRef{}, err
	}
	ref := rbac.SubjectRef{Kind: kind}
	if kind == "ServiceAccount" {
		ns, nm, ok := strings.Cut(rest, "/")
		if !ok || ns == "" || nm == "" {
			return rbac.SubjectRef{}, fmt.Errorf("invalid ServiceAccount subject %q: expected ServiceAccount:namespace/name", s)
		}
		ref.Namespace, ref.Name = ns, nm
	} else {
		ref.Name = rest
	}
	return ref, nil
}

func normalizeSubjectKind(s string) (string, error) {
	switch strings.ToLower(s) {
	case "user":
		return "User", nil
	case "group":
		return "Group", nil
	case "serviceaccount", "sa":
		return "ServiceAccount", nil
	default:
		return "", fmt.Errorf("unknown subject kind %q: use User, Group, or ServiceAccount", s)
	}
}

// subjectGrantRows are the grants effective in namespace, or all of them when no namespace is named,
// ordered by cluster, then role. A cluster-wide grant is effective in every namespace.
func subjectGrantRows(grants []subjectGrantRow, namespace string) []subjectGrantRow {
	rows := make([]subjectGrantRow, 0, len(grants))
	for _, g := range grants {
		if namespace != "" && g.Scope != "" && g.Scope != namespace {
			continue
		}
		rows = append(rows, g)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Cluster != rows[j].Cluster {
			return rows[i].Cluster < rows[j].Cluster
		}
		return rows[i].RoleRef < rows[j].RoleRef
	})
	return rows
}

func printSubjectGrantTable(cmd *cobra.Command, rows []subjectGrantRow) {
	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "CLUSTER\tROLE\tKIND\tSCOPE\tBINDING\tUNIT")
	for _, r := range rows {
		scope := r.Scope
		if scope == "" {
			scope = "cluster-wide"
		}
		role := r.RoleRef
		if !r.Resolved {
			role += " (unresolved)"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", r.Cluster, role, r.RoleRefKind, scope, r.Binding, r.Unit)
	}
	_ = tw.Flush()
	kit.Fprintln(cmd.OutOrStdout(), fmt.Sprintf("\n%d grants", len(rows)))
}
