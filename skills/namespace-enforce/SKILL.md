---
name: namespace-enforce
description: 'Install and inspect the namespace guardrail pack in ConfigHub with the cub manage namespace CLI — making Pod Security Standard findings enforced (advisory ValidationWarnings) instead of just reported. Use for "enforce our namespace standards", "warn on namespaces without pod-security", "warn on workloads that do not meet their namespace''s Pod Security level", "set up namespace guardrails", "which Units have namespace warnings?". Installs Warn=true Triggers in the namespace guardrail group; dry-run by default. Not for one-off checks (use namespace-findings) or fixing config (use namespace-backfill).'
phase: act
allowed-tools: Bash(cub manage namespace --help) Bash(cub manage namespace * --help) Bash(cub auth status) Bash(cub manage preflight) Bash(cub manage namespace findings *) Bash(cub manage namespace guardrails install *) Bash(cub manage guardrails status *) Bash(cub manage guardrails exception list *)
---

# namespace-enforce

Make namespace Pod Security Standard findings **enforced**, not just reported. Installs a pack of validation policies, defined once in a policy Space and enforced in each Space that opts into the `namespace` group.

## Why this matters

The manager itself cannot set ValidationWarnings/ValidationErrors — only a failed validating **Trigger** can. So each rule is a validating Trigger: a `vet-cel` expression over the Namespace, or `vet-pod-security-standard` over each workload. Triggers are created with `Warn=true` (advisory ValidationWarnings, never blocking), so installing on an existing fleet blocks no one.

## The pack

| Trigger | Checks |
| --- | --- |
| `namespace-has-pod-security` | a `v1/Namespace` must carry a `pod-security.kubernetes.io/enforce` label |
| `namespace-pod-security-standard` | a workload must meet the Pod Security Standard level in its own `pod-security.kubernetes.io/enforce` label, which `cub manage namespace pod-security set` copies from its namespace — so admission would not reject its pods. Unlabeled workloads are not checked; `cub manage namespace pod-security` counts them as UNLABELED. Runs `vet-pod-security-standard` with no level, so `install` refuses a server whose function still requires one |

The **namespace-name invariant** (`metadata.namespace == normalizeName(Component)`) is enforced separately by a cluster-selected **mutating `set-namespace` Trigger** — active correction, the promotable option, wired outside this advisory pack.

## When to use

- "Enforce our namespace standards." / "set up namespace guardrails."
- "Warn on namespaces without pod-security labels."
- "Which Units have namespace warnings or gates?" → `cub manage guardrails status`.
- "Warn on workloads that do not meet their namespace's Pod Security level." → install the pack, then `cub manage namespace pod-security set` copies each namespace's level onto its workloads, which the `namespace-pod-security-standard` rule checks them against.

## Do not load for

- A one-off scan (no enforcement) — use **namespace-findings**.
- Fixing the config the guardrails flag — use **namespace-backfill**.
- General Trigger/ValidationError setup beyond this pack — use **triggers-and-applygates**.

## Preflight gates

1. `cub manage preflight` succeeds. If not, ask the user to run `cub auth login` and retry.
2. The user has permission on the policy Space and the Spaces being wired.

## The loop

1. **Preview** the install (dry-run by default): it prints the policy Space, the group, the Triggers, and which Spaces it would wire (and which it would skip).
   ```bash
   cub manage namespace guardrails install --where-space "Slug LIKE 'apptique-%'" -o table
   ```
2. **Install** the pack with `--commit`:
   ```bash
   cub manage namespace guardrails install --where-space "Slug LIKE 'apptique-%'" --commit
   ```
   Spaces with their own Triggers, a custom `WhereTrigger`, or a `TriggerFilterID` are skipped (not clobbered) and reported; wire those deliberately (see **triggers-and-applygates**). After wiring, existing Units are checked on their next mutation.
3. **Inspect** what's flagged:
   ```bash
   cub manage guardrails status -o table
   ```
4. **Promote a rule to blocking** later (the manager never does this silently): `cub manage guardrails update <slug> --enforce --commit`.

## Exceptions

A resource that is meant to break one rule is excepted from it with an annotation naming the rule, whose value says why:

```yaml
metadata:
  annotations:
    exceptions.guardrails.confighub.com/namespace-has-pod-security: "kube-system: managed by the cluster provider"
```

That rule's Trigger skips the resources carrying it; the other rules still check them. Adding one is a config change for the user to make deliberately (`cub manage guardrails exception add <space>/<unit> --rule <rule> --reason "<why>"`, with `--resource <type>/[<namespace>/]<name>`, as in `apps/v1/Deployment/shop/web`, when the Unit holds more than one resource), not a way to silence a warning. `cub manage guardrails exception list` shows every resource excepted from a rule and the reason recorded; `exception remove` takes one away.

## Stop conditions

- A Space already selects Triggers another way — report it (the install skips it); wire it deliberately, don't clobber existing policy config.
- The user wants to *fix* a flagged namespace — hand off to **namespace-backfill**.
- Never bypass a gate by dropping a Trigger or editing gate state — fix the data, or fix the Trigger in the policy Space.

## Tool boundary

Allowed: `guardrails install/status` and read commands. Not allowed: bypassing gates, `kubectl`, publishing a Release.

## References

- `cub manage namespace guardrails install --help`, `cub manage guardrails status --help`.
- Companion skills: **namespace-findings**, **namespace-backfill**, **triggers-and-applygates**.
