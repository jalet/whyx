# Lay out value-file layers for whyx

Structure a `helm-charts` repository's `envs/` tree so that `whyx` (and Argo CD)
resolve value-file layers in the order you intend. Do this when bootstrapping a
new repo, onboarding a new tenant, or adding an environment or cluster.

## Prerequisites

- A repository whose root holds both `charts/` and `envs/` directories.
- Charts placed under a category folder: `charts/base/`, `charts/apps/`, or
  `charts/vendor/`.
- `whyx` installed, to verify the result.

## Understand the merge order

`whyx` resolves up to seven layers for a `(target, chart)` pair and merges them
lowest-to-highest. Later layers win; maps deep-merge, lists replace. Any missing
file is skipped, so the delta layers are optional.

| # | Layer             | File                                              | Owner            |
| - | ----------------- | ------------------------------------------------- | ---------------- |
| 1 | Chart defaults    | `charts/<category>/<chart>/values.yaml`           | chart author     |
| 2 | Platform-wide     | `envs/_platform/values.yaml`                      | platform team    |
| 3 | Tenant-wide       | `envs/<tenant>/values.yaml`                       | platform team    |
| 4 | Environment-wide  | `envs/<tenant>/<env>/values.yaml`                 | platform team    |
| 5 | Cluster (target)  | `envs/<tenant>/<env>/<cluster>/values.yaml`       | platform team    |
| 6 | Infra contract    | `.../<cluster>/platform.generated.yaml`           | Pulumi (machine) |
| 7 | Promoted versions | `.../<cluster>/versions.generated.yaml`           | Kargo (machine)  |

A target is the path `tenant/env/cluster`. Each segment is one directory level
under `envs/`, and the segment names are yours to choose (for example
`project/dev/apps`).

## Create the directory tree

Lay out the `envs/` tree so the target path is a real directory chain. For a
target `project/dev/apps`:

```text
envs/
  _platform/
    values.yaml                  # layer 2: applies to every target
  project/
    values.yaml                  # layer 3: applies to the whole tenant
    dev/
      values.yaml                # layer 4: applies to dev across clusters
      apps/
        values.yaml              # layer 5: this cluster only
        platform.generated.yaml  # layer 6: written by Pulumi
        versions.generated.yaml  # layer 7: written by Kargo
```

Each `values.yaml` is a delta: put a key at the broadest layer that should own
it, and override it lower down only where it differs. Anything you omit falls
through to the layer above.

> **Note:** `_platform` is a fixed directory name. The tenant, env, and cluster
> names are free-form, but every segment must be non-empty and must not be `.`
> or `..`.

## Place the hand-owned layers

- **Platform-wide (`envs/_platform/values.yaml`)**: defaults for every tenant,
  env, and cluster. Use it for fleet-wide policy.
- **Tenant-wide (`envs/<tenant>/values.yaml`)**: settings shared by all of one
  tenant's environments and clusters.
- **Environment-wide (`envs/<tenant>/<env>/values.yaml`)**: settings shared by
  every cluster in one environment, such as a shared endpoint for `dev`.
- **Cluster (`envs/<tenant>/<env>/<cluster>/values.yaml`)**: the most specific
  hand-edited layer, for one cluster.

## Leave room for the machine-owned layers

Layers 6 and 7 live in the cluster directory and are written by automation, not
by hand:

- `platform.generated.yaml` is the infrastructure contract emitted by Pulumi.
- `versions.generated.yaml` carries promoted image versions from Kargo.

Because they are the two highest layers, they win over hand-edited values. Do
not edit them directly; change the source pipeline instead. If neither tool runs
yet, omit the files. `whyx` skips them.

## Verify the layout

From inside the repo, list the resolved layers for a target and chart. `whyx`
prints only the files that exist, in merge order:

```console
$ whyx project/dev/apps backend --layers
1  chart defaults    chart author     /repo/charts/apps/backend/values.yaml
2  platform-wide     platform team    /repo/envs/_platform/values.yaml
3  tenant-wide       platform team    /repo/envs/project/values.yaml
4  environment-wide  platform team    /repo/envs/project/dev/values.yaml
5  cluster           platform team    /repo/envs/project/dev/apps/values.yaml
```

Confirm each path is the file you expect. A file you created but that does not
appear is in the wrong place. To then check which layer wins for a specific
value, see [Trace why a Helm value is set](trace-a-helm-value.md).

## Troubleshooting

| Symptom                                                    | Cause and fix                                                                                                       |
| ---------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------ |
| A `values.yaml` you added is missing from `--layers`       | The path does not match `envs/<tenant>/<env>/<cluster>/`. Check the segment names against your target exactly.      |
| `chart not found under charts/{base,apps,vendor}`          | The chart directory is not under `base/`, `apps/`, or `vendor/`. Move it into one of the three category folders.    |
| `no value files found for target and chart`                | No layer file exists for this target. At least one of the seven files must be present; create the cluster `values.yaml`. |
| A generated layer overrides a value you set by hand        | Layers 6 and 7 outrank hand-edited layers by design. Change the value in Pulumi or Kargo, not in `envs/`.          |
| `helm-charts repo root not found (need charts/ and envs/)` | The root is missing `charts/` or `envs/`. Create both at the repo top level, or pass `--repo`.                     |
