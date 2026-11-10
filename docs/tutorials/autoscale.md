# Tutorial: managing autoscaling with `cub manage autoscale`

This walks `cub manage autoscale` end to end against a live ConfigHub fleet: see what
autoscales, find the anti-patterns, edit an HPA as data, convert it to KEDA, and
stand up enforcement. Every command and block of output below is real.

`cub manage autoscale` reads and writes ConfigHub Units — it never touches a cluster. Its
writes create new Unit revisions; rolling those out is a separate `cub unit
apply`. Read commands default to JSON; add `-o table` for the human view used here.

## Prerequisites

- A ConfigHub session — `cub auth login`.
- The binary: `make build` produces `cub manage autoscale`. It also runs as a `cub`
  plugin (`cub autoscale …`); this tutorial uses the `cub manage autoscale` form.

## 1. Verify the session

```console
$ cub manage preflight
cub manage autoscale: ready (ConfigHub session valid)
```

## 2. Inventory the fleet

`snapshot` gives per-cluster counts: HPAs, KEDA ScaledObjects, scalable workloads
(and how many are actually autoscaled), and PodDisruptionBudgets.

```console
$ cub manage autoscale snapshot -o table
CLUSTER                                 HPAS  SCALEDOBJECTS  WORKLOADS  AUTOSCALED  PDBS  UNITS
cluster-worker-kubernetes-yaml-cluster  0     0              1          0           0     6
dev-cluster                             0     0              19         0           0     33
nsmgr-proto-apptique                    0     0              1          0           1     8
nsmgr-proto-m3                          1     1              1          1           0     9
prod-cluster                            0     0              19         0           0     38

5 clusters, 1 HPAs, 1 ScaledObjects, 41 workloads (1 autoscaled), 1 PDBs
```

