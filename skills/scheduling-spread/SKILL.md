---
name: scheduling-spread
description: 'Set how Kubernetes workload replicas spread across zones and nodes as config-as-data with the cub manage scheduling CLI — topology spread constraints and pod anti-affinity against the workload''s own Pods, one workload or across a whole selector via reusable spread profiles (topology-spread-soft, anti-affinity-soft). Reads each cluster''s default spread constraints first, since a workload with none of its own gets them. Use for "spread this deployment across zones", "spread the replicas across nodes so a node loss doesn''t take them all", "add pod anti-affinity", "remove the hard anti-affinity", "spread every prod workload across zones", "apply the topology-spread-soft profile". Dry-run by default; requires --commit --change-desc. Not for nodeSelector / tolerations / node affinity (use scheduling-place), read-only checks (use scheduling-audit / scheduling-findings), PodDisruptionBudgets (use workload-harden), or enforcement Triggers (use scheduling-guardrails).'
phase: act
allowed-tools: Bash(cub manage scheduling --help) Bash(cub manage scheduling * --help) Bash(cub auth status) Bash(cub manage preflight) Bash(cub manage scheduling spread) Bash(cub manage scheduling spread *) Bash(cub manage scheduling findings *) Bash(cub manage scheduling set-topology-spread *) Bash(cub manage scheduling set-pod-anti-affinity *) Bash(cub manage profile) Bash(cub manage profile *) Bash(cub manage scheduling fleet-edit *)
---

# scheduling-spread

Set how a workload's replicas spread across zones and nodes — as data, dry-run by default. One workload, or across a `--where` selector via a spread profile.

- **`spread`** (read first) — per cluster, the default topology spread constraints its scheduler gives a Pod with none of its own; per workload, the constraints that apply and their source (`explicit` | `cluster-default` | `none`), and its pod anti-affinity terms. `--issues-only` shows the workloads with no constraints of their own.
- **`set-topology-spread <space>/<unit>`** — set the constraint for one topology key, replacing any on that key and keeping the others. Defaults: `--topology-key topology.kubernetes.io/zone`, `--max-skew 1`, `--when-unsatisfiable ScheduleAnyway`, `--match-label-keys pod-template-hash`. `--remove` takes the constraint on `--topology-key` away.
- **`set-pod-anti-affinity <space>/<unit>`** — set a pod anti-affinity term on one topology key (default preferred, per node: `kubernetes.io/hostname`). `--required` makes it `requiredDuringScheduling`; `--remove` takes the term away.
- **`cub manage profile install | list | apply`** — the spread profiles in the shared library (stored Invocations): `topology-spread-soft` (zones then nodes, maxSkew 1, ScheduleAnyway, per Deployment revision) and `anti-affinity-soft` (preferred pod anti-affinity across nodes).
- **`fleet-edit --profile <slug> [--where …]`** — apply a profile across a selector in one operation.

Both constraint kinds select the workload's own Pods (its pod-template labels), so they count toward the skew. All writes **edit Units but do not publish them** — rolling out is a separate `cub release publish <space>`, which bundles the Space's Units for its release Target.

## Why this matters

A Deployment, ReplicaSet or StatefulSet with no spread constraints of its own gets its cluster's defaults. A cluster's defaults are its kube-scheduler configuration, which cannot be read from the cluster: `spread` takes them from the Target's `Scheduling.DefaultTopologySpread` fact when an operator recorded one, and otherwise assumes Kubernetes' built-in defaults (hostname maxSkew 3, zone maxSkew 5, both ScheduleAnyway), which is what EKS, GKE and AKS run (`defaultSource`: `target-fact` | `managed-distribution` | `assumed`). Those defaults are soft and loose — a zone may hold five more replicas than another — so a workload that must survive a zone loss needs a constraint of its own. On a managed cluster, whose scheduler configuration cannot be changed, `topology-spread-soft` applied with `fleet-edit` is the explicit, fleet-wide substitute for a cluster-level default.

`cub manage scheduling` edits the source of record with `set-yq` under the hood; everything is **dry-run by default** and requires `--commit --change-desc`, and never bypasses ValidationErrors.

## When to use

