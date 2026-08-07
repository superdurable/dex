# Dex Python examples

These examples target [`dex-python-sdk==0.0.2`](https://pypi.org/project/dex-python-sdk/0.0.2/)
(`import dex`). Requires Python 3.11+.

The sample process hosts one gRPC Worker on `127.0.0.1:8803` and an HTTP
controller on `127.0.0.1:8080`. One Registry and disk BlobCache are shared by its
Worker and Client.

## Run locally

```bash
dexcli dev --temporal-db-filename /tmp/dex-examples-python.db

# Required once per Temporal namespace:
temporal --address 127.0.0.1:7233 operator search-attribute create \
  --name ActiveStepTypes --type KeywordList
temporal --address 127.0.0.1:7233 operator search-attribute create \
  --name CustomKeywordField --type Keyword
temporal --address 127.0.0.1:7233 operator search-attribute create \
  --name CustomStringField --type Text

uv sync --locked
uv run --frozen python main.py
```

Defaults connect to Dex at `localhost:8801`. Override with
`DEX_FLOW_SERVICE_ADDRESS`, `DEX_WORKER_BIND_ADDRESS`, `DEX_WORKER_TARGET`,
`DEX_EXAMPLES_HTTP_ADDRESS`, `DEX_BLOB_CACHE_DIR`.

When Dex runs in Docker, set `DEX_WORKER_TARGET=host.docker.internal:8803`.

## Verify every example

The E2E suite starts Dex through `dexcli dev` and runs unit + integ tests that
exercise every product, design-pattern, and Python-only example:

```bash
make e2eTests
# or
./run-e2e-tests.sh
```

Unit tests only (no Dex server):

```bash
make unitTests
```

## Product examples

- [Money transfer](./dex_examples/workflow/money/transfer)
- [Microservice orchestration](./dex_examples/workflow/microservices)
- [Engagement](./dex_examples/workflow/engagement)
- [Subscription](./dex_examples/workflow/subscription)
- [Polling](./dex_examples/workflow/polling)
- [Signup](./dex_examples/workflow/signup)
- [Job post](./dex_examples/workflow/jobpost)
- [Shortlist candidates](./dex_examples/workflow/shortlistcandidates)

HTTP routes: `/moneytransfer`, `/microservice`, `/engagement`, `/subscription`,
`/polling`, `/signup`, `/jobpost`, `/shortlist_candidates`.

## Design patterns

All under [`dex_examples/patterns/workflow`](./dex_examples/patterns/workflow),
HTTP under `/design-pattern/...`:

- Cron schedule
- Drain internal / signal channels
- Interruptible execution
- Manual intervention
- Parallel states (simple / with await)
- Parent–child
- Polling (simple / backoff)
- Failure recovery (saga)
- Reminders
- Resettable timer
- Scalable parallel
- Storage singleton
- Timeout handling
- Wait for state completion

## Python-only examples

- [Basic](./dex_examples/basic) — timer + approval channel + RPCs (`/basic`)
- [Resource control](./dex_examples/resourcecontrol) — controller/processing pair (`/controller`)
- [AI agent email](./dex_examples/ai_agent_email) — durable email drafting UI at `/ai-agent`
  (static assets remain under [`ai-agent-email/`](./ai-agent-email))
