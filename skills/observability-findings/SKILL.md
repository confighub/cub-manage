---
name: observability-findings
description: 'Produce severity-ranked observability findings across a ConfigHub fleet with the cub manage observability CLI — a metrics-exposing Service with no ServiceMonitor selecting it (the cross-Unit coverage join), and a dangling ServiceMonitor that selects nothing. Use for "what observability gaps do we have?", "which metrics Services are not scraped?", "any ServiceMonitors selecting nothing?", "observability findings for the checkout component". Not for the raw coverage report (use observability-audit), fixes (use observability-instrument), or live cluster state (use kubectl); analysis is over ConfigHub-managed Units only.'
phase: verify
allowed-tools: Bash(cub manage observability --help) Bash(cub manage observability * --help) Bash(cub auth status) Bash(cub manage preflight) Bash(cub manage observability findings) Bash(cub manage observability findings *)
---

# observability-findings

Report observability gaps across the fleet, most-severe first. Read-only.

## Why this matters

Two analyzers:
- **unmonitored-service** (medium) — a metrics-exposing Service with **no ServiceMonitor** selecting it. This is the cross-Unit join a per-Unit validator can't do: the ServiceMonitor and the Service are separate Units.
- **dangling-servicemonitor** (low) — a ServiceMonitor that selects no Service in its namespace.

Analysis is over **ConfigHub-managed Units only**.

## When to use

- "What observability gaps do we have?" → `findings`.
- "Which metrics Services aren't scraped?" → `findings --analyzer unmonitored-service`.
- "Any ServiceMonitors selecting nothing?" → `findings --analyzer dangling-servicemonitor`.
- "Findings for the checkout component / in prod." → `findings --component checkout` / `--environment prod`.

## Do not load for

- The raw coverage / sidecar report — use **observability-audit**.
- Fixing gaps — use **observability-instrument**.
- Live cluster state — `kubectl`.

## Preflight gates

1. `cub manage preflight` succeeds. If it fails, ask the user to run `cub auth login` (interactive) and retry.

## The toolkit

- `cub manage observability findings -o table` — severity-ranked findings (`--severity`, `--analyzer unmonitored-service|dangling-servicemonitor`, `--cluster`, `--namespace`).

## Stop conditions

- Zero findings — report the fleet is clean for that scope.
- The user wants to fix something — hand off to **observability-instrument**.

## Tool boundary

Read-only. Never use `kubectl` to answer a ConfigHub-state question.

## References

- `cub manage observability findings --help`.
- Companion skills: **observability-audit**, **observability-instrument**.
