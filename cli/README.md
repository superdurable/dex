# Dex CLI

`dexcli` starts a complete local Dex development environment with one command.
It also gives humans and AI agents a JSON-first interface to every public Dex
FlowService API without requiring a browser.

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

If a default port is already in use, `dexcli dev` binds the next free port and
prints the addresses it selected. Running several `dexcli dev` processes on the
same machine therefore starts isolated stacks: distinct Dex ports, a distinct
local Temporal server, and a distinct SQLite database. Point later CLI commands
at a non-default stack with `--server` or `DEX_FLOW_SERVICE_ADDRESS`.

Use `--open` to open Dex Web after every component becomes ready. Ctrl+C stops
Dex Web, Dex Server, and all internal processes owned by `dexcli`.

Local Temporal state persists in
`$HOME/.dex/dev/<temporal-port>/temporal.db`. Dex Web step inputs and values
larger than 1 KB persist next to that database in `temporal.db.dex-blobs`, so
execution history and its blobs share a lifecycle. Override the database path
with:

```bash
dexcli dev --temporal-db-filename ./temporal.db
```

Choose another blob directory with:

```bash
dexcli dev --blob-store-dir ./dex-blobs
```

`--blob-store-dir` takes precedence over the directory derived from
`--temporal-db-filename`.

Configure local Attribute Store projection with the same YAML section accepted
by Dex Server:

```bash
dexcli dev --attribute-store-config ./attribute-store.yaml
```

```yaml
attributeStore:
  stores:
    entityStore:
      type: postgres
      dsn: postgres://user:password@localhost:5432/database
      tableName: public.user_profiles
```

Only `attributeStore` is read from this file. `dexcli` continues to own its local
API, Temporal, Web, and blob-store settings. The destination database and table
must exist and be reachable before startup.

## Application-facing flags

```text
--bind-address string          local bind IP (default 127.0.0.1)
--dex-port int                 Dex gRPC port (default 8801)
--web-port int                 Dex Web port (default 8802)
--temporal-db-filename string  local Temporal SQLite file (default $HOME/.dex/dev/<temporal-port>/temporal.db)
--blob-store-dir string        persistent Dex blob storage directory (default <temporal-db-filename>.dex-blobs)
--attribute-store-config string  Dex YAML file supplying Attribute Store settings
--open                         open Dex Web after readiness
```

Operators hosting Dex can use the retained backend compatibility flags for
external mode, namespaces, and internal ports. Dex does not print those
endpoints, and application developers do not need them.

## Operate flows

Commands connect to `127.0.0.1:8801` by default. Override the target with
`--server host:port` or `DEX_FLOW_SERVICE_ADDRESS`; the flag takes precedence.

```bash
dexcli health
dexcli flow search --query 'FlowStatus = "Running"'
dexcli flow inspect order-123 --all-history
dexcli flow watch order-123 --follow-runs
```

JSON is the default and stable machine-readable output. Use `--output table`
for terminal-oriented output. Large strings and objects are loaded from Dex's
blob store by default. To inspect only their references and avoid the additional
request, pass `--no-hydrate`:

```bash
dexcli flow history order-123 --no-hydrate
```

RPC input and output use the same Blob Store hydration as other history values.
Pass `--no-hydrate` to retain their references.

The friendly Flow commands are:

```text
dexcli flow search [--query QUERY] [--page-size N] [--page-token TOKEN] [--all]
dexcli flow summary FLOW_ID [--run-id RUN_ID]
dexcli flow state FLOW_ID [--run-id RUN_ID]
dexcli flow history FLOW_ID [--run-id RUN_ID] [--start-event-id N]
                    [--page-size N] [--page-token BASE64] [--all]
dexcli flow inspect FLOW_ID [--run-id RUN_ID] [--all-history]
dexcli flow watch FLOW_ID [--run-id RUN_ID] [--from-event-id N] [--follow-runs]
dexcli flow stop FLOW_ID --run-id RUN_ID
                 --type cancel|terminate|fail [--reason TEXT] --yes
dexcli flow time-travel FLOW_ID --run-id RUN_ID
                        --type beginning|history-event-time|step-type|step-execution-id
                        [--target VALUE] [--step-method wait-for|execute]
                        --reason TEXT --yes
```

With the default JSON output, `watch` writes one object per line and exits when
the run becomes terminal.
`--follow-runs` continues into the current run after Continue-As-New. Stop and
time travel require both an exact run ID and `--yes`, including in non-interactive use.
`--step-method` is required with `--type step-execution-id` and invalid for other types.

## Call any FlowService API

The installed binary contains the protobuf descriptor for its FlowService
version. Agents can discover and invoke every public RPC without server-side
gRPC reflection:

```bash
dexcli api list
dexcli api describe GetAttributes
dexcli api call GetAttributes --data '{"flowId":"order-123","allKeys":true}'
dexcli api call SetAttributes --data @request.json --yes
printf '%s' '{"flowId":"order-123"}' | dexcli api call GetFlowSummary --data -
```

`api call` accepts canonical protobuf JSON: lower-camel-case field names, enum
names, and base64-encoded bytes. Unlike friendly Flow commands, it deliberately
does not hydrate or reshape values. Mutating RPCs require `--yes`.

Successful commands write only their result to stdout. Failures write a JSON
object to stderr with the operation, error kind, gRPC code/name, and status
details when supplied. Usage and missing-confirmation failures exit with status
2; connection and RPC failures exit with status 1.

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
- “address is already in use”: an explicit `--dex-port`, `--web-port`,
  `--temporal-port`, or `--temporal-ui-port` is taken. Stop that process or pass
  another port. Unspecified ports are assigned automatically.
- “attribute index synchronization failed”: operators should verify that Dex
  can list and add backend visibility indexes. Workers never require a manual
  registration command.
## License

[Super Durable Source License 1.0](LICENSE), with legacy portions under their
original terms as described in [LEGACY_NOTICES.md](LEGACY_NOTICES.md).
