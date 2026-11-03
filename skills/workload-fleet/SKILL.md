---
name: workload-fleet
description: 'Fleet-scale workload remediation and the reusable profile library with the cub manage workload CLI — profile install/list/apply (parameterized Invocations: resource tiers, harden, probes, anti-affinity, termination policy), and fleet-edit (apply a profile across a --where selector of workloads in one operation). Use for "harden every prod workload", "set medium resources across the checkout component", "apply the anti-affinity profile fleet-wide", "list the workload profiles". Dry-run by default; requires --commit --change-desc. Not for a single-workload fix (use workload-harden), read-only checks (use workload-audit / workload-findings), enforcement Triggers (use workload-guardrails), or propagating a fix to downstream variants (use promote-release).'
phase: act
allowed-tools: Bash(cub manage workload --help) Bash(cub manage workload * --help) Bash(cub auth status) Bash(cub manage preflight) Bash(cub manage workload readiness *) Bash(cub manage workload findings *) Bash(cub manage profile) Bash(cub manage profile *) Bash(cub manage workload fleet-edit *)
---

# workload-fleet

Apply a fix across **many** workloads and manage the reusable **profile library** — as data, dry-run by default. Two surfaces:

- **`cub manage profile install | list | apply`** — the profile library: named, parameterized edits stored as ConfigHub Invocations in the `common` Space (`resources-small/medium/large`, `harden-restricted`, `probes-http`, `anti-affinity-soft`, `termination-message-policy`). `install` seeds them (once per org); `apply <slug> <space>/<unit>` invokes one over a single workload.
- **`fleet-edit --profile <slug> [--where/shorthands] [--param]`** — applies a profile to *every* workload matching a selector in one server-side operation (the bulk analog of `cub manage profile apply`), scoped to workload kinds.

All **edit/create Units but do not publish them**.

## Why this matters

"Remediate the gaps across every prod workload" is a set-scale workflow no per-object validator does. `fleet-edit` runs one `InvokeStoredInvocation` over a `--where` selector (no client loop, comments preserved). A profile is one vocabulary for both a single `cub manage profile apply` and a bulk `fleet-edit`. Everything is **dry-run by default** and requires `--commit --change-desc`.

## When to use

- "List the available workload profiles." → `cub manage profile list`.
- "Set up the profile library." → `cub manage profile install`.
- "Apply the medium resource tier to workload X." → `cub manage profile apply resources-medium <space>/<unit> --param container=<name>`.
- "Harden every prod workload." → `fleet-edit --profile harden-restricted --environment prod`.
- "Set medium resources across the checkout component." → `fleet-edit --profile resources-medium --component checkout --param container=*`.

## Do not load for

- A single-workload fix — use **workload-harden**.
- Read-only checks — use **workload-audit** / **workload-findings** / **workload-availability**.
- Enforcement Triggers / guardrails — use **workload-guardrails**.
- Propagating a fix from a base Space to its variants — use **promote-release**.
- Publishing the Space's Release — that is `cub release publish <space>`.

## Preflight gates

1. `cub manage preflight` succeeds. If it fails, ask the user to run `cub auth login` (interactive) and retry.
2. For `cub manage profile apply` / `fleet-edit`, the profile library exists — run `cub manage profile install` once if `cub manage profile list` is empty.
3. The user has write permission on the target Spaces.

## The loop

1. **Scope + preview** (dry-run — the default):
   ```bash
   cub manage profile list -o table
   cub manage workload fleet-edit --profile harden-restricted --environment prod
   ```
   `fleet-edit` reports how many of the matched Units *would* change, then each one's diff: every path the profile would set, with the value before and after. Inspect the selector and the diffs carefully — a broad `--where` touches many Units.
2. **Commit** with `--commit --change-desc`:
   ```bash
   cub manage workload fleet-edit --profile harden-restricted --environment prod --commit \
     --change-desc "Harden prod workloads. User prompt: ..."
   ```
3. **Verify**: `cub manage workload readiness --environment prod --failing-only` shows the dimension cleared.
4. **Roll out** is a separate step — hand off to `release-publish`.

## Stop conditions

- The selector is broader than intended (dry-run count surprises you) — narrow `--where` / shorthands before committing.
- A ValidationError attaches on a Unit. **Do not bypass** — fix via **triggers-and-applygates**.
- A single workload is the real target — hand off to **workload-harden**.
- Variant propagation requested — hand off to **promote-release**.
- The user wants the change deployed — hand off to `release-publish`.

## Tool boundary

Allowed: `cub manage profile install|list|apply`, `fleet-edit` (dry-run by default; `--commit` passes `--change-desc`), and read commands. Not allowed: bypassing gates, `kubectl` mutations, publishing a Release.

## References

- `cub manage profile --help`, `cub manage workload fleet-edit --help`.
- Companion skills: **workload-harden**, **workload-audit**, `promote-release`, `triggers-and-applygates`, `release-publish`, `rollback-revision`.
