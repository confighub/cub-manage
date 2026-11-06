// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

// The scheduling profile library. The library machinery -- the Space, the stored
// Invocations, the parameter binding, and the install/list/apply and fleet-edit
// commands -- is managerkit/profiles. What is scheduling-specific is which
// profiles are seeded and what the help text calls them.

import (
	"strings"

	"github.com/spf13/cobra"

	api "github.com/confighub/sdk/core/function/api"
	"github.com/confighub/sdk/managerkit/profiles"

	"github.com/confighub/cub-manage/internal/cub"
	"github.com/confighub/cub-manage/internal/kit"
	"github.com/confighub/cub-manage/internal/scheduling"
)

// workloadKindsWhereData scopes fleet-edit to pod-bearing workload Units, so a
// scheduling profile never reaches a Unit with no pod template to edit.
const workloadKindsWhereData = "kind IN ('Deployment', 'StatefulSet', 'DaemonSet', 'ReplicaSet', 'Job', 'CronJob', 'Pod')"

const gpuYQ = `.spec.template.spec.nodeSelector.pool = "gpu" | .spec.template.spec.tolerations = [{"key": "nvidia.com/gpu", "operator": "Exists", "effect": "NoSchedule"}]`
const spotYQ = `.spec.template.spec.nodeSelector.pool = "spot" | .spec.template.spec.tolerations = [{"key": "spot", "operator": "Equal", "value": "true", "effect": "NoSchedule"}]`
const nodePoolYQ = `.spec.template.spec.nodeSelector.pool = $params.pool`

// topologySpreadSoftYQ spreads replicas evenly across zones and then nodes,
// preferring rather than requiring it, with each Deployment revision spread on
// its own. It is the explicit form of a cluster-level default for clusters whose
// scheduler configuration cannot be changed, and replaces only the constraints
// on those two keys.
var topologySpreadSoftYQ = strings.Join([]string{
	upsertSpreadYQ(spreadConstraint{topologyKey: scheduling.ZoneKey, maxSkew: 1, whenUnsatisfiable: scheduling.ScheduleAnyway, matchLabelKeys: []string{"pod-template-hash"}}),
	upsertSpreadYQ(spreadConstraint{topologyKey: scheduling.HostnameKey, maxSkew: 1, whenUnsatisfiable: scheduling.ScheduleAnyway, matchLabelKeys: []string{"pod-template-hash"}}),
}, " | ")

// antiAffinitySoftYQ adds preferred pod anti-affinity across nodes against the
// workload's own Pods.
var antiAffinitySoftYQ = antiAffinityYQ(scheduling.HostnameKey, false, 100, false)

// podTemplates confines a scheduling profile to the resources with a pod
// template, so that it never writes one into another resource in the same Unit.
var podTemplates = cub.WhereResourceTypes(cub.PodTemplateResourceTypes...)

// Library is this command group's profile library. Its profiles are installed
// with every other group's by the top-level 'profile install'.
var Library = profiles.Library{
	Tool:           "cub-manage",
	Noun:           "scheduling profile",
	Target:         "a workload",
	ParamExample:   "--param pool=gpu for node-pool",
	Preflight:      cub.Preflight,
	InvocationName: kit.RootName,
	FleetEdit: profiles.FleetEdit{
		WhereData: workloadKindsWhereData,
		Scope:     "pod-bearing workload kinds",
		Example:   `fleet-edit --profile placement-gpu --environment prod --component ml --commit --change-desc "…"`,
	},
	Profiles: []profiles.Spec{
		{
			Slug:          "placement-gpu",
			Description:   "set-yq: pin to the gpu node pool + tolerate the nvidia.com/gpu taint",
			Function:      "set-yq",
			Args:          []api.FunctionArgument{profiles.Arg("yq-expression", gpuYQ)},
			WhereResource: podTemplates,
		},
		{
			Slug:          "placement-spot",
			Description:   "set-yq: pin to the spot node pool + tolerate the spot taint",
			Function:      "set-yq",
			Args:          []api.FunctionArgument{profiles.Arg("yq-expression", spotYQ)},
			WhereResource: podTemplates,
		},
		{
			Slug:        "node-pool",
			Description: "set-yq: pin nodeSelector.pool to the given pool (param: pool)",
			Function:    "set-yq",
			Args: []api.FunctionArgument{
				profiles.Arg("yq-expression", nodePoolYQ),
				profiles.YQParamArg("pool"),
			},
			Params:        []profiles.Param{{Name: "pool"}},
			WhereResource: podTemplates,
		},
		{
			Slug:          "topology-spread-soft",
			Description:   "set-yq: spread replicas evenly across zones, then nodes (maxSkew 1, ScheduleAnyway, per Deployment revision)",
			Function:      "set-yq",
			Args:          []api.FunctionArgument{profiles.Arg("yq-expression", topologySpreadSoftYQ)},
			WhereResource: podTemplates,
		},
		{
			Slug:          "anti-affinity-soft",
			Description:   "set-yq: preferred pod anti-affinity across nodes (selector from pod-template labels)",
			Function:      "set-yq",
			Args:          []api.FunctionArgument{profiles.Arg("yq-expression", antiAffinitySoftYQ)},
			WhereResource: podTemplates,
		},
	},
}

func newFleetEditCmd() *cobra.Command { return Library.FleetEditCommand() }
