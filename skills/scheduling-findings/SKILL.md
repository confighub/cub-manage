---
name: scheduling-findings
description: 'Produce severity-ranked Kubernetes scheduling-constraint findings across a ConfigHub fleet with the cub manage scheduling CLI — controllers that tolerate a taint but do not constrain where they land, and topology spread / pod anti-affinity that cannot work as written (minDomains without DoNotSchedule, a selector that misses the workload''s own Pods, a topology key no node carries, minDomains above the zone count, a hard spread without matchLabelKeys over a rollout, required anti-affinity per node). Use for "what scheduling problems do we have?", "which workloads tolerate a taint but do not pin a node pool?", "is any spread constraint broken?", "which workloads could stay Pending because of their spread?", "scheduling findings for the ml component". Not for the raw placement / spread report (use scheduling-audit), fixing placement (use scheduling-place), fixing spread (use scheduling-spread), or PodDisruptionBudget coverage (use workload-availability); analysis is over ConfigHub-managed Units only.'
phase: verify
allowed-tools: Bash(cub manage scheduling --help) Bash(cub manage scheduling * --help) Bash(cub auth status) Bash(cub manage preflight) Bash(cub manage scheduling findings) Bash(cub manage scheduling findings *)
---

# scheduling-findings

Report scheduling-constraint anti-patterns — placement and spread — across the fleet, most-severe first. Read-only.

## Why this matters

Each analyzer flags a constraint that does not do what its author meant:

| Analyzer | Severity | Flags |
| --- | --- | --- |
| `toleration-without-placement` | Medium | Tolerates a taint but has no nodeSelector and no required node affinity. A toleration only *permits* scheduling onto tainted nodes, so the pod may still land on general nodes. |
| `spread-min-domains-not-hard` | High | `minDomains` without `whenUnsatisfiable: DoNotSchedule`, which the API server rejects. |
| `unknown-topology-key` | High / Low | A spread constraint or pod (anti-)affinity term on a node label no node in the cluster carries: a hard spread constraint leaves the Pods `Pending` (High); anything else has no effect (Low). |
| `spread-selector-misses-own-pods` | Medium | A spread constraint whose labelSelector does not select the workload's own Pods, so they never count toward the skew and can pile into one domain. |
| `spread-min-domains-exceeds-zones` | Medium | `minDomains` above the cluster's zone count, with more replicas than the zones can then hold; the rest stay `Pending`. |
| `spread-rollout-without-match-label-keys` | Low | A Deployment's hard spread constraint without `matchLabelKeys: [pod-template-hash]`, so a rollout counts both revisions' Pods together. |
| `required-anti-affinity-per-node` | Low | Required pod anti-affinity per node, which blocks scaling past the node count and a rollout's surge. |

The checks on node labels and zones use the facts collected onto each cluster's Target (`Cluster.NodeLabelKeys`, `Cluster.Zones`) and are skipped where those were not collected. Checks that need cluster node-pool / taint facts (a nodeSelector for a pool no cluster advertises; a nodeSelector onto a tainted pool with no matching toleration) wait for those Target facts. Analysis is over **ConfigHub-managed Units only**.

## When to use

- "What scheduling problems do we have?" → `findings`.
- "Which workloads tolerate a taint but don't pin a node pool?" → `findings --analyzer toleration-without-placement`.
- "Is any spread constraint broken?" / "could any workload stay Pending because of its spread?" → `findings` (the spread analyzers), or `--analyzer spread-min-domains-not-hard,unknown-topology-key,…`.
- "Scheduling findings for the ml component / in prod." → `findings --component ml` / `--environment prod`.

## Do not load for

- The raw per-workload placement or spread report — use **scheduling-audit** (`placement`, `spread`).
- Fixing placement — use **scheduling-place**.
- Fixing spread — use **scheduling-spread**.
- PodDisruptionBudget coverage, or which workloads have no spread at all (`missing-spread`) — use **workload-availability** / **workload-findings**.

## Preflight gates

1. `cub manage preflight` succeeds. If it fails, ask the user to run `cub auth login` (interactive) and retry.

## The toolkit

- `cub manage scheduling findings -o table` — severity-ranked findings (`--severity`, `--analyzer <name>[,<name>…]`, `--cluster`, `--namespace`).

Server-side `--where` + label shorthands scope the fleet; `--cluster` / `--namespace` are client-side.

## Stop conditions

- Zero findings — report the fleet is clean for that scope.
- The user wants to fix something — hand off to **scheduling-place** (`toleration-without-placement`) or **scheduling-spread** (every spread and anti-affinity finding, e.g. `set-topology-spread … --when-unsatisfiable DoNotSchedule` for `spread-min-domains-not-hard`, `set-pod-anti-affinity … --remove` plus a hostname `set-topology-spread` for `required-anti-affinity-per-node`).

## Tool boundary

Read-only. Never use `kubectl` to answer a ConfigHub-state question.

## References

- `cub manage scheduling findings --help`.
- Companion skills: **scheduling-audit**, **scheduling-place**, **scheduling-spread**.
