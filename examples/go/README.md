# Dex Go examples

These examples use the published `github.com/superdurable/dex/sdk-go v0.1.0` module. They do not import generated protobufs or replace the SDK with a local checkout.

The sample process hosts one gRPC Worker on `127.0.0.1:8803` and an HTTP controller on `127.0.0.1:8080`. One Registry and disk BlobCache are shared by its Worker and Client.

## Run locally

Start Dex, then build and run the examples:

```bash
dexcli dev --temporal-db-filename /tmp/dex-examples.db
make bins
./dex-samples
```

The defaults connect to Dex at `localhost:8801`. These environment variables override the local addresses:

- `DEX_FLOW_SERVICE_ADDRESS`: Dex gRPC target.
- `DEX_WORKER_BIND_ADDRESS`: WorkerService bind address.
- `DEX_WORKER_TARGET`: address advertised to Dex when it differs from the bind address.
- `DEX_EXAMPLES_HTTP_ADDRESS`: HTTP controller bind address.
- `DEX_BLOB_CACHE_DIR`: shared Client/Worker blob-cache directory.

When Dex runs in Docker, set `DEX_WORKER_TARGET=host.docker.internal:8803`.

## Verify every example

The E2E suite starts Dex through `dexcli dev` and runs every start, channel publish, and RPC path:

```bash
make e2eTests
```

## Examples

- [Money transfer saga](./workflows/moneytransfer)
- [Microservice orchestration](./workflows/microservices)
- [Employer/job-seeker engagement](./workflows/engagement)
- [Subscription](./workflows/subscription)
- [Polling and channel coordination](./workflows/polling)
