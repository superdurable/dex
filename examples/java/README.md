# Dex Java examples

These examples target `io.superdurable:dex-sdk:0.1.10` (`io.superdurable.dex`).

The sample process hosts one gRPC Worker on `127.0.0.1:8803` and an HTTP
controller on port `8080`. One Registry and disk BlobCache are shared by its
Worker and Client.

## Layout

```
src/main/java/io/superdurable/dex/
├── products/       # real-world business scenarios
├── patterns/       # design patterns
├── primitives/     # one minimal example per Dex primitive
└── shared/         # mock services and HTTP helpers
```

HTTP routes use category prefixes:

- `/products/<kebab>/...` — e.g. `/products/job-post/create`
- `/patterns/<kebab>/...` — e.g. `/patterns/polling/start/simple`
- `/primitives/<kebab>/...` — e.g. `/primitives/step/start`

## Run locally

```bash
dexcli dev
./gradlew bootRun
```

The Worker synchronizes all registered Indexed Attributes with Dex before it
opens its listener; no backend CLI registration is required.

Use JDK 17. Defaults connect to Dex at `localhost:8801`. Override with
`DEX_FLOW_SERVICE_ADDRESS`, `DEX_WORKER_BIND_ADDRESS`, `DEX_WORKER_TARGET`,
`DEX_EXAMPLES_HTTP_ADDRESS`, `DEX_BLOB_CACHE_DIR`.

## Verify

Run the integration suite against an isolated `dexcli dev` environment:

```bash
./run-integration-tests.sh
```

Examples catch concrete types from `io.superdurable.dex.exceptions`.
`FlowNotFoundException` is for read operations with no matching execution;
`FlowNotActiveException` is for RPC or mutation operations after a Flow closes.
`ErrorSubStatus` remains diagnostic metadata and is not used for control flow.

The Go examples support `./run-e2e-tests.sh --keep-running` to leave Dex running
after E2E tests for manual HTTP exploration.

## Products

- [Money transfer](./src/main/java/io/superdurable/dex/products/moneytransfer)
- [Order processing](./src/main/java/io/superdurable/dex/products/orderprocessing)
- [Microservice orchestration](./src/main/java/io/superdurable/dex/products/microservices)
- [Engagement](./src/main/java/io/superdurable/dex/products/engagement)
- [Subscription](./src/main/java/io/superdurable/dex/products/subscription)
- [Polling](./src/main/java/io/superdurable/dex/products/polling)
- [Signup](./src/main/java/io/superdurable/dex/products/signup)
- [Job post](./src/main/java/io/superdurable/dex/products/jobpost)
- [Shortlist candidates](./src/main/java/io/superdurable/dex/products/shortlistcandidates)

## Patterns

Under [`patterns/`](./src/main/java/io/superdurable/dex/patterns):

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
- [Entity Store user profiles](./src/main/java/io/superdurable/dex/patterns/entitystore) ([PostgreSQL setup](../entity-store))
- Timeout handling
- Wait for state completion

## Primitives

Minimal examples under [`primitives/`](./src/main/java/io/superdurable/dex/primitives):

- [Step](./src/main/java/io/superdurable/dex/primitives/step)
- [Step custom retry](./src/main/java/io/superdurable/dex/primitives/customretry)
- [Step durability](./src/main/java/io/superdurable/dex/primitives/durability)
- [Step heartbeat](./src/main/java/io/superdurable/dex/primitives/stepheartbeat)
- [Step options override](./src/main/java/io/superdurable/dex/primitives/optionsoverride)
- [Step decision](./src/main/java/io/superdurable/dex/primitives/stepdecision)
- [Step wait types](./src/main/java/io/superdurable/dex/primitives/waittypes)
- [Attribute](./src/main/java/io/superdurable/dex/primitives/attribute)
- [Timer](./src/main/java/io/superdurable/dex/primitives/timer)
- [Channel](./src/main/java/io/superdurable/dex/primitives/channel)
- [RPC](./src/main/java/io/superdurable/dex/primitives/rpc)
- [SubFlow](./src/main/java/io/superdurable/dex/primitives/subflow)
- [Client APIs](./src/main/java/io/superdurable/dex/primitives/clientapis)
