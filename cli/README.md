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

When a released `dexcli` starts a local development environment, it checks the
published `cli-v*` GitHub Releases in the background. If a newer version is
available, the terminal shows a colored upgrade reminder:

```bash
brew update && brew upgrade dexcli
```

The check never delays startup. It is skipped for development builds and when
GitHub cannot be reached.

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

Dex Web opens after every component becomes ready. Pass `--open=false` to skip
that. Ctrl+C stops Dex Web, Dex Server, and all internal processes owned by
`dexcli`.

Local state persists in `$HOME/.dex/dev/<port>/dex.sqlite.db`.
Dex Web step inputs and values larger than 1 KB persist next to that database
in `dex.blobs`, so execution history and its blobs share a lifecycle. Override
the database path with:

```bash
dexcli dev --sqlite-db-filename ./dex.sqlite.db
```

Choose another blob directory with:

```bash
dexcli dev --blob-store-dir ./dex-blobs
```

`dexcli dev` enables the in-memory Stream Store without Redis. Stream messages
are discarded when the CLI process stops. Repeated source values append
independent messages.

`--blob-store-dir` takes precedence over the directory derived from
`--sqlite-db-filename`.

Server logs go to a temp folder that `dexcli` prints on readiness and deletes
when it exits. Keep them with:

```bash
dexcli dev --server-log-folder ./dex-logs
```

When investigating the local Temporal Server, add `--verbose-engine-log`. It
writes Temporal Server info logs to `temporal-engine-server.log` in that folder.
Use `--server-log-folder` if you need the file after `dexcli` exits.

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
--attribute-store-config string    Dex YAML file supplying Attribute Store settings
--bind-address string              local bind IP (default 127.0.0.1)
--blob-store-dir string            persistent Dex blob storage directory (default $HOME/.dex/blobs)
--dex-port int                     Dex gRPC port (default 8801)
--flow-rendering-dir string        directory containing Flow Definition Graph JSON files
--open                             open Dex Web after readiness (default true)
--web-port int                     Dex Web port (default 8802)
--sqlite-db-filename string        local SQLite file (default $HOME/.dex/dev/<port>/dex.sqlite.db)
--server-log-folder string         keep server logs (default temp folder, deleted on exit)
--verbose-engine-log               write Temporal Server logs to temporal-engine-server.log
--external-temporal-address string      external Temporal host:port
--external-temporal-namespace string    external Temporal namespace (default default)
```

Local Temporal gRPC and Web ports are assigned automatically. Operators can
point Dex at an existing Temporal with `--external-temporal-address` and
`--external-temporal-namespace`. Dex does not print those endpoints, and
application developers do not need them.

## Operate flows

Commands connect to `127.0.0.1:8801` by default. Override the target with
`--server host:port` or `DEX_FLOW_SERVICE_ADDRESS`; the flag takes precedence.

```bash
dexcli health
dexcli flow start order-123 --flow-type OrderFlow --start-step-type StartOrder --input '{"order":123}' --yes
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

## Visualize Flow source

Generate a static possible-path graph from one Go or Python Flow source file:

```bash
dexcli visualize ./order_flow.go
```

The default writes `order_flow.flow.json` next to the source. The JSON is the
versioned Flow Definition Graph consumed by Dex Web and other tools. Choose
another prefix or write the graph to stdout:

```bash
dexcli visualize ./order_flow.py --out -
dexcli visualize ./order_flow.go --out ./build/order-flow
dexcli dev --flow-rendering-dir ./build
```

Open **Flow Rendering** in Dex Web to browse the loaded definitions. The page
renders the graph interactively. A Flow frame contains Step frames, with WaitFor
paths above Execute decisions. Conditional returns are grouped below dispatch
diamonds. Channels, Attributes, RPCs, Streams, folded SubFlows, and diagnostics
can be shown or hidden independently. Streams are hidden by default. Definitions
are loaded once during startup; restart **dexcli dev** after changing the
directory.

Python analysis requires Python 3.11 or newer. Pass `--python /path/to/python`
to select an interpreter. The analyzer parses Python with the standard-library
AST and never imports or executes the application module.

Go analysis requires a local Go toolchain and a module/package that passes type
checking. Version 1 accepts one Flow per file. Step registration, transitions,
waits, RPC next Steps, execute-failure recovery targets, and persistence
resource access must be directly visible in that file. Wait conditions and
Execute decisions are structured node details rather than labels inferred from
edges. Channel edges run from publishers through the Channel to consuming
WaitFor paths. Attribute edges run from writers through the Attribute group to
readers. Terminal decisions and cancellation remain inside their Execute card.
The graph also records repeatable best-effort Step Stream writes. Python
synchronous Step generators and asynchronous Step handlers are both recognized.
Heartbeat checkpoints are runtime details and are omitted. Step Stream progress
from an RPC or Flow timeout handler produces a blocking diagnostic. Business
helpers may remain in other files, but they must not hide Dex control flow.
Dynamic targets produce an Unknown node and a blocking diagnostic. A partial
JSON artifact is still written, and the command exits with status 1.

