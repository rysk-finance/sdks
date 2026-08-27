# ryskV12

Monorepo for the Rysk V12 client stack: the Go CLI and the SDKs that wrap it.

| Package | Path | Published as | Release tag |
| --- | --- | --- | --- |
| CLI | [`packages/cli`](packages/cli) | GitHub release binaries | `cli-vX.Y.Z` |
| TypeScript SDK | [`packages/ts`](packages/ts) | npm [`ryskv12`](https://www.npmjs.com/package/ryskv12) | `ts-vX.Y.Z` |
| Python SDK | [`packages/py`](packages/py) | PyPI [`ryskV12`](https://pypi.org/project/ryskV12/) | `py-vX.Y.Z` |
| Rust SDK | [`packages/rs`](packages/rs) | crates.io `ryskv12` | `rs-vX.Y.Z` |

The SDKs do not reimplement the protocol — they spawn the CLI binary and talk
to it over JSON-RPC on stdio. Published SDK packages download the CLI from this
repo's releases (`scripts/fetch_latest_release.sh`, picking the newest `cli-v*`
release). Point them elsewhere with `RYSK_CLI_REPO` / `RYSK_CLI_TAG_PREFIX`.

Each SDK declares the oldest CLI it works with, and warns (or fails, under
`strict_version`) when the binary it finds is older. Release the CLI before the
SDK that requires it.

## Local development

```sh
make dev-bin     # build the CLI from source into every SDK package
make build       # build all four packages
make test-py     # python test suite
make test-rs     # rust test suite
make help        # everything else
```

`make dev-bin` puts a locally built binary at `packages/ts/ryskV12`,
`packages/py/ryskV12cli` and `packages/rs/ryskV12` — the paths each SDK's
examples and integration tests already look in — so they exercise your working
tree, not a published release. Every SDK's integration tests skip themselves
when that binary is absent.

## Releasing

Versions are independent per package, and every release is manual: nothing
publishes on a merge.

1. **Bump** the version in the package's manifest, in a PR. `version-check.yaml`
   fails a PR that changes an SDK without bumping it.
2. **Release the CLI first** if the SDK needs a newer one — run *Release CLI*
   with a version (e.g. `4.0.0`). It cross-compiles linux and darwin on both
   architectures and publishes a `cli-v4.0.0` release with the binaries attached.
3. **Run the SDK's release workflow.** It builds from `main`, refuses to
   republish a version that is already tagged, checks that a CLI release exists
   satisfying that SDK's minimum, then tags, releases and publishes.

## Publishing credentials

npm and PyPI publish over OIDC trusted publishing — there is no long lived token
in the repo. Each registry's trusted publisher must name this repository and the
workflow file that publishes it:

| Registry | Workflow | Environment |
| --- | --- | --- |
| PyPI | `py-release.yaml` | `pypi` |
| npm | `ts-release.yaml` | none |

The environment is part of the OIDC claim, so a publisher configured with the
wrong one — or with none where the job declares one — is rejected.
