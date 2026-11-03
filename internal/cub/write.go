// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cub

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/confighub/sdk/core/cubapi"
	api "github.com/confighub/sdk/core/function/api"
	goclientnew "github.com/confighub/sdk/core/openapi/goclient-new"
)

// InvokeMutation runs a mutating function over the Units matched by sel. An empty
// ch.Description is a dry-run — the server previews the mutations without writing;
// a non-empty one commits, recording ch.Description on the new revision. With
// ch.Diff the returned Result carries each Unit's diff. Units are edited, never
// applied to a cluster (that is a separate apply step).
func InvokeMutation(ctx context.Context, c *cubapi.Client, fn string, args []api.FunctionArgument, sel cubapi.Selector, ch cubapi.Change) (*cubapi.Result, error) {
	return cubapi.InvokeFunction(ctx, c,
		api.FunctionInvocation{FunctionName: fn, Arguments: args}, sel, ch)
}

// InvokeMutations runs several mutating functions over the Units matched by sel
// in one request, each on the output of the one before: one Revision per Unit on
// commit, and on a dry run one diff of their combined effect.
func InvokeMutations(ctx context.Context, c *cubapi.Client, fns []api.FunctionInvocation, sel cubapi.Selector, ch cubapi.Change) (*cubapi.Result, error) {
	return cubapi.InvokeFunctions(ctx, c, fns, sel, ch)
}

// MutateUnitYQ runs the mutating set-yq function over the Units matched by sel
// with the given yq expression, on the resources whereResource selects (see
// WhereResourceTypes). Dry-run/commit follows ch.Description as above.
func MutateUnitYQ(ctx context.Context, c *cubapi.Client, yqExpr, whereResource string, sel cubapi.Selector, ch cubapi.Change) (*cubapi.Result, error) {
	return cubapi.InvokeFunction(ctx, c,
		api.FunctionInvocation{
			FunctionName:  "set-yq",
			Arguments:     []api.FunctionArgument{{ParameterName: "yq-expression", Value: yqExpr}},
			WhereResource: whereResource,
		}, sel, ch)
}

// CreateUnit creates a new Unit (e.g. a generated PodDisruptionBudget) in the
// Space identified by u.SpaceID and writes data as its configuration. Configuration
// is not a field of a Unit, so it goes in a second call to the Unit's data endpoint;
// the returned Unit is the one that write produced, so its HeadRevisionNum names the
// revision holding the configuration. The unit is created but not applied — deploying
// it to a cluster is a separate apply step. Set u.LastChangeDescription to record
// the reason on the revisions.
func CreateUnit(ctx context.Context, c *cubapi.Client, u goclientnew.Unit, data string) (*goclientnew.Unit, error) {
	res, err := c.API.CreateUnitWithResponse(ctx, u.SpaceID, &goclientnew.CreateUnitParams{}, u)
	if cubapi.IsAPIError(err, res) {
		return nil, cubapi.InterpretErrorGeneric(err, res)
	}
	if res.JSON200 == nil {
		return nil, fmt.Errorf("unexpected response from create unit API")
	}
	// A write to a Unit answers with the operation's result; the Unit is inside it.
	created := res.JSON200.Unit
	if created == nil {
		return nil, fmt.Errorf("create unit returned no unit")
	}
	if data == "" {
		return created, nil
	}
	return PutUnitData(ctx, c, created.SpaceID, created.UnitID, data, u.LastChangeDescription)
}

// PutUnitData writes a Unit's configuration through the Unit's data endpoint,
// recording changeDesc on the revision it cuts.
func PutUnitData(ctx context.Context, c *cubapi.Client, spaceID, unitID goclientnew.UUID, data, changeDesc string) (*goclientnew.Unit, error) {
	params := &goclientnew.UploadUnitDataParams{}
	if changeDesc != "" {
		params.LastChangeDescription = &changeDesc
	}
	res, err := c.API.UploadUnitDataWithBodyWithResponse(ctx, spaceID, unitID, params,
		"application/octet-stream", strings.NewReader(data))
	if cubapi.IsAPIError(err, res) {
		return nil, cubapi.InterpretErrorGeneric(err, res)
	}
	if res.JSON200 == nil || res.JSON200.Unit == nil {
		return nil, fmt.Errorf("unexpected response from unit data API")
	}
	return res.JSON200.Unit, nil
}

