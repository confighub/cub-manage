// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/confighub/cub-manage/internal/celrule/celruletest"
)

// The basic pack installs, and each of its checks is described.
func TestBasicPackChecks(t *testing.T) {
	if err := basicPack.Validate(); err != nil {
		t.Fatal(err)
	}
	celruletest.UnrelatedResourcesPass(t, basicPack)
	for _, check := range basicPack.Checks {
		if check.DisplayName == "" || check.Description == "" {
			t.Errorf("check %s has no DisplayName or Description", check.Name)
		}
	}
}

// Control numbers and check names are unique across the packs, since a finding names its check
// and a framework report names its control.
func TestPacksDoNotShareChecks(t *testing.T) {
	names, controls := map[string]string{}, map[string]string{}
	for _, pack := range allPacks {
		if err := pack.Validate(); err != nil {
			t.Errorf("the %s pack: %v", pack.Group, err)
		}
		for _, check := range pack.Checks {
			if other, ok := names[check.Name]; ok {
				t.Errorf("check %s is in the %s and %s packs", check.Name, other, pack.Group)
			}
			if other, ok := controls[check.Control]; ok {
				t.Errorf("control %s is in the %s and %s packs", check.Control, other, pack.Group)
			}
			names[check.Name], controls[check.Control] = pack.Group, pack.Group
		}
	}
}

// targetFacts are the facts of a Target whose cluster serves Deployments and one CRD.
var targetFacts = map[string]any{"TargetFacts": map[string]any{
	"Cluster.KubernetesVersion":            "1.33.1",
	"Cluster.NamespaceScopedResourceTypes": "apps/v1/Deployment,v1/ConfigMap",
	"Cluster.ClusterScopedResourceTypes":   "v1/Namespace",
	"Cluster.NamespaceScopedCRDs":          "keda.sh/v1alpha1/ScaledObject",
	"Cluster.ClusterScopedCRDs":            "",
}}

// served-api fails a resource of a type the Target's cluster does not serve, and passes one it
// does, a CRD's included, and every resource where the Target has no facts.
func TestServedAPI(t *testing.T) {
	check := celruletest.Check(t, basicPack, "served-api")
	removed := "apiVersion: extensions/v1beta1\nkind: Ingress\nmetadata:\n  name: web\n  namespace: shop\n"
	result := celruletest.EvalCheckIn(t, targetFacts, check, removed)
	celruletest.Expect(t, result, "apiVersion")
	celruletest.ExpectResource(t, result, "shop/web", "extensions/v1beta1/Ingress")
	if len(result.FailedAttributes) == 1 && result.FailedAttributes[0].Value != "extensions/v1beta1" {
		t.Errorf("value = %v, want the apiVersion", result.FailedAttributes[0].Value)
	}
	if len(result.Details) != 1 || result.Details[0] != "the cluster of the Target does not serve extensions/v1beta1/Ingress: the version was removed, or its CRD is not installed" {
		t.Errorf("details = %q", result.Details)
	}

	celruletest.Expect(t, celruletest.EvalCheckIn(t, targetFacts, check, "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\n"))
	celruletest.Expect(t, celruletest.EvalCheckIn(t, targetFacts, check, "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: shop\n"))
	celruletest.Expect(t, celruletest.EvalCheckIn(t, targetFacts, check, "apiVersion: keda.sh/v1alpha1\nkind: ScaledObject\nmetadata:\n  name: web\n"))
	// A prefix of a served type is not served.
	celruletest.Expect(t, celruletest.EvalCheckIn(t, targetFacts, check, "apiVersion: apps/v1\nkind: Deploy\nmetadata:\n  name: web\n"), "apiVersion")
	celruletest.Expect(t, celruletest.EvalCheckIn(t, targetFacts, check, "apiVersion: keda.sh/v1alpha1\nkind: TriggerAuthentication\nmetadata:\n  name: web\n"), "apiVersion")

	celruletest.Expect(t, celruletest.EvalCheck(t, check, removed))
	celruletest.Expect(t, celruletest.EvalCheckIn(t, map[string]any{"TargetFacts": map[string]any{"Owner": "platform"}}, check, removed))
}

// displayNamePattern is what the server accepts as a Trigger's DisplayName. A rule's is its
// RuleInfo's, or its check's, which is also the name of the rule a check is separated into.
var displayNamePattern = regexp.MustCompile(`^[A-Za-z0-9]([\-_ .|A-Za-z0-9]*[A-Za-z0-9.!?])?$`)

func TestDisplayNamesAreValid(t *testing.T) {
	for _, pack := range allPacks {
		for _, check := range pack.Checks {
			if !displayNamePattern.MatchString(check.DisplayName) {
				t.Errorf("check %s: DisplayName %q is not one the server accepts", check.Name, check.DisplayName)
			}
		}
		for _, rule := range pack.RuleInfo {
			if !displayNamePattern.MatchString(rule.DisplayName) {
				t.Errorf("rule %s: DisplayName %q is not one the server accepts", rule.Slug, rule.DisplayName)
			}
		}
	}
}

// The correctness rule runs every check of vet-correctness but deprecated-api, which is a rule of
// its own, each named by the check the function knows it by.
func TestCorrectnessRule(t *testing.T) {
	var names []string
	for _, check := range basicPack.Checks {
		if check.Rule != "correctness" {
			continue
		}
		if check.Function != "vet-correctness" || check.FunctionCheck != check.Name || !strings.HasPrefix(check.Control, "CH-02") {
			t.Errorf("check %s = %+v", check.Name, check)
		}
		names = append(names, check.Name)
	}
	want := []string{"selector-matches-template", "label-syntax", "name-syntax", "port-names", "port-references",
		"volume-references", "exactly-one-of", "configmap-size", "cronjob", "deprecated-fields"}
	if !slices.Equal(names, want) {
		t.Errorf("correctness runs %v, want %v", names, want)
	}
}
