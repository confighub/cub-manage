// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

// This tool's guardrail pack. The mechanism -- policy Space, Triggers, the group
// selection on each in-scope Space, and reporting the Units a Trigger marked
// -- is managerkit/guardrails'. What is this tool's is the rules, and which
// Units the annotate command has something to say about.

import (
	"context"
	"slices"

	"github.com/spf13/cobra"

	"github.com/confighub/sdk/core/cubapi"
	"github.com/confighub/sdk/core/function/api"
	"github.com/confighub/sdk/managerkit/guardrails"

	"github.com/confighub/cub-manage/internal/celrule"
	"github.com/confighub/cub-manage/internal/cub"
	"github.com/confighub/cub-manage/internal/workload"
	"github.com/confighub/cub-manage/internal/workload/snapshot"
)

// pdbCoverageAnnotation is written by `guardrails annotate` onto each uncovered
// multi-replica workload Unit, and read by the pdb-coverage rule
// (annotate-then-validate -- the one finding a per-Unit CEL check cannot see).
const pdbCoverageAnnotation = "workload.confighub.com/pdb-coverage"

const containers = "r.spec.template.spec.containers"

// containerFailures fails each container of a controller's pod template that
// does not pass ok, at field, with value and detail; and a controller with no
// containers at all, which has none to pass.
func containerFailures(ok, field, value, detail string) string {
	noContainers := celrule.Failure(celrule.Lit("spec.template.spec.containers"), celrule.None, celrule.Lit("the pod template has no containers"))
	return "(!has(" + containers + ") ? [" + noContainers + "] : " +
		containers + ".filter(c, !(" + ok + ")).map(c, " + celrule.Failure(celrule.ContainerPath(field), value, detail) + "))"
}

var (
	celHasLimits = containerFailures(
		"has(c.resources) && has(c.resources.limits) && 'memory' in c.resources.limits",
		"resources.limits.memory", celrule.None, "'container ' + c.name + ' sets no memory limit'")

	// A pod that runs as non-root covers its containers; otherwise each has to.
	celRunsNonRoot = celrule.When(
		"!(has(r.spec.template.spec.securityContext) && has(r.spec.template.spec.securityContext.runAsNonRoot) && r.spec.template.spec.securityContext.runAsNonRoot)",
		containerFailures(
			"has(c.securityContext) && has(c.securityContext.runAsNonRoot) && c.securityContext.runAsNonRoot",
			"securityContext.runAsNonRoot", "c.?securityContext.?runAsNonRoot",
			"'container ' + c.name + ' does not set runAsNonRoot: true, and neither does its pod'"))

	// readOnlyRootFilesystem is a container setting only; a pod has none to
	// cover its containers. A container that writes to its filesystem mounts an
	// emptyDir where it writes, or is excepted from the rule.
	celReadOnlyRootFS = containerFailures(
		"has(c.securityContext) && has(c.securityContext.readOnlyRootFilesystem) && c.securityContext.readOnlyRootFilesystem",
		"securityContext.readOnlyRootFilesystem", "c.?securityContext.?readOnlyRootFilesystem",
		"'container ' + c.name + ' does not set readOnlyRootFilesystem: true'")

	celTerminationMsg = containerFailures(
		"has(c.terminationMessagePolicy) && c.terminationMessagePolicy == 'FallbackToLogsOnError'",
		"terminationMessagePolicy", "c.?terminationMessagePolicy",
		"'container ' + c.name + ' does not set terminationMessagePolicy: FallbackToLogsOnError'")

	// Annotate-then-validate: warn while a pdb-coverage finding annotation is present.
	celNoPDBFinding = celrule.AnnotationFailures(pdbCoverageAnnotation,
		"no PodDisruptionBudget selects this workload, as guardrails annotate found")
)

// databaseImage matches an image of a common database server by its repository
// name, the last path segment before any image tag or digest, so registry
// mirrors (bitnami/postgresql, docker.io/library/mysql) match too.
const databaseImage = `^([^/]+/)*(postgres|postgresql|postgis|timescaledb|timescaledb-ha|mysql|mariadb|percona-server|mongo|mongodb|cassandra|scylla|cockroach|couchdb|neo4j|clickhouse-server)([:@].*)?$`

// celDatabaseStateful fails a Deployment or ReplicaSet that runs a database
// image. Their Pods are interchangeable, so a rollout can run two database
// Pods on one volume, or start a replacement before the old one lets go of it;
// a StatefulSet gives each replica a stable identity and its own volume.
var celDatabaseStateful = celrule.When(
	"has("+containers+")",
	containers+".filter(c, has(c.image) && c.image.matches('"+databaseImage+"')).map(c, "+
		celrule.Failure(celrule.ContainerPath("image"), celrule.Some("c.image"),
			"'container ' + c.name + ' runs the database image ' + c.image + ' in a ' + r.kind + ' rather than a StatefulSet'")+")")

