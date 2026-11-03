---
name: rbac-fleet
description: 'Apply RBAC changes across many ConfigHub Units at once with the cub manage rbac CLI: an rbac-* profile applied in bulk (fleet-edit --profile). Use for "add deletecollection to the developer role in every dev cluster", "remove the wildcard verb from this persona role fleet-wide", "add the oncall group to viewers across prod". Dry-run by default; requires --commit + --change-desc; never bypasses ValidationErrors and never publishes a Release. Not for a single Unit (use rbac-edit), not for inventory/queries (use rbac-audit / rbac-whocan), not for installing policy (use rbac-guardrails), not for propagating a base change to its variants (use promote-release), not for rolling out to clusters (use release-publish).'
phase: act
allowed-tools: Bash(cub manage rbac --help) Bash(cub manage rbac * --help) Bash(cub auth status) Bash(cub manage preflight) Bash(cub manage rbac snapshot *) Bash(cub manage rbac list *) Bash(cub manage profile list *) Bash(cub manage profile install *) Bash(cub manage rbac fleet-edit *)
---

# rbac-fleet

Change RBAC across many Units in one server-side request:

- **fleet-edit --profile** — apply the same `rbac-*` profile (add/remove a verb, a User or Group, or a ServiceAccount) to every Unit with roles or bindings that the selector matches, e.g. a persona role replicated across clusters.

It is **dry-run by default** and writes nothing until you re-run with `--commit` and a `--change-desc`. It never publishes a Release.

## Why this matters

The fleet is queried and changed like a database: one `--where` selector compiles to a single server-side operation — no looping over clusters, no drift between them. fleet-edit makes the same change everywhere it matches.

## When to use

- "Add `deletecollection` to the `developer` ClusterRole in every dev cluster" → `fleet-edit --profile rbac-add-verb`.
- "Remove the wildcard verb from this persona role fleet-wide" → `fleet-edit --profile rbac-remove-verb`.
- "Add the `oncall` group to `viewers` across prod" → `fleet-edit --profile rbac-add-subject`.

## Do not load for

- A change to a single Unit — use **rbac-edit**.
- Inventory / who-can / findings — use **rbac-audit** / **rbac-whocan** / **rbac-findings**.
- Installing or enforcing policy guardrails — use **rbac-guardrails**.
- Propagating a base change to its variant Spaces ("upgrade the downstreams", "which are behind their base?") — use **promote-release**.
- Rolling the changes out to clusters — use `cub release publish` / the **release-publish** skill.

## Preflight gates

1. `cub manage preflight` succeeds (cub installed, ConfigHub session valid). If not, ask the user to run `cub auth login` and retry.
2. The profile library exists. It is seeded once per organization with `cub manage profile install` (re-running it brings every profile up to date). If a fleet-edit fails because the profile is not found, run `cub manage profile install` and retry; `cub manage profile list` shows each profile's parameters.
3. The user has Edit permission on the targeted Units (the commit fails server-side otherwise — report it, don't retry blindly).

## Scoping — always pass a selector

Scope every fleet edit deliberately — a raw `--where` filter, `--component`, or a label shorthand. Without one, fleet-edit applies to every Unit holding a Role, ClusterRole, RoleBinding or ClusterRoleBinding, so never run it unscoped unless that is the intent. The selector is ANDed with `ToolchainType = 'Kubernetes/YAML'`. The grammar is **AND-only** (no OR, no parentheses — a parenthesized clause fails with `invalid attribute name`). Common selectors:

- `--environment dev` — label shorthand; expands to `Space.Labels.Environment = 'dev'`. Other shorthands: `--region`, `--owner`, `--layer`, `--variant`; `--component <c>` selects the Spaces that are Variants of that Component.
- `--where "Space.Labels.Environment = 'dev'"` — the raw equivalent (Units inherit their cluster's Space labels).
- `--environment prod --region us-east` — shorthands AND together (and AND onto any raw `--where`).
- `--where "Unreleasable = false"` — by any Unit/Space/Target attribute.
- `--where "Slug = 'rbac'"` — by Unit slug across the fleet.

Confirm the selector hits the intended Units first with **rbac-audit** (`cub manage rbac snapshot` / `list`).

## The loop

1. **Scope & preview (dry-run).** Run with no `--commit`; cub manage rbac reports the Units that would change and writes nothing:
   ```bash
   cub manage rbac fleet-edit --profile rbac-add-verb --where "Space.Labels.Environment = 'dev'" \
     --param roleKind=ClusterRole --param role=developer --param rule=0 --param verb=deletecollection
   ```
   The report lists each Unit (`SPACE  UNIT  MUTATED  ERROR`) and "N of M Unit(s) would change", followed by each Unit's diff: every path the edit would change, with the value before and after.
2. **Review the blast radius** with the user — fleet operations touch many Units. If the list or a diff is wrong, fix the `--where`; never commit an over-broad change.
3. **Commit** with a real change description capturing the request and clarifications:
   ```bash
   cub manage rbac fleet-edit --profile rbac-add-verb --where "Space.Labels.Environment = 'dev'" \
     --param roleKind=ClusterRole --param role=developer --param rule=0 --param verb=deletecollection \
     --commit --change-desc "dev developers: allow deletecollection (OPS-12)"
   ```
4. **Stop.** The changes created new revisions; they are NOT released (the report ends by saying so). Hand off rollout to **release-publish** (`cub release publish`), which respects ValidationErrors.

## Flags

- `fleet-edit --profile <slug>` with the profile's parameters as `--param name=value`, the same as `profile apply` (see **rbac-edit**: `roleKind`/`role`/`rule`/`verb`; `bindingKind`/`binding`/`subjectKind`/`subject`; `bindingKind`/`binding`/`namespace`/`serviceAccount`), plus the scope (`--where`, `--component`, label shorthands), `--commit`, `--change-desc`.

## Tool boundary

- Allowed: `cub manage rbac fleet-edit` (dry-run + commit), read-only inspection (`cub manage rbac snapshot/list`).
- Not allowed: bypassing gates, publishing a Release, deleting Units, raw `kubectl`.

## Stop conditions

- The dry-run blast radius is wrong or larger than intended — narrow `--where`, never commit.
- A commit fails on permission or a gate — report the server message; do not bypass.
- Variant propagation requested — hand off to **promote-release**.
- The user wants the changes live — hand off to **release-publish**.

## Safety

Fleet operations are high blast-radius. Be especially careful with edits touching `cluster-admin`, wildcards, or privilege-escalation verbs across many clusters, and always confirm the dry-run Unit list before `--commit`.

## References

- `cub manage rbac fleet-edit --help`, `cub manage profile list`.
- Companion skills: **rbac-edit** (single Unit), **rbac-audit** (scope check), **promote-release** (variant propagation), **release-publish** (rollout).
