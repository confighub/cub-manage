# cub-manage

`cub-manage` manages Kubernetes configuration stored as data in [ConfigHub](https://confighub.com),
across a whole fleet of clusters at once. It is a `cub` plugin, run as `cub manage`, and also runs
standalone as `cub-manage`.

Each command group owns one concern:

| Group           | Manages                                                                                   |
| --------------- | ----------------------------------------------------------------------------------------- |
| `namespace`     | Namespace objects, their Pod Security Standard, and their names across variants           |
| `rbac`          | Roles, ClusterRoles, their bindings, and ServiceAccounts: effective access and hygiene    |
| `netpol`        | NetworkPolicy coverage, connectivity, and default-deny                                    |
| `workload`      | Workload readiness: security context, resources, probes, PodDisruptionBudgets, spread check |
| `scheduling`    | Placement and spread: nodeSelector, tolerations, affinity, topology spread constraints    |
| `autoscale`     | HorizontalPodAutoscalers and KEDA ScaledObjects                                           |
| `observability` | ServiceMonitor coverage and telemetry sidecars                                            |

Each group reads the fleet and reports what it finds — including properties of a set of resources
that a per-object validator cannot see, such as whether a PodDisruptionBudget in one Unit covers a
workload in another — fixes it by editing ConfigHub Units rather than clusters, and installs a
guardrail pack that keeps it fixed. [docs/design.md](docs/design.md) describes the design, the
conventions every group follows, and where each group's responsibility ends.

The tool is meant to be driven by an AI agent as well as used by people. The agent skills in
[`skills/`](skills) teach an agent when and how to use each command.

## Install

```sh
cub plugin install confighub/cub-manage            # the latest release
cub plugin install confighub/cub-manage@v0.1.0     # a given release
cub plugin upgrade manage                          # move to the latest release
```

`cub-manage` uses your `cub` session. Sign in with `cub auth login`, then check with:

```sh
cub manage preflight
```

## Build

```sh
make build                  # -> bin/cub-manage
make test
```

Install a build as the plugin, or run `bin/cub-manage` directly:

```sh
cub plugin install ./bin/cub-manage
```

## Release

Push a tag `vX.Y.Z` on `main`. The release workflow tests, builds a binary for Linux and macOS
on amd64 and arm64 with the version stamped in, and publishes them as a GitHub release, which
is what `cub plugin install confighub/cub-manage` installs.

```sh
git tag v0.1.0 && git push origin v0.1.0
```

## Examples

```sh
cub manage workload readiness -o table                       # per-workload readiness scorecard
cub manage netpol coverage --environment prod -o table       # namespaces without a default-deny
cub manage rbac who-can delete secrets -o table              # effective access, across the fleet
cub manage namespace pod-security -o table                   # the level each namespace's workloads meet

cub manage workload harden shop-prod/checkout                # dry run: shows the change
cub manage workload harden shop-prod/checkout --commit --change-desc "Run checkout as non-root"

cub manage profile install                                   # seed the shared profile library
cub manage workload fleet-edit --profile harden-restricted --environment dev
cub manage workload guardrails install --commit
cub manage guardrails status -o table
cub manage guardrails exception add shop-prod/batch --rule workload-pdb-coverage \
    --reason "singleton by design" --commit --change-desc "batch runs one replica"
cub manage guardrails exception list -o table                # what is excepted, and why
cub manage guardrails status --by-rule -o table              # each rule: Created, Audited, Enforced, Disabled
cub manage guardrails update workload-runs-nonroot --priority High --enforced-by 2026-12-31 --commit
cub manage guardrails update workload-runs-nonroot --enforce --commit   # warnings become errors
```

## Conventions

- **Output.** Read commands print JSON unless `-o` says otherwise: `-o table` for people, and
  `yaml`, `jq=<expr>` and `yq=<expr>`.
- **Scope.** `--where "<expr>"` is one ConfigHub Unit filter, evaluated on the server, that can
  refer to the Unit, its Space (`Space.*`) and its Target (`Target.*`). `--component`
  selects the Spaces that are Variants of a Component; `--environment`, `--region`, `--owner`,
  `--layer` and `--variant` are shorthands for the standard Space labels. All are joined with `AND`. `--cluster` and `--namespace` narrow what is displayed.
- **Clusters.** A cluster is a ConfigHub Target. Units whose Space has no release Target are grouped
  under `None`.
- **Writes** are dry runs unless `--commit` is given with `--change-desc`. They edit or create Units
  and never apply them to a cluster; releasing a change is a separate step. They never bypass a
  gate.
- **Guardrails** install as warnings. Make one blocking with
  `cub manage guardrails update <slug> --enforce --commit`.

Run `cub manage <group> --help` for a group's commands.

## License

MIT. See [LICENSE](LICENSE).
