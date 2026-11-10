// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

// This tool's guardrail pack. The mechanism -- policy Space, Triggers, the group
// selection on each in-scope Space, and reporting the Units a Trigger marked
// -- is managerkit/guardrails'. What is this tool's is the rules, and which
// Units the annotate command has something to say about.

import (
	"context"

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

const controllerKinds = "['Deployment', 'StatefulSet', 'DaemonSet', 'ReplicaSet']"

const containers = "r.spec.template.spec.containers"

// containerFailures fails each container of a controller's pod template that
// does not pass ok, at field, with value and detail; and a controller with no
// containers at all, which has none to pass.
func containerFailures(ok, field, value, detail string) string {
	noContainers := celrule.Failure(celrule.Lit("spec.template.spec.containers"), celrule.None, celrule.Lit("the pod template has no containers"))
	return celrule.When("r.kind in "+controllerKinds,
		"(!has("+containers+") ? ["+noContainers+"] : "+
			containers+".filter(c, !("+ok+")).map(c, "+celrule.Failure(celrule.ContainerPath(field), value, detail)+"))")
}

var (
	celHasLimits = celrule.Result(containerFailures(
		"has(c.resources) && has(c.resources.limits) && 'memory' in c.resources.limits",
		"resources.limits.memory", celrule.None, "'container ' + c.name + ' sets no memory limit'"))

	// A pod that runs as non-root covers its containers; otherwise each has to.
	// The kind is checked first, since a resource that is not a controller may
	// have no pod template to read.
	celRunsNonRoot = celrule.Result(celrule.When(
		"r.kind in "+controllerKinds+" && !(has(r.spec.template.spec.securityContext) && has(r.spec.template.spec.securityContext.runAsNonRoot) && r.spec.template.spec.securityContext.runAsNonRoot)",
		containerFailures(
			"has(c.securityContext) && has(c.securityContext.runAsNonRoot) && c.securityContext.runAsNonRoot",
			"securityContext.runAsNonRoot", "c.?securityContext.?runAsNonRoot",
			"'container ' + c.name + ' does not set runAsNonRoot: true, and neither does its pod'")))

	celTerminationMsg = celrule.Result(containerFailures(
		"has(c.terminationMessagePolicy) && c.terminationMessagePolicy == 'FallbackToLogsOnError'",
		"terminationMessagePolicy", "c.?terminationMessagePolicy",
		"'container ' + c.name + ' does not set terminationMessagePolicy: FallbackToLogsOnError'"))

	// Annotate-then-validate: warn while a pdb-coverage finding annotation is present.
	celNoPDBFinding = celrule.AnnotationFinding(pdbCoverageAnnotation,
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
var celDatabaseStateful = celrule.Result(celrule.When(
	"r.kind in ['Deployment', 'ReplicaSet'] && has("+containers+")",
	containers+".filter(c, has(c.image) && c.image.matches('"+databaseImage+"')).map(c, "+
		celrule.Failure(celrule.ContainerPath("image"), celrule.Some("c.image"),
			"'container ' + c.name + ' runs the database image ' + c.image + ' in a ' + r.kind + ' rather than a StatefulSet'")+")"))

// untaggedOrLatestImage matches an image reference whose image tag is latest, or
// that has neither an image tag nor a digest, where the runtime pulls latest. The
// last path segment is checked alone, so a registry host's port is not mistaken
// for an image tag.
const untaggedOrLatestImage = `(:latest$)|^([^/]+/)*[^/:@]+$`

var Pack = guardrails.Pack{
	Group: "workload",
	Rules: []guardrails.Rule{
		{Slug: "workload-has-limits", DisplayName: "Containers set a memory limit", Description: "Warns on a controller whose containers don't all set resources.limits.memory. Fix: `cub manage workload set-resources <space>/<unit>`.", Expression: celHasLimits},
		{Slug: "workload-runs-nonroot", DisplayName: "Workloads run as non-root", Description: "Warns on a controller not running as non-root (pod or all containers). Fix: `cub manage workload harden <space>/<unit>`.", Expression: celRunsNonRoot},
		{Slug: "workload-termination-message-policy", DisplayName: "Containers fall back to logs for termination messages", Description: "Warns on a controller whose containers don't all set terminationMessagePolicy: FallbackToLogsOnError. Fix: the termination-message-policy profile.", Expression: celTerminationMsg},
		{Slug: "workload-explicit-image-tag", DisplayName: "Images name a version", Description: "Warns on a container image with no image tag or digest, or with the latest image tag. Fix: set a version with `cub function set set-image-reference-by-uri <repository> :<version>`.",
			Function: "vet-images", Arguments: []api.FunctionArgument{{ParameterName: "image-filter", Value: `{"DenyRegexp": "` + untaggedOrLatestImage + `"}`}}},
		{Slug: "workload-database-statefulset", DisplayName: "Databases run as StatefulSets", Description: "Warns on a Deployment or ReplicaSet running a database image (postgres, mysql, mariadb, mongo, cassandra, ...). Fix: run it as a StatefulSet, with volumeClaimTemplates for its data.", Expression: celDatabaseStateful},
		{Slug: "workload-pdb-coverage", DisplayName: "Workloads have a PodDisruptionBudget", Description: "Warns while a workload.confighub.com/pdb-coverage annotation is present (set by `cub manage workload guardrails annotate`). Fix: `cub manage workload ensure-pdb`, then re-run annotate.", Expression: celNoPDBFinding},
	},
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
  workload-termination-message-policy  containers set FallbackToLogsOnError
  workload-explicit-image-tag        images name an image tag other than latest, or a digest
  workload-database-statefulset      database images run in a StatefulSet or DaemonSet
  workload-pdb-coverage              warns while a PDB-coverage finding is annotated

The first five are plain per-resource checks (vet-cel, and vet-images for image
tags) — a single Unit answers them, no annotation needed. The last realizes
annotate-then-validate for the one property vet-cel can't see under
one-resource-per-Unit: whether a *matching* PDB exists in some other Unit. 'guardrails annotate' writes that finding onto each
uncovered workload Unit and this Trigger turns it into a warning.

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
