# whyx

`whyx` explains **why a rendered Helm value is what it is**. Given a deployment
target and a chart, it replays the layered value-file merge one layer at a time
and shows each layer as a git-style diff -- so the origin of any value reads
like a commit history.

It is read-only and needs no cluster access: it operates on a `helm-charts`
repository checkout, using the same value-file precedence that Argo CD applies
when it renders.

> Status: functional. Layer resolution, Helm-faithful merge, per-layer diff,
> and rendering (diff/table/json) all work; `--layers`, `--effective`, the full
> cascade, and focused-key tracing are wired end to end.

## The layer model

For a target `tenant/env/cluster` and a chart, values merge lowest-to-highest
(later wins; maps deep-merge, lists replace):

| # | Layer             | Source                                            | Owner            |
|---|-------------------|---------------------------------------------------|------------------|
| 1 | Chart defaults    | `charts/<category>/<chart>/values.yaml`           | chart author     |
| 2 | Platform-wide     | `envs/_platform/values.yaml`                      | platform team    |
| 3 | Tenant-wide       | `envs/<tenant>/values.yaml`                       | platform team    |
| 4 | Environment-wide  | `envs/<tenant>/<env>/values.yaml`                 | platform team    |
| 5 | Cluster (target)  | `envs/<tenant>/<env>/<cluster>/values.yaml`       | platform team    |
| 6 | Infra contract    | per-chart projection of the infra contract -- the resolved helmParameters, not the whole file (`.../<cluster>/enabled/<chart>.yaml`) | Pulumi (machine) |
| 7 | Promoted versions | `.../<cluster>/versions.generated.yaml`           | Kargo (machine)  |

Missing layers are skipped -- the delta layers are often absent.

## Usage

```text
whyx <target> <chart> [key] [flags]

  <target>      tenant/env/cluster     e.g. project/dev/apps
  <chart>       chart name             e.g. backend
  [key]         dotted path to trace   e.g. image.tag  (omit = full cascade)

  --repo        path to the helm-charts repo (default: auto-detect)
  --effective   print only the final merged values
  --layers      list the resolved layer files in order
  --format      diff | table | json   (default: diff)
  --no-color    disable colored output (also honors NO_COLOR, non-TTY)
  -v, --verbose verbose diagnostics on stderr
```

Full cascade -- which layer set each value:

```console
$ whyx project/dev/apps backend
@@ layer 1 · chart defaults · chart author @@
  + image.tag: dev
  + replicas: 1
@@ layer 5 · cluster · platform team @@
  ~ replicas: 1 -> 2
@@ layer 7 · promoted versions · Kargo (machine) @@
  ~ image.tag: dev -> prod
```

Focused on one key -- the lineage of a single value, ending in the layer that
won:

```console
$ whyx project/dev/apps backend image.tag
@@ layer 1 · chart defaults · chart author @@
  + image.tag: dev
@@ layer 7 · promoted versions · Kargo (machine) @@
  ~ image.tag: dev -> prod
= image.tag: prod
  set by layer 7 · promoted versions · Kargo (machine)
```

Other views: `--format table` and `--format json` for the cascade,
`--effective` for the final merged values, and `--layers` to list the resolved
files. Dotted keys with literal dots are bracket-quoted in output, e.g.
`datasources["datasources.yaml"].apiVersion`.

## Guides

- [Trace why a Helm value is set](docs/trace-a-helm-value.md) -- find which
  layer set a value, for the full cascade or a single key.
- [Lay out value-file layers for whyx](docs/lay-out-value-file-layers.md) --
  structure a `helm-charts` repo's `envs/` tree so layers resolve as intended.

## Install

```sh
# latest tagged release (recommended once v0.1.0 is published)
go install github.com/jalet/whyx/cmd/whyx@latest

# or pin a specific version
go install github.com/jalet/whyx/cmd/whyx@v0.1.0
```

## Development

Tooling is managed with [mise](https://mise.jdx.dev):

```sh
mise install        # Go + golangci-lint, pinned in mise.toml
mise run build
mise run test       # go test -race -shuffle=on ./...
mise run lint
mise run fmt        # or fmt:check
mise run vuln
mise run ci         # the full gate (also what CI runs)
```

## Layout

```text
cmd/whyx/           entry point (thin; wires the CLI)
internal/cli/       cobra command wiring
internal/whyx/      pipeline orchestration + Config
internal/layers/    resolve the ordered layer files for (target, chart)
internal/merge/     Helm-faithful incremental coalescing
internal/diff/      per-step structured value diff
internal/render/    diff | table | json output
```

## Releases

Releases are automated with [release-please](https://github.com/googleapis/release-please).
Pushes to `main` maintain a release PR; merging it tags the version, publishes a
GitHub Release, and updates [CHANGELOG.md](CHANGELOG.md).

Versioning is driven by [Conventional Commits](https://www.conventionalcommits.org):

- `fix:` -- patch bump (0.1.0 -> 0.1.1)
- `feat:` -- minor bump (0.1.0 -> 0.2.0)
- `feat!:` or a `BREAKING CHANGE:` footer -- major bump (0.1.0 -> 1.0.0)

## License

MIT (placeholder -- confirm before publishing). See [LICENSE](LICENSE).