`list` is the autoscaler explorer — kind, scale target, bounds, and whether it's
**pinned** (`min == max`, so it can't scale). Narrow it with `--kind`
(`HorizontalPodAutoscaler` or `ScaledObject`), `--cluster` and `--namespace`:

```console
$ cub manage autoscale list -o table --where "Slug LIKE '%hpa%'"
CLUSTER         NAMESPACE  KIND          NAME  TARGET          MIN  MAX  PINNED  UNIT
nsmgr-proto-m3  m3app      HPA           web   Deployment/web  5    15   no      nsmgr-proto-m3/autoscale-test-hpa
nsmgr-proto-m3  m3app      ScaledObject  web   web             2    10   no      nsmgr-proto-m3/web-hpa

2 resources
```

## 3. Find the anti-patterns

`findings` ranks issues most-severe first, over ConfigHub Units only:

- **pinned-autoscaler** — an HPA/ScaledObject with `min == max`.
- **pdb-blocks-min-scale** — the cross-resource check: a PodDisruptionBudget whose
  `minAvailable` is `>=` the autoscaler's `minReplicas`, so at minimum scale no pod
  may be voluntarily evicted (node drains stall).
- **missing-autoscaler** — a Deployment/StatefulSet nothing autoscales.

```console
$ cub manage autoscale findings -o table --severity medium
No findings.

$ cub manage autoscale findings -o table --cluster dev-cluster | head -4
SEVERITY  ANALYZER            CLUSTER      NAMESPACE  KIND        NAME      MESSAGE
Low       missing-autoscaler  dev-cluster  appchat    Deployment  backend   no HorizontalPodAutoscaler or ScaledObject targets this workload
Low       missing-autoscaler  dev-cluster  appchat    Deployment  frontend  no HorizontalPodAutoscaler or ScaledObject targets this workload
```

## 4. Edit an HPA as data

`set-hpa` edits an HPA's replica bounds and cpu/memory targets. Writes are dry-run
by default; `--commit --change-desc` writes the revision:

```console
$ cub manage autoscale set-hpa nsmgr-proto-m3/autoscale-test-hpa --min 3 --max 20 --cpu 65 -o table
UNIT                MUTATED  ERROR
autoscale-test-hpa  yes      -

set-hpa: 1 of 1 Unit(s) would change (dry-run — pass --commit --change-desc to write)

$ cub manage autoscale set-hpa nsmgr-proto-m3/autoscale-test-hpa --min 3 --max 20 --cpu 65 \
    --commit --change-desc "raise bounds to 3-20, cpu 65%" -o table
UNIT                MUTATED  ERROR
autoscale-test-hpa  yes      -

set-hpa: 1 of 1 Unit(s) changed

Not released: 'cub release publish nsmgr-proto-m3' releases a Space's Units to its Target.
```

For reusable edits, the profile library stores named, parameterized set-yq
Invocations:

```console
$ cub manage profile install
Space common ready
Profile common/hpa-conservative ready
Profile common/hpa-aggressive ready
Profile common/hpa-range ready

$ cub manage profile list -o table
PROFILE           FUNCTION  PARAMS   DESCRIPTION
hpa-aggressive    set-yq    -        set-yq: pack tighter — cpu target 85% average Utilization (fewer replicas)
hpa-conservative  set-yq    -        set-yq: scale out early — cpu target 60% average Utilization (more headroom)
hpa-range         set-yq    min,max  set-yq: set minReplicas/maxReplicas (params: min, max)

$ cub manage profile apply hpa-range nsmgr-proto-m3/autoscale-test-hpa \
    --param min=5 --param max=15 --commit --change-desc "apply hpa-range 5-15" -o table
UNIT                MUTATED  ERROR
autoscale-test-hpa  yes      -

profile apply hpa-range: 1 of 1 Unit(s) changed

Not released: 'cub release publish nsmgr-proto-m3' releases a Space's Units to its Target.
```

## 5. Convert HPA → KEDA

`convert-keda` rewrites an HPA as an equivalent KEDA ScaledObject — preserving
`min`/`max` and cpu/memory metrics — by running the `convert-hpa-to-keda` function
in an embedded executor **in-process** (no server-side function). It reports
like every other write: a dry run shows what the conversion would change, the
HorizontalPodAutoscaler deleted and the ScaledObject added. A Unit with no
HorizontalPodAutoscaler is reported unchanged.

```console
$ cub manage autoscale convert-keda nsmgr-proto-m3/autoscale-test-hpa -o table
UNIT                MUTATED  ERROR
autoscale-test-hpa  yes      -

convert-keda: 1 of 1 Unit(s) would change (dry-run — pass --commit --change-desc to write)

Changes to unit nsmgr-proto-m3/autoscale-test-hpa from convert-keda:
Resource: autoscaling/v2/HorizontalPodAutoscaler m3app/web  (deleted)
    ...
Resource: keda.sh/v1alpha1/ScaledObject m3app/web  (added)
    apiVersion: keda.sh/v1alpha1
    kind: ScaledObject
    metadata:
      name: web
      namespace: m3app
    spec:
      scaleTargetRef:
        name: web
      minReplicaCount: 5
      maxReplicaCount: 15
      triggers:
      - type: cpu
        metricType: Utilization
        metadata:
          value: "65"
```

The Unit is edited, not applied; deploying a KEDA ScaledObject also requires the
KEDA operator installed in the target cluster. Only cpu/memory Resource metrics are
translated — Pods/Object/External metrics need a matching KEDA scaler and are left
for you to add.

## 6. Fleet-wide

To change many autoscalers at once, `fleet-edit` applies a profile across a
`--where` selector in one operation (dry-run first to see the blast radius):

```console
$ cub manage autoscale fleet-edit --profile hpa-conservative --where "Slug = 'autoscale-test-hpa'" -o table
SPACE           UNIT                MUTATED  ERROR
nsmgr-proto-m3  autoscale-test-hpa  yes      -

fleet-edit hpa-conservative: 1 of 1 Unit(s) would change (dry-run — pass --commit --change-desc to write)
```

Carrying that change on to the Spaces cloned from a base is variant
promotion, which lives in `cub` and the **promote-release** skill rather than in
this tool.

## 7. Enforce with guardrails

`guardrails install` stands up a `Warn=true` Trigger in the `common`
Space, labeled `Pack: autoscale`, and adds the `autoscale` group to each
in-scope Space's `WhereTrigger`. It's dry-run by default and conservative — it
skips Spaces that already select Triggers their own way:

```console
$ cub manage autoscale guardrails install --commit -o table
Guardrail group "autoscale" ready in common.
  ...
Applied — guardrail group "autoscale" in policy Space "common"
  triggers: autoscale-not-pinned
  ...
```

`autoscale-not-pinned` (`vet-cel`) flags any HPA/ScaledObject with `min == max`.

Schema validation is not autoscaling's: the tool-level `schema` group's
`schema-valid` rule (`vet-schemas`) validates every mutation against the
Kubernetes/CRD schema catalog — the **post-convert check** for `convert-keda`,
since `keda.sh` is in the catalog. Install it on its own with
`cub manage guardrails install --commit`. A committed ScaledObject passes:

```console
$ cub function do --space nsmgr-proto-m3 --where "Slug = 'web-hpa'" vet-schemas
  Passed: true  Function: vet-schemas
```

`cub manage guardrails status -o table` lists Units now carrying warnings. Promote a rule to
blocking later with
`cub manage guardrails update autoscale-not-pinned --enforce --commit`.

## Reference

- **Scoping** any command: `--where "<expr>"` (Unit / `Space.*` / `Target.*`,
  flat `AND`-only) plus shorthands `--component` / `--environment` / `--region` /
  `--owner` / `--layer` / `--variant`. `--cluster` / `--namespace` (on read
  commands) are client-side display filters.
- **Discipline**: reads default to JSON (`-o table` for humans); writes are
  dry-run until `--commit --change-desc`; nothing is released (roll it out with
  `cub release publish <space>` separately); ValidationErrors are never bypassed.
- **HPA → KEDA** runs client-side in an embedded executor; deploying ScaledObjects
  needs the KEDA operator in the target cluster.
