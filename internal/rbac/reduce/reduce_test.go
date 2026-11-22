// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package reduce

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/confighub/sdk/core/function/api"
	"github.com/confighub/sdk/core/workerapi"
	impl "github.com/confighub/sdk/function-impl"
	"github.com/google/uuid"
	"sigs.k8s.io/yaml"

	"github.com/confighub/cub-manage/internal/rbac"
)

// The reduce programs run in the server, so they are checked against the rbac package's Go by
// running them here, through the same executor the server runs them in, on the same resources: the
// map on each resource's Unit, the reducer on each cluster's rows.

const rbacV1 = "rbac.authorization.k8s.io/v1"

func rule(apiGroups, resources, verbs []string, resourceNames ...string) map[string]any {
	r := map[string]any{"apiGroups": apiGroups, "resources": resources, "verbs": verbs}
	if len(resourceNames) > 0 {
		r["resourceNames"] = resourceNames
	}
	return r
}

func clusterRole(name string, labels map[string]any, rules []any, selectors ...map[string]any) map[string]any {
	doc := map[string]any{"apiVersion": rbacV1, "kind": "ClusterRole",
		"metadata": map[string]any{"name": name, "labels": labels}, "rules": rules}
	if len(selectors) > 0 {
		var sels []any
		for _, s := range selectors {
			sels = append(sels, map[string]any{"matchLabels": s})
		}
		doc["aggregationRule"] = map[string]any{"clusterRoleSelectors": sels}
	}
	return doc
}

func role(name, namespace string, rules []any) map[string]any {
	return map[string]any{"apiVersion": rbacV1, "kind": "Role",
		"metadata": map[string]any{"name": name, "namespace": namespace}, "rules": rules}
}

func binding(kind, name, namespace, refKind, refName string, subjects ...map[string]any) map[string]any {
	md := map[string]any{"name": name}
	if namespace != "" {
		md["namespace"] = namespace
	}
	var subs []any
	for _, s := range subjects {
		subs = append(subs, s)
	}
	return map[string]any{"apiVersion": rbacV1, "kind": kind, "metadata": md,
		"roleRef":  map[string]any{"apiGroup": "rbac.authorization.k8s.io", "kind": refKind, "name": refName},
		"subjects": subs}
}

func subject(kind, name string) map[string]any {
	return map[string]any{"kind": kind, "name": name}
}

func serviceAccount(name, namespace string) map[string]any {
	return map[string]any{"kind": "ServiceAccount", "name": name, "namespace": namespace}
}

// fleet is each cluster's resources, one Unit each.
var fleet = map[string][]map[string]any{
	"prod": {
		clusterRole("secret-reader", nil, []any{rule([]string{""}, []string{"secrets"}, []string{"get", "list"})}),
		binding("ClusterRoleBinding", "ops-secrets", "", "ClusterRole", "secret-reader", subject("Group", "ops")),
		// monitoring aggregates pod-reader, which aggregates log-reader in turn.
		clusterRole("monitoring", nil, []any{}, map[string]any{"aggregate-to": "monitoring"}),
		clusterRole("pod-reader", map[string]any{"aggregate-to": "monitoring", "tier": "pods"},
			[]any{rule([]string{""}, []string{"pods"}, []string{"get", "list", "watch"})},
			map[string]any{"aggregate-to": "pod-reader"}),
		clusterRole("log-reader", map[string]any{"aggregate-to": "pod-reader"},
			[]any{rule([]string{""}, []string{"pods/log"}, []string{"get"})}),
		binding("ClusterRoleBinding", "sre-monitoring", "", "ClusterRole", "monitoring", subject("Group", "sre")),
		role("deployer", "payments", []any{
			rule([]string{"apps"}, []string{"deployments", "deployments/scale"}, []string{"*"}),
			rule([]string{""}, []string{"configmaps"}, []string{"get", "update"}, "payments-config"),
		}),
		binding("RoleBinding", "ci-deployer", "payments", "Role", "deployer",
			serviceAccount("ci", "payments"), subject("User", "alice")),
		binding("RoleBinding", "bob-admin", "payments", "ClusterRole", "cluster-admin", subject("User", "bob")),
		binding("RoleBinding", "eve-reads", "billing", "ClusterRole", "secret-reader", subject("User", "eve")),
		binding("ClusterRoleBinding", "viewers", "", "ClusterRole", "view", subject("Group", "everyone")),
		binding("ClusterRoleBinding", "nodes", "", "ClusterRole", "system:node", subject("Group", "system:nodes")),
		binding("RoleBinding", "orphan", "payments", "Role", "missing", subject("User", "mallory")),
		role("wildcard", "scratch", []any{rule([]string{"*"}, []string{"*"}, []string{"*"})}),
		binding("RoleBinding", "scratch-all", "scratch", "Role", "wildcard", subject("User", "alice")),
	},
	"dev": {
		clusterRole("secret-reader", nil, []any{rule([]string{""}, []string{"secrets"}, []string{"get"})}),
		binding("ClusterRoleBinding", "devs-secrets", "", "ClusterRole", "secret-reader", subject("Group", "devs"), subject("User", "alice")),
		binding("ClusterRoleBinding", "breakglass", "", "ClusterRole", "cluster-admin", subject("Group", "oncall")),
		// The role this names is in prod, not here.
		binding("ClusterRoleBinding", "sre-monitoring", "", "ClusterRole", "monitoring", subject("Group", "sre")),
		// A ClusterRoleBinding cannot name a Role.
		binding("ClusterRoleBinding", "bad-ref", "", "Role", "deployer", subject("User", "alice")),
	},
}

