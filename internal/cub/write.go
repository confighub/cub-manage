// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cub

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/confighub/sdk/core/cubapi"
	api "github.com/confighub/sdk/core/function/api"
	goclientnew "github.com/confighub/sdk/core/openapi/goclient-new"
	"sigs.k8s.io/yaml"
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

// Creation is a Unit a command creates, with the configuration it is created
// with. Unit names the Space by SpaceID; SpaceSlug is how the report names it.
type Creation struct {
	SpaceSlug string
	Unit      goclientnew.Unit
	Data      string
}

// EnsureUnits makes each Unit hold its configuration: it creates the Units that
// do not exist and replaces the configuration of those that do, so running a
// command twice changes nothing the second time. On a dry run (an empty
// ch.Description) the server checks each write without keeping it; for a Unit
// that would be created, the outcome describes the resources it would hold. The
// outcomes read like a function invocation's, so the command reports them
// through the shared write report, and a Unit that cannot be written is
// reported, not fatal to the others. The Units are written, never applied to a
// cluster.
func EnsureUnits(ctx context.Context, c *cubapi.Client, creations []Creation, ch cubapi.Change) *cubapi.Result {
	res := &cubapi.Result{DryRun: ch.DryRun()}
	for _, cr := range creations {
		o := cubapi.UnitOutcome{SpaceID: cr.Unit.SpaceID.String(), SpaceSlug: cr.SpaceSlug, UnitSlug: cr.Unit.Slug}
		existing, err := ResolveUnit(ctx, c, cr.SpaceSlug, cr.Unit.Slug)
		switch {
		case err == nil:
			updated, err := UpdateUnitData(ctx, c, existing, cr.Data, "", ch)
			if err != nil {
				o.UnitID, o.Error = existing.UnitID.String(), err.Error()
			} else {
				o = updated.Outcomes[0]
			}
		case errors.Is(err, errUnitNotFound):
			o = createWithData(ctx, c, cr, ch)
		default:
			o.Error = err.Error()
		}
		res.Outcomes = append(res.Outcomes, o)
	}
	return res
}

func createWithData(ctx context.Context, c *cubapi.Client, cr Creation, ch cubapi.Change) cubapi.UnitOutcome {
	o := cubapi.UnitOutcome{
		SpaceID: cr.Unit.SpaceID.String(), SpaceSlug: cr.SpaceSlug, UnitSlug: cr.Unit.Slug, HasMutations: true,
	}
	u := cr.Unit
	u.LastChangeDescription = ch.Description
	created, err := createUnit(ctx, c, u, ch.DryRun())
	if err != nil {
		o.Error = err.Error()
		return o
	}
	o.UnitID = created.UnitID.String()
	if ch.DryRun() {
		if o.Diff, err = addedDiff(cr.Data); err != nil {
			o.Error = err.Error()
			return o
		}
		o.Success = true
		return o
	}
	written, err := uploadUnitData(ctx, c, created.SpaceID, created.UnitID, cr.Data, ch)
	if err != nil {
		o.Error = fmt.Sprintf("created, but writing its configuration failed: %v", err)
		return o
	}
	o.Success, o.Diff = true, written.Diff
	if written.Conflicts != nil {
		o.Conflicts = *written.Conflicts
	}
	return o
}

func createUnit(ctx context.Context, c *cubapi.Client, u goclientnew.Unit, dryRun bool) (*goclientnew.Unit, error) {
	params := &goclientnew.CreateUnitParams{}
	if dryRun {
		params.DryRun = &dryRun
	}
	res, err := c.API.CreateUnitWithResponse(ctx, u.SpaceID, params, u)
	if cubapi.IsAPIError(err, res) {
		return nil, cubapi.InterpretErrorGeneric(err, res)
	}
	// A write to a Unit answers with the operation's result; the Unit is inside it.
	if res.JSON200 == nil || res.JSON200.Unit == nil {
		return nil, fmt.Errorf("create unit returned no unit")
	}
	return res.JSON200.Unit, nil
}

