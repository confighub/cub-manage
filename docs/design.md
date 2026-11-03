# cub-manage design

`cub-manage` manages Kubernetes configuration stored as data in ConfigHub, across a whole fleet of
clusters at once. It is one command-line tool, run standalone as `cub-manage` or as the `cub`
plugin command `cub manage`, with one command group per concern:

| Group           | Owns                                                                                            |
| --------------- | ----------------------------------------------------------------------------------------------- |
| `namespace`     | The Namespace object, its Pod Security Standard labels, and its name across a component's variants |
| `rbac`          | Role, ClusterRole, RoleBinding, ClusterRoleBinding, ServiceAccount                              |
| `netpol`        | NetworkPolicy, reasoned about against the Namespaces, workloads and Services it covers          |
| `workload`      | A workload's pod security context, resources, probes, operational hygiene, and availability (PodDisruptionBudgets, replica spread) |
| `scheduling`    | Where a workload may land: nodeSelector, tolerations, node affinity                             |
| `autoscale`     | HorizontalPodAutoscaler and KEDA ScaledObject                                                   |
| `observability` | Prometheus ServiceMonitor coverage and telemetry sidecars                                       |

This document describes what the tool is for, how it is built, the conventions every command
group follows, and where one group's responsibility ends and the next one's begins.

## 1. Why a fleet manager

