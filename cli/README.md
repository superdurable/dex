# Dex CLI

`dexcli` starts a complete local Dex development environment with one command.

## Install

```bash
brew install superdurable/tap/dexcli
```

The Homebrew formula installs all runtime dependencies. Node.js is not required
at runtime.

## Start locally

```bash
dexcli dev
```

The default endpoints are:

| Service | Address |
|---|---|
| Dex Web | `http://127.0.0.1:8802` |
| Dex Server | `127.0.0.1:8801` |

Use `--open` to open Dex Web after every component becomes ready. Ctrl+C stops
Dex Web, Dex Server, and all internal processes owned by `dexcli`.

Persist local Dex executions in SQLite:

```bash
dexcli dev --temporal-db-filename ./temporal.db
```

Dex Web step inputs and values larger than 1 KB use the bundled local blob
store. By default they persist across `dexcli` restarts in `$HOME/.dex/blobs`.
With the command above they instead persist in `./temporal.db.dex-blobs`, so
execution history and its blobs share a lifecycle. Choose another directory
with:

```bash
dexcli dev --blob-store-dir ./dex-blobs
```

`--blob-store-dir` takes precedence over the directory derived from
`--temporal-db-filename`.

## Application-facing flags

```text
--bind-address string          local bind IP (default 127.0.0.1)
--dex-port int                 Dex gRPC port (default 8801)
--web-port int                 Dex Web port (default 8802)
--temporal-db-filename string  local Temporal SQLite file
--blob-store-dir string        persistent Dex blob storage directory (default $HOME/.dex/blobs)
--open                         open Dex Web after readiness
```

Operators hosting Dex can use the retained backend compatibility flags for
external mode, namespaces, and internal ports. Dex does not print those
endpoints, and application developers do not need them.

## Build and test

Requirements are Go 1.24+, Node.js 22+, and the workflow-backend CLI used by
the local supervisor.

```bash
make -C cli build
make -C cli test
make -C cli integration-test
```

The Web build is embedded into the resulting `cli/dexcli` binary. Running that
binary does not read `web/`, `node_modules`, or the source tree.

## Troubleshooting

- “workflow backend CLI was not found”: reinstall `dexcli` with Homebrew.
- “address is already in use”: stop the listed service or select another port.
- “attribute index synchronization failed”: operators should verify that Dex
  can list and add backend visibility indexes. Workers never require a manual
  registration command.
## License

[Super Durable Source License 1.0](LICENSE), with legacy portions under their
original terms as described in [LEGACY_NOTICES.md](LEGACY_NOTICES.md).