```text
dexcli visualize SOURCE [--language auto|go|python]
                         [--out PATH_PREFIX|-]
                         [--python PYTHON_PATH]
```

Invalid command usage exits with status 2. The JSON contract is documented by
[`schema/flow-definition-graph.v1.schema.json`](schema/flow-definition-graph.v1.schema.json).

The friendly Flow commands are:

```text
dexcli flow start FLOW_ID --flow-type TYPE [--start-step-type TYPE]
                  [--input JSON|@FILE|-] [--attributes JSON|@FILE|-]
                  [--config JSON|@FILE|-] [--retry-policy JSON|@FILE|-]
                  [--step-options PROTOBUF_JSON|@FILE|-]
                  [--flow-timeout DURATION] [--flow-timeout-policy fail|cancel|handler]
                  [--id-reuse-policy previous-failed|not-running|disallow|terminate-running]
                  [--start-delay DURATION] [--ignore-already-started] [--request-id ID] --yes
dexcli flow wait FLOW_ID [--needs-results] [--wait-time DURATION]
dexcli flow search [--query QUERY] [--page-size N] [--page-token TOKEN] [--all]
dexcli flow summary FLOW_ID [--run-id RUN_ID]
dexcli flow state FLOW_ID [--run-id RUN_ID]
dexcli flow history FLOW_ID [--run-id RUN_ID] [--start-event-id N]
                    [--page-size N] [--page-token BASE64] [--all]
dexcli flow inspect FLOW_ID [--run-id RUN_ID] [--all-history]
dexcli flow watch FLOW_ID [--run-id RUN_ID] [--from-event-id N] [--follow-runs]
dexcli flow stop FLOW_ID [--run-id RUN_ID]
                 [--type cancel|terminate|fail] [--reason TEXT] --yes
dexcli flow skip-timer FLOW_ID --step-type TYPE [--execution N]
                    (--condition-id ID|--condition-index N) --yes
dexcli flow wait-step FLOW_ID --step-type TYPE [--execution N] [--wait-time DURATION]
dexcli flow update-config FLOW_ID --config JSON|@FILE|- --yes
dexcli flow trigger-continue-as-new FLOW_ID --yes
dexcli flow time-travel FLOW_ID [--run-id RUN_ID]
                        --type beginning|history-event-time|step-type|step-execution-id
                        [--target VALUE] [--step-method wait-for|execute]
                        [--reason TEXT] --yes
```

With the default JSON output, `watch` writes one object per line and exits when
the run becomes terminal.
`--follow-runs` continues into the current run after Continue-As-New. Stop and
time travel operate on the current run by default; pass `--run-id` to target an
exact run. All mutating Flow commands require `--yes`, including in
non-interactive use.
`--step-method` is required with `--type step-execution-id` and invalid for other types.

`start --input` and every `value` in `--attributes` use natural JSON. Strings,
numbers, booleans, nulls, objects, and arrays are converted to Dex Values using
the SDK encoding rules. For example:

```bash
dexcli flow start order-123 --flow-type OrderFlow --start-step-type StartOrder \
  --input '{"orderId":"123","items":["book"]}' \
  --attributes '[{"key":"status","value":"new","index":{"type":"keyword"},"sync":true}]' \
  --yes
```

`--config` accepts a JSON object with optional `activeStepSearchMode` (`all`,
`wait-for`, or `disabled`), Continue-As-New thresholds, `stepDurability`
(`sync` or `async`), `workerTarget`, and `attributeStoreNames`. `--retry-policy`
uses `initialInterval`, `backoffCoefficient`, `maximumInterval`, and
`maximumAttempts`; intervals are Go duration strings. `--step-options` accepts
protobuf JSON because it mirrors the full nested StepOptions message. When a
Flow timeout is set without `--flow-timeout-policy`, the server's `fail` default
applies because dexcli cannot inspect the application's Flow registry.
Only one option in a `start` command may use `-`, because stdin can be consumed
once.

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
- “address is already in use”: an explicit `--dex-port` or `--web-port` is
  taken. Stop that process or pass another port. Unspecified Dex ports and all
  local Temporal ports are assigned automatically.
- “attribute index synchronization failed”: operators should verify that Dex
  can list and add backend visibility indexes. Workers never require a manual
  registration command.
## License

[Super Durable Source License 1.0](LICENSE), with legacy portions under their
original terms as described in [LEGACY_NOTICES.md](LEGACY_NOTICES.md).
