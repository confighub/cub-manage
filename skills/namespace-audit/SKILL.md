---
name: namespace-audit
description: 'Inventory Kubernetes namespaces stored in ConfigHub across the fleet, using the cub manage namespace CLI — the Namespace objects and the workloads in each namespace, per cluster. Use for "what namespaces do we have?", "per-cluster namespace/workload counts", "list the namespaces in apptique", "which workloads run in namespace X?". Not for namespaces missing a Namespace object or colliding on a cluster (use namespace-findings), pod-security levels (use namespace-pod-security), NetworkPolicy coverage (use netpol-audit) or RBAC (use rbac-audit). Not for cross-variant consistency (use namespace-consistency) or live cluster state (use kubectl); analysis is over ConfigHub-managed Units only.'
phase: verify
allowed-tools: Bash(cub manage namespace --help) Bash(cub manage namespace * --help) Bash(cub auth status) Bash(cub manage preflight) Bash(cub manage namespace snapshot) Bash(cub manage namespace snapshot *) Bash(cub manage namespace list) Bash(cub manage namespace list *)
---

# namespace-audit

Inventory the namespaces ConfigHub holds across the fleet: the Namespace objects, and the workloads in each namespace, per cluster. This is the "what do we have" surface; it never mutates. Where the gaps are -- a namespace without a Namespace object, two colliding on one cluster -- is **namespace-findings**'; which Pod Security Standard level each namespace sets and meets is **namespace-pod-security**'s.

NetworkPolicy coverage is **`cub manage netpol`**'s subject and RBAC is **`cub manage rbac`**'s.

## Why this matters

A runtime tenancy controller (Capsule, the retired HNC) governs one cluster; a per-resource validator sees one object. Neither can answer *what namespaces does the fleet have, and what runs in each?* — that is a property of the whole set of resources, joined across types, over the fleet's source of record. `cub manage namespace` loads a fleet snapshot (Namespaces + the workloads occupying them, joined with Space/Target metadata) and reasons over it. Clusters are ConfigHub Targets; Units whose Space has no release Target group under a single `None` cluster. Analysis is over **ConfigHub-managed Units only**. Output is JSON by default; add `-o table` for humans.

## When to use

- "What namespaces do we have?" / "audit our namespace governance."
- "Per-cluster counts of namespaces and workloads" → `snapshot`.
- "List the namespaces (or the workloads in one) in cluster X" → `list`.

## Do not load for

- "Is a component's namespace the same across dev/prod?" — cross-variant consistency (use **namespace-consistency**).
- "Which namespaces are missing a Namespace object, or collide on a cluster?" / "give me the ranked list of everything wrong" — governance findings (use **namespace-findings**).
- "Which namespaces have no pod-security label?" / "what level can namespace X enforce?" — use **namespace-pod-security**.
- Live cluster state not in ConfigHub — `kubectl get ns`.
- Fixing gaps (scaffold / backfill) — that is a later write skill.

## Preflight gates

1. `cub manage preflight` succeeds (the ConfigHub session is valid against the server). If it fails, ask the user to run `cub auth login` (an interactive browser sign-in an agent cannot complete), then retry.

## The toolkit

### Fleet inventory — `cub manage namespace snapshot`

Per-cluster counts of Namespace and workload, plus Units, gated, and unapplied. Canonical base/policy Spaces are excluded.

```bash
cub manage namespace snapshot -o table
```

### Explorer — `cub manage namespace list`

Every Namespace and the workloads occupying it, with its cluster, Space, and Unit.

```bash
cub manage namespace list --kind Namespace -o table
cub manage namespace list --cluster prod-cluster --namespace apptique
```

### Scoping the fleet

Scope server-side with a single Unit `--where` predicate — one Unit-level filter can reference Unit, Space, and Target metadata, so prefer it over post-filtering large JSON. Example: `--where "Unreleasable = false"` (the Units a Release delivers) or `--where "Space.Slug LIKE 'apptique-%'"`.

For the standard Space labels, use `--component <slug>` (compiles to `Space.ComponentID = '<id>'`) and the shorthands `--environment`, `--region`, `--owner`, `--layer`, `--variant` (each compiles to `Space.Labels.<Key> = '<value>'`), AND-joined with any raw `--where`. ConfigHub `where` is flat AND-only — no parentheses, no `OR` (a parenthesized clause fails with `invalid attribute name`); express alternatives as separate runs.

`--cluster` / `--namespace` are client-side display filters over the fetched snapshot, not server-side scope. Units whose Space has no release Target (base/template Spaces) group under `None`.

## Stop conditions

- Snapshot empty (no Kubernetes/YAML Units in scope) — report it; suggest widening scope or checking `cub auth status` / org context.
- The question is about cross-variant consistency, a ranked finding list or pod security — hand off to **namespace-consistency** / **namespace-findings** / **namespace-pod-security**.

## Tool boundary

Read-only inventory. Fixing gaps (scaffold / backfill / enforce) lives in the write skills. Never use `kubectl` to answer a ConfigHub-state question.

## References

- `cub manage namespace snapshot --help`, `cub manage namespace list --help`.
- Companion skills: **namespace-consistency**, **namespace-findings**, **namespace-pod-security**.
