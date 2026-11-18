---
name: netpol-fix
description: 'Fix NetworkPolicy coverage gaps as data in ConfigHub with the cub manage netpol CLI: create a default-deny for a namespace, author an allow policy between two workloads, derive allow policies from the ConfigHub dependency graph (Links) or from the routes that name the Services an ingress controller serves, or patch an existing policy with the netpol-allow-dns and netpol-except-metadata profiles. Use for "lock down the payments namespace", "allow frontend to reach checkout", "generate allow policies from our Links", "only let the ingress controller reach argocd-server", "add the DNS allowance to this egress policy". All writes are dry-run by default and require --commit with a --change-desc; Units are created/edited but NOT released. Not for inventory/findings (use netpol-audit/netpol-findings), fleet-wide remediation (use netpol-fleet), or installing enforcement (use netpol-guardrails).'
phase: act
allowed-tools: Bash(cub manage netpol --help) Bash(cub manage netpol * --help) Bash(cub auth status) Bash(cub manage preflight) Bash(cub manage netpol coverage *) Bash(cub manage netpol findings *) Bash(cub manage netpol who-can-reach *) Bash(cub unit get *) Bash(cub manage netpol default-deny *) Bash(cub manage netpol allow *) Bash(cub manage netpol allow-from-links *) Bash(cub manage netpol allow-from-refs *) Bash(cub manage profile list *) Bash(cub manage profile install *) Bash(cub manage profile apply *)
---

# netpol-fix

The config-as-data fix path for NetworkPolicy: close a coverage gap or restore intended connectivity by **editing the source of record**, so the cluster converges through the normal release pipeline with no drift. Every write is **dry-run by default**; nothing is written until you re-run with `--commit` and a `--change-desc`, and even then the Unit is created/edited but **not released**.

## Why this matters

A per-resource validator can only *report* a gap; fixing it means editing the cluster out-of-band, which drifts. Here the fix is a new or edited ConfigHub Unit — versioned, reviewable, and rolled out deliberately later.

## When to use

- "Lock down the payments namespace" / "add a default-deny" → `default-deny <namespace>`.
- "Allow frontend to reach checkout" → `allow <src> <dst>`.
- "Generate allow policies from our dependency graph / Links" → `allow-from-links`.
- "Only let the ingress controller reach the workloads behind our routes" / "only admit the clients the config says connect" / "replace this allow-all ingress with what is actually used" → `allow-from-refs`.
- "This egress default-deny breaks DNS — add the allowance" → `cub manage profile apply netpol-allow-dns <space>/<unit>`.
- "Except the cloud-metadata IP from this wide egress" → `cub manage profile apply netpol-except-metadata <space>/<unit>`.

## Do not load for

- Inventory / coverage / findings — **netpol-audit**, **netpol-findings**.
- Fixing the *whole fleet* at once — **netpol-fleet** (`default-deny` with no namespace, `fleet-edit --profile`).
- Installing enforcement (Triggers/ValidationWarnings) — **netpol-guardrails**.
- Publishing the change — that's `cub release publish <space>` (the **release-publish** skill); this skill only edits stored config.

## Preflight gates