// runLocally runs the map on each resource, as its own Unit, and the reducer on each cluster's rows,
// and returns what the reducer returned, in order of cluster.
func runLocally(t *testing.T, reducer api.FunctionInvocation) api.AttributeValueList {
	t.Helper()
	executor := impl.NewStandardExecutor(nil, true)
	invoke := func(request *api.FunctionInvocationRequest) api.AttributeValueList {
		t.Helper()
		request.FunctionContext.ToolchainType = workerapi.ToolchainKubernetesYAML
		response, err := executor.Invoke(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if !response.Success {
			t.Fatalf("%v failed: %v", request.FunctionInvocations[0].FunctionName, response.ErrorMessages)
		}
		var values api.AttributeValueList
		if output, ok := response.Outputs[api.OutputTypeAttributeValueList]; ok {
			if err := json.Unmarshal(output, &values); err != nil {
				t.Fatal(err)
			}
		}
		return values
	}

	clusters := slices.Sorted(func(yield func(string) bool) {
		for cluster := range fleet {
			if !yield(cluster) {
				return
			}
		}
	})
	var results api.AttributeValueList
	for _, cluster := range clusters {
		targetID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(cluster))
		var rows api.RowList
		for i, doc := range fleet[cluster] {
			data, err := yaml.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			mapInvocation := MapInvocation()
			for _, value := range invoke(&api.FunctionInvocationRequest{
				ConfigData:          string(data),
				FunctionInvocations: api.FunctionInvocationList{mapInvocation},
			}) {
				rows = append(rows, api.Row{AttributeValue: value, Origin: api.RowOrigin{
					UnitSlug:    unitSlug(cluster, i),
					UnitLabels:  map[string]string{},
					SpaceLabels: map[string]string{},
					TargetID:    &targetID,
					TargetSlug:  cluster,
				}})
			}
		}
		if len(rows) == 0 {
			continue
		}
		results = append(results, invoke(&api.FunctionInvocationRequest{
			FunctionInvocations: api.FunctionInvocationList{reducer},
			Rows:                rows,
			GroupKey:            targetID.String(),
		})...)
	}
	return results
}

func unitSlug(cluster string, i int) string {
	return fmt.Sprintf("%s-unit-%d", cluster, i)
}

// goClusters is the fleet as the rbac package reads it.
func goClusters() map[string]*rbac.ClusterRbac {
	var resources []rbac.FleetResource
	for cluster, docs := range fleet {
		for i, doc := range docs {
			// The snapshot loader hands the rbac package JSON-decoded documents.
			encoded, _ := json.Marshal(doc)
			var decoded any
			_ = json.Unmarshal(encoded, &decoded)
			resources = append(resources, rbac.FleetResource{
				Origin: rbac.ResourceOrigin{Cluster: cluster, UnitSlug: unitSlug(cluster, i)},
				Doc:    decoded,
			})
		}
	}
	return rbac.BuildClusterRbac(resources)
}

func sortedJSON[T any](t *testing.T, items []T) []string {
	t.Helper()
	var out []string
	for _, item := range items {
		encoded, err := json.Marshal(item)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, string(encoded))
	}
	slices.Sort(out)
	return out
}

