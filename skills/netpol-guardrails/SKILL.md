---
name: netpol-guardrails
description: 'Install and inspect the NetworkPolicy guardrail policy pack in ConfigHub with the cub manage netpol CLI — making NetworkPolicy findings enforced (advisory ValidationWarnings) instead of just reported. Use for "enforce our network-policy rules", "block allow-all policies", "warn on egress to 0.0.0.0/0", "set up NetworkPolicy guardrails", "which Units have NetworkPolicy warnings?", "flag workloads no policy selects", "require a default-deny". Installs Warn=true vet-cel Triggers in the netpol guardrail group, some reading the NetworkPolicies, workloads and Namespaces on the cluster; dry-run by default. Not for one-off checks (use netpol-findings) or fixing config (use netpol-fix).'
phase: act
allowed-tools: Bash(cub manage netpol --help) Bash(cub manage netpol * --help) Bash(cub auth status) Bash(cub manage preflight) Bash(cub manage netpol findings *) Bash(cub manage netpol guardrails install *) Bash(cub manage guardrails status *) Bash(cub manage guardrails exception list *)
---

# netpol-guardrails

Make NetworkPolicy findings **enforced**, not advisory. Installs a pack of validation policies, defined once in a policy Space and enforced in each Space that opts into the `netpol` group, including those that read the cluster's other resources: a workload no ingress policy selects, a policy that selects nothing, a namespace with no default-deny, and a flow one end allows and the other drops.

## Why this matters

The manager itself cannot set ValidationWarnings/ValidationErrors — only a failed validating **Trigger** can. The shape rules check one NetworkPolicy. The rules about the cluster read the NetworkPolicies, workloads and Namespaces of the Units released to the same Target as the Space, which each Space selects for itself; a change to one of those Units re-runs them on the Units that read it. A Unit with no Target is on no known cluster, so they pass it, and the coverage and default-deny rules pass the Units of a cluster whose Target says nothing enforces NetworkPolicy. Triggers are created with `Warn=true` (advisory ValidationWarnings, never blocking), so installing on an existing fleet blocks no one.

## The pack

| Trigger | Checks |
| --- | --- |
| `netpol-no-allow-all-ingress` | ingress rules must name their sources (no empty `from`) |
| `netpol-no-wide-cidr-egress` | no egress to `0.0.0.0/0` or `::/0` |
| `netpol-no-open-egress` | egress rules name destinations or ports (one with neither allows everything) |
| `netpol-no-metadata-egress` | no egress that reaches the cloud metadata IP `169.254.169.254`: an ipBlock containing it without excepting it, or a rule with no `to` that allows TCP 80 (fix: the `netpol-except-metadata` profile) |
| `netpol-egress-allows-dns` | a policy that isolates egress and allows some must allow DNS (UDP 53). Policies add up, so where a separate policy allows DNS, except this one; `netpol findings` (`egress-blocks-dns`) checks the union |
| `netpol-ingress-coverage` | a workload has a NetworkPolicy isolating its ingress |
| `netpol-selects-pods` | a policy's podSelector, and each podSelector peer of its rules, selects some workload in its namespace (a renamed or misspelled label) |
| `netpol-default-deny` | a Namespace holding workloads has a policy denying ingress by default (fix: `cub manage netpol default-deny <namespace>`) |
| `netpol-allowed-at-both-ends` | a flow a rule allows is not dropped at the other end, by policies isolating the other direction there |

## When to use

- "Enforce our NetworkPolicy rules." / "set up NetworkPolicy guardrails."
- "Block / warn on allow-all policies / egress to 0.0.0.0/0 / egress to the metadata IP."
- "Which Units have NetworkPolicy warnings or gates?" → `cub manage guardrails status`.
- "Flag the workloads no ingress policy selects." → the `install`ed `netpol-ingress-coverage` rule.

## Do not load for

- A one-off hygiene scan (no enforcement) — use **netpol-findings**.
- Fixing the config the guardrails flag — use **netpol-fix** / **netpol-fleet**.
- General Trigger/ValidationError setup beyond this pack — use **triggers-and-applygates**.

## Preflight gates

1. `cub manage preflight` succeeds. If not, ask the user to run `cub auth login` and retry.
2. The user has permission on the policy Space and the Spaces being wired.

## The loop

1. **Preview** the install (dry-run by default): it prints the policy Space, the group, the Triggers, and which Spaces it would wire (and which it would skip).
   ```bash
   cub manage netpol guardrails install --where-space "ComponentID = '<component-id>'" -o table
   ```
2. **Install** the pack with `--commit`:
   ```bash
   cub manage netpol guardrails install --where-space "Slug LIKE 'apptique-%'" --commit
   ```
   Spaces with a pre-existing custom `WhereTrigger` are skipped (not clobbered) and reported; add the `netpol` group to their `WhereTrigger` deliberately (see **triggers-and-applygates**).
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
    exceptions.guardrails.confighub.com/netpol-no-wide-cidr-egress: "egress gateway; reaches the internet by design"
```

That rule's Trigger skips the resources carrying it; the other rules still check them. Adding one is a config change for the user to make deliberately (`cub manage guardrails exception add <space>/<unit> --rule <rule> --reason "<why>"`, with `--resource <type>/[<namespace>/]<name>`, as in `apps/v1/Deployment/shop/web`, when the Unit holds more than one resource), not a way to silence a warning. `cub manage guardrails exception list` shows every resource excepted from a rule and the reason recorded; `exception remove` takes one away.

## Stop conditions

- A Space already selects Triggers another way — report it (the install skips it); wire it deliberately, don't clobber existing policy config.
- The user wants to *fix* a flagged Unit — hand off to **netpol-fix**.
- Never bypass a gate by dropping a Trigger or editing gate state — fix the data, or fix the Trigger in the policy Space.

## Tool boundary

Allowed: `guardrails install/status` and read commands. Not allowed: bypassing gates, `kubectl`, publishing a Release.

## References

- `cub manage netpol guardrails install --help`, `... status --help`.
- Companion skills: **netpol-findings**, **netpol-fix**, **triggers-and-applygates**.
