---
name: scheduling-place
description: 'Set Kubernetes workload placement as config-as-data with the cub manage scheduling CLI — nodeSelector, tolerations, and node affinity — one workload or across a whole selector via reusable placement profiles. Use for "pin this workload to the gpu pool", "add a toleration for the spot taint", "put the ml deployments on the gpu nodes", "set node affinity to a zone", "apply the placement-gpu profile", "roll placement downstream". Dry-run by default; requires --commit --change-desc. Not for read-only checks (use scheduling-audit / scheduling-findings), enforcement Triggers (use scheduling-guardrails), or pod anti-affinity / topology spread (that is availability, owned by `cub manage workload`).'
phase: act
allowed-tools: Bash(cub manage scheduling --help) Bash(cub manage scheduling * --help) Bash(cub auth status) Bash(cub manage preflight) Bash(cub manage scheduling placement *) Bash(cub manage scheduling findings *) Bash(cub manage scheduling set-node-selector *) Bash(cub manage scheduling set-tolerations *) Bash(cub manage scheduling set-node-affinity *) Bash(cub manage profile) Bash(cub manage profile *) Bash(cub manage scheduling fleet-edit *)
---

# scheduling-place

Set where workloads land — as data, dry-run by default. One workload, or across a `--where` selector via a placement profile.

- **`set-node-selector <space>/<unit> --selector k=v`** — pin to matching nodes.
- **`set-tolerations <space>/<unit> --toleration key[=value][:effect]`** — tolerate node taints.
- **`set-node-affinity <space>/<unit> --required "key=v1,v2"`** — a required node affinity term (operator In).
- **`cub manage profile install | list | apply`** — the placement profile library (stored Invocations): `placement-gpu`, `placement-spot`, and parameterized `node-pool` (`--param pool=…`).
- **`fleet-edit --profile <slug> [--where …]`** — apply a profile across a selector in one operation.

All **edit Units but do not publish them** — rolling out is a separate `cub release publish <space>`, which bundles the Space's Units for its release Target.

## Why this matters

A toleration only *permits* scheduling onto a tainted node — pair it with a nodeSelector or node affinity to actually land there. `cub manage scheduling` edits the source of record with `set-yq` under the hood; everything is **dry-run by default** and requires `--commit --change-desc`, and never bypasses ValidationErrors.

## When to use

- "Pin the trainer to the gpu pool." → `set-node-selector` or `cub manage profile apply placement-gpu`.
- "Tolerate the spot taint." → `set-tolerations … --toleration spot:NoSchedule`.
- "Put it on a specific zone." → `set-node-affinity … --required "topology.kubernetes.io/zone=us-east-1a,us-east-1b"`.
- "Put every prod ml workload on gpu nodes." → `fleet-edit --profile placement-gpu --environment prod --component ml`.
- "Pin to an arbitrary pool." → `cub manage profile apply node-pool <space>/<unit> --param pool=<name>`.

## Do not load for

- Read-only checks — use **scheduling-audit** / **scheduling-findings**.
- Enforcement Triggers — use **scheduling-guardrails**.
- Pod anti-affinity / topology spread — use **`cub manage workload`** (availability).
- Publishing the Space's Release — that is `cub release publish <space>`.

## Preflight gates

1. `cub manage preflight` succeeds. If it fails, ask the user to run `cub auth login` (interactive) and retry.
2. For `cub manage profile apply` / `fleet-edit`, the library exists — run `cub manage profile install` once if `cub manage profile list` is empty.
3. The user has write permission on the target Space(s).

## The loop

1. **See the gap**: `cub manage scheduling placement --issues-only` / `cub manage scheduling findings`.
2. **Preview** (dry-run — the default):
   ```bash
   cub manage scheduling set-node-selector ml-prod/trainer --selector pool=gpu
   cub manage scheduling fleet-edit --profile placement-gpu --environment prod --component ml
   ```
3. **Commit** with `--commit --change-desc` (summary + verbatim user prompt).
4. **Verify**: `cub manage scheduling placement --cluster <c> --namespace <ns>` shows the workload constrained; `findings` clears.
5. **Roll out** is a separate step — hand off to `release-publish`.

## Safety

- A nodeSelector or required node affinity that no node satisfies leaves pods `Pending`. Confirm the pool / zone exists before committing broadly.
- Prefer scoping `fleet-edit` narrowly (dry-run shows the count) before a fleet-wide commit.

## Stop conditions

- A ValidationError attaches (a validating Trigger failed). **Do not bypass** — fix the data (or the rule), via **triggers-and-applygates**.
- The user wants the change deployed — hand off to `release-publish`.

## Tool boundary

Allowed: `set-node-selector`, `set-tolerations`, `set-node-affinity`, `cub manage profile`, `fleet-edit` (dry-run by default; `--commit` passes `--change-desc`), and the read commands. Not allowed: bypassing gates, `kubectl` mutations, publishing a Release.

## References

- `cub manage scheduling set-node-selector --help`, `… set-tolerations --help`, `… set-node-affinity --help`, `… profile --help`, `… fleet-edit --help`.
- Companion skills: **scheduling-audit**, **scheduling-findings**, `promote-release`, `triggers-and-applygates`, `release-publish`.