func TestWhoCanMatchesTheGoEngine(t *testing.T) {
	whoCan, _ := Programs()
	clusters := goClusters()
	total := 0
	defer func() {
		// The comparison means something only if the queries find grants.
		if total < 20 {
			t.Errorf("the queries found %d grants in all; the fixture should give them more to find", total)
		}
	}()
	for _, query := range []rbac.AccessQuery{
		{Verb: "get", Resource: "secrets"},
		{Verb: "list", Resource: "secrets"},
		{Verb: "get", Resource: "secrets", Namespace: "billing"},
		{Verb: "get", Resource: "secrets", Namespace: "payments"},
		{Verb: "delete", Resource: "secrets"},
		{Verb: "get", Resource: "pods"},
		{Verb: "get", Resource: "pods/log"},
		{Verb: "watch", Resource: "pods/log"},
		{Verb: "update", Resource: "deployments", APIGroup: "apps"},
		{Verb: "update", Resource: "deployments/scale", APIGroup: "apps", Namespace: "payments"},
		{Verb: "update", Resource: "deployments"},
		{Verb: "update", Resource: "configmaps", Namespace: "payments"},
		{Verb: "update", Resource: "configmaps", Name: "payments-config"},
		{Verb: "update", Resource: "configmaps", Name: "other-config"},
		{Verb: "create", Resource: "widgets", APIGroup: "example.com", Namespace: "scratch"},
		{Verb: "get", Resource: "nodes"},
	} {
		t.Run(fmt.Sprintf("%s %s %s ns=%s name=%s", query.Verb, query.APIGroup, query.Resource, query.Namespace, query.Name), func(t *testing.T) {
			var want []Grant
			for _, g := range rbac.WhoCan(clusters, query) {
				want = append(want, Grant{
					Cluster: g.Cluster, Subject: g.SubjectKey, Scope: g.Scope, RoleRef: g.RoleRefName,
					RoleRefKind: g.Binding.RoleRef.Kind, Builtin: g.ViaBuiltinRole, Binding: g.Binding.Name,
					Unit: g.Binding.Origin.UnitSlug,
				})
			}
			total += len(want)
			got, err := DecodeValues[Grant](runLocally(t, ReducerInvocation(whoCan,
				"verb="+query.Verb, "resource="+query.Resource, "apiGroup="+query.APIGroup,
				"namespace="+query.Namespace, "name="+query.Name)))
			if err != nil {
				t.Fatal(err)
			}
			if g, w := sortedJSON(t, got), sortedJSON(t, want); !slices.Equal(g, w) {
				t.Errorf("reduce:\n  %s\nGo:\n  %s", strings.Join(g, "\n  "), strings.Join(w, "\n  "))
			}
		})
	}
}

func TestAccessMatchesTheGoEngine(t *testing.T) {
	_, access := Programs()
	clusters := goClusters()
	for _, ref := range []rbac.SubjectRef{
		{Kind: "User", Name: "alice"},
		{Kind: "Group", Name: "sre"},
		{Kind: "ServiceAccount", Name: "ci", Namespace: "payments"},
		{Kind: "ServiceAccount", Name: "ci", Namespace: "other"},
		{Kind: "User", Name: "mallory"},
		{Kind: "User", Name: "nobody"},
	} {
		t.Run(fmt.Sprintf("%s %s/%s", ref.Kind, ref.Namespace, ref.Name), func(t *testing.T) {
			var want []SubjectGrant
			for _, g := range rbac.SubjectAccess(clusters, ref) {
				want = append(want, SubjectGrant{
					Cluster: g.Cluster, RoleRef: g.RoleRefName, RoleRefKind: g.Binding.RoleRef.Kind,
					Resolved: g.Role != nil, Scope: g.Scope, Binding: g.Binding.Name, Unit: g.Binding.Origin.UnitSlug,
				})
			}
			got, err := DecodeValues[SubjectGrant](runLocally(t, ReducerInvocation(access,
				"kind="+ref.Kind, "name="+ref.Name, "namespace="+ref.Namespace)))
			if err != nil {
				t.Fatal(err)
			}
			if g, w := sortedJSON(t, got), sortedJSON(t, want); !slices.Equal(g, w) {
				t.Errorf("reduce:\n  %s\nGo:\n  %s", strings.Join(g, "\n  "), strings.Join(w, "\n  "))
			}
		})
	}
}

func TestGroupByCluster(t *testing.T) {
	if got := GroupByCluster(""); got != `row.Origin.TargetID == null ? [] : [row.Origin.TargetID]` {
		t.Errorf("all clusters: %s", got)
	}
	if got := GroupByCluster(`pr"od`); !strings.Contains(got, `row.Origin.TargetSlug != "pr\"od"`) {
		t.Errorf("a named cluster is quoted as a CEL string: %s", got)
	}
}
