# Dex Go examples

These examples target `github.com/superdurable/dex/sdk-go v0.1.1`.

`dex.None` marks a nil-only Step, RPC, or Channel payload. Calls pass `nil`.

The sample process hosts one gRPC Worker on `127.0.0.1:8803` and an HTTP controller on `127.0.0.1:8080`. One Registry and disk BlobCache are shared by its Worker and Client.

## Run locally

Start Dex, then build and run the examples:

```bash
dexcli dev --temporal-db-filename /tmp/dex-examples.db
make bins
./dex-samples
```

When Temporal search attributes are not pre-created:

```bash
temporal --address 127.0.0.1:7233 operator search-attribute create \
  --name ActiveStepTypes --type KeywordList
temporal --address 127.0.0.1:7233 operator search-attribute create \
  --name CustomKeywordField --type Keyword
temporal --address 127.0.0.1:7233 operator search-attribute create \
  --name CustomStringField --type Text
```

The defaults connect to Dex at `localhost:8801`. These environment variables override the local addresses:

- `DEX_FLOW_SERVICE_ADDRESS`: Dex gRPC target.
- `DEX_WORKER_BIND_ADDRESS`: WorkerService bind address.
- `DEX_WORKER_TARGET`: address advertised to Dex when it differs from the bind address.
- `DEX_EXAMPLES_HTTP_ADDRESS`: HTTP controller bind address.
- `DEX_BLOB_CACHE_DIR`: shared Client/Worker blob-cache directory.

When Dex runs in Docker, set `DEX_WORKER_TARGET=host.docker.internal:8803`.

## Verify every example

The E2E suite starts Dex through `dexcli dev` and runs every start, channel publish, and RPC path covered by the existing product integ tests:

```bash
make e2eTests
```

## Product examples

- [Money transfer saga](./workflows/moneytransfer)
- [Microservice orchestration](./workflows/microservices)
- [Employer/job-seeker engagement](./workflows/engagement)
- [Subscription](./workflows/subscription)
- [Polling and channel coordination](./workflows/polling)
- [Signup](./workflows/signup)
- [Job post](./workflows/jobpost)
- [Shortlist candidates](./workflows/shortlistcandidates)

## Design patterns

All under [`workflows/patterns`](./workflows/patterns), HTTP under `/design-pattern/...`:

- [Cron schedule](./workflows/patterns/cron) (auto-started; no HTTP)
- [Drain internal / signal channels](./workflows/patterns/drainchannels)
- [Interruptible execution](./workflows/patterns/interruptible)
- [Manual intervention](./workflows/patterns/intervention)
- [Parallel states](./workflows/patterns/parallel)
- [Parent–child](./workflows/patterns/parentchild)
- [Polling (simple / backoff)](./workflows/patterns/polling)
- [Failure recovery](./workflows/patterns/recovery)
- [Reminders](./workflows/patterns/reminders)
- [Resettable timer](./workflows/patterns/resettabletimer)
- [Scalable parallel](./workflows/patterns/scalableparallel)
- [Storage singleton](./workflows/patterns/storage)
- [Timeout handling](./workflows/patterns/timeout)
- [Wait for state completion](./workflows/patterns/waitforstatecompletion)
