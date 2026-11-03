---
name: netpol-fleet
description: 'Fleet-wide NetworkPolicy remediation in ConfigHub with the cub manage netpol CLI: create a default-deny for every uncovered namespace at once (default-deny with no namespace), or apply a netpol-* profile to every NetworkPolicy in scope (fleet-edit). Use for "default-deny every namespace that lacks one", "remediate all our coverage gaps", "except the metadata IP from every egress policy in prod". Dry-run by default; requires --commit with --change-desc; Units are created or edited but NOT released. Not for a single namespace/policy (use netpol-fix), inventory/findings (use netpol-audit/netpol-findings), installing enforcement (use netpol-guardrails), or propagating a baseline downstream (use promote-release).'
phase: act
allowed-tools: Bash(cub manage netpol --help) Bash(cub manage netpol * --help) Bash(cub auth status) Bash(cub manage preflight) Bash(cub manage netpol coverage *) Bash(cub manage netpol findings *) Bash(cub manage netpol default-deny *) Bash(cub manage profile list *) Bash(cub manage profile install *) Bash(cub manage netpol fleet-edit *)
---

# netpol-fleet

Fleet-wide remediation. Bulk-close coverage gaps across every namespace that lacks a default-deny, or fix every existing policy in scope with a profile. **Dry-run by default**; nothing is written until `--commit` with a `--change-desc`, and Units are created or edited but **not released**.

## Why this matters

`netpol-fix` closes one gap; this closes them all at once, driven directly by the coverage analysis — the fleet-scale version of the same config-as-data, no-drift fix.

## When to use

- "Default-deny every namespace that lacks one." / "remediate all our coverage gaps." → `default-deny` with no namespace.
- "Add the DNS allowance to every egress default-deny" / "except the metadata IP everywhere" → `fleet-edit --profile netpol-allow-dns` / `netpol-except-metadata`.

## Do not load for

- A single namespace or policy — use **netpol-fix**.
- Inventory / findings — **netpol-audit**, **netpol-findings**.
- Installing enforcement Triggers — **netpol-guardrails**.
- Propagating a baseline from a base Space to its variants — **promote-release**.
- Publishing the Space's Release — **release-publish**.

## Preflight gates

1. `cub manage preflight` succeeds. If not, ask the user to run `cub auth login` and retry.

## The toolkit

### Bulk remediation — `cub manage netpol default-deny` (no namespace)

Without a namespace argument, `default-deny` finds every namespace in the fleet scope (`--where`, `--component`, the label shorthands, `--cluster`) with workloads but no default-deny ingress (with `--egress`, no egress default-deny either) and makes a default-deny Unit for each, in its workloads' Space. Idempotent — a namespace that already has a default-deny, under any name, is skipped, and an existing Unit is brought up to date rather than duplicated. A namespace whose Space cannot be inferred is reported with an error row, not guessed; make it with `default-deny <namespace> --cluster <c> --space <space>`.

```bash
cub manage netpol default-deny -o table                             # dry-run: what would be created
cub manage netpol default-deny --egress                              # also deny egress, allowing DNS
cub manage netpol default-deny --cluster prod-cluster \
  --commit --change-desc "Bulk default-deny for uncovered namespaces. User prompt: ..."
```

### Bulk policy fixes — `cub manage netpol fleet-edit --profile`

Applies one profile to every NetworkPolicy Unit the scope selects: `netpol-allow-dns` (the kube-dns `:53` allowance, on policies that restrict egress without it) or `netpol-except-metadata` (except `169.254.169.254/32` from egress ipBlocks that contain it). Each changes only the policies that need it. The profiles come from the shared library (`cub manage profile install`, once per organization).

```bash
cub manage netpol fleet-edit --profile netpol-except-metadata --environment prod -o table
```

## The loop

1. **Preview** with no `--commit` — the report lists each Unit it would create or change, with its diff.
2. **Confirm** the scope (count, clusters, namespaces) with the user — fleet ops have wide blast radius.
3. **Commit** with a `--change-desc` that reads sensibly per Unit (the same description is recorded on every affected Unit).
4. **Stop.** Units are created or edited, NOT released. Roll out via **release-publish**.

## Stop conditions

- The user hasn't confirmed a wide blast radius — show the dry-run and confirm before `--commit`.
- Propagating the baseline to downstream variants — hand off to **promote-release**.
- Publish/rollout requested — hand off to **release-publish**.

## Tool boundary

Allowed: the dry-run/commit fleet writes above (with `--change-desc`) plus read commands. Not allowed: publishing a Release, `kubectl` mutation, bypassing gates.

## References

- `cub manage netpol default-deny --help`, `cub manage netpol fleet-edit --help`.
- Companion skills: **netpol-audit**, **netpol-fix**, **promote-release**, **release-publish**.
