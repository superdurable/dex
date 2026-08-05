# Dex Go examples

These examples target `github.com/superdurable/dex/sdk-go v0.1.0` plus the
pending `dex.None` API. Their module temporarily replaces the SDK with this
repository's `sdk-go` directory until the next release.

`dex.None` marks a nil-only Step, RPC, or Channel payload. Calls pass `nil`.

The sample process hosts one gRPC Worker on `127.0.0.1:8803` and an HTTP controller on `127.0.0.1:8080`. One Registry and disk BlobCache are shared by its Worker and Client. The Dataset Deal DSL example also uses PostgreSQL.

## Run locally

Start PostgreSQL and Dex, then build and run the examples:

```bash
docker compose -f dataset-deal/docker-compose.yml up -d --wait
dexcli dev --temporal-db-filename /tmp/dex-examples.db
./dataset-deal/register-search-attributes.sh localhost:7233
make bins
./dex-samples
```

The defaults connect to Dex at `localhost:8801`. These environment variables override the local addresses:

- `DEX_FLOW_SERVICE_ADDRESS`: Dex gRPC target.
- `DEX_WORKER_BIND_ADDRESS`: WorkerService bind address.
- `DEX_WORKER_TARGET`: address advertised to Dex when it differs from the bind address.
- `DEX_EXAMPLES_HTTP_ADDRESS`: HTTP controller bind address.
- `DEX_BLOB_CACHE_DIR`: shared Client/Worker blob-cache directory.
- `DATASET_DEAL_POSTGRES_URL`: Dataset Deal PostgreSQL connection URL.

When Dex runs in Docker, set `DEX_WORKER_TARGET=host.docker.internal:8803`.

## Verify every example

The E2E suite starts Dex through `dexcli dev` and runs every start, channel publish, and RPC path:

```bash
make e2eTests
```

Run only the interactive Dataset Deal scenario and its full API verification:

```bash
make datasetDealDemo
```

Set `KEEP_DATASET_DEAL_DEMO=1` to leave PostgreSQL, Dex, Temporal, the worker,
and the REST/UI server running. The script prints all UI URLs and shutdown
details.

## Examples

- [Money transfer saga](./workflows/moneytransfer)
- [Microservice orchestration](./workflows/microservices)
- [Employer/job-seeker engagement](./workflows/engagement)
- [Subscription](./workflows/subscription)
- [Polling and channel coordination](./workflows/polling)
- [Dataset Deal DSL](./workflows/datasetdeal)

Dataset Deal stores reusable seller process definitions in PostgreSQL. Each
execution snapshots its definition and exposes all runtime state through Dex.