Kubernetes has good tools for checking one object at a time. Linters such as
[kube-score](https://github.com/zegl/kube-score) and [kube-linter](https://github.com/stackrox/kube-linter),
and policy engines such as [Kyverno](https://kyverno.io/) and [OPA Gatekeeper](https://open-policy-agent.github.io/gatekeeper/),
flag a container that runs as root or has no memory limit. Tenancy controllers such as
[Capsule](https://github.com/projectcapsule/capsule), and the retired
[Hierarchical Namespace Controller](https://github.com/kubernetes-retired/hierarchical-namespaces),
stamp policy into namespaces inside one cluster. Two things are out of their reach, and they are
what `cub-manage` is for.

**Properties of a set.** Many of the questions that matter are not about one object:

- Does every namespace have a default-deny NetworkPolicy? Is this workload selected by any policy?
  Does an egress allow on one workload have a matching ingress allow on the other?
- Is this multi-replica Deployment covered by a PodDisruptionBudget that lives in another file?
- Is this metrics Service selected by any ServiceMonitor?
- Who can delete Secrets in production, once ClusterRole aggregation and every binding are taken
  into account?
- Is a component's namespace named the same, and held to the same Pod Security Standard, in every
  environment and region it runs in?

Each needs the whole set of related resources, across files and often across clusters. A check
that sees one object, or one cluster, cannot answer it.

**Fixing without drift.** A validator reports; someone then edits the live cluster, and the cluster
no longer matches its source. `cub-manage` fixes the configuration instead: it edits or creates
ConfigHub Units, the change is recorded as a revision with a description, and it reaches clusters
through the same release path as any other change.

Where a per-object validator already finds a problem, `cub-manage`'s contribution is the fix and
the fleet: a scorecard across every cluster, one operation that remediates a whole environment,
and a guardrail that keeps it fixed.

## 2. Scope

- **Kubernetes configuration held in ConfigHub.** Analysis covers the Units ConfigHub manages,
  including Units rendered from Helm charts or Kustomizations; nothing assumes they were authored
  a particular way. Resources running in a cluster but not in ConfigHub are out of scope, and a
  finding such as "no namespace is uncovered" is a claim about the managed configuration.
- **A cluster is a Target.** A Unit belongs to the cluster its Space's release Target delivers to.
  Units in a Space with no release Target (bases, templates) are grouped under the cluster `None`,
  and base and policy Spaces are left out of per-cluster analysis.
- **Configuration, not ConfigHub's own permissions.** The `rbac` group is about Kubernetes RBAC,
  not who may edit what in ConfigHub.
- **Writes stop at the Unit.** No command applies anything to a cluster. Releasing a change is a
  separate, deliberate step.

## 3. Users

Every command is designed to be driven by an AI agent working in a terminal, and to be usable by
a person. That shapes the output (JSON by default, compact, with provenance rather than whole
manifests), the safety model (§8), and how the tool is taught: through agent skills in
[`skills/`](../skills), one per task, rather than human user guides.

## 4. Architecture

Every command group is the same pipeline over a different resource family:

```
  fleet snapshot ──► analysis engine ──► findings ──► fixes as data ──► guardrails
  (read Units)       (deterministic)     (ranked)     (edit Units)      (Triggers)
```

**Snapshot.** One query selects the Units in scope with a single ConfigHub `where` predicate (§5.3)
and reads the resources of the kinds the group needs from them, joined with each Unit's Space and
Target. Each group names its own resource kinds; the loading is shared.

**Engine.** A pure, table-tested Go package per group parses those resources into a typed model
with provenance (cluster, Space, Unit, namespace, resource name) and computes the group's
analyses: effective RBAC access with ClusterRole aggregation, NetworkPolicy selector matching and
reachability, PodDisruptionBudget coverage, and so on. Parsing is lenient: a malformed resource is
skipped so that one bad Unit cannot stop a fleet-wide analysis. This is the logic an agent should
not recompute in its context window on every task.

**Fixes.** Writes go through ConfigHub functions run on the server — `set-yq`, `set-path`,
`set-namespace`, `set-container-resources`, `set-pod-container-security-context-defaults`, and
others — so comments and formatting are preserved and the configuration is never re-serialized.
A Unit can hold several resources, and a function runs on every one of them unless told
otherwise, so a write that edits one kind of resource names its types in the invocation's
`WhereResource`; a profile stores it with the profile.
A new resource (a PodDisruptionBudget, a NetworkPolicy, a ServiceMonitor) is created as a new Unit
whose content is derived from what it has to match. The one exception is `autoscale convert-keda`,
which runs the `convert-hpa-to-keda` function in-process and writes back the result.

**Profiles.** A profile is a named, reusable edit: a ConfigHub Invocation that wraps a function
with preset arguments and exposes what varies as parameters (a resource tier's container, a node
pool's name). Every group's profiles live in one library in the shared `common` Space. `profile
apply` applies one to a Unit; a group's `fleet-edit --profile` applies one to every Unit a filter
selects, restricted to the resource kinds that group manages.

**Guardrails.** Each group has a pack of validating Triggers, defined once in the `common` Space
and selected by each Space, or Target, that opts into the group. Rules install as warnings and are
made blocking with `cub trigger update <slug> --space common --unwarn`. A resource is excepted
from one rule by the annotation `exceptions.guardrails.confighub.com/<rule>`, whose value says why.

Most rules are per-resource CEL expressions run by `vet-cel`, or another validating function such
as `vet-images`. A rule about a set cannot be expressed that way: a Trigger sees one Unit, so it
cannot tell whether a PodDisruptionBudget in another Unit covers the workload in this one. For
those, the group's `guardrails annotate` command runs the fleet analysis and writes the finding
onto each affected Unit as an annotation, and a per-resource rule warns while the annotation is
present. The annotation is a cached finding, so it is rewritten on every run; a stale "covered"
is worse than none.

### 4.1 Code layout

```
cmd/cub-manage/          main: the cub plugin hook, then the root command
internal/cli/            the root command and the commands that are the tool's rather than a group's
internal/kit/            what every group shares: invocation name, command layout, flag spellings
internal/cub/            the ConfigHub session and the writes groups have in common
internal/<group>/        the group's analysis engine
internal/<group>/snapshot/  the group's fleet snapshot
internal/<group>/cli/    the group's commands
skills/                  agent skills, one per task
```

The engine directory for the `namespace` group is `nsmanager`, since `namespace` is too common a
Go identifier to make a good package name.

The generic parts of a manager tool — the snapshot loader, the guardrail pack, the profile
library, write reporting, the output flag — come from the open-source ConfigHub Go SDK
([`cubapi`](https://pkg.go.dev/github.com/confighub/sdk/core/cubapi),
[`cliutil`](https://pkg.go.dev/github.com/confighub/sdk/cliutil) and
[`managerkit`](https://pkg.go.dev/github.com/confighub/sdk/managerkit)). What is in this repository
is the Kubernetes knowledge.

## 5. Command surface

Consistency is a requirement: an agent or a person who has used one group should be able to use
the next without reading its help. Where a group needs something the others don't, it adds a
command; it does not spell a shared one differently.

### 5.1 Structure

```
cub manage <group> <command> [args] [flags]
cub manage <tool-command>
```

Tool commands are the ones that would otherwise be identical in every group:

| Command                                 | What it does                                                                 |
| --------------------------------------- | ---------------------------------------------------------------------------- |
| `preflight`                             | Verify the ConfigHub session against the server                              |
| `version`                               | Print the version                                                            |
| `guardrails status`                     | The Units any guardrail has marked, whichever pack the rule belongs to       |
| `profile install` / `list` / `apply`    | Seed every group's profiles into the shared library; list them; apply one    |

A group's help lists its read commands, then its write commands, under those two headings.

### 5.2 Commands every group has

| Command                  | Kind  | Contract                                                                                       |
| ------------------------ | ----- | ---------------------------------------------------------------------------------------------- |
| `snapshot`               | read  | Per-cluster inventory: counts of the group's resource kinds, Units, gated and unreleased Units |
| `list`                   | read  | Enumerate the group's resources across the fleet, one row per resource                        |
| `findings`               | read  | Severity-ranked findings from every analyzer the group has                                     |
| `guardrails install`     | write | Plan, or with `--commit` install, the group's pack and opt Spaces into it                      |

A group whose pack has a rule about a set also has `guardrails annotate` (§4). A group with
profiles also has `fleet-edit --profile <slug>`.

Each group adds its own analyses (for example `who-can`, `coverage`, `readiness`, `placement`) and
its own fixes.

### 5.3 Flags

Fleet scope, on every read command and every bulk write:

- `--where "<expr>"` — one ConfigHub Unit filter, evaluated on the server, which can refer to the
  Unit, its Space (`Space.*`) and its Target (`Target.*`), e.g. `"Unreleasable = false"` or
  `"Space.Slug LIKE 'shop-%'"`. A `where` expression is a flat conjunction: there is no `OR`, and
  a parenthesized clause is rejected.
- `--component <slug or ID>` — the Spaces that are Variants of that Component
  (`Space.ComponentID`).
- `--environment`, `--region`, `--owner`, `--layer`, `--variant` — shorthands for the standard
  Space labels, each `Space.Labels.<Key> = '<value>'`.

All of these are joined with `AND`.

Display filters, applied to what was read rather than sent to the server: `--cluster` (a Target
slug, or `None`) and `--namespace`. `--cluster` stays client-side because a server-side
`Target.Slug` predicate would drop the Units that have no Target.

Findings: `--severity` (only findings of that severity) and `--analyzer`.

Output: `-o`/`--output` on every command — `json` (the default), `yaml`, `table`, `jq=<expr>`,
`yq=<expr>`.

Writes: `--commit`, without which a write is a dry run, and `--change-desc`, required with it.

### 5.4 Arguments and names

- A command that writes to one Unit takes it as `<space>/<unit>`.
- A command that creates a Unit derives what it can (the Space from the namespace's workloads, a
  selector from the workload's pod labels) and takes `--space` only to disambiguate.
- Commands that add something that may already exist are named `ensure-<thing>` and are
  idempotent; commands that set fields are named `set-<field>`.
- A bulk write is `fleet-edit`.
- A flag that narrows a report to the rows with a problem is `--issues-only`.
- Guardrail rule slugs start with the group name: `<group>-<rule>`.
- Finding annotations are `<group>.confighub.com/<finding>`.

### 5.5 Where the surface does not yet follow the conventions

The command groups were built separately and consolidated into this tool afterwards. These are the
places they still differ, with the change that would bring each into line. Existing guardrail rule
slugs and finding annotations are already installed in organizations, so renaming them needs a
migration (install the new rule, remove the old one) rather than just a new release.

| Group           | Today                                                                                      | Proposed                                                                       |
| --------------- | ------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------ |
| `autoscale`     | `findings --min-severity` (that severity and above), no `--analyzer`; the other groups' `--severity` is an exact match | one flag with one meaning everywhere, and `--analyzer`                         |
| `autoscale`     | `list` has no `--kind`                                                                     | `--kind`                                                                       |
| `scheduling`    | `findings` has no `--analyzer`                                                             | `--analyzer`                                                                   |
| `rbac`          | `findings`, `who-can`, `access` have no `--cluster` / `--namespace`                        | add them                                                                       |
| `namespace`     | `findings` has no `--namespace`                                                            | add it                                                                         |
| `namespace`     | `consistency --component-name` (a display filter on the Component slug) beside `--component` (a server-side scope) | drop `--component-name`; `--component` already scopes to one Component |
| several         | Findings severity is printed `HIGH` / `MEDIUM` by `rbac`, `workload`, `scheduling` and `observability`, `High` / `Medium` by the others; the summary line has three shapes (`8 findings`; `6 findings (5 high, 1 medium, 0 low)`; `(high=0 medium=2 low=4)`) | one rendering of a finding, shared |
| several         | A command that creates a Unit (`ensure-pdb`, `ensure-servicemonitor`, `default-deny`) reports a sentence; one that edits reports the shared table. `convert-keda` prints a log line to stderr | the shared write report for both; no log output |
| several         | After a write: "Publish the Space to deploy", "Not published — rolling out is a separate `cub release publish`", "It is not applied until you apply it" | one sentence, in the shared write report |
| several         | `--incomplete-only`, `--inconsistent-only`, `--failing-only`, `--unconstrained-only`, `--uncovered-only`, `--missing-only`, `--issues-only` | `--issues-only` everywhere                    |
| `rbac`          | `edit <op> <space>/<unit>` and `fleet-edit <op>`, with their own Invocation library (`edit install`, in its own Space) | the edits become profiles in the shared library: `profile apply`, `fleet-edit --profile` |
| `rbac`          | `edit` subcommands have no `-o`                                                            | `-o`, reporting through the shared write report                                |
| `netpol`        | `fleet default-deny`                                                                       | `fleet-edit`, or `default-deny --where …` for the uncovered namespaces it selects |
| `netpol`        | `fix dns\|metadata <space>/<unit>` also takes the fleet-scope flags                        | drop them, or make `fix` a bulk command and say so                             |
| `netpol`        | `default-deny <namespace>`, `allow <src> <dst>` take names, not `<space>/<unit>`           | keep (they create Units), and document as the creating-command form (§5.4)    |
| `namespace`     | `apply-envelope` selects with `--space` / `--cluster` / `--namespace` beside the fleet-scope flags | one selection model                                                    |
| `scheduling`    | rule `workload-toleration-needs-placement`                                                 | `scheduling-toleration-needs-placement`                                        |
| `rbac`          | rules `no-rbac-wildcards`, `no-rbac-privilege-escalation`, `no-cluster-admin-binding`      | `rbac-no-wildcards`, `rbac-no-privilege-escalation`, `rbac-no-cluster-admin-binding` |
| `autoscale`     | rule `autoscaler-not-pinned`; rule `schema-valid`, which validates every resource kind     | `autoscale-not-pinned`; schema validation moves to a tool-level pack (§7.6)    |
| `observability` | rule `servicemonitor-coverage`; annotation `observability.confighub.com/coverage`          | `observability-servicemonitor-coverage`                                        |
| `namespace`, `rbac`, `netpol` | no profiles                                                                  | profiles where a fix is a reusable edit                                        |

## 6. The command groups

Each group's commands are listed by `cub manage <group> --help`. This section records what each
group decides and why.

### 6.1 namespace

Reads Namespaces and the workloads in them. Analyses:

- `envelope` — per namespace: is there a Namespace object, and does it carry a
  `pod-security.kubernetes.io/enforce` label? Also flags two Namespace Units that resolve to the
  same name on the same Target. Base Units still named `confighubplaceholder` are exempt; the
  `vet-placeholders` function already keeps them from being released.
- `pod-security` — per namespace, the most restrictive Pod Security Standard all its workloads
  meet, computed with the server's `vet-pod-security-standard` function (the same checks the
  PodSecurity admission controller runs), compared with the namespace's label, and the workloads
  that block the next level.
- `consistency` — per Component, over the Spaces that are its Variants: is the namespace name,
  and its Pod Security Standard, the same in every one?

Fixes: `apply-envelope` sets a namespace's level to what its workloads meet (or `--level`), never
lowering one, and labels the namespace's workloads with it so the `namespace-pod-security-standard`
rule can hold each workload to its namespace's level before it reaches a cluster; labeling a
workload's metadata does not restart it. `backfill` clones a base Space's Units into an existing
Space and re-homes them with `set-namespace`, which also rewrites RoleBinding subject namespaces
and Service DNS references.

A namespace maps to a Space, which holds the namespace's workloads as well as the Namespace
object. The namespace name of a component is its Component's slug, normalized, and the same in
every variant.

### 6.2 rbac

Reads Roles, ClusterRoles, their bindings and ServiceAccounts. The engine implements Kubernetes
authorization: verb, resource and API-group wildcards, subresources, `resourceNames`,
`nonResourceURLs`, RoleBindings to ClusterRoles, namespace scoping, and ClusterRole aggregation to
a fixed point.

Analyses: `who-can <verb> <resource>` and its inverse `access <subject>`. Findings: wildcard
grants, privilege-escalation verbs (`escalate`, `bind`, `impersonate`), risky grants,
cluster-admin bindings, bindings to roles that don't exist, and ServiceAccounts nothing binds.

Fixes: structured edits — add or remove a verb on a rule, a subject on a binding — as
parameterized `set-yq` Invocations, so the server applies them to the YAML in place.

### 6.3 netpol

Reads NetworkPolicies together with Namespaces, pod-bearing workloads and Services. The engine
matches label selectors (`matchLabels` and `matchExpressions`), scopes them by namespace, and
combines policies additively: a pod is isolated in a direction as soon as any policy selects it,
and allowed whatever any of them allows.

Analyses: `coverage` (per namespace: is there a default-deny; per workload: is it selected for
ingress and egress), `who-can-reach` and `reachable-from`. Findings: a namespace with no
default-deny ingress, a workload no ingress policy selects, an allow-all policy, egress that admits
the cloud metadata address `169.254.169.254`, and an egress allow with no matching ingress allow
at the destination.

Fixes: `default-deny` (with `--egress`, also denying egress but allowing DNS, since a bare egress
default-deny breaks name resolution), `allow`, `fix dns|metadata` on an existing policy, and
`allow-from-links`, which derives ingress allows from the dependency graph that ConfigHub Links
record: for each consumer → producer Link, an allow on the producer admitting the consumer. It
writes one policy per destination listing every admitted source, the usual Kubernetes shape, and
re-running adds only the missing sources. `--per-edge` writes one policy per Link instead.

Because a Link joins Units and a Unit can hold several resources, `allow-from-links` matches the
Link's Unit slug to a resource name, prefers the Service, and skips Units it cannot disambiguate.

### 6.4 workload

Reads pod-bearing workloads (Deployment, StatefulSet, DaemonSet, ReplicaSet, Job, CronJob, Pod)
and PodDisruptionBudgets, and scores each workload on five dimensions:

- **security** — non-root, no privilege escalation, not privileged, all capabilities dropped,
  read-only root filesystem, a RuntimeDefault seccomp profile, and no automounted ServiceAccount
  token;
- **resources** — CPU and memory requests and limits;
- **probes** — liveness, readiness, startup;
- **hygiene** — `terminationMessagePolicy: FallbackToLogsOnError`;
- **availability** — for a workload with more than one replica, a PodDisruptionBudget that selects
  it and does not block every eviction, and pod anti-affinity or topology spread so that one node
  or zone cannot take every replica.

Analyses: `readiness` (the scorecard) and `availability`. Fixes: `harden`, `set-resources`,
`set-probes`, `ensure-pdb` (a new PodDisruptionBudget Unit whose selector is the workload's pod
labels), `ensure-spread`, and profiles for each.

### 6.5 scheduling

Reads the placement fields of pod templates. A toleration only permits a pod onto a tainted node;
it does not put it there. So a workload counts as constrained only with a nodeSelector or a
required node affinity, and the finding is a workload that tolerates a taint but constrains
nothing. Fixes: `set-node-selector`, `set-tolerations`, `set-node-affinity`, and placement
profiles.

Checks that need to know each cluster's node pools and taints — a nodeSelector for a pool no
cluster has — wait until those are recorded on Targets.

### 6.6 autoscale

Reads HorizontalPodAutoscalers, KEDA ScaledObjects, the workloads they scale, and the
PodDisruptionBudgets that select them. Findings: an autoscaler whose minimum equals its maximum, a
Deployment or StatefulSet nothing scales, and a PodDisruptionBudget that blocks every eviction at
the autoscaler's minimum. Fixes: `set-hpa`, `convert-keda`, and autoscaling profiles.

### 6.7 observability

Reads Services, ServiceMonitors and workloads. `coverage` joins each Service that exposes a metrics
port with the ServiceMonitors whose selectors match it; `sidecars` reports which workloads run a
telemetry sidecar. Findings: a metrics Service no ServiceMonitor selects, and a ServiceMonitor that
selects no Service. Fixes: `ensure-servicemonitor`, which derives the ServiceMonitor's selector from
the Service, and `inject-sidecar`, which uses `set-path` to replace a container of the same name or
append one, so a re-run never duplicates it.

## 7. Boundaries and handoffs

The groups were designed separately and some of their ground overlaps. The rule for drawing the
line: **a group owns a resource kind, or a set of fields, and is the only group that writes it.**
Other groups may read it, and may report findings that involve it, but they hand the fix to the
owner.

### 7.1 Ownership

| Resource or fields                                                        | Owner           |
| ------------------------------------------------------------------------- | --------------- |
| Namespace object; `pod-security.kubernetes.io/*` labels, on the Namespace and on workloads | `namespace` |
| Namespace name (`metadata.namespace`) across a component's variants        | `namespace`     |
| Role, ClusterRole, RoleBinding, ClusterRoleBinding, ServiceAccount        | `rbac`          |
| NetworkPolicy                                                             | `netpol`        |
| Pod and container `securityContext`, `automountServiceAccountToken`       | `workload`      |
| Container `resources`, probes, `terminationMessagePolicy`                 | `workload`      |
| PodDisruptionBudget; pod anti-affinity; `topologySpreadConstraints`       | `workload`      |
| `nodeSelector`, `tolerations`, node affinity                              | `scheduling`    |
| HorizontalPodAutoscaler, KEDA ScaledObject; replica bounds                | `autoscale`     |
| ServiceMonitor; telemetry sidecar containers                              | `observability` |

Pod anti-affinity and topology spread are availability, not placement: they spread a workload's
own replicas so that losing one node or zone cannot take all of them, which is the same concern as
the PodDisruptionBudget. Placement is choosing which nodes a pod may run on.

### 7.2 Handoffs

These are the points where one group's finding is another group's fix. A group's help and its
skills name the other group's command rather than duplicating it.

| From            | Finding                                                         | To              | Fix                                            |
| --------------- | --------------------------------------------------------------- | --------------- | ---------------------------------------------- |
| `namespace`     | `pod-security`: workloads block the next level                  | `workload`      | `harden`, then `namespace pod-security` again, then `apply-envelope` |
| `namespace`     | `backfill` created NetworkPolicy and RBAC Units from a base     | `netpol`, `rbac`| `coverage`, `findings` judge what was copied   |
| `autoscale`     | `pdb-blocks-min-scale`                                          | `workload`      | relax the PodDisruptionBudget; or `autoscale set-hpa` to raise the minimum |
| `observability` | `inject-sidecar` added a container                              | `workload`      | `readiness` scores it; `set-resources --container`, `harden` |
| `scheduling`    | a request to spread replicas                                    | `workload`      | `ensure-spread`                                |
| `workload`      | a request to choose nodes                                       | `scheduling`    | `set-node-selector`, `set-node-affinity`       |

### 7.3 Namespace templates

A namespace's starting configuration — its Namespace object, a default-deny NetworkPolicy,
baseline RBAC — is a template, and `namespace backfill` copies whatever a base Space holds into an
existing one. Copying is the `namespace` group's job; it does not judge what it copies. The
NetworkPolicies it brings are `netpol`'s to evaluate, and the RBAC is `rbac`'s. The `namespace`
group's `envelope` check is deliberately limited to the namespace itself, so that it never reports a
weaker version of an analysis another group does properly.

### 7.4 PodDisruptionBudgets and autoscaling

Two groups check a PodDisruptionBudget that blocks every eviction: `workload` compares it with the
workload's `replicas`, and `autoscale` with the autoscaler's minimum. They are the same check with
two notions of how many replicas there are, and when an autoscaler scales the workload, only the
second is right. Worse, a Deployment that an autoscaler scales usually leaves `replicas` unset, so
`workload` treats it as single-replica and skips its availability checks entirely.

**Proposed:** `workload` owns the check, and computes availability against the workload's minimum
replicas — the autoscaler's minimum when one targets the workload, `replicas` otherwise — reading
the autoscalers for that purpose. `autoscale` drops `pdb-blocks-min-scale` and keeps the checks
that are about the autoscaler itself.

### 7.5 Sidecars

A telemetry sidecar is a container like any other, so `workload` scores it with the rest.
`inject-sidecar` writes only the container's name, image and port; the security context and
resources it needs are `workload`'s to set. A profile that injects a sidecar with hardened defaults
and resources already set would remove that second step; it belongs in `observability`, since it is
one edit that adds one container.

### 7.6 Rules that are not any group's

`autoscale`'s pack includes `schema-valid`, which runs `vet-schemas` on every resource, so that the
KEDA ScaledObjects `convert-keda` writes are validated against their schema. Schema validation is
not about autoscaling, and a Space that opts into the `autoscale` group should not get it for every
other kind. It belongs in a tool-level pack, opted into on its own.

### 7.7 Gaps between groups

- **Scraping through a default-deny.** A namespace with a default-deny ingress policy blocks
  Prometheus from scraping the Services `observability` has covered. `netpol` should be able to
  allow ingress to the metrics port of each ServiceMonitor-selected Service from the namespace
  Prometheus runs in.
- **ServiceAccount tokens.** `workload harden` stops automounting the ServiceAccount token, which
  breaks a workload that calls the Kubernetes API. Whether it does is visible in `rbac`: a
  workload whose ServiceAccount is bound to a role needs its token.

## 8. Safety

- **Dry run by default.** Every write previews its change and does nothing without `--commit`, and
  a commit requires `--change-desc`, which is recorded on each revision.
- **Never past a gate.** No command bypasses an ApplyGate or a validation error, and no command
  releases a change.
- **Advisory first.** Guardrails install as warnings; making one blocking is a separate, deliberate
  step.
- **Read-only skills.** A skill that only reads can only run read commands: its `allowed-tools`
  list nothing that writes.
- **Warnings where a fix can break something running.** An egress default-deny without DNS; a
  required anti-affinity that can leave replicas Pending on a cluster with too few nodes (so
  `ensure-spread` defaults to preferred); a read-only root filesystem on a container that writes
  to disk; limits low enough to throttle or kill; a PodDisruptionBudget that would stop node
  drains; a `set-namespace` that re-homes resources in a Space with running workloads.

## 9. Future work

- **One snapshot.** Each group reads its own resource kinds, and several read the same ones
  (workloads, Namespaces, PodDisruptionBudgets). One shared snapshot would make cross-group
  analysis (§7.4, §7.7) natural and a fleet-wide report cheap.
- **Tool-level `findings` and `guardrails install`.** Every group's findings in one ranked list,
  and every pack opted into in one command.
- **Validation over a set of Units.** Annotate-then-validate stands in for a validating function
  that can see more than one Unit. With one, the set rules — PodDisruptionBudget, NetworkPolicy and
  ServiceMonitor coverage — become ordinary guardrails, and the existing per-object validators'
  cross-object checks work as Triggers too.
- **Server-side analysis.** The engines run in the client. A map-reduce over Units on the server
  would let the analyses run where the data is; several group by a field inside the resources (the
  namespace), not by Space or Target, which such a facility would need to support.
- **Cluster facts.** Whether a cluster's network plugin enforces NetworkPolicy at all, and which
  node pools and taints it has, recorded on its Target, would qualify `netpol`'s findings and enable
  `scheduling`'s pool checks.
- **Resource-level dependencies.** Links join Units, so `allow-from-links` guesses which resource in
  a Unit is meant. A Link that can name a resource would remove the guess.
- **Namespace provisioning.** Creating a new component's namespace from a template, and a mutating
  Trigger that holds a component's namespace name to its Component's slug in every variant.
  `ResourceQuota` and `LimitRange` as namespace template members, with defaulting functions to fill
  them. Labels that other tools expect on namespaces, such as those of
  [Argo CD](https://argo-cd.readthedocs.io/), [Flux](https://fluxcd.io/) and
  [Istio](https://istio.io/).
- **Promotion.** Carrying a fix from a base Space to its variants is ConfigHub's upgrade, preserving
  each variant's overrides; the groups leave it to `cub`.
