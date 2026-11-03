---
name: rbac-edit
description: 'Make guardrailed structured edits to a single Kubernetes RBAC Unit in ConfigHub with the rbac-* profiles of cub manage: add/remove a verb on a role rule, or add/remove a subject (User, Group or ServiceAccount) on a binding. Use for "give the viewer role get on pods", "remove the wildcard verb from this role", "add the oncall group to the admins binding", "revoke alice from breakglass", "drop the deletecollection verb". Edits are dry-run by default and require an explicit --commit with a --change-desc; they never bypass ValidationErrors and never publish a Release. Not for inventory/queries (use rbac-audit / rbac-whocan), not for installing policy (use rbac-guardrails), not for applying/rolling out a change (use cub release publish / the release-publish skill).'
phase: act
allowed-tools: Bash(cub manage rbac --help) Bash(cub manage rbac * --help) Bash(cub auth status) Bash(cub manage preflight) Bash(cub manage rbac snapshot *) Bash(cub manage rbac list *) Bash(cub unit data *) Bash(cub unit get *) Bash(cub manage profile list *) Bash(cub manage profile install *) Bash(cub manage profile apply *)
---

# rbac-edit

Apply a single structured change to one RBAC Unit — add/remove a verb on a role rule, or add/remove a subject on a binding. Each change is an `rbac-*` profile in the shared profile library: a parameterized server-side `set-yq` Invocation that modifies the literal YAML in place (comments and formatting preserved), applied with `cub manage profile apply`, which supplies only the variable values as `--param`s. It is **dry-run by default**; nothing is written until you re-run with `--commit` and a `--change-desc`.

## Why this matters

This is the safe write path for RBAC: structured edits instead of hand-editing YAML, a diff preview before any write, a recorded change description for provenance, and no gate-bypassing. The edit only changes ConfigHub's stored config (a new revision); rolling it out to a cluster is a separate, deliberate step.

## When to use

- "Give the `viewer` ClusterRole `get` on pods" → `rbac-add-verb`.
- "Remove the wildcard verb / drop `deletecollection` from this role" → `rbac-remove-verb`.
- "Add the `oncall` group to the `admins` binding" → `rbac-add-subject`.
- "Revoke `alice` from `breakglass`" → `rbac-remove-subject`; "remove the `ci` service account" → `rbac-remove-service-account`.

## Do not load for

- Finding/inspecting RBAC — use **rbac-audit** (`list`) or **rbac-whocan**.
- Installing/enforcing policy guardrails — use **rbac-guardrails**.
- Publishing or rolling out the change — that's `cub release publish <space>` (the **release-publish** skill); this skill only edits stored config.
- The same edit across many Units — use **rbac-fleet** (`cub manage rbac fleet-edit`).
- Whole-Unit rewrites or non-RBAC fields — use `cub`/cub-mutate directly.

## Preflight gates

1. `cub manage preflight` succeeds (cub installed, ConfigHub session valid). If not, ask the user to run `cub auth login` and retry.
2. The profile library exists. It is seeded once per organization with `cub manage profile install` (re-running it brings every profile up to date). If an apply fails because the profile is not found, run `cub manage profile install` and retry; `cub manage profile list` shows each profile's parameters.
3. The user has Edit permission on the target Unit (the commit will fail server-side otherwise — report the error, don't retry blindly).

## The loop

1. **Locate the resource.** Find the Unit (`<space>/<unit>`) and the exact role/binding name with **rbac-audit** (`cub manage rbac list --kind ... --cluster ...`). For a verb edit you also need the **rule index** within the role — inspect the Unit's YAML to find it:
   ```bash
   cub unit data <unit> --space <space>      # read the role's rules[] to pick the index
   ```
2. **Preview (dry-run).** Run the edit with no `--commit`; cub manage rbac prints the exact mutation diff and writes nothing:
   ```bash
   cub manage profile apply rbac-add-verb <space>/<unit> --param roleKind=ClusterRole --param role=viewer --param rule=0 --param verb=get
   cub manage profile apply rbac-remove-verb <space>/<unit> --param roleKind=ClusterRole --param role=admin --param rule=0 --param 'verb=*'
   cub manage profile apply rbac-add-subject <space>/<unit> --param bindingKind=ClusterRoleBinding --param binding=viewers --param subjectKind=Group --param subject=oncall
   cub manage profile apply rbac-remove-service-account <space>/<unit> --param bindingKind=RoleBinding --param binding=rb --param namespace=apps --param serviceAccount=ci
   ```
3. **Confirm the diff** with the user (or against the stated intent). If it's not what was asked, adjust the parameters — don't commit a wrong diff.
4. **Commit** with a real change description that captures the user's request and any clarifications:
   ```bash
   cub manage profile apply rbac-add-verb <space>/<unit> --param roleKind=ClusterRole --param role=viewer --param rule=0 --param verb=get \
     --commit --change-desc "grant viewer get on pods (ticket OPS-12)"
   ```
5. **Stop.** The edit created a new revision; it is NOT released (the report ends by saying so). If the user wants it live, hand off to **release-publish** (`cub release publish`), which respects ValidationErrors.

## Profiles & parameters

- `rbac-add-verb` / `rbac-remove-verb`: `roleKind` (Role|ClusterRole), `role`, `rule` (index), `verb`.
- `rbac-add-subject` / `rbac-remove-subject`: `bindingKind` (RoleBinding|ClusterRoleBinding), `binding`, `subjectKind` (User|Group), `subject`.
- `rbac-add-service-account` / `rbac-remove-service-account`: `bindingKind`, `binding`, `namespace`, `serviceAccount`.
- Every parameter is required. The adds are idempotent (no duplicates).
- `--commit` performs the write; without it you get a dry-run. `--change-desc` is required with `--commit`.

## Tool boundary

- Allowed: `cub manage profile apply` with an `rbac-*` profile (dry-run + commit), `cub manage profile install` / `list`, read-only inspection (`cub manage rbac list/snapshot`, `cub unit data/get`).
- Not allowed: bypassing gates, publishing a Release, deleting Units, raw `kubectl`/hand-edited YAML.

## Stop conditions

- The dry-run diff doesn't match the intent — fix the parameters, never commit a wrong change.
- Committing fails on permission or a gate — report the server message; do not try to bypass it.
- The user wants the change live — hand off to **release-publish**.

## Safety

RBAC edits are high-stakes. Be especially careful with edits that touch `cluster-admin`, wildcard verbs/resources, or privilege-escalation verbs (escalate/bind/impersonate), and with removing a subject that could lock the operator out. Surface these in the confirmation step.

## References

- `cub manage profile apply --help`, `cub manage profile list`.
- Companion skills: **rbac-fleet**, **rbac-audit**, **rbac-whocan**, **rbac-findings**, **rbac-guardrails**, and **release-publish** (to roll out).
