---
name: workload-availability
description: 'Report the disruption-survival posture of multi-replica Kubernetes workloads across a ConfigHub fleet with the cub manage workload CLI — PodDisruptionBudget coverage (does a matching PDB exist in some other Unit?), the minAvailable>=replicas / maxUnavailable:0 eviction-lock footgun, and pod anti-affinity / topology spread presence. Use for "which multi-replica workloads have no PDB?", "is workload X covered by a PodDisruptionBudget?", "which workloads could lose every replica to one node/zone failure?", "does this workload have anti-affinity?". Not for security/resources/probes scoring (use workload-audit), ranked findings (use workload-findings), the spread each workload gets or its cluster''s default spread (use scheduling-audit), PDB fixes (use workload-harden), or spread fixes (use scheduling-spread); analysis is over ConfigHub-managed Units only.'
phase: verify
allowed-tools: Bash(cub manage workload --help) Bash(cub manage workload * --help) Bash(cub auth status) Bash(cub manage preflight) Bash(cub manage workload availability) Bash(cub manage workload availability *) Bash(cub manage workload snapshot) Bash(cub manage workload snapshot *)
---

# workload-availability

Report which multi-replica workloads can survive a voluntary disruption (a node drain, a zone loss): whether a **PodDisruptionBudget** matches them, whether that PDB is so tight it blocks *all* evictions, and whether they declare pod anti-affinity or topology spread. Read-only.

## Why this matters

**PDB coverage is a cross-Unit property.** Under one-resource-per-Unit, a workload and its PodDisruptionBudget live in *separate* Units, so a per-Unit validating Trigger sees the workload alone and cannot tell whether a matching PDB exists — it structurally false-positives. `cub manage workload` does the selector→pod join over the whole fleet snapshot (matching a PDB's `selector` against a workload's pod-template labels within its namespace + cluster) and reports true coverage. It also flags the highest-signal PDB footgun — `minAvailable >= replicas` or `maxUnavailable: 0`, which makes node drains hang forever — which needs the PDB read *together with* the workload's replica count. Analysis is over **ConfigHub-managed Units only**.

## When to use

- "Which multi-replica workloads have no matching PDB?" → `availability` (or `--issues-only`).
- "Is workload X covered by a PodDisruptionBudget?" → `availability --namespace X`.
- "Which workloads could lose every replica to one node/zone failure?" → `availability` (SPREAD = no).
- "Any PDBs that block all evictions?" → `availability` (BLOCKS-EVICT = yes).

## Do not load for

- Security context / resources / probes scoring — use **workload-audit** (`readiness`).
- The ranked list of everything wrong — use **workload-findings**.
- Fixing PDB coverage (`ensure-pdb`) — use **workload-harden**.
- Fixing spread (`cub manage scheduling set-topology-spread` / `set-pod-anti-affinity`) — use **scheduling-spread**.
- Which spread a workload without its own gets from its cluster's defaults — use **scheduling-audit** (`cub manage scheduling spread`).
- Live cluster state — `kubectl`.

## Preflight gates

1. `cub manage preflight` succeeds. If it fails, ask the user to run `cub auth login` (interactive) and retry.

## The toolkit

### Availability report — `cub manage workload availability`

Per multi-replica workload: replicas, matching PDB (or MISSING), whether the PDB blocks all evictions, and whether it declares spread of its own (SPREAD = no means it relies on its cluster's default spread, which `cub manage scheduling spread` reports). Single-replica workloads and DaemonSet / Job / CronJob / Pod are out of scope (a PDB gains a single instance nothing).

```bash
cub manage workload availability -o table
cub manage workload availability --issues-only -o table         # just the gaps
cub manage workload availability --cluster prod-cluster --namespace checkout
```

### Scoping

Same server-side `--where` + `--component` and the label shorthands (`--environment`, …) as the other read commands; `--cluster` / `--namespace` are client-side display filters.

## Stop conditions

- No multi-replica workloads in scope — report it (nothing to cover).
- The question is about non-availability readiness or a ranked finding list — hand off to **workload-audit** / **workload-findings**.

## Tool boundary

Read-only. Fixing coverage (`ensure-pdb`) lives in **workload-harden**; fixing spread (`cub manage scheduling set-topology-spread`) lives in **scheduling-spread**. Never use `kubectl`.

## References

- `cub manage workload availability --help`.
- Companion skills: **workload-audit**, **workload-findings**, **workload-harden**, **scheduling-spread**, **scheduling-audit**.
