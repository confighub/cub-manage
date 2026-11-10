// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

// The namespace profile library. Most of what this group fixes is computed per
// namespace -- the level 'pod-security set' sets, the template backfill copies --
// but holding a component's namespace name to one value in every variant is the
// same edit wherever it is made.

import (
	"github.com/spf13/cobra"

	api "github.com/confighub/sdk/core/function/api"
	"github.com/confighub/sdk/managerkit/profiles"

	"github.com/confighub/cub-manage/internal/cub"
	"github.com/confighub/cub-manage/internal/kit"
)

// namespacedKinds scopes fleet-edit to the Units holding a resource a namespace
// applies to.
const namespacedKinds = "kind IN ('Namespace', 'Deployment', 'StatefulSet', 'DaemonSet', 'ReplicaSet', 'Job', 'CronJob', " +
	"'Pod', 'Service', 'ConfigMap', 'Secret', 'ServiceAccount', 'Role', 'RoleBinding', 'NetworkPolicy', " +
	"'PodDisruptionBudget', 'HorizontalPodAutoscaler', 'Ingress', 'PersistentVolumeClaim')"

// Library is this command group's profile library. Its profiles are installed
// with every other group's by the top-level 'profile install'.
var Library = profiles.Library{
	Tool:           "cub-manage",
	Target:         "a Unit",
	ParamExample:   "--param namespace=shop for namespace-set",
	Preflight:      cub.Preflight,
	InvocationName: kit.RootName,
	FleetEdit: profiles.FleetEdit{
		WhereData: namespacedKinds,
		Scope:     "Units with namespaced resources",
		Example:   `fleet-edit --profile namespace-set --component shop --param namespace=shop --commit --change-desc "…"`,
	},
	Profiles: []profiles.Spec{
		{
			Slug: "namespace-set",
			Description: "set-namespace: move a Unit's resources to a namespace, with the RBAC subjects and " +
				"Service DNS names that refer to it -- the fix for namespace-name-inconsistent",
			Function: "set-namespace",
			Params:   []profiles.Param{{Name: "namespace"}},
			Args:     []api.FunctionArgument{profiles.TmplArg("namespace-name", "namespace")},
		},
	},
}

func newFleetEditCmd() *cobra.Command { return Library.FleetEditCommand() }