// untaggedOrLatestImage matches an image reference whose image tag is latest, or
// that has neither an image tag nor a digest, where the runtime pulls latest. The
// last path segment is checked alone, so a registry host's port is not mistaken
// for an image tag.
const untaggedOrLatestImage = `(:latest$)|^([^/]+/)*[^/:@]+$`

var Pack = guardrails.Pack{
	Group:     "workload",
	RuleInfo:  slices.Concat(podSecurityRules, kubescapeRules),
	Libraries: []guardrails.PolicyLibrary{kubescapeLibrary},
	Checks: slices.Concat(podSecurityChecks, kubescapeChecks, []guardrails.Check{
		{Control: "CH-0320", Name: "workload-readonly-root-filesystem", Rule: "workload-readonly-root-filesystem",
			DisplayName: "Containers have a read-only root filesystem",
			Description: "Warns on a controller whose containers don't all set securityContext.readOnlyRootFilesystem: true. Fix: `cub manage workload harden <space>/<unit>`, which sets it where unset; for a container that writes to its filesystem, mount an emptyDir where it writes, or except it with `cub manage guardrails exception add`.",
			Engine:      guardrails.EngineCEL, Priority: guardrails.PriorityMedium, ResourceTypes: celrule.Controllers, Failures: celReadOnlyRootFS,
			Fix:         "cub manage workload harden <space>/<unit>",
			Corresponds: []string{"KS:C-0017", "KL:no-read-only-root-fs", "KY:require-ro-rootfs"}},
		{Control: "CH-0332", Name: "workload-runs-nonroot", Rule: "workload-runs-nonroot",
			DisplayName: "Workloads run as non-root",
			Description: "Warns on a controller not running as non-root (pod or all containers). Fix: `cub manage workload harden <space>/<unit>`.",
			Engine:      guardrails.EngineCEL, Priority: guardrails.PriorityMedium, ResourceTypes: celrule.Controllers, Failures: celRunsNonRoot,
			Fix:         "cub manage workload harden <space>/<unit>",
			Corresponds: []string{"PSS:runAsNonRoot", "KL:run-as-non-root"}},
		{Control: "CH-0340", Name: "workload-has-limits", Rule: "workload-has-limits",
			DisplayName: "Containers set a memory limit",
			Description: "Warns on a controller whose containers don't all set resources.limits.memory. Fix: `cub manage workload set-resources <space>/<unit>`.",
			Engine:      guardrails.EngineCEL, Priority: guardrails.PriorityHigh, ResourceTypes: celrule.Controllers, Failures: celHasLimits,
			Fix:         "cub manage workload set-resources <space>/<unit>",
			Corresponds: []string{"KS:C-0271", "KL:unset-memory-requirements", "GK:containerlimits"}},
		{Control: "CH-0360", Name: "workload-pdb-coverage", Rule: "workload-pdb-coverage",
			DisplayName: "Workloads have a PodDisruptionBudget",
			Description: "Warns while a workload.confighub.com/pdb-coverage annotation is present (set by `cub manage workload guardrails annotate`). Fix: `cub manage workload ensure-pdb`, then re-run annotate.",
			// guardrails annotate writes the finding on each resource of the Unit.
			Engine: guardrails.EngineCEL, Priority: guardrails.PriorityMedium, Failures: celNoPDBFinding,
			Fix: "cub manage workload ensure-pdb"},
		{Control: "CH-0361", Name: "workload-termination-message-policy", Rule: "workload-termination-message-policy",
			DisplayName: "Containers fall back to logs for termination messages",
			Description: "Warns on a controller whose containers don't all set terminationMessagePolicy: FallbackToLogsOnError. Fix: the termination-message-policy profile.",
			Engine:      guardrails.EngineCEL, Priority: guardrails.PriorityLow, ResourceTypes: celrule.Controllers, Failures: celTerminationMsg,
			Fix: "the termination-message-policy profile"},
		{Control: "CH-0362", Name: "workload-database-statefulset", Rule: "workload-database-statefulset",
			DisplayName: "Databases run as StatefulSets",
			Description: "Warns on a Deployment or ReplicaSet running a database image (postgres, mysql, mariadb, mongo, cassandra, ...). Fix: run it as a StatefulSet, with volumeClaimTemplates for its data.",
			Engine:      guardrails.EngineCEL, Priority: guardrails.PriorityMedium,
			ResourceTypes: []string{"apps/v1/Deployment", "apps/v1/ReplicaSet"}, Failures: celDatabaseStateful},
		{Control: "CH-0380", Name: "workload-explicit-image-tag", Rule: "workload-explicit-image-tag",
			DisplayName: "Images name a version",
			Description: "Warns on a container image with no image tag or digest, or with the latest image tag. Fix: set a version with `cub function set set-image-reference-by-uri <repository> :<version>`.",
			Engine:      guardrails.EngineFunction, Priority: guardrails.PriorityMedium,
			Function: "vet-images", Arguments: []api.FunctionArgument{{ParameterName: "image-filter", Value: `{"DenyRegexp": "` + untaggedOrLatestImage + `"}`}},
			Fix:         "cub function set set-image-reference-by-uri <repository> :<version>",
			Corresponds: []string{"KL:latest-tag", "KY:disallow-latest-tag", "GK:disallowedtags"}},
	}),
}

