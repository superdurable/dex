# Dex Python examples

These examples target [`dex-python-sdk==0.1.10`](https://pypi.org/project/dex-python-sdk/0.1.10/)
(`import dex`). Requires Python 3.11+.

The primary sample process hosts one asyncio `AsyncWorker` on `127.0.0.1:8803` and a
Quart HTTP controller on `127.0.0.1:8080`. Controllers and nested parent/child Steps
use `AsyncClient`. One Registry and disk BlobCache are shared by Worker and Client.

For the sync `Client` / `Worker` surface (Flask), see [`sync-python/`](./sync-python/).

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
- `/patterns/<kebab>/...` — e.g. `/patterns/polling/start/simple`
- `/primitives/<kebab>/...` — e.g. `/primitives/channel/approve`

## Run locally

```bash
dexcli dev
uv sync --locked
uv run --frozen python main.py
```

Defaults connect to Dex at `localhost:8801`. Override with
`DEX_FLOW_SERVICE_ADDRESS`, `DEX_WORKER_BIND_ADDRESS`, `DEX_WORKER_TARGET`,
`DEX_EXAMPLES_HTTP_ADDRESS`, `DEX_BLOB_CACHE_DIR`.

## Verify

```bash
make unitTests
make e2eTests
```

The Go examples support `./run-e2e-tests.sh --keep-running` to leave Dex running
after E2E tests for manual HTTP exploration.

## Products

- [Money transfer](./dex_examples/products/money-transfer)
- [Microservice orchestration](./dex_examples/products/microservices)
- [Engagement](./dex_examples/products/engagement)
- [Subscription](./dex_examples/products/subscription)
- [Polling](./dex_examples/products/polling)
- [Signup](./dex_examples/products/signup)
- [Job post](./dex_examples/products/job-post)
- [Shortlist candidates](./dex_examples/products/shortlist-candidates)
- [AI agent email](./ai-agent-email/) (Python only; UI assets in [`ai-agent-email/`](./ai-agent-email))

## Patterns

Under [`dex_examples/patterns/`](./dex_examples/patterns/), including
[cron](./dex_examples/patterns/cron),
[polling](./dex_examples/patterns/polling),
[resource-control](./dex_examples/patterns/resource-control) (Python only),
and others.

## Primitives

Seven minimal examples under [`dex_examples/primitives/`](./dex_examples/primitives/):
step, attribute, channel, timer, rpc, subflow, and client-apis.