- "Spread this deployment across zones." → `set-topology-spread <space>/<unit>`.
- "Spread the replicas across nodes so one node loss doesn't take them all." → `set-topology-spread <space>/<unit> --topology-key kubernetes.io/hostname` (or `set-pod-anti-affinity <space>/<unit>`).
- "Add pod anti-affinity." → `set-pod-anti-affinity <space>/<unit>` (preferred, per node).
- "Remove the hard anti-affinity that blocks scaling." → `set-pod-anti-affinity <space>/<unit> --remove`, then usually `set-topology-spread … --topology-key kubernetes.io/hostname`.
- "Spread every prod workload across zones." → `fleet-edit --profile topology-spread-soft --environment prod`.
- "What spread does workload X get today?" → `spread --cluster <c> --namespace <ns>` before changing anything.

## Do not load for

- nodeSelector / tolerations / node affinity (which nodes a workload may land on) — use **scheduling-place**.
- Read-only checks — use **scheduling-audit** (`spread`, `placement`) / **scheduling-findings**.
- PodDisruptionBudgets — use **workload-harden** (`cub manage workload ensure-pdb`).
- Enforcement Triggers — use **scheduling-guardrails**.
- Publishing the Space's Release — that is `cub release publish <space>`.

## Preflight gates

1. `cub manage preflight` succeeds. If it fails, ask the user to run `cub auth login` (interactive) and retry.
2. For `cub manage profile apply` / `fleet-edit`, the library exists — run `cub manage profile install` once if `cub manage profile list` is empty.
3. The user has write permission on the target Space(s).

## The loop

1. **See what applies now**: `cub manage scheduling spread -o table` (cluster defaults and each workload's source), `--issues-only` for the workloads with no constraints of their own; `cub manage scheduling findings` for constraints that cannot work as written.
2. **Preview** (dry-run — the default):
   ```bash
   cub manage scheduling set-topology-spread web-prod/web                                  # zones, soft
   cub manage scheduling set-topology-spread web-prod/web --topology-key kubernetes.io/hostname
   cub manage scheduling set-pod-anti-affinity web-prod/web                                # preferred, per node
   cub manage scheduling fleet-edit --profile topology-spread-soft --environment prod
   ```
   Each reports whether the Unit *would* change and the diff. Read the diff before committing, and show it to the user — it is what they are agreeing to.
3. **Commit** with `--commit --change-desc` (summary + verbatim user prompt).
4. **Verify**: `cub manage scheduling spread --cluster <c> --namespace <ns>` shows the workload's source as `explicit`, with the constraint you set; `findings` clears.
5. **Roll out** is a separate step — hand off to `release-publish`.

## Safety

- Prefer `--when-unsatisfiable ScheduleAnyway` (the default): `DoNotSchedule` leaves replicas `Pending` when a domain has no room.
- Prefer preferred anti-affinity (the default) or a topology spread constraint over `--required`: required anti-affinity per node leaves a replica `Pending` once every node has one, which blocks scaling past the node count and a rollout's surge.
- `--min-domains` needs `--when-unsatisfiable DoNotSchedule`; the API server rejects `minDomains` without it.
- Keep `--match-label-keys pod-template-hash` (the default) on Deployments, so a rollout spreads each revision on its own rather than counting both revisions' Pods together.
- Scope `fleet-edit` narrowly first (dry-run shows the count) before a fleet-wide commit.

## Stop conditions

- A ValidationError attaches (a validating Trigger failed). **Do not bypass** — fix the data (or the rule), via **triggers-and-applygates**.
- The user wants placement changed rather than spread — hand off to **scheduling-place**.
- The user wants the change deployed — hand off to `release-publish`.

## Tool boundary

Allowed: `set-topology-spread`, `set-pod-anti-affinity`, `cub manage profile`, `fleet-edit` (dry-run by default; `--commit` passes `--change-desc`), and the read commands `spread` / `findings`. Not allowed: bypassing gates, `kubectl` mutations, publishing a Release.

## References

- `cub manage scheduling spread --help`, `… set-topology-spread --help`, `… set-pod-anti-affinity --help`, `… fleet-edit --help`, `cub manage profile --help`.
- Companion skills: **scheduling-audit**, **scheduling-findings**, **scheduling-place**, **workload-availability**, `promote-release`, `triggers-and-applygates`, `release-publish`.
