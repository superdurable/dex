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
