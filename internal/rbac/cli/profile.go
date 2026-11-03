// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

// The RBAC profile library: edits to roles and bindings, as set-yq profiles in
// the shared library. Each one edits the literal YAML in place on the server, so
// comments and formatting survive, and each adds idempotently.

import (
	"github.com/spf13/cobra"

	api "github.com/confighub/sdk/core/function/api"
	"github.com/confighub/sdk/managerkit/profiles"

	"github.com/confighub/cub-manage/internal/cub"
	"github.com/confighub/cub-manage/internal/kit"
)

var (
	roles    = cub.WhereResourceTypes("rbac.authorization.k8s.io/v1/Role", "rbac.authorization.k8s.io/v1/ClusterRole")
	bindings = cub.WhereResourceTypes("rbac.authorization.k8s.io/v1/RoleBinding", "rbac.authorization.k8s.io/v1/ClusterRoleBinding")
)

// The resource each edit changes, selected by kind and name, since a Unit can
// hold several of each. A removal selects only a binding that has subjects:
// mapping over a missing list would write an empty one.
const (
	selectRole    = `select(.kind == $params.roleKind and .metadata.name == $params.role)`
	selectBinding = `select(.kind == $params.bindingKind and .metadata.name == $params.binding)`
)

// yqProfile is a set-yq profile on the resources where selects, with the given
// parameters bound for the expression to read as $params.<name>.
func yqProfile(slug, description, where, expr string, params ...profiles.Param) profiles.Spec {
	args := []api.FunctionArgument{profiles.Arg("yq-expression", expr)}
	for _, p := range params {
		args = append(args, profiles.YQParamArg(p.Name))
	}
	return profiles.Spec{
		Slug: slug, Description: description, Function: "set-yq",
		Args: args, Params: params, WhereResource: where,
	}
}

func param(names ...string) []profiles.Param {
	out := make([]profiles.Param, len(names))
	for i, n := range names {
		out[i] = profiles.Param{Name: n}
	}
	return out
}

var (
	roleParams    = append(param("roleKind", "role"), profiles.Param{Name: "rule", DataType: "int"}, profiles.Param{Name: "verb"})
	subjectParams = param("bindingKind", "binding", "subjectKind", "subject")
	accountParams = param("bindingKind", "binding", "namespace", "serviceAccount")
)

// Library is this command group's profile library. Its profiles are installed
// with every other group's by the top-level 'profile install'.
var Library = profiles.Library{
	Tool:           "cub-manage",
	Target:         "an RBAC Unit",
	ParamExample:   "--param roleKind=ClusterRole --param role=viewer --param rule=0 --param verb=get for rbac-add-verb",
	Preflight:      cub.Preflight,
	InvocationName: kit.RootName,
	FleetEdit: profiles.FleetEdit{
		WhereData: "kind IN ('Role', 'ClusterRole', 'RoleBinding', 'ClusterRoleBinding')",
		Scope:     "Units with roles or bindings",
		Example: `fleet-edit --profile rbac-add-subject --environment prod --param bindingKind=ClusterRoleBinding ` +
			`--param binding=viewers --param subjectKind=Group --param subject=oncall --commit --change-desc "…"`,
	},
	Profiles: []profiles.Spec{
		yqProfile("rbac-add-verb",
			"set-yq: add a verb to a rule of a Role or ClusterRole",
			roles, `(`+selectRole+`.rules[$params.rule | tonumber].verbs) |= ((. + [$params.verb]) | unique)`,
			roleParams...),
		yqProfile("rbac-remove-verb",
			"set-yq: remove a verb from a rule of a Role or ClusterRole",
			roles, `(`+selectRole+`.rules[$params.rule | tonumber].verbs) |= (. - [$params.verb])`,
			roleParams...),
		yqProfile("rbac-add-subject",
			"set-yq: add a User or Group to a RoleBinding or ClusterRoleBinding",
			bindings, selectBinding+`.subjects |= ((. // []) + [{"kind": $params.subjectKind, "name": $params.subject, "apiGroup": "rbac.authorization.k8s.io"}] | unique)`,
			subjectParams...),
		yqProfile("rbac-remove-subject",
			"set-yq: remove a User or Group from a RoleBinding or ClusterRoleBinding",
			bindings, `(`+selectBinding+` | select(has("subjects")) | .subjects) |= map(select((.kind == $params.subjectKind and .name == $params.subject) | not))`,
			subjectParams...),
		yqProfile("rbac-add-service-account",
			"set-yq: add a ServiceAccount to a RoleBinding or ClusterRoleBinding",
			bindings, selectBinding+`.subjects |= ((. // []) + [{"kind": "ServiceAccount", "name": $params.serviceAccount, "namespace": $params.namespace}] | unique)`,
			accountParams...),
		yqProfile("rbac-remove-service-account",
			"set-yq: remove a ServiceAccount from a RoleBinding or ClusterRoleBinding",
			bindings, `(`+selectBinding+` | select(has("subjects")) | .subjects) |= map(select((.kind == "ServiceAccount" and .name == $params.serviceAccount and .namespace == $params.namespace) | not))`,
			accountParams...),
	},
}

func newFleetEditCmd() *cobra.Command { return Library.FleetEditCommand() }
