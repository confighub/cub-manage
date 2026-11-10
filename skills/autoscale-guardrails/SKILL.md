---
name: autoscale-guardrails
description: 'Install and inspect the autoscaling enforcement pack with the cub manage autoscale CLI — a Warn=true vet-cel Trigger in the common Space that flags any HPA/ScaledObject that is pinned (min == max), added to in-scope Spaces as the autoscale guardrail group — and the tool-level basic group (cub manage guardrails install), whose validate rule (vet-schemas) schema-validates every mutation (the post-convert check for convert-keda output, since keda.sh is in the schema catalog). guardrails install (dry-run by default) and status (Units with ValidationWarnings/Gates). Use for "enforce that no HPA is pinned", "make sure converted ScaledObjects pass schema validation", "which autoscalers are flagged?". Not for read-only findings (use autoscale-findings) or fixing autoscaling (use autoscale-edit).'
phase: act
allowed-tools: Bash(cub manage autoscale --help) Bash(cub manage autoscale * --help) Bash(cub auth status) Bash(cub manage preflight) Bash(cub manage autoscale guardrails) Bash(cub manage autoscale guardrails *) Bash(cub manage guardrails install *) Bash(cub manage guardrails status *) Bash(cub manage guardrails exception list *)
---

# autoscale-guardrails

Turn the autoscaling anti-patterns into **enforcement** — advisory ValidationWarnings (promotable to blocking ValidationErrors) that fire in the normal apply pipeline.

- **`cub manage autoscale guardrails install`** — creates the `common` Space and a `Warn=true` Trigger, labeled `Pack: autoscale`, then adds the `autoscale` group to each in-scope Space's `WhereTrigger`. **Dry-run by default**; re-run with `--commit`.
  - `autoscale-not-pinned` (`vet-cel`) — an HPA/ScaledObject must not have `min == max`.
- **`cub manage guardrails install`** — the tool-level `basic` group, opted into on its own. Among its rules:
  - `validate` (`vet-schemas`) — a resource must pass Kubernetes/CRD schema validation. This is the **post-convert check** for `convert-keda`: a committed ScaledObject fires the Mutation Trigger and is validated against `keda.sh`'s schema.
- **`cub manage guardrails status`** — lists Units carrying autoscaling ValidationWarnings or ValidationErrors.

## Why this matters

Both rules are plain **per-resource** checks — a single Unit answers each, so no annotate-then-validate is needed. The manager can't set warnings directly; only a failed Trigger can. `install` is conservative: it skips Spaces that already select Triggers their own way (their own Triggers, a custom WhereTrigger, or a TriggerFilterID) rather than clobbering them — those are reported with the reason.

## When to use

- "Enforce that no HPA is ever pinned." → `guardrails install` (preview), then `--commit`.
- "Make sure converted ScaledObjects pass schema validation." → the `validate` Trigger: `cub manage guardrails install` (preview), then `--commit`.
- "Which autoscalers are flagged?" → `cub manage guardrails status`.

## Do not load for

- Read-only findings — use **autoscale-findings**.
- Fixing autoscaling — use **autoscale-edit**.
- General Trigger/ValidationError mechanics beyond this pack — use `triggers-and-applygates`.

## Preflight gates

1. `cub manage preflight` succeeds. If it fails, ask the user to run `cub auth login` (interactive) and retry.
2. The user has permission to create the policy Space and Triggers, and to update in-scope Spaces' WhereTrigger.

## The loop

1. **Preview** (dry-run — the default): `cub manage autoscale guardrails install -o table` — prints the Triggers, which Spaces it would wire, which are already wired, and which it skips (with the reason).
2. **Install** with `--commit`: `cub manage autoscale guardrails install --commit` (and `cub manage guardrails install --commit` for schema validation). The Triggers are `Warn=true` (advisory). Promote one to blocking with `cub manage guardrails update <slug> --enforce --commit`.
3. **Check** what's flagged: `cub manage guardrails status -o table`.
4. **Fix** flagged autoscalers via **autoscale-edit**, then re-check.

## Exceptions

A resource that is meant to break one rule is excepted from it with an annotation naming the rule, whose value says why:

```yaml
metadata:
  annotations:
    exceptions.guardrails.confighub.com/autoscale-not-pinned: "batch queue: fixed size by design"
```

That rule's Trigger skips the resources carrying it; the other rules still check them. Adding one is a config change for the user to make deliberately (`cub manage guardrails exception add <space>/<unit> --rule <rule> --reason "<why>"`, with `--resource <type>/[<namespace>/]<name>`, as in `apps/v1/Deployment/shop/web`, when the Unit holds more than one resource), not a way to silence a warning. `cub manage guardrails exception list` shows every resource excepted from a rule and the reason recorded; `exception remove` takes one away.

## Stop conditions

- A Space is skipped (its own Triggers, a custom WhereTrigger, or a TriggerFilterID) — report it; wiring it means adding the `autoscale` group to that Space's existing selection, not overwriting it.
- The user wants a blocking gate now — install advisory first, verify, then `cub manage guardrails update <rule> --enforce --commit`.

## Tool boundary

Allowed: `cub manage autoscale guardrails install` and `cub manage guardrails install` (dry-run unless `--commit`), `status`. Not allowed: bypassing gates, `kubectl` mutations, publishing a Release. Fixing flagged autoscalers is **autoscale-edit**.

## References

- `cub manage autoscale guardrails install --help`, `cub manage guardrails install --help`, `cub manage guardrails status --help`.
- Companion skills: **autoscale-edit**, **autoscale-findings**, `triggers-and-applygates`.