1. `cub manage preflight` succeeds. If not, ask the user to run `cub auth login` and retry.
2. For the profiles, the profile library exists (`cub manage profile install`, once per organization; re-running brings it up to date).
3. The user has Edit/Create permission on the target Space (the commit fails server-side otherwise — report the error, don't retry blindly).

## The loop

1. **Find the gap** with **netpol-audit** / **netpol-findings** (`coverage`, `findings`).
2. **Preview (dry-run)** — run the write with no `--commit`; the shared write report lists each Unit that would be created or changed and its diff, and nothing is written:
   ```bash
   cub manage netpol default-deny payments --cluster prod-cluster                 # ingress default-deny
   cub manage netpol default-deny payments --cluster prod-cluster --egress         # + egress, allowing DNS
   cub manage netpol allow frontend checkout --cluster prod-cluster --port 8080
   cub manage netpol allow-from-links --space apptique-prod                        # one policy per dest, from Links
   cub manage netpol allow-from-refs --namespace argocd -o table                   # from routes and Service addresses in config
   cub manage profile apply netpol-allow-dns apptique-prod/default-deny-apptique
   cub manage profile apply netpol-except-metadata apptique-prod/wide-egress
   ```
3. **Confirm** the diff matches intent. Adjust flags if not.
4. **Commit** with a real change description:
   ```bash
   cub manage netpol default-deny payments --cluster prod-cluster \
     --commit --change-desc "Default-deny ingress for payments (no prior policy). User prompt: ..."
   ```
5. **Stop.** The write created/edited a Unit; it is NOT released (the report ends by saying so). To roll it out, hand off to **release-publish** (`cub release publish`), which respects ValidationErrors.

## Notes & idioms

- **default-deny**, **allow** and **allow-from-links** create their Unit, or bring an existing one up to date, so re-running changes nothing.
- **default-deny** infers the Space/cluster Target from the namespace's workloads; use `--cluster`/`--space` to disambiguate. `--egress` includes the kube-dns `:53` allowance so it doesn't break name resolution.
- **default-deny** leaves a namespace alone that already has one under any name (a policy selecting every pod that isolates the direction without admitting all traffic) and reports that Unit unchanged. When another Unit's policy denies ingress, `--egress` adds the egress half as `default-deny-egress-<namespace>`.
- **allow** is ingress on the destination by default (`--egress` for source-side egress); `--port` restricts it.
- **allow-from-links** reads ConfigHub Links (consumer→producer) and authors one consolidated policy per destination (all sources as `from` peers) — the idiomatic shape; `--per-edge` for one policy per pair. Re-running **upserts** (adds only missing sources to an existing policy; idempotent). A Link to a multi-resource Unit targets the resource whose name matches the Unit slug.
- **allow-from-refs** reads three kinds of reference (`--refs routes,config,scrape`). Routes: for Ingresses and Traefik IngressRoutes, the controller serving each one (Traefik, or the Ingress's IngressClass's controller, found by its image; `--ingress-controller <ns>/<workload>` otherwise) is admitted to the pods of each Service it names, on the pod port. Config: a Service address (`svc.ns.svc`, or a shorter name with a scheme or port, for a Service ConfigHub has) in a workload's env, args or command, or in a ConfigMap it mounts, reads through env, or finds by label with a kiwigrid k8s-sidecar (Grafana's datasources), admits the workload to that Service's pods. Scrape: a scraper's Kubernetes discovery (Prometheus/VictoriaMetrics `kubernetes_sd_configs`, or Alloy `discovery.kubernetes` into `prometheus.scrape`), with its keep/drop/replace relabel rules applied, admits the scraper to the pods and ports it scrapes. It takes the rules admitting every source out of the destination's policies, adds the controller's rule, and creates `allow-<workload>-ingress` when no policy isolates the destination. When every variant of an upstream Unit takes the same edit, or would get the same new policy, it makes the change in the upstream instead (`--no-upstream` to change the variants): in a ChangeSet in the base, closed and named by a ChangeOrder on the component's ChangeWorkflow, both named `--rollout` (default `netpol-allow-from-refs`) and annotated `Trigger=<space>/<slug>`. Promote it stage by stage with `cub variant promote <variant> --change-order <base>/<rollout>`; that is a separate step, and releasing each stage is the user's. A base whose Component has no ChangeWorkflow, or that has a ChangeSet or ChangeOrder of that name already, is reported and not changed. Read its **access changes** before committing: a client no route names, such as a metrics scraper or an in-cluster caller, loses access. A Unit imported from elsewhere may need `--guard Override=External --clearance Override=External`.
- **netpol-allow-dns** adds the kube-dns `:53` egress allowance to a policy that restricts egress and allows nothing on port 53; **netpol-except-metadata** excepts `169.254.169.254/32` from each egress ipBlock whose CIDR contains it. Both are idempotent `set-yq` edits that change only the policies needing them (the report shows the Unit unchanged otherwise).

## Stop conditions

- The user asks to publish/roll out — hand off to **release-publish**, don't publish here.
- A commit hits a ValidationError or permission error — report it; fix the data or route to **triggers-and-applygates**; never bypass a gate.
- Whole-fleet remediation requested — hand off to **netpol-fleet**.

## Tool boundary

Allowed: the dry-run/commit writes above (always with `--change-desc` on commit) plus read commands for context. Not allowed: publishing a Release, `kubectl` mutation, bypassing gates.

## References

- `cub manage netpol default-deny --help`, `cub manage netpol allow --help`, `cub manage netpol allow-from-links --help`, `cub manage netpol allow-from-refs --help`, `cub manage profile apply --help`.
- Companion skills: **netpol-audit**, **netpol-findings**, **netpol-fleet**, **netpol-guardrails**, **release-publish**.
