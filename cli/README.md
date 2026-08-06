# Dex CLI

`dexcli` starts a complete local Dex development environment with one command.

## Install

```bash
brew install superdurable/tap/dexcli
```

The Homebrew formula installs Temporal CLI as a runtime dependency. Node.js is
not required at runtime.

## Start locally

```bash
dexcli dev
```

The default endpoints are:

| Service | Address |
|---|---|
| Dex Web | `http://127.0.0.1:8802` |
| Dex Server | `127.0.0.1:8801` |
| Temporal Web | `http://127.0.0.1:8233` |
| Temporal | `127.0.0.1:7233` |

Use `--open` to open Dex Web after every component becomes ready. Ctrl+C stops
Dex Web, Dex Server, and the Temporal process owned by `dexcli`.

Persist local Temporal executions in SQLite:

```bash
dexcli dev --temporal-db-filename ./temporal.db
```

Dex Web step inputs and values larger than 1 KB use the bundled local blob
store. By default they persist across `dexcli` restarts in `$HOME/.dex/blobs`.
With the command above they instead persist in `./temporal.db.dex-blobs`, so
Temporal history and its blobs share a lifecycle. Choose another directory
with:

```bash
dexcli dev --blob-store-dir ./dex-blobs
```

`--blob-store-dir` takes precedence over the directory derived from
`--temporal-db-filename`.

## Existing Temporal server

```bash
dexcli dev --temporal-address localhost:7233
```

External mode does not start or stop Temporal. The target namespace must
already contain `FlowType` (`Keyword`) and `ActiveStepTypes` (`KeywordList`).

Select another namespace with `--temporal-namespace`. The first release uses
plaintext endpoints; Temporal Cloud authentication is not supported yet.

## ParadeDB flow indexing

Start the development server with `--flow-index-backend paradedb` and
`--paradedb-dsn`. Schema, table, and connection pool flags are also available.
Apply an additive schema only through Dex Server:

```bash
dexcli index apply --file flow-index.yaml --address localhost:8801
```

See the [ParadeDB Attribute Index guide](../docs/ParadeDB-Attribute-Index.md)
for schema format and terminal consistency semantics.

## Flags

```text
--bind-address string          local bind IP (default 127.0.0.1)
--dex-port int                 Dex gRPC port (default 8801)
--web-port int                 Dex Web port (default 8802)
--temporal-address string      external Temporal host:port
--temporal-namespace string    Temporal namespace (default default)
--temporal-port int            local Temporal port (default 7233)
--temporal-ui-port int         local Temporal Web port (default 8233)
--temporal-db-filename string  local Temporal SQLite file
--blob-store-dir string        persistent Dex blob storage directory (default $HOME/.dex/blobs)
--open                         open Dex Web after readiness
--flow-index-backend string    visibility or paradedb (default visibility)
--paradedb-dsn string          ParadeDB PostgreSQL connection string
--paradedb-schema string       ParadeDB schema (default dex)
--paradedb-table string        latest-state table (default flow_index)
--paradedb-max-connections int connection pool size (default 10)
```

Local-only Temporal flags cannot be combined with `--temporal-address`.

## Build and test

Requirements are Go 1.24+, Node.js 22+, and Temporal CLI.

```bash
make -C cli build
make -C cli test
make -C cli integration-test
```

The Web build is embedded into the resulting `cli/dexcli` binary. Running that
binary does not read `web/`, `node_modules`, or the source tree.

## Troubleshooting

- “Temporal CLI was not found”: reinstall `dexcli` with Homebrew or install the
  `temporal` formula.
- “address is already in use”: stop the listed service or select another port.
- “missing search attribute”: register `FlowType` as `Keyword` and
  `ActiveStepTypes` as `KeywordList` in the selected external namespace.
## License

[Super Durable Source License 1.0](LICENSE), with legacy portions under their
original terms as described in [LEGACY_NOTICES.md](LEGACY_NOTICES.md).
