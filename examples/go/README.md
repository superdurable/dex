# Dex Go examples

These examples target `github.com/superdurable/dex/sdk-go v0.1.10`.

`dex.None` marks a nil-only Step, RPC, or Channel payload. Calls pass `nil`.

The sample process hosts one gRPC Worker on `127.0.0.1:8803` and an HTTP controller
on `127.0.0.1:8080`. One Registry and disk BlobCache are shared by its Worker and
Client. The Dataset Deal DSL example also uses PostgreSQL.

## Layout

```
products/       # real-world business scenarios
patterns/       # design patterns
primitives/     # one minimal example per Dex primitive
registry/       # shared Flow registry
server/         # HTTP helpers
shared/         # mock services
cmd/server/     # Worker and HTTP entrypoint
```

HTTP routes use category prefixes:

- `/products/<kebab>/...` — e.g. `/products/job-post/create`
- `/patterns/<kebab>/...` — e.g. `/patterns/polling/start/simple`
- `/primitives/<kebab>/...` — e.g. `/primitives/channel/approve`

## Run locally

Start PostgreSQL and Dex, then build and run the examples:

```bash
docker compose -f dataset-deal/docker-compose.yml up -d --wait
dexcli dev --sqlite-db-filename /tmp/dex-examples.db
make bins
./dex-samples
```

The Worker synchronizes all registered Indexed Attributes with Dex before it
opens its listener; no backend CLI registration is required.

The defaults connect to Dex at `localhost:8801`. These environment variables override the local addresses:

- `DEX_FLOW_SERVICE_ADDRESS`: Dex gRPC target.
- `DEX_WORKER_BIND_ADDRESS`: WorkerService bind address.
- `DEX_WORKER_TARGET`: address advertised to Dex when it differs from the bind address.
- `DEX_EXAMPLES_HTTP_ADDRESS`: HTTP controller bind address.
- `DEX_BLOB_CACHE_DIR`: shared Client/Worker blob-cache directory.
- `DATASET_DEAL_POSTGRES_URL`: Dataset Deal PostgreSQL connection URL.

When Dex runs in Docker, set `DEX_WORKER_TARGET=host.docker.internal:8803`.

## Error handling

Examples match expected SDK failures with `errors.As`. Reads use
`FlowNotFoundError`; RPC, publish, and mutation paths use `FlowNotActiveError`.
Duplicate starts use `FlowAlreadyStartedError`, and server long-poll expiry uses
`LongPollTimeoutError`. A Flow that closes without completing returns
`FlowUncompletedError`. `ServiceError.SubStatus` is retained for diagnostics
and is not used for control flow.

## Verify

The E2E suite starts Dex through `dexcli dev` and runs every start, channel
publish, and RPC path covered by the existing product integ tests:

```bash
make e2eTests
```

Pass `--keep-running` to leave Dex, Temporal, PostgreSQL, and Dex Web running
after tests for manual exploration:

```bash
./run-e2e-tests.sh --keep-running
```

Run only the interactive Dataset Deal scenario and its full API verification:

```bash
make datasetDealDemo
```

When PostgreSQL, Dex, and `dex-samples` are already running, trigger the three
demo executions without starting or stopping services:

```bash
DATASET_DEAL_API_URL=http://127.0.0.1:28804 make triggerDatasetDealDemo
```

`DATASET_DEAL_PROCESS_ID` optionally changes the created process ID. Repeated
runs update that process definition and create new full, refund, and pending
executions.

Set `KEEP_DATASET_DEAL_DEMO=1` to leave PostgreSQL, Dex, Temporal, the worker,
and the REST/UI server running. The script prints all UI URLs and shutdown
details.

## Products

- [Money transfer saga](./products/money-transfer)
- [Microservice orchestration](./products/microservices)
- [Employer/job-seeker engagement](./products/engagement)
- [Subscription](./products/subscription)
- [Polling and channel coordination](./products/polling)
- [Signup](./products/signup)
- [Job post](./products/job-post)
- [Shortlist candidates](./products/shortlist-candidates)
- [Dataset Deal DSL](./products/dataset-deal) (Go only)

## Patterns

Under [`patterns/`](./patterns):

- [Cron schedule](./patterns/cron) (auto-started; no HTTP)
- [Drain internal / signal channels](./patterns/drain-channels)
- [Interruptible execution](./patterns/interruptible)
- [Manual intervention](./patterns/intervention)
- [Parallel states](./patterns/parallel)
- [Parent–child](./patterns/parent-child)
- [Polling (simple / backoff)](./patterns/polling)
- [Failure recovery](./patterns/recovery)
- [Reminders](./patterns/reminders)
- [Resettable timer](./patterns/resettable-timer)
- [Scalable parallel](./patterns/scalable-parallel)
- [Entity Store user profiles](./patterns/entity-store) ([PostgreSQL setup](../entity-store))
- [Timeout handling](./patterns/timeout)
- [Wait for state completion](./patterns/wait-for-state-completion)

## Primitives

Seven minimal examples under [`primitives/`](./primitives/): step, attribute,
channel, timer, rpc, subflow, and client-apis.
