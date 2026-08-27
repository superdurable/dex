# Dex Server

Dex Server provides the FlowService API for durable application Flows. It
persists Flow state and dispatches Step and RPC tasks to application Workers.
Learn about the programming model in the [Dex documentation](https://docs.superdurable.io/).

## Local development

Use `dexcli` to start a complete local Dex environment:

```shell
brew install superdurable/tap/dexcli
dexcli dev
```

`dexcli dev` starts Dex Server, Dex Web, and the internal workflow backend. See
the [CLI README](../cli/README.md) for endpoints, persistence, and configuration
options.

## Deploy and contribute

For standalone server configuration and integration-test setup, see
[server contributor guidance](CONTRIBUTING.md). For production operations, see
the [server operations guide](../docs/content/production/server-operations.mdx).

Integration and replay test instructions are available in
[integ/README.md](integ/README.md) and [replayTests/README.md](replayTests/README.md).

The optional `streamStore` configuration enables Redis 7+ Standalone-backed
resumable Streams. `redisURL` enables the feature. `maxMessageBytes` limits each
serialized Value to 100 KiB by default. The remaining settings tune approximate
per-message charging, trim watermarks, and background trim concurrency. Redis
is intentionally excluded from server readiness; only Stream RPCs fail when it
is unavailable. Configure dedicated Redis memory with `maxmemory` and
`noeviction` so memory pressure becomes a visible write error.

Capacity is not stored in Redis. Each write supplies the limit for all Flow
instances with that Stream name. Charged bytes approximate the serialized
Value, Flow ID, public and internal idempotency identities, and configured
overhead. Client idempotency keys cannot contain `#`; Step SDKs use
`<runID>#<stepExecutionID>`. Reaching the default 90% trigger starts singleton
background FIFO trimming toward the 80% target. A write that would exceed 100%
is not appended; it returns `ResourceExhausted` after scheduling trim and can
be retried later.

## License

[Super Durable Source License 1.0](LICENSE.md), with legacy portions under their
original terms as described in [LEGACY_NOTICES.md](LEGACY_NOTICES.md).
