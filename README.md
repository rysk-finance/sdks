# ryskV12

Monorepo for the Rysk V12 client stack: the Go CLI and the SDKs that wrap it.

| Package | Path | Published as | Release tag |
| --- | --- | --- | --- |
| CLI | [`packages/cli`](packages/cli) | GitHub release binaries | `cli-vX.Y.Z` |
| TypeScript SDK | [`packages/ts`](packages/ts) | npm [`ryskv12`](https://www.npmjs.com/package/ryskv12) | `ts-vX.Y.Z` |
| Python SDK | [`packages/py`](packages/py) | PyPI [`ryskV12`](https://pypi.org/project/ryskV12/) | `py-vX.Y.Z` |

The SDKs do not reimplement the protocol — they spawn the CLI binary and talk
to it over JSON-RPC on stdio. Published SDK packages download the CLI from this
repo's releases (`scripts/fetch_latest_release.sh`, picking the newest `cli-v*`
release). Point them elsewhere with `RYSK_CLI_REPO` / `RYSK_CLI_TAG_PREFIX`.

## Local development

```sh
make dev-bin     # build the CLI from source into packages/ts and packages/py
make build       # build all three packages
make test-py     # python test suite
make help        # everything else
```

`make dev-bin` puts a locally built binary at `packages/ts/ryskV12` and
`packages/py/ryskV12cli` — the paths the TS and python examples already pass to
the SDK — so examples and e2e runs use your working tree, not a published release.

## Releasing

Versions are independent per package. CI is path-filtered: touching
`packages/ts/**` only runs the TS pipeline, and so on.

- **CLI** — run the *Release CLI* workflow manually with a version (e.g. `3.0.4`).
  It cross-compiles linux/amd64, linux/arm64 and darwin/arm64, then publishes a
  `cli-v3.0.4` release with the binaries attached.
- **TS / Python** — bump the version in `packages/ts/package.json` or
  `packages/py/pyproject.toml` in the PR. On merge to `main` the matching
  workflow tags, releases, and publishes to npm / PyPI. A release is skipped if
  its tag already exists, so unrelated merges are no-ops.

`version-check.yaml` fails a PR that changes an SDK without bumping its version.

## Secrets

`NPM_TOKEN` (repo) and `PYPI_TOKEN` (`pypi` environment).
