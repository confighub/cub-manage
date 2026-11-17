// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cub

// A change to a base, a Space whose Units have variants, is made in a ChangeSet
// and promoted by a ChangeOrder: the ChangeSet bounds the base's revisions, and
// the ChangeOrder, adopting its end Tag, carries them to the variants stage by
// stage along the component's ChangeWorkflow. Both are named alike and
// annotated with what the change is for, such as the Trigger whose findings it
// fixes.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/confighub/sdk/core/cubapi"
	goclientnew "github.com/confighub/sdk/core/openapi/goclient-new"
)

// TriggerAnnotation is the annotation on a ChangeSet and ChangeOrder naming the
// Triggers, as space/slug, whose findings the change fixes.
const TriggerAnnotation = "Trigger"

// Rollout names a change made in bases and promoted to their variants.
type Rollout struct {
	// Slug names the ChangeSet and the ChangeOrder in each base.
	Slug        string
	Description string
	Annotations map[string]string
}

// RolloutBase is a base a Rollout is made in, and the ChangeWorkflow promoting it.
type RolloutBase struct {
	SpaceID   goclientnew.UUID
	SpaceSlug string
	Component string
	Workflow  string
	// WorkflowID is nil when the base cannot be promoted: Problem says why.
	WorkflowID *goclientnew.UUID
	Problem    string
}

// PrepareRolloutBase finds the ChangeWorkflow for promoting a base's changes,
// and checks that its Space has neither a ChangeSet nor a ChangeOrder of the
// rollout's name. The workflow is one the base's Component allows: the one
// kept in the base, or the only one. A base it cannot be found for has a
// Problem, and is not changed.
func PrepareRolloutBase(ctx context.Context, c *cubapi.Client, spaceID goclientnew.UUID, slug string) (RolloutBase, error) {
	base := RolloutBase{SpaceID: spaceID}
	space, err := cubapi.ResolveSpace(ctx, c, cubapi.ParseRef(spaceID.String()), cubapi.ResolveOpts{})
	if err != nil {
		return base, err
	}
	base.SpaceSlug = space.Space.Slug
	inSpace := cubapi.Where{}.Eq("SpaceID", spaceID.String()).Eq("Slug", slug)
	if sets, err := cubapi.ListChangeSets(ctx, c, inSpace, cubapi.ListOpts{Select: "ChangeSetID,Slug,SpaceID"}); err != nil {
		return base, err
	} else if len(sets) > 0 {
		base.Problem = fmt.Sprintf("ChangeSet %s/%s exists already; name the rollout with --rollout", base.SpaceSlug, slug)
		return base, nil
	}
	if orders, err := cubapi.ListChangeOrders(ctx, c, inSpace, cubapi.ListOpts{Select: "ChangeOrderID,Slug,SpaceID"}); err != nil {
		return base, err
	} else if len(orders) > 0 {
		base.Problem = fmt.Sprintf("ChangeOrder %s/%s exists already; name the rollout with --rollout", base.SpaceSlug, slug)
		return base, nil
	}
	if space.Space.ComponentID == nil {
		base.Problem = fmt.Sprintf("%s is no Component's variant, so there is no ChangeWorkflow to promote its changes with", base.SpaceSlug)
		return base, nil
	}
	components, err := cubapi.ListComponents(ctx, c, cubapi.Where{}.Eq("ComponentID", space.Space.ComponentID.String()), cubapi.ListOpts{})
	if err != nil {
		return base, err
	}
	if len(components) == 0 || components[0].Component == nil {
		base.Problem = fmt.Sprintf("the Component of %s is not found", base.SpaceSlug)
		return base, nil
	}
	component := components[0].Component
	base.Component = component.Slug
	if len(component.AllowedChangeWorkflowIDs) == 0 {
		base.Problem = fmt.Sprintf("Component %s allows no ChangeWorkflow; create one for its upgrades and allow it", component.Slug)
		return base, nil
	}
	ids := make([]string, 0, len(component.AllowedChangeWorkflowIDs))
	for _, id := range component.AllowedChangeWorkflowIDs {
		ids = append(ids, id.String())
	}
	workflows, err := cubapi.ListChangeWorkflows(ctx, c, cubapi.Where{}.InStrings("ChangeWorkflowID", ids), cubapi.ListOpts{Include: "SpaceID"})
	if err != nil {
		return base, err
	}
	var chosen *goclientnew.ExtendedChangeWorkflow
	for _, w := range workflows {
		if w.ChangeWorkflow != nil && w.ChangeWorkflow.SpaceID == spaceID {
			chosen = w
		}
	}
	if chosen == nil && len(workflows) == 1 {
		chosen = workflows[0]
	}
	if chosen == nil || chosen.ChangeWorkflow == nil {
		base.Problem = fmt.Sprintf("Component %s allows %d ChangeWorkflows and none is kept in %s, so which one promotes its upgrades is not clear", component.Slug, len(workflows), base.SpaceSlug)
		return base, nil
	}
	id := chosen.ChangeWorkflow.ChangeWorkflowID
	base.WorkflowID = &id
	base.Workflow = chosen.ChangeWorkflow.Slug
	if chosen.Space != nil {
		base.Workflow = chosen.Space.Slug + "/" + chosen.ChangeWorkflow.Slug
	}
	return base, nil
}