// UpdateUnitData replaces a Unit's configuration with data, or on a dry run (an
// empty ch.Description) reports what replacing it would change. ifMatch is the
// DataHash of the configuration data was made from, so a write that landed in
// between is refused rather than overwritten; empty, the write is unconditional.
// The outcome reads like a function invocation's, for the shared write report.
func UpdateUnitData(ctx context.Context, c *cubapi.Client, ref UnitRef, data, ifMatch string, ch cubapi.Change) (*cubapi.Result, error) {
	written, err := uploadUnitDataIfMatch(ctx, c, ref.SpaceID, ref.UnitID, data, ifMatch, ch)
	if err != nil {
		return nil, err
	}
	o := cubapi.UnitOutcome{
		UnitID: ref.UnitID.String(), SpaceID: ref.SpaceID.String(), SpaceSlug: ref.SpaceSlug, UnitSlug: ref.UnitSlug,
		Success: true, Diff: written.Diff,
	}
	o.HasMutations = o.Diff != nil && len(o.Diff.Resources) > 0
	if written.Conflicts != nil {
		o.Conflicts = *written.Conflicts
	}
	return &cubapi.Result{DryRun: ch.DryRun(), Outcomes: []cubapi.UnitOutcome{o}}, nil
}

func uploadUnitData(ctx context.Context, c *cubapi.Client, spaceID, unitID goclientnew.UUID, data string, ch cubapi.Change) (*goclientnew.UnitCreateOrUpdateResponse, error) {
	return uploadUnitDataIfMatch(ctx, c, spaceID, unitID, data, "", ch)
}

func uploadUnitDataIfMatch(ctx context.Context, c *cubapi.Client, spaceID, unitID goclientnew.UUID, data, ifMatch string, ch cubapi.Change) (*goclientnew.UnitCreateOrUpdateResponse, error) {
	include := "Diff"
	params := &goclientnew.UploadUnitDataParams{Include: &include}
	if ch.DryRun() {
		dryRun := true
		params.DryRun = &dryRun
	} else {
		params.LastChangeDescription = &ch.Description
	}
	if ifMatch != "" {
		params.IfMatch = &ifMatch
	}
	gp, err := ch.GuardParams()
	if err != nil {
		return nil, err
	}
	params.Guards, params.InheritedGuards, params.Clearance = gp.Guards, gp.InheritedGuards, gp.Clearance
	res, err := c.API.UploadUnitDataWithBodyWithResponse(ctx, spaceID, unitID, params,
		"application/octet-stream", strings.NewReader(data))
	if cubapi.IsAPIError(err, res) {
		return nil, cubapi.InterpretErrorGeneric(err, res)
	}
	if res.JSON200 == nil || res.JSON200.Unit == nil {
		return nil, fmt.Errorf("unexpected response from unit data API")
	}
	return res.JSON200, nil
}

// addedDiff describes the resources in a new Unit's configuration as added, as
// the server describes them once the configuration is written. A dry-run create
// keeps nothing, so there is no Unit to write the configuration to and ask.
func addedDiff(data string) (*goclientnew.ConfigDiff, error) {
	add := goclientnew.Add
	diff := &goclientnew.ConfigDiff{}
	for _, doc := range splitDocuments(data) {
		var meta struct {
			APIVersion string `json:"apiVersion"`
			Kind       string `json:"kind"`
			Metadata   struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"metadata"`
		}
		if err := yaml.Unmarshal([]byte(doc), &meta); err != nil {
			return nil, fmt.Errorf("parse generated configuration: %w", err)
		}
		name := meta.Metadata.Name
		if meta.Metadata.Namespace != "" {
			name = meta.Metadata.Namespace + "/" + name
		}
		diff.Resources = append(diff.Resources, goclientnew.ResourceDiff{
			Resource:   &goclientnew.ResourceInfo{ResourceType: meta.APIVersion + "/" + meta.Kind, ResourceName: name},
			ChangeType: &add,
			ToValue:    doc,
		})
	}
	return diff, nil
}

// splitDocuments splits YAML into its non-empty documents.
func splitDocuments(data string) []string {
	var docs []string
	for _, d := range strings.Split("\n"+data, "\n---") {
		if d = strings.TrimSpace(d); d != "" {
			docs = append(docs, d+"\n")
		}
	}
	return docs
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
// clones are re-homed afterwards with set-namespace). On a dry run the server
// checks the clones without keeping them. allowExists makes re-runs idempotent
// (an already-present clone is reported, not re-created). The clones are
// created but not applied to a cluster.
func BulkCloneUnits(ctx context.Context, c *cubapi.Client, srcWhere, destWhere string, allowExists, dryRun bool) ([]goclientnew.UnitCreateOrUpdateResponse, error) {
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
	if dryRun {
		params.DryRun = &dryRun
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
