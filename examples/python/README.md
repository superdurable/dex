# Dex Python examples

These examples target the published Dex Python SDK version pinned in
[`pyproject.toml`](./pyproject.toml)
(`import dex`). Requires Python 3.11+.

The primary sample process runs one asyncio `AsyncWorker` with outbound pull
streams and a Quart HTTP controller on `127.0.0.1:8080`. Controllers and nested parent/child Steps
use `AsyncClient`. One Registry and disk BlobCache are shared by Worker and Client.

For the sync `Client` / `Worker` surface (Flask), see [`sync-python/`](./sync-python/).

RPC and mutation calls can return `FlowNotActiveOrNotFoundError` when the Flow is missing
or the operation requires an active execution that has ended. A query-only RPC
can still read a retained terminal Flow when it has no locks, transactionality,
durable effects, or Server-forced Update routing. At that confirmed read boundary,
handle the error as missing or unavailable without a lifecycle probe, a short
`WaitForFlow` timeout, or historical Step-output decoding. RPC success alone does
not prove that the Flow is active.

## Layout

```
dex_examples/
├── products/       # real-world business scenarios
├── patterns/       # design patterns
├── primitives/     # one minimal example per Dex primitive
├── shared/         # mock services and HTTP helpers
├── app.py          # Worker registry
└── http_app.py     # Quart blueprint assembly
```

HTTP routes use category prefixes:

- `/products/<kebab>/...` — e.g. `/products/job-post/create`
- `/patterns/<kebab>/...` — e.g. `/patterns/polling/start`
- `/primitives/<kebab>/...` — e.g. `/primitives/channel/approve`

## Run locally

```bash
dexcli dev
uv sync --locked
uv run --frozen python main.py
```

The examples share namespace-level slots by index type: `keyword1`,
`text1`, `int1`, and numbered later slots such as `keyword2`.
Use a fresh local store and a new port after changing an example's index schema.

Application searches must constrain `FlowType` and parenthesize caller filters.
The sample search endpoints enforce their Flow type.

Defaults connect to Dex at `localhost:8801`. Override with
`DEX_FLOW_SERVICE_ADDRESS`, `DEX_EXAMPLES_HTTP_ADDRESS`, or `DEX_BLOB_CACHE_DIR`.
Workers use pull streams and need only an outbound connection to the Dex Server.
The sync examples in [`sync-python/`](./sync-python/) use the same behavior.

## Verify

```bash
make unitTests
make e2eTests
```

The Go examples support `./run-e2e-tests.sh --keep-running` to leave Dex running
after E2E tests for manual HTTP exploration.

The tests always use pull Workers. Set `DEX_INTEG_ENGINE=zd` to run them
against server-zd. Set `DEX_SERVER_ZD_BINARY` to reuse a
built `dex-server-zd`. `run-e2e-tests.sh` and `run-integ-tests.sh` both accept
these variables.

Run the async tests (`tests/`) and the sync tests
(`sync-python/sync_tests/`) in separate pytest processes. Both suites register
some of the same Flow types. Dex routes pull tasks by Flow type, so a task can
reach the async Worker while its event loop is idle during the sync tests. The
test scripts already run them separately.

## Products

- [Money transfer](./dex_examples/products/money-transfer)
- [Order processing](./dex_examples/products/order-processing)
- [Microservice orchestration](./dex_examples/products/microservices)
- [Engagement](./dex_examples/products/engagement)
- [Subscription](./dex_examples/products/subscription)
- [User onboarding process](./dex_examples/products/signup)
- [Job posting](./dex_examples/products/job-post)
- [Deal DSL](./dex_examples/products/deal_dsl)
- [AI Agent](./ai-agent/) (Python only; one application snapshot RPC, durable plans, queued messages, Steer, MCP tools, context compaction, and UI assets in [`ai-agent/`](./ai-agent))

## Patterns

Under [`dex_examples/patterns/`](./dex_examples/patterns/), including
[Cron schedule](./dex_examples/patterns/cron),
[polling an external job in one long-running Step](./dex_examples/patterns/polling),
[resource-control](./dex_examples/patterns/resource-control) (Python only),
and others.

## Primitives

Seven minimal examples under [`dex_examples/primitives/`](./dex_examples/primitives/):
step, attribute, channel, timer, rpc, subflow, and client-apis.