// CreateChangeSet creates the rollout's ChangeSet in a base.
func CreateChangeSet(ctx context.Context, c *cubapi.Client, spaceID goclientnew.UUID, r Rollout) (*goclientnew.ChangeSet, error) {
	res, err := c.API.CreateChangeSetWithResponse(ctx, spaceID, &goclientnew.CreateChangeSetParams{}, goclientnew.ChangeSet{
		Slug: r.Slug, DisplayName: r.Slug, SpaceID: spaceID, Description: r.Description, Annotations: r.Annotations,
	})
	if cubapi.IsAPIError(err, res) {
		return nil, cubapi.InterpretErrorGeneric(err, res)
	}
	return res.JSON200, nil
}

// CloseChangeSet takes every Unit the ChangeSet is open on out of it.
func CloseChangeSet(ctx context.Context, c *cubapi.Client, changeSetID goclientnew.UUID) error {
	body, err := json.Marshal(map[string]any{"ChangeSetID": nil})
	if err != nil {
		return err
	}
	where := fmt.Sprintf("ChangeSetID = '%s'", changeSetID)
	res, err := c.API.BulkPatchUnitsWithBodyWithResponse(ctx, &goclientnew.BulkPatchUnitsParams{Where: &where},
		"application/merge-patch+json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	var responses []goclientnew.UnitCreateOrUpdateResponse
	switch {
	case res.JSON200 != nil:
		responses = *res.JSON200
	case res.JSON207 != nil:
		responses = *res.JSON207
	default:
		return cubapi.InterpretErrorGeneric(err, res)
	}
	var failures []string
	for _, r := range responses {
		if r.Error != nil {
			failures = append(failures, r.Error.Message)
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("%d Unit(s) could not be taken out of the ChangeSet: %s", len(failures), strings.Join(failures, "; "))
	}
	return nil
}

// CreateChangeOrder names the rollout's change in a base, adopting the end Tag
// of its closed ChangeSet, to be promoted along the base's ChangeWorkflow.
func CreateChangeOrder(ctx context.Context, c *cubapi.Client, base RolloutBase, changeSetID goclientnew.UUID, r Rollout) (*goclientnew.ChangeOrder, error) {
	cs, err := c.API.GetChangeSetWithResponse(ctx, base.SpaceID, changeSetID, &goclientnew.GetChangeSetParams{})
	if cubapi.IsAPIError(err, cs) {
		return nil, cubapi.InterpretErrorGeneric(err, cs)
	}
	if cs.JSON200 == nil || cs.JSON200.ChangeSet == nil {
		return nil, fmt.Errorf("ChangeSet %s/%s not found", base.SpaceSlug, r.Slug)
	}
	res, err := c.API.CreateChangeOrderWithResponse(ctx, base.SpaceID, &goclientnew.CreateChangeOrderParams{}, goclientnew.ChangeOrder{
		Slug: r.Slug, DisplayName: r.Slug, SpaceID: base.SpaceID, Description: r.Description, Annotations: r.Annotations,
		EndTagID: cs.JSON200.ChangeSet.EndTagID, ChangeWorkflowID: base.WorkflowID,
	})
	if cubapi.IsAPIError(err, res) {
		return nil, cubapi.InterpretErrorGeneric(err, res)
	}
	return res.JSON200, nil
}

// TriggerRefs finds the Triggers of the given slugs, as space/slug, in
// policySpace or, when it is empty, wherever the organization has them. A slug
// with no Trigger is left out; one with Triggers in several Spaces is an error.
func TriggerRefs(ctx context.Context, c *cubapi.Client, policySpace string, slugs []string) (map[string]string, error) {
	where := cubapi.Where{}.InStrings("Slug", slugs)
	if policySpace != "" {
		space, err := cubapi.ResolveSpace(ctx, c, cubapi.ParseRef(policySpace), cubapi.ResolveOpts{})
		if err != nil {
			return nil, err
		}
		where = where.Eq("SpaceID", space.Space.SpaceID.String())
	}
	triggers, err := cubapi.ListTriggers(ctx, c, where, cubapi.ListOpts{Include: "SpaceID"})
	if err != nil {
		return nil, err
	}
	refs := map[string]string{}
	for _, t := range triggers {
		if t.Trigger == nil || t.Space == nil {
			continue
		}
		ref := t.Space.Slug + "/" + t.Trigger.Slug
		if have, ok := refs[t.Trigger.Slug]; ok && have != ref {
			return nil, fmt.Errorf("Triggers %s and %s have the same slug; name the policy Space with --policy-space", have, ref)
		}
		refs[t.Trigger.Slug] = ref
	}
	return refs, nil
}
