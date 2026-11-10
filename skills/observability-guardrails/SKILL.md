---
name: observability-guardrails
description: 'Install and operate the observability enforcement pack with the cub manage observability CLI — an common Space with a Warn=true vet-cel Trigger that flags metrics-exposing Services with no ServiceMonitor, fed by annotate-then-validate (coverage is a cross-Unit property). guardrails install (dry-run by default), status (Units with ValidationWarnings/Gates), annotate (write the coverage finding onto uncovered Service Units). Use for "enforce that metrics services are scraped", "warn on services with no ServiceMonitor", "which services are flagged?". Not for read-only findings (use observability-findings) or fixing coverage (use observability-instrument).'
phase: act
allowed-tools: Bash(cub manage observability --help) Bash(cub manage observability * --help) Bash(cub auth status) Bash(cub manage preflight) Bash(cub manage observability guardrails) Bash(cub manage observability guardrails *) Bash(cub manage guardrails status *) Bash(cub manage guardrails exception list *)
---

# observability-guardrails

Turn the ServiceMonitor-coverage gap into **enforcement** — an advisory ValidationWarning (promotable to a blocking ValidationError) that fires in the normal apply pipeline.

- **`guardrails install`** — creates the `common` Space, a `Warn=true` `vet-cel` Trigger (`observability-servicemonitor-coverage`), labeled `Pack: observability`, then adds the `observability` group to each in-scope Space's `WhereTrigger`. **Dry-run by default**; re-run with `--commit`.
- **`cub manage guardrails status`** — lists Units carrying observability ValidationWarnings or ValidationErrors.
- **`guardrails annotate`** — writes the `observability.confighub.com/servicemonitor-coverage` annotation onto each metrics-Service Unit no ServiceMonitor selects (the producing half of annotate-then-validate). Dry-run unless `--commit --change-desc`.

## Why this matters

ServiceMonitor coverage is a **cross-Unit** property (the ServiceMonitor and the Service are separate Units), so a per-Unit `vet-cel` can't compute it directly. This uses **annotate-then-validate**: `annotate` computes coverage and stamps the finding on the uncovered Service Unit, and the Trigger turns that annotation into a warning. `install` is conservative — it skips Spaces that already select Triggers their own way rather than clobbering them.

## When to use

- "Enforce that metrics Services are scraped." → `guardrails install` (preview), then `--commit`, then `guardrails annotate --commit`.
- "Warn on Services with no ServiceMonitor." → the `install`ed Trigger + `annotate`.
- "Which Services are flagged?" → `cub manage guardrails status`.

## Do not load for

- Read-only findings — use **observability-findings**.
- Fixing coverage — use **observability-instrument**.
- General Trigger/ValidationError mechanics beyond this pack — use `triggers-and-applygates`.

## Preflight gates

1. `cub manage preflight` succeeds. If it fails, ask the user to run `cub auth login` (interactive) and retry.
2. The user has permission to create the policy Space and Trigger, and to update in-scope Spaces' WhereTrigger.

## The loop

1. **Preview** (dry-run — the default): `cub manage observability guardrails install -o table`.
2. **Install** with `--commit`. The Trigger is `Warn=true` (advisory); promote to blocking with `cub manage guardrails update observability-servicemonitor-coverage --enforce --commit`.
3. **Feed the finding**: `cub manage observability guardrails annotate` (dry-run), then `--commit --change-desc`. Re-run after adding ServiceMonitors so the cached annotation stays fresh.
4. **Check**: `cub manage guardrails status -o table`.
5. **Fix** flagged Services via **observability-instrument**, then re-check.

## Exceptions

A resource that is meant to break one rule is excepted from it with an annotation naming the rule, whose value says why:

```yaml
metadata:
  annotations:
    exceptions.guardrails.confighub.com/observability-servicemonitor-coverage: "scraped by the sidecar, not a ServiceMonitor"
```

That rule's Trigger skips the resources carrying it; the other rules still check them. Adding one is a config change for the user to make deliberately (`cub manage guardrails exception add <space>/<unit> --rule <rule> --reason "<why>"`, with `--resource <type>/[<namespace>/]<name>`, as in `apps/v1/Deployment/shop/web`, when the Unit holds more than one resource), not a way to silence a warning. `cub manage guardrails exception list` shows every resource excepted from a rule and the reason recorded; `exception remove` takes one away.

## Stop conditions

- A Space is skipped (its own Triggers, a custom WhereTrigger, or a TriggerFilterID) — report it; wiring it means adding the `observability` group to that Space's existing selection, not overwriting it.
- The user wants a blocking gate now — install advisory first, verify, then `cub manage guardrails update <rule> --enforce --commit`.

## Tool boundary

Allowed: `guardrails install` (dry-run unless `--commit`), `status`, `annotate` (dry-run unless `--commit --change-desc`). Not allowed: bypassing gates, `kubectl` mutations, publishing a Release. Fixing coverage is **observability-instrument**.

## References

- `cub manage observability guardrails --help`, `… install --help`, `… status --help`, `… annotate --help`.
- Companion skills: **observability-instrument**, **observability-findings**, `triggers-and-applygates`.
