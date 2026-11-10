---
name: scheduling-guardrails
description: 'Install and inspect the scheduling enforcement pack with the cub manage scheduling CLI — a common Space of Warn=true vet-cel Triggers (a controller that tolerates a taint must pin where it lands; a topology spread constraint must select the workload''s own Pods; minDomains only with whenUnsatisfiable DoNotSchedule), added to in-scope Spaces as the scheduling guardrail group. guardrails install (dry-run by default) and status (Units with ValidationWarnings/Gates). Use for "enforce that tolerations come with placement", "warn on workloads that tolerate a taint but pin nothing", "warn on spread constraints that miss their own Pods", "flag minDomains without DoNotSchedule", "which workloads are flagged?". Not for read-only findings (use scheduling-findings), fixing placement (use scheduling-place), or fixing spread (use scheduling-spread).'
phase: act
allowed-tools: Bash(cub manage scheduling --help) Bash(cub manage scheduling * --help) Bash(cub auth status) Bash(cub manage preflight) Bash(cub manage scheduling guardrails) Bash(cub manage scheduling guardrails *) Bash(cub manage guardrails status *) Bash(cub manage guardrails exception list *)
---

# scheduling-guardrails

Turn the scheduling anti-patterns a single Unit can answer into **enforcement** — an advisory ValidationWarning (promotable to a blocking ValidationError) that fires in the normal apply pipeline.

- **`guardrails install`** — creates the `common` Space and `Warn=true` `vet-cel` Triggers labeled `Pack: scheduling`, then adds the `scheduling` group to each in-scope Space's `WhereTrigger`. **Dry-run by default**; re-run with `--commit`. The rules:
  - `scheduling-toleration-needs-placement` — a controller that tolerates a taint must pin where it lands (nodeSelector or required node affinity).
  - `scheduling-spread-selects-own-pods` — a topology spread constraint must have a labelSelector that its own pod template's labels match, or its Pods never count toward the skew.
  - `scheduling-min-domains-needs-donotschedule` — `minDomains` only with `whenUnsatisfiable: DoNotSchedule`, which the API server otherwise rejects.
- **`cub manage guardrails status`** — lists Units carrying scheduling ValidationWarnings or ValidationErrors.

## Why this matters

Each rule is a plain **per-resource `vet-cel`** check — a single Unit answers it (does this controller have tolerations but no nodeSelector and no required node affinity? does its spread constraint select its own Pods?), so no annotate-then-validate is needed. The checks that need a cluster's facts (a topologyKey no node carries, minDomains above the zone count) stay in **scheduling-findings**, since a Trigger sees one Unit and not its cluster. The manager can't set warnings directly; only a failed Trigger can. `install` is conservative: it skips Spaces that already select Triggers their own way (their own Triggers, a custom WhereTrigger, or a TriggerFilterID) rather than clobbering them.

## When to use

- "Enforce that tolerations come with real placement." → `guardrails install` (preview), then `--commit`.
- "Warn on workloads that tolerate a taint but pin nothing." → the installed `vet-cel` Trigger.
- "Warn on spread constraints that miss their own Pods / minDomains without DoNotSchedule." → the same `guardrails install`.
- "Which workloads are flagged?" → `cub manage guardrails status`.

## Do not load for

- Read-only findings — use **scheduling-findings**.
- Fixing placement — use **scheduling-place**.
- Fixing spread — use **scheduling-spread**.
- General Trigger/ValidationError mechanics beyond this pack — use `triggers-and-applygates`.

## Preflight gates

1. `cub manage preflight` succeeds. If it fails, ask the user to run `cub auth login` (interactive) and retry.
2. The user has permission to create the policy Space and Trigger, and to update in-scope Spaces' WhereTrigger.

## The loop

1. **Preview** (dry-run — the default): `cub manage scheduling guardrails install -o table` — prints the Triggers, which Spaces it would wire, which are already wired, and which it skips (with the reason).
2. **Install** with `--commit`: `cub manage scheduling guardrails install --commit`. The Triggers are `Warn=true` (advisory). Promote one to blocking with `cub manage guardrails update <rule> --enforce --commit`, e.g. `cub manage guardrails update scheduling-min-domains-needs-donotschedule --enforce --commit`.
3. **Check** what's flagged: `cub manage guardrails status -o table`.
4. **Fix** flagged workloads via **scheduling-place** (placement) or **scheduling-spread** (`set-topology-spread`, which selects the workload's own Pods), then re-check.

## Exceptions

A resource that is meant to break one rule is excepted from it with an annotation naming the rule, whose value says why:

```yaml
metadata:
  annotations:
    exceptions.guardrails.confighub.com/scheduling-toleration-needs-placement: "runs on every node by design"
```

That rule's Trigger skips the resources carrying it; the other rules still check them. Adding one is a config change for the user to make deliberately (`cub manage guardrails exception add <space>/<unit> --rule <rule> --reason "<why>"`, with `--resource <type>/[<namespace>/]<name>`, as in `apps/v1/Deployment/shop/web`, when the Unit holds more than one resource), not a way to silence a warning. `cub manage guardrails exception list` shows every resource excepted from a rule and the reason recorded; `exception remove` takes one away.

## Stop conditions

- A Space is skipped (its own Triggers, a custom WhereTrigger, or a TriggerFilterID) — report it; wiring it means adding the `scheduling` group to that Space's existing selection, not overwriting it.
- The user wants a blocking gate now — install advisory first, verify, then `cub manage guardrails update <rule> --enforce --commit`.

## Tool boundary

Allowed: `guardrails install` (dry-run unless `--commit`), `status`. Not allowed: bypassing gates, `kubectl` mutations, publishing a Release. Fixing flagged workloads is **scheduling-place** / **scheduling-spread**.

## References

- `cub manage scheduling guardrails --help`, `… install --help`, `… status --help`.
- Companion skills: **scheduling-place**, **scheduling-spread**, **scheduling-findings**, `triggers-and-applygates`.
