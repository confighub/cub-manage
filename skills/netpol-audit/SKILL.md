---
name: netpol-audit
description: 'Inventory and audit Kubernetes NetworkPolicy coverage stored in ConfigHub across the fleet, using the cub manage netpol CLI. Use for "what NetworkPolicies do we have?", "which namespaces have no default-deny?", "which workloads are uncovered?", "per-cluster NetworkPolicy/namespace/workload counts", "list the policies in apptique-prod", "audit our network segmentation". Not for "who can reach X" connectivity (use netpol-connectivity) or hygiene/anti-pattern checks (use netpol-findings); not for live cluster state (use kubectl); coverage is over ConfigHub-managed Units only.'
phase: verify
allowed-tools: Bash(cub manage netpol --help) Bash(cub manage netpol * --help) Bash(cub auth status) Bash(cub manage preflight) Bash(cub manage netpol snapshot) Bash(cub manage netpol snapshot *) Bash(cub manage netpol list) Bash(cub manage netpol list *) Bash(cub manage netpol coverage) Bash(cub manage netpol coverage *)
---

# netpol-audit

Inventory the NetworkPolicy-relevant config ConfigHub holds across the fleet, and report **coverage** — which namespaces have a default-deny and which workloads no policy selects. This is the "what do we have / where are the gaps" surface; it never mutates.

## Why this matters

A per-resource validator only ever sees one object, so it can't answer the questions that matter for segmentation — *does every namespace have a default-deny? is any workload uncovered?* Those are properties of the whole set. `cub manage netpol` loads a fleet snapshot (NetworkPolicies + Namespaces + workloads + Services, joined with Space/Target metadata) and reasons over it together. Clusters are ConfigHub Targets (the Space slug stands in for unbound Units). Coverage is computed over **ConfigHub-managed Units only**. Output is JSON by default; add `-o table` for humans.

## When to use

- "What NetworkPolicies do we have across the fleet?" / "audit our network segmentation."
- "Which namespaces have no default-deny ingress?" / "which workloads are uncovered?" → `coverage`.
- "Per-cluster counts of policies / namespaces / workloads / services" → `snapshot`.
- "List the NetworkPolicies (or namespaces/workloads) in cluster/namespace X" → `list`.

## Do not load for

- "Who can reach X?" / "what can X reach?" — connectivity (use **netpol-connectivity**).
- "Any allow-all / metadata-egress / asymmetry / anti-patterns?" — hygiene (use **netpol-findings**).
- Live cluster NetworkPolicy not in ConfigHub — `kubectl get netpol`.

## Preflight gates

1. `cub manage preflight` succeeds (the ConfigHub session is valid against the server). If it fails, ask the user to run `cub auth login` (an interactive browser sign-in an agent cannot complete), then retry.

## The toolkit

### Fleet inventory — `cub manage netpol snapshot`

Per-cluster counts of NetworkPolicy / Namespace / workload / Service, plus Units, gated, and unapplied. Canonical base/policy Spaces are excluded.

```bash
cub manage netpol snapshot -o table
```

### Coverage gaps — `cub manage netpol coverage`

Per namespace: is a default-deny ingress/egress present, how many workloads, how many uncovered. This is the headline audit.

```bash
cub manage netpol coverage -o table
cub manage netpol coverage --namespace payments
cub manage netpol coverage --direction ingress    # only namespaces with an ingress gap
```

### Explorer — `cub manage netpol list`

Every NetworkPolicy-relevant resource (NetworkPolicy, Namespace, workloads, Service) with its cluster, Space, and Unit.

```bash
cub manage netpol list --kind NetworkPolicy -o table
cub manage netpol list --cluster prod-cluster --namespace apptique
```

### Scoping the fleet

Scope server-side with a single Unit `--where` predicate — one Unit-level filter can reference Unit, Space, and Target metadata, so prefer it over post-filtering large JSON. Example: `--where "Unreleasable = false"` (the Units a Release delivers) or `--where "Space.Slug LIKE 'prod-%'"`.

For the standard Space labels, use `--component <slug>` (compiles to `Space.ComponentID = '<id>'`) and the shorthands `--environment`, `--region`, `--owner`, `--layer`, `--variant` (each compiles to `Space.Labels.<Key> = '<value>'`), AND-joined with any raw `--where`. ConfigHub `where` is flat AND-only — no parentheses, no `OR` (a parenthesized clause fails with `invalid attribute name`); express alternatives as separate runs.

`--cluster` / `--namespace` are client-side display filters over the fetched snapshot, not server-side scope. Units whose Space has no release Target (base/template Spaces) group under `None`.

## Stop conditions

- Snapshot empty (no Kubernetes/YAML Units in scope) — report it; suggest widening scope or checking `cub auth status` / org context.
- The question is about effective reachability or anti-patterns — hand off to **netpol-connectivity** / **netpol-findings**.

## Tool boundary

Read-only inventory + coverage. Mutations live in **netpol-fix** / **netpol-fleet**. Never use `kubectl` to answer a ConfigHub-state question.

## References

- `cub manage netpol snapshot --help`, `cub manage netpol coverage --help`, `cub manage netpol list --help`.
- Companion skills: **netpol-connectivity**, **netpol-findings**, **netpol-fix**.
