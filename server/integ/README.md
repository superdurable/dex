### Integration tests

This directory contains integration tests for the Dex service.

* [How to run](../CONTRIBUTING.md#how-to-run-server-or-integration-test)
* The integration tests are written without Dex SDKs. The workflows are implemented in REST API routes. e.g. [this basic workflow](./workflow/basic/routers.go)

Custom Attribute Store integration uses isolated MySQL and PostgreSQL services:

```shell
docker compose -f docker-compose/attribute-store-dependencies.yml up -d --wait
make attributeStoreIntegTests
```

The suite covers startup schema contracts, both SQL upsert dialects, filtering,
schema refresh recovery, and additive columns.

In-process tests use an isolated local Blob Store with the default 1 KiB
offload threshold. S3-specific tests replace it with MinIO.

Step cancellation coverage runs against both Temporal and Cadence. It verifies
Flow-wide and sibling selectors; queued and active executions; local and
regular activities; local-timeout fallback with cumulative attempts;
heartbeat-driven handler cancellation;
fire-and-continue behavior; late-result suppression;
continue-as-new; Step and RPC producers; signal and synchronous-update RPC
delivery; RPC sibling-selector rejection; snapshot exclusion of RPC next Steps;
and clean active state.

Resumable Stream integration requires Redis 7 on `127.0.0.1:6379`. The standard
dependency Compose files provide it with the `noeviction` policy. Run the
focused server and Redis coverage with:

```shell
docker compose -f docker-compose/integ-dependencies.yml up -d redis
make streamIntegTests
```

The focused suite covers cross-Flow global FIFO trimming, idempotency, stale
TTL references, resume behavior, long polling, concurrent writers, trim lease
recovery, native TTL, disabled configuration, and Redis failure isolation.
