---
name: namespace-backfill
description: 'Fix namespace gaps as config-as-data with the cub manage namespace CLI — set each namespace''s Pod Security Standard to the most restrictive level its workloads meet, on the Namespace and its workloads (pod-security set), and clone a base Space''s namespace policy Units, such as its Namespace, into an existing Space, re-homed with set-namespace (backfill). Use for "add pod-security labels to namespace X", "raise namespace X to restricted", "backfill the base namespace Units into apptique-prod", "fix the missing-pod-security or missing-namespace-object findings". Dry-run by default; requires --commit --change-desc. Not for read-only checks (use namespace-audit / namespace-findings) or enforcement Triggers (a later skill).'
phase: act
allowed-tools: Bash(cub manage namespace --help) Bash(cub manage namespace * --help) Bash(cub auth status) Bash(cub manage preflight) Bash(cub manage namespace findings *) Bash(cub manage namespace pod-security *) Bash(cub manage namespace backfill *)
---

# namespace-backfill

Fix the gaps `namespace-findings` / `namespace-audit` surface — **as data**, so the cluster converges through the normal apply pipeline with no drift. Two commands:

- **`pod-security set`** — labels each selected namespace with the most restrictive Pod Security Standard level all its workloads meet (or `--level`), and copies the label onto the namespace's workloads so the `namespace-pod-security-standard` guardrail checks each against it. It never lowers a level: a namespace that already enforces a stricter one keeps it, and its workloads get copies, so the guardrail flags the ones it rejects. The fix for `missing-pod-security`.
- **`backfill`** — clones a base Space's namespace policy Units, such as its Namespace, into an existing Space and re-homes them with `set-namespace`. The fix for `missing-namespace-object`. What the base defines is the base's business; backfill clones and re-homes without reading the policies.

Both **create/edit Units but do not publish them** — rolling out is a separate, deliberate `cub release publish <space>`, which bundles the Space's Units for its release Target.

## Why this matters

A runtime tenancy controller injects policy objects into live namespaces; correcting the template doesn't reconcile what's already there, and ad-hoc `kubectl` fixes drift from the source of record. `cub manage namespace` fixes the **data**: `pod-security set` uses the hermetic, idempotent `set-label`, on the Namespace and on each workload's own metadata (never its pod template, so nothing restarts); `backfill` clones from a base Space (such as one the installer's `namespace-envelope` package created) via the bulk-clone API. Both are **dry-run by default** and require `--commit --change-desc`.

## When to use

- "Add pod-security labels to namespace X." / "fix the missing-pod-security findings." / "raise namespace X to restricted." → `pod-security set --namespace <ns>` (or `--space <s>`, `--cluster <c>`).
- "Backfill the base namespace Units into apptique-prod." / "fix the missing-namespace-object findings." → `backfill --space <s> --from <base> --namespace <ns>`.

## Do not load for

- Read-only checks — use **namespace-audit** / **namespace-findings** / **namespace-pod-security**.
- Cross-variant consistency — use **namespace-consistency**.
- Enforcement Triggers / guardrails — a later skill.
- Publishing the Space's Release — that is `cub release publish <space>` (a separate, deliberate step).

## Preflight gates

1. `cub manage preflight` succeeds. If it fails, ask the user to run `cub auth login` (interactive) and retry.
2. The user has write permission on the target Space(s).

## The loop

1. **See the gap** (read-only): `cub manage namespace findings`, or `cub manage namespace pod-security --status unlabeled`.
2. **Preview** the fix (dry-run — the default):
   ```bash
   cub manage namespace pod-security set --where "Space.Slug = 'apptique-prod'"
   cub manage namespace backfill --space apptique-prod --from namespace-envelope --namespace apptique
   ```
   `pod-security set` selects namespaces with the fleet scope (`--where`, `--component`, the label shorthands) narrowed by `--cluster` / `--namespace` — the scope picks the Namespace Units to label, while each namespace is still judged by all of its workloads, wherever their Units are. It lists each selected namespace with the level it would apply (the ACHIEVABLE column of `cub manage namespace pod-security`), `keep <level>` where the Namespace's own label stays, or why it is skipped (cluster-owned, unchecked workloads, no workloads and no `--level`, or workloads below `--level`), then each Unit's diff. To raise a level, run `cub manage namespace pod-security` first, fix what blocks the next level (`cub manage workload harden`, for one), and re-run `pod-security` to confirm before applying. `backfill` reports the base Units it would clone, each checked by the server, in the shared write report (it cannot show `set-namespace`'s changes to clones that do not exist yet) — it **excludes the base's Namespace when the dest already has one** (cloning it would create a `duplicate-namespace` collision), so it adds only the members the destination lacks.
3. **Commit** with `--commit --change-desc` (compose the description: summary, verbatim user prompt, condensed clarifications):
   ```bash
   cub manage namespace pod-security set --where "Space.Slug = 'apptique-prod'" --commit \
     --change-desc "Apply the Pod Security Standard level the workloads meet. User prompt: ..."
   cub manage namespace backfill --space apptique-prod --from namespace-envelope --namespace apptique --commit \
     --change-desc "Backfill the base namespace Units. User prompt: ..."
   ```
   Re-runs are idempotent (`pod-security set` no-ops; `backfill` clones with allow-exists).
4. **Verify**: `cub manage namespace pod-security --cluster <c> --namespace <ns>` shows the level enforced; `cub manage namespace findings --cluster <c>` shows the gaps cleared.
5. **Roll out** is a separate step — hand off to `release-publish` (the Units are created/edited, not released).

## Stop conditions

- A ValidationError attaches (a validating Trigger failed). **Do not bypass** — diagnose and fix the data (or the Trigger), via **triggers-and-applygates**.
- `backfill` reports "nothing missing" — the destination already has everything the base defines; nothing to do.
- The user wants the change deployed — hand off to `release-publish`.

## Tool boundary

Allowed: `pod-security set`, `backfill` (dry-run by default; `--commit` passes `--change-desc`), and the read commands. Not allowed: bypassing gates, `kubectl` mutations, publishing a Release.

## References

- `cub manage namespace pod-security set --help`, `cub manage namespace backfill --help`.
- Companion skills: **namespace-audit**, **namespace-findings**, `kubernetes-resources`, `triggers-and-applygates`, `release-publish`.
