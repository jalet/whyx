# Trace why a Helm value is set

Use `whyx` to find which value-file layer is responsible for a rendered Helm
value, so you can stop guessing which file to edit when a deployment looks
wrong.

## Prerequisites

- `whyx` on your `PATH` (`go install github.com/jalet/whyx/cmd/whyx@latest`).
- A `helm-charts` repository checkout containing `charts/` and `envs/`.
- The deployment target (`tenant/env/cluster`) and chart name you want to
  inspect. If you are unsure how the `envs/` tree is structured, see
  [Lay out value-file layers for whyx](lay-out-value-file-layers.md).

`whyx` is read-only and needs no cluster access. It replays the same value-file
precedence Argo CD applies when it renders.

## Trace the full cascade

Run `whyx` from inside the repo checkout with a target and a chart. It walks the
seven merge layers lowest-to-highest and prints each layer that changed a value,
as a git-style diff.

```console
$ whyx project/dev/apps backend
@@ layer 5 · cluster · platform team @@
  ~ replicas: 1 -> 2
@@ layer 7 · promoted versions · Kargo (machine) @@
  ~ image.tag: dev -> prod
```

Read the diff bottom-up to find the final value, top-down to see its history:

- `+` a layer introduced the key.
- `~` a layer overrode an existing value (`old -> new`).
- `-` a layer removed the key.

The hunk header names the layer's merge index, kind, and owner, so you know
which file and which team to go to. Layers that exist but change nothing for
this chart are omitted; absent layers are skipped entirely.

The chart-defaults layer (1) is hidden by default -- it is just the chart
author's baseline, and an override still shows that baseline as the `before`
value (here `~ replicas: 1 -> 2`). Pass `--chart-defaults` to include it:

```console
$ whyx project/dev/apps backend --chart-defaults
@@ layer 1 · chart defaults · chart author @@
  + image.tag: dev
  + replicas: 1
@@ layer 5 · cluster · platform team @@
  ~ replicas: 1 -> 2
@@ layer 7 · promoted versions · Kargo (machine) @@
  ~ image.tag: dev -> prod
```

## Trace a single key

Pass a dotted key as the third argument to follow just that value's lineage. The
output keeps only the layers that touched the key and ends with a resolved line
naming the winner.

```console
$ whyx project/dev/apps backend image.tag
@@ layer 1 · chart defaults · chart author @@
  + image.tag: dev
@@ layer 7 · promoted versions · Kargo (machine) @@
  ~ image.tag: dev -> prod
= image.tag: prod
  set by layer 7 · promoted versions · Kargo (machine)
```

The `=` line is your answer: the effective value and the single layer that set
it. To edit it, change the file owned by that layer. Focused mode always
includes the chart-defaults layer (unlike the full cascade), so a value set only
by the chart author still resolves.

> **Note:** A key with a literal dot in a segment is bracket-quoted so the path
> is unambiguous, for example `datasources["datasources.yaml"].apiVersion`. Pass
> the key exactly as `whyx` prints it.

## Choose a different view

The diff view is the default. Switch formats when you need to scan many changes
or feed the result to another tool.

| You want to                         | Use                  |
| ----------------------------------- | -------------------- |
| Scan every change in a flat grid    | `--format table`     |
| Pipe structured data to a tool      | `--format json`      |
| See only the final merged values    | `--effective`        |
| List the resolved files, not values | `--layers`           |

For example, confirm exactly which files fed the merge:

```console
$ whyx project/dev/apps backend --layers
1  chart defaults    chart author     /repo/charts/apps/backend/values.yaml
2  platform-wide     platform team    /repo/envs/_platform/values.yaml
5  cluster           platform team    /repo/envs/project/dev/apps/values.yaml
7  promoted versions Kargo (machine)  /repo/envs/project/dev/apps/versions.generated.yaml
```

`--format json` emits one object per layer with an `index`, `name`, `owner`, and
a `changes` array (each change has `op`, `path`, `dotted`, `old`, `new`). Color
applies to the diff view only and turns off automatically when stdout is not a
terminal; force it off with `--no-color` or `NO_COLOR=1`.

## Run against a repo you are not standing in

By default `whyx` walks up from the working directory to find the repo root (the
nearest directory holding both `charts/` and `envs/`). Point it elsewhere with
`--repo`:

```console
$ whyx project/dev/apps backend image.tag --repo ~/src/helm-charts
```

## Troubleshooting

| Message                                                     | Cause and fix                                                                                                  |
| ----------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------- |
| `invalid target: want tenant/env/cluster`                   | The target is not exactly three non-empty segments. Pass `tenant/env/cluster`, for example `project/dev/apps`. |
| `chart not found under charts/{base,apps,vendor}`           | The chart name does not match a directory under one of the three category folders. Check the spelling.         |
| `no value files found for target and chart`                 | The chart resolves, but no layer file exists for this target. Verify the `envs/` paths for the target exist.   |
| `helm-charts repo root not found (need charts/ and envs/)`  | You are outside the repo and gave no `--repo`. Run from inside the checkout or pass `--repo`.                  |
| `unknown format`                                            | `--format` must be `diff`, `table`, or `json`.                                                                 |

Add `-v` for diagnostics on stderr when a result is not what you expect. All
operating errors exit non-zero, so you can branch on them in scripts.
