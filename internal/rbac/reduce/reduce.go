// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

// Package reduce answers who-can and access in the ConfigHub server, as a reduce over each
// cluster, rather than by reading every RBAC resource in the fleet into this process.
//
// The map, records.star, turns each Role, ClusterRole and binding into a record. The reducers,
// whocan.star and access.star with cluster.star before each, run once per cluster on its records
// and return only the grants. They mirror the rbac package's Go, which the tests check them
// against.
package reduce

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/confighub/sdk/core/cubapi"
	"github.com/confighub/sdk/core/function/api"

	"github.com/confighub/cub-manage/internal/rbac"
)

var (
	//go:embed records.star
	recordsProgram string
	//go:embed cluster.star
	clusterProgram string
	//go:embed whocan.star
	whoCanProgram string
	//go:embed access.star
	accessProgram string
)

// rbacResources is the WhereResource of the map: the resources who-can and access read.
const rbacResources = "ConfigHub.ResourceType IN ('rbac.authorization.k8s.io/v1/Role', " +
	"'rbac.authorization.k8s.io/v1/ClusterRole', 'rbac.authorization.k8s.io/v1/RoleBinding', " +
	"'rbac.authorization.k8s.io/v1/ClusterRoleBinding')"

// Grant is one subject's access to what a who-can query asks about.
type Grant struct {
	Cluster     string `json:"cluster"`
	Subject     string `json:"subject"`
	Scope       string `json:"scope,omitempty"` // "" = cluster-wide
	RoleRef     string `json:"roleRef"`
	RoleRefKind string `json:"roleRefKind"`
	Builtin     bool   `json:"builtin,omitempty"`
	Binding     string `json:"binding"`
	Unit        string `json:"unit"`
}

// SubjectGrant is one binding that names the subject an access query asks about.
type SubjectGrant struct {
	Cluster     string `json:"cluster"`
	RoleRef     string `json:"roleRef"`
	RoleRefKind string `json:"roleRefKind"`
	Resolved    bool   `json:"resolved"` // whether the cluster has the role
	Scope       string `json:"scope,omitempty"`
	Binding     string `json:"binding"`
	Unit        string `json:"unit"`
}

// WhoCan finds every subject that can perform the query's action, on each cluster the Units where
// selects are released to, or on the one named cluster.
func WhoCan(ctx context.Context, c *cubapi.Client, where, cluster string, query rbac.AccessQuery) ([]Grant, error) {
	return reduceClusters[Grant](ctx, c, where, cluster, whoCanProgram,
		"verb="+query.Verb, "resource="+query.Resource, "apiGroup="+query.APIGroup,
		"namespace="+query.Namespace, "name="+query.Name)
}

// Access finds every binding that names subject, on each cluster the Units where selects are
// released to, or on the one named cluster.
func Access(ctx context.Context, c *cubapi.Client, where, cluster string, subject rbac.SubjectRef) ([]SubjectGrant, error) {
	return reduceClusters[SubjectGrant](ctx, c, where, cluster, accessProgram,
		"kind="+subject.Kind, "name="+subject.Name, "namespace="+subject.Namespace)
}

// MapInvocation is the map of who-can and access.
func MapInvocation() api.FunctionInvocation {
	return api.FunctionInvocation{
		FunctionName:  "get-starlark",
		Arguments:     []api.FunctionArgument{{Value: recordsProgram}},
		WhereResource: rbacResources,
	}
}

// ReducerInvocation is a reducer of who-can or access, with its parameters as key=value strings.
func ReducerInvocation(program string, params ...string) api.FunctionInvocation {
	args := []api.FunctionArgument{{Value: clusterProgram + "\n" + program}}
	for _, p := range params {
		args = append(args, api.FunctionArgument{Value: p})
	}
	return api.FunctionInvocation{FunctionName: "reduce-starlark", Arguments: args}
}

// GroupByCluster keys each record by its Unit's release Target, a cluster: by ID, since a Target's
// slug is unique only within its Space. A Unit with no release Target holds definitions that deploy
// nowhere, which the analysis leaves out, so its records are in no group. A named cluster keeps only
// the Targets with that slug.
func GroupByCluster(cluster string) string {
	if cluster == "" {
		return `row.Origin.TargetID == null ? [] : [row.Origin.TargetID]`
	}
	quoted, _ := json.Marshal(cluster) // a JSON string is a CEL string literal
	return `row.Origin.TargetID == null || row.Origin.TargetSlug != ` + string(quoted) + ` ? [] : [row.Origin.TargetID]`
}

func reduceClusters[T any](ctx context.Context, c *cubapi.Client, where, cluster, program string, params ...string) ([]T, error) {
	response, err := cubapi.Reduce(ctx, c, cubapi.Selector{Where: where, WhereResource: rbacResources},
		[]api.FunctionInvocation{MapInvocation()}, GroupByCluster(cluster), ReducerInvocation(program, params...))
	if err != nil {
		return nil, err
	}
	// A Unit left out could hold the binding that grants access, so an answer without it is wrong
	// rather than partial.
	if len(response.UnitErrors) > 0 {
		var units []string
		for _, unitError := range response.UnitErrors {
			units = append(units, fmt.Sprintf("%s/%s: %s", unitError.SpaceSlug, unitError.UnitSlug,
				strings.Join(unitError.ErrorMessages, "; ")))
		}
		return nil, fmt.Errorf("could not read the RBAC resources of %d units:\n  %s",
			len(units), strings.Join(units, "\n  "))
	}
	var results []T
	for _, group := range response.Groups {
		values, err := cubapi.ReduceGroupValues(group)
		if err != nil {
			return nil, err
		}
		decoded, err := DecodeValues[T](values)
		if err != nil {
			return nil, err
		}
		results = append(results, decoded...)
	}
	return results, nil
}

// DecodeValues reads each value a reducer returned as a T.
func DecodeValues[T any](values api.AttributeValueList) ([]T, error) {
	results := make([]T, 0, len(values))
	for _, value := range values {
		encoded, err := json.Marshal(value.Value)
		if err != nil {
			return nil, err
		}
		var result T
		if err := json.Unmarshal(encoded, &result); err != nil {
			return nil, fmt.Errorf("read a value the reducer returned: %w", err)
		}
		results = append(results, result)
	}
	return results, nil
}

// Programs returns the who-can and access reducer programs, for running them elsewhere, such as
// in the tests.
func Programs() (whoCan, access string) {
	return whoCanProgram, accessProgram
}