// InvokeSetPath runs the set-path function (path + YAML value) over the Units
// matched by sel — find-or-append a document at a path (e.g. a sidecar container
// at spec.template.spec.containers.?name=<x>), on the resources whereResource
// selects.
func InvokeSetPath(ctx context.Context, c *cubapi.Client, path, valueYAML, whereResource string, sel cubapi.Selector, ch cubapi.Change) (*cubapi.Result, error) {
	return cubapi.InvokeFunction(ctx, c,
		api.FunctionInvocation{
			FunctionName:  "set-path",
			Arguments:     []api.FunctionArgument{{Value: path}, {Value: valueYAML}},
			WhereResource: whereResource,
		}, sel, ch)
}

// BulkCloneUnits clones the Units matched by srcWhere into the Space(s) matched
// by destWhere, via the BulkCreateUnits API, applying a no-op merge patch (the
// clones are re-homed afterwards with set-namespace). Unlike a function
// invocation, unit creation has no server-side dry-run, so callers preview the
// plan client-side and only call this on commit. allowExists makes re-runs
// idempotent (an already-present clone is reported, not re-created). The clones
// are created but not applied to a cluster.
func BulkCloneUnits(ctx context.Context, c *cubapi.Client, srcWhere, destWhere string, allowExists bool) ([]goclientnew.UnitCreateOrUpdateResponse, error) {
	include := "SpaceID,TargetID,UpstreamUnitID"
	params := &goclientnew.BulkCreateUnitsParams{
		Where:      &srcWhere,
		WhereSpace: &destWhere,
		Include:    &include,
	}
	if allowExists {
		t := "true"
		params.AllowExists = &t
	}
	res, err := c.API.BulkCreateUnitsWithBodyWithResponse(ctx, params,
		"application/merge-patch+json", bytes.NewReader([]byte("null")))
	if cubapi.IsAPIError(err, res) {
		return nil, cubapi.InterpretErrorGeneric(err, res)
	}
	switch {
	case res.JSON200 != nil:
		return *res.JSON200, nil
	case res.JSON207 != nil:
		return *res.JSON207, nil
	}
	return nil, fmt.Errorf("unexpected response from bulk create units API")
}

// UnitSelector selects one Unit by its Space and slug.
func UnitSelector(spaceID goclientnew.UUID, unitSlug string) cubapi.Selector {
	return cubapi.Selector{Where: fmt.Sprintf("SpaceID = '%s' AND Slug = '%s'", spaceID.String(), unitSlug)}
}

// A Unit can hold several resources, and a function runs on every one unless an
// invocation's WhereResource narrows it: an edit to a pod template would create
// spec.template in the Service beside a Deployment. Edits to one kind of
// resource pass WhereResourceTypes of the types they edit.

// PodTemplateResourceTypes are the resource types whose pod template is at
// spec.template.
var PodTemplateResourceTypes = []string{
	"apps/v1/Deployment", "apps/v1/StatefulSet", "apps/v1/DaemonSet", "apps/v1/ReplicaSet", "batch/v1/Job",
}

// HPAResourceTypes are the HorizontalPodAutoscaler versions.
var HPAResourceTypes = []string{
	"autoscaling/v1/HorizontalPodAutoscaler", "autoscaling/v2/HorizontalPodAutoscaler",
	"autoscaling/v2beta1/HorizontalPodAutoscaler", "autoscaling/v2beta2/HorizontalPodAutoscaler",
}

// NetworkPolicyResourceType is the NetworkPolicy resource type.
const NetworkPolicyResourceType = "networking.k8s.io/v1/NetworkPolicy"

// WhereResourceTypes is a WhereResource expression selecting the resources of the
// given types.
func WhereResourceTypes(types ...string) string {
	quoted := make([]string, len(types))
	for i, t := range types {
		quoted[i] = "'" + t + "'"
	}
	return "ConfigHub.ResourceType IN (" + strings.Join(quoted, ", ") + ")"
}
