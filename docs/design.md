# cub-manage design

`cub-manage` manages Kubernetes configuration stored as data in ConfigHub, across a whole fleet of
clusters at once. It is one command-line tool, run standalone as `cub-manage` or as the `cub`
plugin command `cub manage`, with one command group per concern:

| Group           | Owns                                                                                            |
| --------------- | ----------------------------------------------------------------------------------------------- |
| `namespace`     | The Namespace object, its Pod Security Standard labels, and its name across a component's variants |
| `rbac`          | Role, ClusterRole, RoleBinding, ClusterRoleBinding, ServiceAccount                              |
| `netpol`        | NetworkPolicy, reasoned about against the Namespaces, workloads and Services it covers          |
| `workload`      | A workload's pod security context, resources, probes, operational hygiene, and availability (PodDisruptionBudgets, whether replicas are spread) |
| `scheduling`    | The constraints the scheduler reads: where a workload may land (nodeSelector, tolerations, node affinity) and how its replicas spread (topology spread constraints, pod anti-affinity, each cluster's default spread) |
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
A new resource (a PodDisruptionBudget, a NetworkPolicy, a ServiceMonitor) is made as a Unit of its
own, whose content is derived from what it has to match; the command creates the Unit, or brings it
up to date if it exists, so running it again changes nothing. The one exception to server-side
functions is `autoscale convert-keda`, which runs the `convert-hpa-to-keda` function in-process and
writes back the result.

Every write reports the same way, whether it edited Units or created them: one row per Unit, what
changed in each, path by path, and after a commit, that nothing is released until the Space is
(`cub release publish <space>`).

**Profiles.** A profile is a named, reusable edit: a ConfigHub Invocation that wraps a function
with preset arguments and exposes what varies as parameters (a resource tier's container, a node
pool's name). Every group's profiles live in one library in the shared `common` Space, and their
slugs start with the group they belong to where the edit is not already named for what it does
(`rbac-add-verb`, `netpol-allow-dns`). `profile apply` applies one to a Unit; a group's `fleet-edit
--profile` applies one to every Unit a filter selects, restricted to the resource kinds that group
manages. It refuses to run without a selector, `--where` or a label shorthand, so that a fleet-wide
change is always a deliberate one.

**Guardrails.** Each group has a pack of validating Triggers, defined once in the `common` Space
and selected by each Space, or Target, that opts into the group. Rules install as warnings and are
made blocking with `cub manage guardrails update <slug> --enforce --commit`, which installing again
leaves in place. A resource is excepted from one rule by the annotation
`exceptions.guardrails.confighub.com/<rule>`, whose value says why. `cub manage guardrails
exception` adds, removes and lists them: an exception is a decision about one resource, so `add`
takes `--resource` whenever the Unit holds more than one, and `list` reads every exception back
with its reason, and counts the exceptions to each rule, including the rules that have none.
Rules about every resource or every Unit rather than one group's kinds are the tool's: the `basic` group, installed with `cub manage guardrails install` (section 7.6).

Most rules are per-resource CEL expressions run by `vet-cel`, or another validating function such
as `vet-images`. A `vet-cel` rule reports, for a resource that fails it, each attribute that failed
-- its path and value -- with a sentence saying why, so a reader can point at the field to fix or
the resource to except; `internal/celrule` builds those expressions. Each rule also has a
DisplayName, which its Trigger is given, for where a person reads it, such as the Validation page.
A rule about a set cannot be expressed that way: a Trigger sees one Unit, so it
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
internal/celrule/        builds the vet-cel expressions of the guardrail rules
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
| `guardrails install`                    | Install the tool-level `basic` group, whose rules are about every resource or Unit |
| `guardrails status`                     | The Units any guardrail has marked, whichever group the rule belongs to; with `--by-rule`, each rule's stage (Created, Audited, Enforced, Disabled), priority, due date, and Units passed, warned and blocked |
| `guardrails exception add` / `remove`   | Except one resource from one rule of any group, with the reason; remove it   |
| `guardrails exception list`             | Every resource excepted from a rule, with its reason, and the count per rule |
| `guardrails update`                     | Set a rule's priority and the date it is due to be enforced; enforce it, or return it to audit |
| `profile install` / `list` / `apply`    | Seed every group's profiles into the shared library; list them; apply one    |

A group's help lists its read commands, then its write commands, under those two headings.

### 5.2 Commands every group has

| Command                  | Kind  | Contract                                                                                       |
| ------------------------ | ----- | ---------------------------------------------------------------------------------------------- |
| `snapshot`               | read  | Per-cluster inventory: counts of the group's resource kinds, Units, gated and unreleased Units |
| `list`                   | read  | Enumerate the group's resources across the fleet, one row per resource                        |
| `findings`               | read  | Severity-ranked findings from every analyzer the group has                                     |
| `fleet-edit --profile`   | write | Apply one of the library's profiles to the group's kinds in every Unit a selector names        |
| `guardrails install`     | write | Plan, or with `--commit` install, the group's pack and opt Spaces into it                      |

A group whose pack has a rule about a set also has `guardrails annotate` (§4).

Every `list` prints the same row — cluster, kind, name, namespace, Space, Unit — and `autoscale`'s
adds the autoscaler's target and replica range. Every `findings` prints the same report: findings of
one shape (severity, analyzer, cluster, namespace, kind, name, Space, Unit, message; `namespace`'s
consistency findings name a Component instead of a resource), most severe first, with totals by
severity and by analyzer.

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
slug, or `None`) and `--namespace`, on every command that reports resources. `--cluster` stays
client-side because a server-side `Target.Slug` predicate would drop the Units that have no Target.

Lists: `--kind`. Findings: `--severity` (only findings of that severity) and `--analyzer` (a
comma-separated list of the group's analyzers; any other name is an error rather than an empty
report). A report narrowed
to the rows with a problem: `--issues-only`.

A write selects what it changes the way a read selects what it reports: the fleet scope and the
display filters. A command that needs every resource to judge the ones it changes reads them all and
applies the scope to what it writes: `namespace apply-envelope` judges a namespace by all of its
workloads, wherever their Units are, and labels the namespaces the scope selects.

Output: `-o`/`--output` on every command — `json` (the default), `yaml`, `table`, `jq=<expr>`,
`yq=<expr>`.

Writes: `--commit`, without which a write is a dry run, and `--change-desc`, required with it.

### 5.4 Arguments and names

- A command that writes to one Unit takes it as `<space>/<unit>`.
- A command that creates a Unit derives what it can (the Space from the namespace's workloads, a
  selector from the workload's pod labels) and takes `--space` only to disambiguate. It takes what
  it is creating from, which is not a Unit: `ensure-pdb` a workload's `<space>/<unit>`, `netpol
  default-deny` a namespace, `netpol allow` two workloads. Without its argument, `default-deny`
  covers every namespace in scope that needs one.
- Every command that creates a Unit is idempotent: it creates the Unit, or brings an existing one
  up to date.
- Commands that add something that may already exist are named `ensure-<thing>`; commands that set
  fields are named `set-<field>`.
- A reusable edit is a profile; the bulk form of any profile is `fleet-edit`.
- A flag that narrows a report to the rows with a problem is `--issues-only`.
- Guardrail rule slugs start with the group name: `<group>-<rule>`.
- Analyzer names are singular and name what is wrong: `orphaned-binding`, `missing-pdb`,
  `uncovered-ingress`.
- A finding annotation is named for the gap it records, and so is the rule that reads it:
  `<group>.confighub.com/<gap>-coverage` and `<group>-<gap>-coverage`, as in
  `workload.confighub.com/pdb-coverage` and `workload-pdb-coverage`.

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
and Service DNS references. The `namespace-set` profile is the same function as a reusable edit: with
`fleet-edit --component <c>`, it moves every variant of a Component to one namespace, the fix for an
inconsistent namespace name.

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

Fixes: the `rbac-*` profiles, which add or remove a verb on a rule, a User or Group on a binding,
or a ServiceAccount on a binding. Each is a parameterized `set-yq` edit, so the server applies it to
the YAML in place, and each add is idempotent. `profile apply` makes one edit; `rbac fleet-edit`
makes it everywhere a persona role or binding is replicated.

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

Fixes: `default-deny` for a namespace, or for every namespace in scope that lacks one (with
`--egress`, also denying egress but allowing DNS, since a bare egress default-deny breaks name
resolution). A default-deny is any policy that selects every pod, isolates the direction and has
no rule admitting all traffic, whatever its name, so the command leaves a namespace that has one
alone; when another Unit's policy denies ingress, `--egress` adds the egress half in a policy of
its own rather than a second policy of the same name. `allow`; the `netpol-allow-dns` and `netpol-except-metadata` profiles for existing
policies, each of which changes only the policies that need it; and `allow-from-links`, which
derives ingress allows from the dependency graph that ConfigHub Links
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

Each check a workload fails is a finding whose analyzer names what is wrong (`missing-run-as-non-root`,
`missing-memory-limit`, `missing-pdb`), its severity set by its dimension.

Analyses: `readiness` (the scorecard) and `availability`. Fixes: `harden`, `set-resources`,
`set-probes`, `ensure-pdb` (a PodDisruptionBudget Unit whose selector is the workload's pod
labels), and profiles for each. Spread is checked here and set by `scheduling` (§7.1).

### 6.5 scheduling

Reads the fields of pod templates that the scheduler reads, and each cluster's Target facts.

**Placement** is which nodes a pod may run on. A toleration only permits a pod onto a tainted
node; it does not put it there. So a workload counts as constrained only with a nodeSelector or a
required node affinity, and the finding is a workload that tolerates a taint but constrains
nothing. Fixes: `set-node-selector`, `set-tolerations`, `set-node-affinity`, and placement
profiles.

**Spread** is how a workload's replicas are distributed across zones and nodes: its topology
spread constraints and pod anti-affinity. A Deployment, ReplicaSet or StatefulSet with no
constraints of its own gets its cluster's default constraints, which live in the kube-scheduler
configuration. That configuration cannot be read from a cluster, and on a managed service (EKS,
GKE, AKS) it cannot be changed, so those clusters run Kubernetes' built-in defaults: a maxSkew of
3 across nodes and 5 across zones, both only preferred. `spread` reports each cluster's defaults
and where they are known from — a `Scheduling.DefaultTopologySpread` fact an operator set on the
Target, the managed distribution the collected `Cluster.Distribution` fact names, or an
assumption — and, per workload, whether its spread is its own, the cluster's default, or none.

Where the scheduler configuration cannot be changed, a fleet-wide default is written into the
workloads instead: the `topology-spread-soft` profile applied with `fleet-edit`. That makes the
default explicit and versioned per workload, at the cost of every workload carrying it.

Findings: minDomains without DoNotSchedule (the API server rejects it); a constraint whose selector
does not select the workload's own Pods, so they never count toward the skew; a topologyKey no
node carries (a hard constraint then leaves Pods Pending); minDomains above the cluster's zone count
with more replicas than the zones can hold; a Deployment's hard constraint without
`matchLabelKeys: [pod-template-hash]`, which counts both revisions' Pods during a rollout; and
required anti-affinity per node. The checks on node labels and zones use the `Cluster.Zones` and
`Cluster.NodeLabelKeys` facts, and are skipped on clusters that have not had them collected.
Fixes: `set-topology-spread` and `set-pod-anti-affinity`, which upsert one topology key and keep
the rest, and the `topology-spread-soft` and `anti-affinity-soft` profiles.

Checks that need each cluster's node pools and taints — a nodeSelector for a pool no cluster has,
or a hard spread constraint that counts tainted nodes the Pods cannot use — wait until those are
recorded on Targets.

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
| PodDisruptionBudget                                                       | `workload`      |
| `nodeSelector`, `tolerations`, `affinity` (node, pod and pod anti-affinity), `topologySpreadConstraints` | `scheduling` |
| HorizontalPodAutoscaler, KEDA ScaledObject; replica bounds                | `autoscale`     |
| ServiceMonitor; telemetry sidecar containers                              | `observability` |

`scheduling` owns every field of a pod template that the scheduler reads, because the people who
set them — operations, platform and infrastructure teams — set them together and against the same
facts about the clusters: which zones and node pools exist, which taints the nodes carry, and what
the scheduler does by default. Spreading replicas so that losing one node or zone cannot take all
of them is also an availability concern, the same one as the PodDisruptionBudget, so `workload`
keeps checking for it (`missing-spread`) and hands the fix to `scheduling`.

### 7.2 Handoffs

These are the points where one group's finding is another group's fix. A group's help and its
skills name the other group's command rather than duplicating it.

| From            | Finding                                                         | To              | Fix                                            |
| --------------- | --------------------------------------------------------------- | --------------- | ---------------------------------------------- |
| `namespace`     | `pod-security`: workloads block the next level                  | `workload`      | `harden`, then `namespace pod-security` again, then `apply-envelope` |
| `namespace`     | `backfill` created NetworkPolicy and RBAC Units from a base     | `netpol`, `rbac`| `coverage`, `findings` judge what was copied   |
| `autoscale`     | `pdb-blocks-min-scale`                                          | `workload`      | relax the PodDisruptionBudget; or `autoscale set-hpa` to raise the minimum |
| `observability` | `inject-sidecar` added a container                              | `workload`      | `readiness` scores it; `set-resources --container`, `harden` |
| `workload`      | `missing-spread`: replicas a node or zone loss can all take     | `scheduling`    | `set-topology-spread`, `set-pod-anti-affinity`, or the `topology-spread-soft` profile |
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

Some rules are about every resource, or every Unit, rather than any one group's kinds. They are
the tool-level `basic` group, installed with `cub manage guardrails install` and opted into on its
own, rather than part of the pack of a group a Space opts into for other reasons:

| Rule                     | Display name                              | Checks                                                       |
| ------------------------ | ----------------------------------------- | ------------------------------------------------------------ |
| `validate`               | Resources match their schemas             | `vet-schemas`: the Kubernetes or CRD schema, including the KEDA ScaledObjects `convert-keda` writes |
| `format`                 | YAML has no ambiguous syntax              | `vet-format`: anchors, empty values, repeated keys, yes/no booleans, old-style octals |
| `validate-dupes`         | No duplicate keys in merged lists         | `vet-merge-keys`: two containers, env vars and the like with one name |
| `vet-namespace-required` | Namespaced resources name their namespace | `vet-cel`: `metadata.namespace` on every namespaced kind     |
| `no-merge-conflicts`     | No unresolved merge conflicts             | `vet-no-merge-conflicts`: the Unit's outstanding conflicts   |
| `no-link-findings`       | Links find everything they name           | `vet-no-link-findings`: what its Links' resolves could not do |
| `vet-no-placeholders`    | No placeholders left to fill              | `vet-placeholders`, on Units released to a Target            |
| `vet-immutable`          | Immutable fields are unchanged            | `vet-immutable`, on Units released to a Target               |

The slugs are those of the Triggers these rules were first written as, by hand, so installing the
group where those Triggers exist updates them in place, keeping whether each warns or blocks,
rather than adding a second copy.

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
  required anti-affinity or a DoNotSchedule spread constraint that can leave replicas Pending on a
  cluster with too few nodes or zones (so `set-pod-anti-affinity` defaults to preferred and
  `set-topology-spread` to ScheduleAnyway); a read-only root filesystem on a container that writes
  to disk; limits low enough to throttle or kill; a PodDisruptionBudget that would stop node
  drains; a `set-namespace` that re-homes resources in a Space with running workloads.

## 9. Future work

- **One snapshot.** Each group reads its own resource kinds, and several read the same ones
  (workloads, Namespaces, PodDisruptionBudgets). One shared snapshot would make cross-group
  analysis (§7.4, §7.7) natural and a fleet-wide report cheap.
- **Tool-level `findings`, and every pack in one install.** Every group's findings in one ranked
  list, and every group's pack opted into in one command.
- **Validation over a set of Units.** Annotate-then-validate stands in for a validating function
  that can see more than one Unit. With one, the set rules — PodDisruptionBudget, NetworkPolicy and
  ServiceMonitor coverage — become ordinary guardrails, and the existing per-object validators'
  cross-object checks work as Triggers too.
- **Server-side analysis.** The engines run in the client. A map-reduce over Units on the server
  would let the analyses run where the data is; several group by a field inside the resources (the
  namespace), not by Space or Target, which such a facility would need to support.
- **Cluster facts.** A Target records its cluster's zones, node label keys and managed
  distribution, which `scheduling`'s spread checks use. Whether its network plugin enforces
  NetworkPolicy at all, and which node pools and taints it has, would qualify `netpol`'s findings
  and enable `scheduling`'s pool and taint checks.
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
