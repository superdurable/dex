# Dex Java examples

These examples target `io.superdurable:dex-sdk:0.1.10` (`io.superdurable.dex`).

The sample process hosts one gRPC Worker on `127.0.0.1:8803` and an HTTP
controller on port `8080`. One Registry and disk BlobCache are shared by its
Worker and Client.

## Run locally

```bash
dexcli dev --temporal-db-filename /tmp/dex-examples.db
./gradlew bootRun
```

The Worker synchronizes all registered Indexed Attributes with Dex before it
opens its listener; no backend CLI registration is required.

Use JDK 17. Defaults connect to Dex at `localhost:8801`. Override with
`DEX_FLOW_SERVICE_ADDRESS`, `DEX_WORKER_BIND_ADDRESS`, `DEX_WORKER_TARGET`,
`DEX_BLOB_CACHE_DIR`.

Run the integration suite against an isolated `dexcli dev` environment:

```bash
./run-integration-tests.sh
```

Examples catch concrete types from `io.superdurable.dex.exceptions`.
`FlowNotFoundException` is for read operations with no matching execution;
`FlowNotActiveException` is for RPC or mutation operations after a Flow closes.
`ErrorSubStatus` remains diagnostic metadata and is not used for control flow.

## Product examples

- [Money transfer](./src/main/java/io/superdurable/dex/workflow/money/transfer)
- [Microservice orchestration](./src/main/java/io/superdurable/dex/workflow/microservices)
- [Engagement](./src/main/java/io/superdurable/dex/workflow/engagement)
- [Subscription](./src/main/java/io/superdurable/dex/workflow/subscription)
- [Polling](./src/main/java/io/superdurable/dex/workflow/polling)
- [Signup](./src/main/java/io/superdurable/dex/workflow/signup)
- [Job post](./src/main/java/io/superdurable/dex/workflow/jobpost)
- [Shortlist candidates](./src/main/java/io/superdurable/dex/workflow/shortlistcandidates)
- [Retrying Worker failure](./src/main/java/io/superdurable/dex/workflow/retryingfailure)

## Design patterns

All under [`patterns/workflow`](./src/main/java/io/superdurable/dex/patterns/workflow),
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
- [Entity Store user profiles](./src/main/java/io/superdurable/dex/patterns/workflow/entitystore) ([PostgreSQL setup](../entity-store))
- Timeout handling
- Wait for state completion