func preflight(ctx context.Context) (*cubapi.Client, error) { return cub.Preflight(ctx) }

func newGuardrailsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "guardrails",
		Short: "Install, inspect, and feed the workload-readiness guardrail policy pack",
		Long: `guardrails manages a pack of workload-readiness validation policies, defined once
in a policy Space and enforced in each Space that opts into the group:

  workload-has-limits                controller containers set a memory limit
  workload-runs-nonroot              controller runs as non-root
  workload-readonly-root-filesystem  containers have a read-only root filesystem
  workload-termination-message-policy  containers set FallbackToLogsOnError
  workload-explicit-image-tag        images name an image tag other than latest, or a digest
  workload-database-statefulset      database images run in a StatefulSet or DaemonSet
  workload-pdb-coverage              warns while a PDB-coverage finding is annotated
  workload-pod-security-baseline     pods meet the baseline Pod Security Standard
  workload-pod-security-restricted   pods meet what the restricted standard adds
  no-cloud-credential-mounts         no hostPath to a cloud's credentials (Kubescape C-0020)
  no-sudo-entrypoint                 no sudo in a container's command (Kubescape C-0062)
  known-vulnerable-images            no Argo CD with CVE-2022-24348, no end of life
                                     ingress-nginx (Kubescape C-0081, C-0292)

The first six are plain per-resource checks (vet-cel, and vet-images for image
tags) — a single Unit answers them, no annotation needed. workload-pdb-coverage realizes
annotate-then-validate for the one property vet-cel can't see under
one-resource-per-Unit: whether a *matching* PDB exists in some other Unit. 'guardrails annotate' writes that finding onto each
uncovered workload Unit and this Trigger turns it into a warning.

The Pod Security rules run vet-pod-security-standard on Pods and every resource
with a pod template. A check is named by its Pod Security Standards ID in lowercase
words (privileged, host-path-volumes, run-as-user, ...), and each finding by the ID
itself. Stage one check ahead of the rest of its level with
'cub manage guardrails update <rule> --separate <check>'. The restricted rule
leaves out run-as-non-root, which workload-runs-nonroot checks; add it with
'guardrails install --check run-as-non-root'. It also skips the resources excepted
from workload-runs-nonroot: a workload that has to run as root cannot meet the
restricted standard.

The Kubescape rules run Kubescape's ValidatingAdmissionPolicies, the ones a cluster
can enforce at admission, with vet-validating-admission-policy. Install writes them
into the policy Space as the Unit guardrails-workload-kubescape, and their
parameters as guardrails-kubescape-params, which install creates once and leaves
for you to edit: set settings.cloudProvider (eks, aks or gke) there for
no-cloud-credential-mounts to know which paths hold credentials.

Triggers are created with Warn=true (advisory ValidationWarnings, never blocking).
Promote one to blocking later with:
  cub trigger update <slug> --space <policy-space> --unwarn

Except one resource from a rule with 'cub manage guardrails exception add';
'cub manage guardrails exception list' shows what is excepted, and why.`,
	}
	cmd.AddCommand(Pack.InstallCmd(preflight), Pack.AnnotateCmd(preflight, annotateSpec))
	return cmd
}

// annotateSpec is the workload half of `guardrails annotate`: which Units have
// a coverage finding, and what to write on them. The rest of the command is
// managerkit/guardrails'.
var annotateSpec = guardrails.AnnotateSpec{
	Key:   pdbCoverageAnnotation,
	Noun:  "workload Unit",
	Short: "Annotate each uncovered multi-replica workload Unit with a PDB-coverage finding",
	Long: `annotate runs the availability analysis and writes a workload.confighub.com/
pdb-coverage annotation onto each multi-replica workload Unit that has no matching
PodDisruptionBudget. Paired with the workload-pdb-coverage rule from
'guardrails install', this turns the cross-Unit coverage finding — the one a
per-Unit rule can't compute — into an advisory ValidationWarning.

Re-run after adding PDBs. Dry run unless --commit --change-desc.`,
	Targets: func(ctx context.Context, c *cubapi.Client, where, cluster string) ([]guardrails.Target, error) {
		snap, err := snapshot.Load(ctx, c, where)
		if err != nil {
			return nil, err
		}
		var out []guardrails.Target
		for _, r := range workload.AnalyzeAvailability(snap.Clusters) {
			if r.HasPDB || r.SpaceID == "" || r.UnitSlug == "" {
				continue // covered, or no Unit to annotate
			}
			if cluster != "" && r.Cluster != cluster {
				continue
			}
			out = append(out, guardrails.Target{
				SpaceID: r.SpaceID, UnitSlug: r.UnitSlug, Value: "uncovered",
				Cluster: r.Cluster, Namespace: r.Namespace,
			})
		}
		return out, nil
	},
}
