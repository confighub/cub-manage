# Tutorial: managing workload scheduling with `cub manage scheduling`

`cub manage scheduling` manages the constraints the Kubernetes scheduler reads:
**placement** (which nodes a workload may land on: nodeSelector, tolerations, node
affinity) and **spread** (how its replicas spread across zones and nodes: topology
spread constraints, pod anti-affinity, and each cluster's default spread).

This walks it end to end against a live ConfigHub fleet: see where workloads are
allowed to land, find the placement anti-pattern, fix it as data, set how replicas
spread, and stand up enforcement. Every command and block of output below is real.

`cub manage scheduling` reads and writes ConfigHub Units — it never touches a cluster.
Its writes create new Unit revisions; rolling those out is a separate `cub unit
apply`. Read commands default to JSON; add `-o table` for the human view used
here.

## Prerequisites

- A ConfigHub session — `cub auth login`.
- The binary: `make build` produces `cub manage scheduling`. It also runs as a `cub`
  plugin (`cub scheduling …`); this tutorial uses the `cub manage scheduling` form.

## 1. Verify the session

```console
$ cub manage preflight
cub manage scheduling: ready (ConfigHub session valid)
```

## 2. See where a workload lands

`placement` reports each workload's `nodeSelector`, `tolerations`, and node
affinity, and whether it is **constrained** — i.e. whether it actually restricts
which nodes it lands on. Here `web` pins nothing:

```console
$ cub manage scheduling placement -o table --cluster nsmgr-proto-m3
CLUSTER         NAMESPACE  KIND        NAME  NODESELECTOR  TOLERATIONS  NODEAFFINITY  CONSTRAINED
nsmgr-proto-m3  m3app      Deployment  web   -             -            none          no

1 workloads (0 constrained, 1 unconstrained)
```

(`snapshot` gives per-cluster counts; `list` is the workload explorer.)

## 3. Create — and detect — the anti-pattern

Suppose we let `web` tolerate the GPU taint but forget to pin it to the GPU pool.
Writes are dry-run by default; `--commit --change-desc` writes the revision:

```console
$ cub manage scheduling set-tolerations nsmgr-proto-m3/web --toleration nvidia.com/gpu:NoSchedule \
    --commit --change-desc "Tolerate gpu taint"
set-tolerations: 1 of 1 Unit(s) changed
```

Now `findings` flags it — a toleration only *permits* scheduling onto a tainted
node; without a matching `nodeSelector` or node affinity the pod may still land on
general nodes:

```console
$ cub manage scheduling findings -o table --cluster nsmgr-proto-m3
SEVERITY  ANALYZER                      CLUSTER         NAMESPACE  KIND        NAME  MESSAGE
Medium    toleration-without-placement  nsmgr-proto-m3  m3app      Deployment  web   tolerates taint(s) [nvidia.com/gpu] but has no nodeSelector or required node affinity — may schedule onto general nodes

1 finding: 1 medium
```

## 4. Fix it as data — with a profile

Profiles are named placement edits stored as ConfigHub Invocations. Seed the
library once, then list it:

```console
$ cub manage profile install
Space common ready
Profile common/placement-gpu ready
Profile common/placement-spot ready
Profile common/node-pool ready

$ cub manage profile list -o table
PROFILE         FUNCTION  PARAMS  DESCRIPTION
node-pool       set-yq    pool    set-yq: pin nodeSelector.pool to the given pool (param: pool)
placement-gpu   set-yq    -       set-yq: pin to the gpu node pool + tolerate the nvidia.com/gpu taint
placement-spot  set-yq    -       set-yq: pin to the spot node pool + tolerate the spot taint
```

Pin `web` to the gpu pool with the parameterized `node-pool` profile:

```console
$ cub manage profile apply node-pool nsmgr-proto-m3/web --param pool=gpu \
    --commit --change-desc "Pin to gpu pool"
profile apply node-pool: 1 of 1 Unit(s) changed
```

(For one workload you can also use `set-node-selector nsmgr-proto-m3/web
--selector pool=gpu` directly, or `set-node-affinity … --required
"topology.kubernetes.io/zone=us-east-1a,us-east-1b"`.)

## 5. Verify

`web` is now constrained, and the finding is gone:

```console
$ cub manage scheduling placement -o table --cluster nsmgr-proto-m3
CLUSTER         NAMESPACE  KIND        NAME  NODESELECTOR  TOLERATIONS     NODEAFFINITY  CONSTRAINED
nsmgr-proto-m3  m3app      Deployment  web   pool=gpu      nvidia.com/gpu  none          yes

1 workloads (1 constrained, 0 unconstrained)

$ cub manage scheduling findings -o table --cluster nsmgr-proto-m3
No findings.
```

## 6. Fleet-wide

To place many workloads at once, `fleet-edit` applies a profile across a
`--where` selector in one operation (dry-run first to see the blast radius):

```console
$ cub manage scheduling fleet-edit --profile placement-gpu --environment prod --component ml
fleet-edit placement-gpu: N of N Unit(s) would change (dry-run — pass --commit --change-desc to write)
```

Carrying that change on to the Spaces cloned from a base is variant
promotion, which lives in `cub` and the **promote-release** skill rather than in
this tool.

## 7. Spread

A Deployment, ReplicaSet or StatefulSet with no topology spread constraints of its
own gets its cluster's default ones. Those are the cluster's kube-scheduler
configuration, which cannot be read from the cluster, so `spread` takes them from
the Target's `Scheduling.DefaultTopologySpread` fact when an operator recorded one,
and otherwise assumes Kubernetes' built-in defaults (hostname maxSkew 3, zone
maxSkew 5, both `ScheduleAnyway`), which is what EKS, GKE and AKS run. Start by
reading what applies:

