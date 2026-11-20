// Copyright (C) ConfigHub, Inc.
// SPDX-License-Identifier: MIT

package cli

// This tool's guardrail pack. The mechanism -- policy Space, Triggers, the group
// selection on each in-scope Space, and reporting the Units a Trigger marked
// -- is managerkit/guardrails'. What is this tool's is the rules.

import (
	"context"
	"slices"

	"github.com/spf13/cobra"

	"github.com/confighub/sdk/core/cubapi"
	"github.com/confighub/sdk/core/function/api"
	"github.com/confighub/sdk/managerkit/guardrails"

	"github.com/confighub/cub-manage/internal/celrule"
	"github.com/confighub/cub-manage/internal/cub"
)

// otherPDBs are the PodDisruptionBudgets on a workload's cluster, which
// workload-pdb-coverage reads.
var otherPDBs = guardrails.OtherData{Key: "pdbs", ResourceTypes: []string{"policy/v1/PodDisruptionBudget", "policy/v1beta1/PodDisruptionBudget"}}

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

	// A workload running more than one replica is guarded by a PodDisruptionBudget in its
	// namespace on its cluster whose selector selects its pods. A budget with no selector
	// selects none. A Unit with no Target is on no known cluster, so it passes.
	celPDBCoverage = celrule.When(
		celrule.OnACluster+" && r.?spec.?replicas.orValue(1) > 1 && podLabels(r) != null && "+
			"!"+celrule.Other(otherPDBs.Key)+".exists(b, "+celrule.NamespaceOf("b")+" == "+celrule.NamespaceOf("r")+
			" && b.?spec.?selector.orValue(null) != null && selects(b.spec.selector, podLabels(r)))",
		"["+celrule.Failure(celrule.PodLabelsPath(), celrule.Some("podLabels(r)"),
			"'runs ' + string(int(r.spec.replicas)) + ' replicas, and no PodDisruptionBudget in namespace ' + "+
				celrule.NamespaceOf("r")+" + ' selects its pods'")+"]")
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
	Checks: slices.Concat(podSecurityChecks, kubescapeChecks, moreChecks, []guardrails.Check{
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
			Corresponds: []string{"PSS:runAsNonRoot", "KL:run-as-non-root", "KS:C-0013", "KS:C-0198", "KS:C-0218"}},
		{Control: "CH-0340", Name: "workload-has-limits", Rule: "workload-has-limits",
			DisplayName: "Containers set a memory limit",
			Description: "Warns on a controller whose containers don't all set resources.limits.memory. Fix: `cub manage workload set-resources <space>/<unit>`.",
			Engine:      guardrails.EngineCEL, Priority: guardrails.PriorityHigh, ResourceTypes: celrule.Controllers, Failures: celHasLimits,
			Fix:         "cub manage workload set-resources <space>/<unit>",
			Corresponds: []string{"KS:C-0271", "KL:unset-memory-requirements", "GK:containerlimits"}},
		{Control: "CH-0360", Name: "workload-pdb-coverage", Rule: "workload-pdb-coverage",
			DisplayName: "Workloads have a PodDisruptionBudget",
			Description: "Warns on a workload running more than one replica that no PodDisruptionBudget in its namespace selects, among those on its cluster: a node drain can take all its pods at once. Not checked for a Unit with no Target. Fix: `cub manage workload ensure-pdb`.",
			Engine:      guardrails.EngineCEL, Priority: guardrails.PriorityMedium, Failures: celPDBCoverage,
			ResourceTypes: celrule.WorkloadTypes(), OtherData: []guardrails.OtherData{otherPDBs},
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
in a policy Space and enforced in each Space that opts into the group. Its rules,
by area ('guardrails status --by-rule' lists them with where each stands):

  Pod Security  workload-pod-security-baseline, workload-pod-security-restricted
  Hardening     workload-runs-nonroot, workload-readonly-root-filesystem,
                no-runtime-socket, no-sensitive-host-paths, readonly-host-paths,
                drop-net-raw, non-root-groups, dedicated-service-account,
                service-account-token, no-ssh-port, no-cloud-credential-mounts and no-sudo-entrypoint
                (Kubescape C-0020, C-0062), high-uid (optional)
  Resources     workload-has-limits, requests, emptydir-size-limit,
                ephemeral-storage and memory-request-equals-limit (optional)
  Reliability   workload-pdb-coverage, workload-termination-message-policy,
                workload-database-statefulset, readiness-probe, probes-differ,
                pdb-allows-eviction, pdb-unhealthy-eviction, replicas,
                rolling-update, no-bare-pods,
                job-cleanup, liveness-probe and priority-class (optional)
  Images        workload-explicit-image-tag, allowed-registries,
                deprecated-registries, known-vulnerable-images (Kubescape C-0081,
                C-0292), image-digest (optional, on released Units)

Optional checks are added with --check <name> or --all. Some take parameters:
replicas.min and replicas.environments (default 2, in Spaces labeled
Environment: prod), priority-class.allowed, and allowed-registries.registries,
which is installed once given.

Most check one resource. workload-pdb-coverage, pdb-allows-eviction, replicas and
service-account-token also read other resources on the workload's cluster -- its
PodDisruptionBudgets, workloads, autoscalers, ServiceAccounts and bindings: those
of the Units released to the same Target as the Space, which each Space selects
for itself. A change to one of them re-runs the rule on the Units that read it.
A Unit with no Target is on no known cluster, so what they read there passes.

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
	cmd.AddCommand(Pack.InstallCmd(preflight))
	return cmd
}
