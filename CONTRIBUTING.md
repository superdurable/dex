# Contributing

## Prerequisites

- Go 1.24+ (see `server/go.mod`; root `go.work` pins the workspace)
- Java 8+ and Gradle wrapper (for `sdk-java` / `examples/java`)
- Python 3.9+ and Poetry (for `sdk-python` / `examples/python`)
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

## IDL (`protos/`)

Protobuf source lives in [`protos/iwf.proto`](protos/iwf.proto). Rename catalog: [`docs/design/idl-renames.md`](docs/design/idl-renames.md).

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
cd sdk-python && poetry install && poetry run pytest   # if tests are configured
cd ../examples/python && poetry install
```

## License headers

Source files use per-directory license headers (MIT under `server/` and
`examples/go/`; Apache-2.0 under the SDKs and Java/Python samples; dual MIT +
Apache-2.0 under `protos/`). Templates and the directory mapping live in
[`script/licenseheaders/`](script/licenseheaders/).

From the repo root:

```bash
make copyright         # add missing headers
make copyright-check   # verify headers are present
make copyright-replace # rewrite to Super Durable per-directory templates (destructive)
```

Skip generated trees (`**/gen/**`, `*.pb.go`, `*_pb.go`, `*.gen.*`). Prefer
`make copyright` over hand-copying when adding files. CI runs
`make copyright-check` via [`.github/workflows/copyright-ci.yml`](.github/workflows/copyright-ci.yml).

## CI

Root workflows under [`.github/workflows/`](.github/workflows/) run path-filtered jobs for server and each SDK/samples tree, plus the copyright check. Prefer fixing those over re-adding nested `*/.github/workflows` duplicates.

## Releases (monorepo tags)

Each component has its own version and tag prefix. Create a GitHub Release for that tag only — workflows filter on the prefix so one release does not publish another component.

| Component | Tag format | Example | What it publishes |
|-----------|------------|---------|-------------------|
| Server / Docker | `server-vX.Y.Z` | `server-v1.0.0` | Docker Hub `iwf-server:v1.0.0` and `iwf-server-lite:v1.0.0` |
| Python SDK | `sdk-python-vX.Y.Z` | `sdk-python-v0.12.0` | PyPI [`iwf-sdk`](https://pypi.org/project/iwf-sdk/) (version from `sdk-python/pyproject.toml`) |
| Java SDK | `sdk-java-vX.Y.Z` | `sdk-java-v0.0.2` | Maven Central `io.superdurable:iwf-sdk` via [`.github/workflows/sdk-java-publish.yml`](.github/workflows/sdk-java-publish.yml) (version from `sdk-java/build.gradle`) |
| Go SDK | `sdk-go/vX.Y.Z` | `sdk-go/v1.2.3` | Go module tag for `github.com/superdurable/iwf/sdk-go` |

Notes:

- Bump the component’s own version file before tagging (`pyproject.toml`, `build.gradle`, etc.).
- Go uses a path-style tag (`sdk-go/v…`) so `go get` resolves the subdirectory module.
- Python, Java, and Docker release workflows also support **workflow_dispatch** for manual runs.

## Package-specific guides

- [server/CONTRIBUTING.md](server/CONTRIBUTING.md)
- [sdk-go/CONTRIBUTION.md](sdk-go/CONTRIBUTION.md)
- [sdk-java/README.md](sdk-java/README.md)
- [sdk-python/README.md](sdk-python/README.md)