```console
$ cub manage scheduling spread -o table --cluster <cluster>
$ cub manage scheduling spread -o table --issues-only
```

The first table is per cluster: its distribution, zones, default spread
constraints, and where those came from (`target-fact`, `managed-distribution` or
`assumed`). The second is per workload: the constraints that apply, their source
(`explicit` for its own, `cluster-default`, or `none`), and its pod anti-affinity
terms. `--issues-only` keeps the workloads with no constraints of their own.

The built-in defaults are soft and loose — a zone may hold five more replicas than
another — so give a workload that must survive a zone loss a constraint of its own.
Each write is a dry run until `--commit --change-desc`:

```console
$ cub manage scheduling set-topology-spread nsmgr-proto-m3/web                     # zones, maxSkew 1, ScheduleAnyway
$ cub manage scheduling set-topology-spread nsmgr-proto-m3/web --topology-key kubernetes.io/hostname
$ cub manage scheduling set-pod-anti-affinity nsmgr-proto-m3/web                   # preferred, per node
```

`set-topology-spread` replaces any constraint on the same topology key and keeps
the others; it selects the workload's own Pods and, by default, sets
`matchLabelKeys: [pod-template-hash]`, so a Deployment's rollout spreads each
revision on its own. Prefer `ScheduleAnyway` (the default): `DoNotSchedule` and
`set-pod-anti-affinity --required` can leave replicas `Pending`, and `--min-domains`
is valid only with `--when-unsatisfiable DoNotSchedule`. `--remove` takes a
constraint or term away.

To record the defaults of a cluster whose scheduler configuration you set:

```console
$ cub target update --space <space> --patch <target> \
    --fact Scheduling.DefaultTopologySpread=topology.kubernetes.io/zone=1:ScheduleAnyway
```

A managed cluster's defaults cannot be changed, so the fleet-wide substitute is
the `topology-spread-soft` profile (zones then nodes, maxSkew 1, `ScheduleAnyway`,
per Deployment revision), applied with `fleet-edit`; `anti-affinity-soft` adds
preferred pod anti-affinity across nodes the same way:

```console
$ cub manage scheduling fleet-edit --profile topology-spread-soft --environment prod
```

`findings` also reports spread that cannot work as written: `minDomains` without
`DoNotSchedule`, a selector that misses the workload's own Pods, a topology key no
node carries, `minDomains` above the cluster's zone count, a hard constraint
without `matchLabelKeys` over a rollout, and required anti-affinity per node.

## 8. Enforce with guardrails

`guardrails install` stands up `Warn=true` `vet-cel` rules
(`scheduling-toleration-needs-placement`, `scheduling-spread-selects-own-pods`,
`scheduling-min-domains-needs-donotschedule`) and wires in-scope Spaces to them. It's
dry-run by default and conservative — it skips Spaces that already select Triggers
their own way:

```console
$ cub manage scheduling guardrails install -o table        # preview
$ cub manage scheduling guardrails install --commit        # apply
$ cub manage guardrails status -o table         # Units now carrying warnings
```

Promote a rule to blocking later with, for example,
`cub manage guardrails update scheduling-toleration-needs-placement --enforce --commit`.

## Reference

- **Scoping** any command: `--where "<expr>"` (Unit / `Space.*` / `Target.*`,
  flat `AND`-only) plus shorthands `--component` / `--environment` / `--region` /
  `--owner` / `--layer` / `--variant`. `--cluster` / `--namespace` are client-side
  display filters.
- **Discipline**: reads default to JSON (`-o table` for humans); writes are
  dry-run until `--commit --change-desc`; nothing is published (roll it out with
  `cub release publish <space>` separately); ValidationErrors are never bypassed.
- **Scheduling vs availability**: topology spread and pod anti-affinity are set
  here; `cub manage workload availability` checks whether a multi-replica workload
  has a PodDisruptionBudget and spread of its own.
