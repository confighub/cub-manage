---
name: observability-instrument
description: 'Instrument Kubernetes workloads for observability as config-as-data with the cub manage observability CLI — author a Prometheus ServiceMonitor for a Service (ensure-servicemonitor), inject an OpenTelemetry/telemetry sidecar container (inject-sidecar via set-path), one workload or across a selector via profiles. Use for "add a ServiceMonitor for the checkout service", "scrape this service", "inject an otel sidecar into web", "add the otel collector to every prod workload", "roll instrumentation downstream". Dry-run by default; requires --commit --change-desc. Not for read-only checks (use observability-audit / observability-findings), enforcement Triggers (use observability-guardrails), or publishing a Release.'
phase: act
allowed-tools: Bash(cub manage observability --help) Bash(cub manage observability * --help) Bash(cub auth status) Bash(cub manage preflight) Bash(cub manage observability coverage *) Bash(cub manage observability sidecars *) Bash(cub manage observability findings *) Bash(cub manage observability ensure-servicemonitor *) Bash(cub manage observability inject-sidecar *) Bash(cub manage profile) Bash(cub manage profile *) Bash(cub manage observability fleet-edit *)
---

# observability-instrument

Instrument workloads — as data, dry-run by default. One workload, or across a selector via a profile.

- **`ensure-servicemonitor <space>/<service-unit>`** — make a ServiceMonitor Unit, `<service>-sm`, whose selector is derived from the Service's labels (fixes uncovered metrics Services); creates it, or brings an existing one up to date.
- **`inject-sidecar <space>/<unit> --image <img>`** — inject/replace an otel-collector sidecar via `set-path` (find-or-append the container by name).
- **`cub manage profile install | list | apply`** — the profile library (e.g. `otel-sidecar`, a parameterized `set-path` Invocation, `--param image=…`).
- **`fleet-edit --profile <slug> [--where …]`** — apply a profile across a selector in one operation.

All **edit/create Units but do not release them** — rolling out is a separate `cub release publish <space>`, which bundles the Space's Units for its release Target.

## Why this matters

ServiceMonitor coverage is a cross-Unit property, and a sidecar must be find-or-appended into a pod template by name — `cub manage observability` does both as data: `ensure-servicemonitor` makes a Unit derived from the Service (idempotent); `inject-sidecar` uses `set-path`'s associative find-or-append so a re-run replaces rather than duplicates. Everything is **dry-run by default** and requires `--commit --change-desc`; ValidationErrors are never bypassed.

## When to use

- "Add a ServiceMonitor for the checkout service." → `ensure-servicemonitor <space>/checkout`.
- "Inject an otel sidecar into web." → `inject-sidecar <space>/web --image otel/opentelemetry-collector:0.100` (or `cub manage profile apply otel-sidecar … --param image=…`).
- "Add the otel collector to every prod workload." → `fleet-edit --profile otel-sidecar --environment prod --param image=…`.

## Do not load for

- Read-only checks — use **observability-audit** / **observability-findings**.
- Enforcement Triggers — use **observability-guardrails**.
- Publishing the Space's Release — that is `cub release publish <space>`.

## Preflight gates

1. `cub manage preflight` succeeds. If it fails, ask the user to run `cub auth login` (interactive) and retry.
2. For `cub manage profile apply` / `fleet-edit`, the library exists — run `cub manage profile install` once if `cub manage profile list` is empty.
3. The user has write permission on the target Space(s).

## The loop

1. **See the gap**: `cub manage observability findings` / `coverage --issues-only` / `sidecars --issues-only`.
2. **Preview** (dry-run — the default):
   ```bash
   cub manage observability ensure-servicemonitor apptique-dev/frontend
   cub manage observability inject-sidecar apptique-dev/frontend --image otel/opentelemetry-collector:0.100
   ```
3. **Commit** with `--commit --change-desc` (summary + verbatim user prompt).
4. **Verify**: `cub manage observability coverage --namespace <ns>` / `sidecars --namespace <ns>`.
5. **Roll out** is a separate step — hand off to `release-publish`.

## Stop conditions

- `ensure-servicemonitor` refuses (Service has no labels, or no metrics port and no `--port`) — supply `--port`, or fix the Service.
- A ValidationError attaches. **Do not bypass** — fix via **triggers-and-applygates**.
- The user wants the change deployed — hand off to `release-publish`.

## Tool boundary

Allowed: `ensure-servicemonitor`, `inject-sidecar`, `cub manage profile`, `fleet-edit` (dry-run by default; `--commit` passes `--change-desc`), and the read commands. Not allowed: bypassing gates, `kubectl` mutations, publishing a Release.

## References

- `cub manage observability ensure-servicemonitor --help`, `… inject-sidecar --help`, `… profile --help`, `… fleet-edit --help`.
- Companion skills: **observability-audit**, **observability-findings**, `kubernetes-resources`, `triggers-and-applygates`, `release-publish`.
