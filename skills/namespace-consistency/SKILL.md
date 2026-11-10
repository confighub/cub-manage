---
name: namespace-consistency
description: 'Check whether each of a component''s workloads runs in the same namespace on every cluster, and its pod-security level is the same across its variant Spaces (environments / regions / clusters) in ConfigHub, using the cub manage namespace CLI. Use for "is our namespace the same across dev/staging/prod?", "which components have drifted namespace names?", "is pod-security consistent across variants of X?", "check cross-variant namespace consistency". Not for a namespace's own Namespace object or pod-security label (use namespace-findings / namespace-pod-security) or the full ranked findings list (use namespace-findings); read-only, ConfigHub-managed Units only.'
phase: verify
allowed-tools: Bash(cub manage namespace --help) Bash(cub manage namespace * --help) Bash(cub auth status) Bash(cub manage preflight) Bash(cub manage namespace consistency) Bash(cub manage namespace consistency *)
---

# namespace-consistency

Report, per component, whether each of its workloads runs in the **same namespace** on every cluster and its **pod-security enforce level** is the same in every variant Space. It never mutates.

## Why this matters

The invariant is that a workload keeps its namespace on every cluster — dev, every prod region. A component may span several namespaces, and a variant may run parts another does not. Variants that share a cluster, such as staging beside prod or a preview per pull request, have to use different namespaces, so they are not compared with each other or the rest. Whether that actually holds is a property of a **set of Spaces** (the component's variants), which no per-cluster controller and no per-resource validator can see: each sees only one cluster or one object. `cub manage namespace consistency` groups the fleet's namespaces by the Component their Space is a Variant of and compares across the variants. This is the read side of the namespace-name invariant; enforcing it is a separate per-cluster `set-namespace` Trigger. Only Variants of a Component participate; canonical base Spaces are excluded.

## When to use

- "Is the apptique namespace the same in dev and prod?"
- "Which components have drifted namespace names across environments/regions?" → `consistency --issues-only`.
- "Is pod-security consistent across variants of component X?" → `consistency --component X`.
- "Check cross-variant namespace consistency across the fleet."

## Do not load for

- "Is namespace X missing a Namespace object or pod-security labels?" — per-namespace completeness (use **namespace-audit**).
- "Give me the ranked list of everything wrong" — governance findings (use **namespace-findings**).
- Live cluster state — `kubectl`.

## Preflight gates

1. `cub manage preflight` succeeds. If it fails, ask the user to run `cub auth login` (interactive) and retry.

## The toolkit

### Cross-variant consistency — `cub manage namespace consistency`

Per component: the variant Spaces, the distinct namespace name(s) and pod-security level(s) seen across them, and whether they are consistent (plus a description of any drift).

```bash
cub manage namespace consistency -o table
cub manage namespace consistency --issues-only -o table   # just the drift
cub manage namespace consistency --component apptique
```

A component is **inconsistent** when a workload runs in a different namespace on one cluster than another (the invariant is broken — the fix is the `namespace-set` profile: `cub manage namespace fleet-edit --profile namespace-set --component <c> --param namespace=<ns>`) or on the pod-security level.

### Scoping the fleet

Scope server-side with a single Unit `--where` predicate (Unit / `Space.*` / `Target.*`), plus `--component` (the Spaces that are Variants of that Component, `Space.ComponentID`) and the label shorthands `--environment`, `--region`, `--owner`, `--layer`, `--variant` (each compiles to `Space.Labels.<Key> = '<value>'`), AND-joined. ConfigHub `where` is flat AND-only — no parentheses, no `OR` (a parenthesized clause fails with `invalid attribute name`). Example: `--where "Unreleasable = false"` (the Units a Release delivers).

Note: `--component` scopes which Units are fetched to one Component's variants, which is the way to report on one component. Consistency needs to see *all* of a component's variant Spaces, so scoping narrower than a whole component can hide drift — scope by Component, environment or region, not by a single Space.

## Stop conditions

- No Space is a Variant of a Component — report it; consistency groups variants by their Component.
- The user wants to *fix* a drifted namespace — that is a write: the `namespace-set` profile, applied with `cub manage namespace fleet-edit --profile namespace-set --component <c>`.

## Tool boundary

Read-only. Fixing drift lives in the write skills. Never use `kubectl` to answer a ConfigHub-state question.

## References

- `cub manage namespace consistency --help`.
- Companion skills: **namespace-audit**, **namespace-findings**.
