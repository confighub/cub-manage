---
name: scheduling-audit
description: 'Inventory and report the scheduling constraints of Kubernetes workloads stored in ConfigHub across a fleet with the cub manage scheduling CLI — where each workload is allowed to land (nodeSelector, tolerations, node affinity) and how its replicas spread (topology spread constraints, pod anti-affinity, and each cluster''s default spread constraints). Use for "where do our workloads schedule?", "which workloads pin to a node pool?", "what does workload X tolerate?", "which workloads are unconstrained?", "how do workload X''s replicas spread?", "which workloads rely on the cluster''s default spread?", "what default spread does cluster X apply?", "per-cluster placement counts". Not for scheduling anti-pattern findings (use scheduling-findings), placement fixes (use scheduling-place), spread fixes (use scheduling-spread), or live cluster state (use kubectl); analysis is over ConfigHub-managed Units only.'
phase: verify
allowed-tools: Bash(cub manage scheduling --help) Bash(cub manage scheduling * --help) Bash(cub auth status) Bash(cub manage preflight) Bash(cub manage scheduling snapshot) Bash(cub manage scheduling snapshot *) Bash(cub manage scheduling list) Bash(cub manage scheduling list *) Bash(cub manage scheduling placement) Bash(cub manage scheduling placement *) Bash(cub manage scheduling spread) Bash(cub manage scheduling spread *)
---

# scheduling-audit

Report where the workloads ConfigHub holds are allowed to land — their nodeSelector, tolerations, and node affinity — and how their replicas spread — topology spread constraints, pod anti-affinity, and each cluster's defaults — across the fleet. Read-only.

## Why this matters

Placement is "which nodes a pod may land on"; spread is "how its replicas are distributed over zones and nodes." `cub manage scheduling` loads a fleet snapshot of pod-bearing workloads and reports each one's placement and spread, so you can see at a glance which workloads pin to a node pool, which tolerate taints, and which are unconstrained (land anywhere). Clusters are ConfigHub Targets (the Space slug stands in for unbound Units). Analysis is over **ConfigHub-managed Units only**. Output is JSON by default; add `-o table` for humans.

Note the distinction the report makes: **tolerations alone do not constrain placement** — they only permit scheduling onto tainted nodes. A workload is "constrained" only if it has a nodeSelector or a required node affinity.

A Deployment, ReplicaSet or StatefulSet with **no spread constraints of its own gets its cluster's defaults**. Those are the cluster's kube-scheduler configuration, which cannot be read from the cluster: `spread` takes them from the Target's `Scheduling.DefaultTopologySpread` fact when an operator recorded one, and otherwise assumes Kubernetes' built-in defaults (hostname maxSkew 3, zone maxSkew 5, both ScheduleAnyway), which is what EKS, GKE and AKS run. `defaultSource` says which: `target-fact` | `managed-distribution` | `assumed`. The built-in defaults are soft and loose, so a workload relying on them is not guaranteed to survive a zone loss.

## When to use

- "Where do our workloads schedule?" / "audit placement." → `placement`.
- "Which workloads pin to a node pool / set a nodeSelector?" → `placement` (NODESELECTOR column) or `snapshot`.
- "What does workload X tolerate?" → `placement --namespace X` / `--cluster X`.
- "Which workloads are unconstrained?" → `placement --issues-only`.
- "How do workload X's replicas spread?" / "does it have anti-affinity?" → `spread --namespace X` / `--cluster X` (SOURCE: `explicit` | `cluster-default` | `none`).
- "Which workloads have no spread constraints of their own?" → `spread --issues-only`.
- "What default spread does cluster X apply?" → `spread --cluster X` (the cluster table: DEFAULT-SPREAD and SOURCE).
- "Per-cluster placement and spread counts." → `snapshot`.
- "List the workloads in cluster/namespace X." → `list`.

## Do not load for

- Placement anti-patterns, ranked — use **scheduling-findings**.
- Changing placement — use **scheduling-place**.
- Changing spread (topology spread / pod anti-affinity) — use **scheduling-spread**.
- PodDisruptionBudget coverage — use **workload-availability**.
- Live cluster state — `kubectl`.

## Preflight gates

1. `cub manage preflight` succeeds. If it fails, ask the user to run `cub auth login` (interactive) and retry.

## The toolkit

- `cub manage scheduling snapshot -o table` — per-cluster workload counts + how many set nodeSelector / tolerations / node affinity / topology spread / pod anti-affinity.
- `cub manage scheduling placement -o table` — per-workload nodeSelector, tolerations, node affinity, and whether it's constrained (`--issues-only`, `--cluster`, `--namespace`).
- `cub manage scheduling spread -o table` — per cluster, its distribution, zones and default spread constraints with their source; per workload, the spread that applies, its source (`explicit` | `cluster-default` | `none`), and its pod anti-affinity terms (`--issues-only` for workloads with no constraints of their own, `--cluster`, `--namespace`). A Target fact that cannot be parsed is reported, and the built-in defaults assumed.
- `cub manage scheduling list -o table` — the workload explorer (`--kind`, `--cluster`, `--namespace`).

### Scoping

Server-side `--where` plus `--component` (one Component's variant Spaces) and the label shorthands (`--environment`, `--region`, `--owner`, `--layer`, `--variant`), AND-only. `--cluster` / `--namespace` are client-side display filters.

## Stop conditions

- Snapshot empty — report it; suggest widening scope or checking `cub auth status`.
- The question is about anti-patterns, fixing placement or fixing spread — hand off to **scheduling-findings** / **scheduling-place** / **scheduling-spread**.

## Tool boundary

Read-only. Never use `kubectl` to answer a ConfigHub-state question.

## References

- `cub manage scheduling snapshot --help`, `cub manage scheduling placement --help`, `cub manage scheduling spread --help`, `cub manage scheduling list --help`.
- Companion skills: **scheduling-findings**, **scheduling-place**, **scheduling-spread**, **workload-availability**.
