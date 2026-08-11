# Contributing

## Repository layout

| Path | Contents |
|------|----------|
| [server/](server/) | Dex server (Temporal/Cadence backend) |
| [protos/](protos/) | Protobuf IDL ([`dex.proto`](protos/dex.proto); renames in [`docs/design/idl-renames.md`](docs/design/idl-renames.md)) |
| [docs/](docs/) | Product docs site ([docs.superdurable.io](https://docs.superdurable.io)); design notes in [`design/`](docs/design/); start at [README.md](docs/README.md) |
| [cli/](cli/) | `dexcli` local development environment |
| [web/](web/) | Dex Web console |
| [sdk-go/](sdk-go/) | Go SDK |
| [blob-cache-go/](blob-cache-go/) | Shared Go disk blob cache |
| [examples/go/](examples/go/) | Go examples |
| [sdk-java/](sdk-java/) | Java SDK |
| [examples/java/](examples/java/) | Java examples |
| [sdk-python/](sdk-python/) | Python SDK |
| [examples/python/](examples/python/) | Python examples |
| [sdk-rust/](sdk-rust/) | Shared Rust SDK Core |

Go SDK + samples use root [`go.work`](go.work). `blob-cache-go` remains outside the workspace until its first release. Build the server separately (`cd server && go build ./...`) to avoid a Cadence/Temporal `genproto` workspace conflict.

## Prerequisites

- Go 1.24+ (see `server/go.mod`; root `go.work` pins the workspace)
- Java 8+ and Gradle wrapper (for `sdk-java` / `examples/java`)
- Python 3.9+ and uv (for `sdk-python` / `examples/python`)
- Docker (for integration tests / local Temporal+Cadence stacks)

## Go workspace

Root [`go.work`](go.work) includes `sdk-go` and `examples/go` (local `replace` for the SDK).

```bash
go work sync
go build ./sdk-go/...
go build ./examples/go/...
```

### Server

Build outside the workspace (or with `GOWORK=off`) so Cadence’s older `genproto` does not clash with Temporal’s split modules:

```bash
cd server && go build ./...
make -C server unitTests
# Integration tests need Cadence/Temporal; see server/CONTRIBUTING.md
```

### Go SDK

```bash
make -C sdk-go ci-tests   # may start docker compose under sdk-go/integ
```

### Blob Cache Go

```bash
make -C blob-cache-go tests
```

### Dex CLI and Web

```bash
make -C cli build
make -C cli test
make -C cli integration-test
```

The CLI integration suite requires Temporal CLI. Web contributors can run
`cli/dexcli dev --web-port 8902` and `npm run dev` in `web/` for hot reload.

## IDL (`protos/`)

Protobuf source lives in [`protos/dex.proto`](protos/dex.proto). Rename catalog: [`docs/design/idl-renames.md`](docs/design/idl-renames.md).

Server-only regen (leave SDK trees alone during the server rewrite):

```bash
make -C protos proto-server-go
# or: make -C server idl-code-gen-server
```

Full regen (server + all SDKs):

```bash
make -C protos proto
# or: make -C server idl-code-gen
```

Interpreter Temporal/Cadence history encoding uses binary protobuf DataConverters
in `server/service/common/converter` (see `server/CONTRIBUTING.md`).

## Java

```bash
cd sdk-java && ./gradlew build
cd ../examples/java && ./gradlew build
```

## Python

```bash
cd sdk-python && uv sync --locked && uv run --frozen pytest
cd ../examples/python && uv sync --locked
```

## License headers

Managed source files use the classifications recorded in
[`script/licenseheaders/legacy-manifest.json`](script/licenseheaders/legacy-manifest.json):

- `legacy-only` preserves the original license while its normalized body still
  matches the cutoff snapshot.
- `mixed` preserves the original header and adds the Super Durable modification
  notice.
- `new` uses `LicenseRef-Super-Durable-1.0`.

`docs/` is not relicensed. Go examples retain MIT; Java and Python examples
retain Apache-2.0. See [LICENSING.md](LICENSING.md) for the repository policy.

From the repo root:

```bash
make copyright         # add missing headers
make copyright-check   # verify classifications, body hashes, and headers
```

Skip generated trees (`**/gen/**`, `*.pb.go`, `*_pb.go`, `*.gen.*`). Prefer
`make copyright` over hand-copying when adding files. It upgrades a modified
`legacy-only` file to `mixed` without replacing its legacy notice. CI runs
`make copyright-check` via [`.github/workflows/copyright-ci.yml`](.github/workflows/copyright-ci.yml).

Files absent from the cutoff manifest use the new header. File-level renames
and copies with at least 20% similarity retain source lineage; non-new copies
use the mixed header. Preserve applicable notices when copying smaller excerpts.

## Contributor License Agreement

All contributors must sign an [individual or corporate CLA](CLA.md). Email the
signed agreement and GitHub handle to licensing@superdurable.io. Pull
requests from handles absent from `.github/cla-signatures.json` fail the CLA
check until the record is updated.

## CI

Root workflows under [`.github/workflows/`](.github/workflows/) run path-filtered jobs for server and each SDK/samples tree, plus the copyright check. Prefer fixing those over re-adding nested `*/.github/workflows` duplicates.

## Releases (monorepo tags)

Each component has its own version and tag prefix. Create a GitHub Release for that tag only — workflows filter on the prefix so one release does not publish another component.

| Component | Tag format | Example | What it publishes |
|-----------|------------|---------|-------------------|
| Server / Docker | `server-vX.Y.Z` | `server-v1.0.0` | Docker Hub `dex-server:v1.0.0` and `dex-server-lite:v1.0.0` |
| Python SDK | `sdk-python/vX.Y.Z` | `sdk-python/v0.1.0` | PyPI [`dex-python-sdk`](https://pypi.org/project/dex-python-sdk/) via [`.github/workflows/sdk-python-publish.yml`](.github/workflows/sdk-python-publish.yml) (version from the tag) |
| Java SDK | `sdk-java/vX.Y.Z` | `sdk-java/v0.0.3` | Maven Central `io.superdurable:dex-sdk` via [`.github/workflows/sdk-java-publish.yml`](.github/workflows/sdk-java-publish.yml) (version from the tag) |
| Go SDK | `sdk-go/vX.Y.Z` | `sdk-go/v1.2.3` | Go module tag for `github.com/superdurable/dex/sdk-go` |
| Blob Cache Go | `blob-cache-go/vX.Y.Z` | `blob-cache-go/v0.1.0` | Go module tag for `github.com/superdurable/dex/blob-cache-go` |
| TypeScript SDK | `sdk-typescript/vX.Y.Z` | `sdk-typescript/v0.1.0` | npm [`@superdurable/dex`](https://www.npmjs.com/package/@superdurable/dex) via [`.github/workflows/sdk-typescript-publish.yml`](.github/workflows/sdk-typescript-publish.yml) (version from the tag) |
| Dex CLI | `cli-vX.Y.Z` | `cli-v0.1.0` | macOS/Linux archives and Homebrew formula input |

Notes:

- Java, TypeScript, and Python derive publish versions from tags (CI stamps the package metadata for the build). Bump committed version files when you want the repo tip to match.
- Go modules use path-style tags (`sdk-go/v…`, `blob-cache-go/v…`) so `go get` resolves each subdirectory module.
- Python, Java, and Docker release workflows also support **workflow_dispatch** for manual runs. Python manual runs build without publishing unless `publish` is selected.
- TypeScript publishing requires a GitHub Release after its one-time npm bootstrap publish.

### Committed `version` vs release tags (Python / TypeScript / Java)

For the Python, TypeScript, and Java SDKs, the **GitHub Release tag** is the source of
truth for what gets published (`sdk-python/vX.Y.Z` → PyPI, `sdk-typescript/vX.Y.Z` → npm,
`sdk-java/vX.Y.Z` → Maven Central). CI applies that version for the release build (Python
and TypeScript stamp package metadata on the runner; Java passes `-PreleaseVersion`) and
does **not** commit the change.

The committed fields still matter; they are not unused:

| Field | Role |
|-------|------|
| [`sdk-python/pyproject.toml`](sdk-python/pyproject.toml) `project.version` | Required package metadata. Used for local / editable installs (`pip` / `uv`), metadata queries, and aligning `uv.lock`. Also used when [`.github/workflows/sdk-python-publish.yml`](.github/workflows/sdk-python-publish.yml) runs on a **pull_request** (no tag): that dry-run builds wheels/sdists using the committed version. |
| [`sdk-typescript/package.json`](sdk-typescript/package.json) `version` | Required package metadata. Used for local installs, `npm pack`, and keeping `package-lock.json` in sync. The TypeScript publish workflow only runs on release tags, so the committed value is not what npm receives unless CI has just rewritten it for that job. |
| [`sdk-java/build.gradle`](sdk-java/build.gradle) default `releaseVersion` | Fallback when `-PreleaseVersion` is omitted (local / SNAPSHOT builds). Maven Central publish always supplies the version from the tag (or workflow_dispatch). |

What the committed value is **not**:

- Not the source of the next PyPI / npm / Maven Central release (the tag /
  workflow_dispatch version is).
- Not automatically updated in git when you publish a tag.
- Not what examples pin when they depend on a registry version.

Practical workflow:

1. Publish with a tag (for example `sdk-python/v0.1.0`) — no need to bump the committed
   file first.
2. Follow up with a PR so tip versions match what you shipped:
   - Python: `pyproject.toml` (+ `uv.lock`) and docs install pins
   - TypeScript: `package.json` (+ `package-lock.json`)
   - Java: default `…-SNAPSHOT` in `build.gradle` / smoke-test coords, plus README pins

`package.json` cannot carry comments; the TypeScript SDK README Releases section
documents the same tag-driven rule. `pyproject.toml` and `build.gradle` may note that
publish takes the version from the tag.

## Package-specific guides

- [server/CONTRIBUTING.md](server/CONTRIBUTING.md)
- [sdk-go/CONTRIBUTION.md](sdk-go/CONTRIBUTION.md)
- [sdk-java/README.md](sdk-java/README.md)
- [sdk-python/README.md](sdk-python/README.md)
- [sdk-typescript/README.md](sdk-typescript/README.md)
