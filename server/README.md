# Dex Server

Dex Server provides the FlowService API for durable application Flows. It
persists Flow state and dispatches Step and RPC tasks to application Workers.
Learn about the programming model in the [Dex documentation](https://docs.superdurable.io/).

Channel messages are stored as FIFO envelopes with server-generated UUIDv7
identities. FlowService can list all pending messages for one Channel or delete
one by ID. Cadence deletion provides best-effort race semantics.

Worker RPC responses may stage Channel deletions. Transactional RPCs validate
every deletion before any Flow mutation on supported backends. Attribute
locking enables transactional execution automatically; Channel deletion
requires an explicit transactional request. Nontransactional RPCs ignore
missing deletions and continue applying their remaining side effects. Cadence
does not provide the same atomicity.

Worker RPCs receive ordinary Attributes and all Channel size metadata by
default. Callers explicitly load AttributeMap definitions and Channel or
ChannelMap definitions when the handler needs their entries or pending message
envelopes. A trailing-slash map name loads every instance. The suffix names one
instance. Empty loads
are echoed separately from their loaded data.

State loading controls only the Worker request projection. Transactional
execution controls atomic commit and Channel deletion validation. Attribute
locking additionally isolates cooperating Steps and RPCs that use the same
lock. A transactional RPC without a shared lock does not prevent another
operation from changing loaded collection state while its handler runs.

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

The optional `streamStore` configuration enables best-effort resumable Streams.
`backend` accepts `memory` for one Dex Server process or `redis` for Redis 7+
Standalone shared by multiple servers. It defaults to `disabled`. The memory
backend loses messages when the process stops. The Redis
backend requires `redisURL`; Redis is intentionally excluded from server
readiness, so only Stream RPCs fail when it is unavailable. Configure dedicated
Redis memory with `maxmemory` and `noeviction` so memory pressure becomes a
visible write error.

`maxMessageBytes` limits each serialized Value to 100 KiB by default. The
remaining settings tune approximate per-message charging, trim watermarks,
messages removed per trim batch, and background trim concurrency. Lease settings
apply only to the Redis backend. The checked-in development config uses memory.

Capacity is not persisted by either backend. Each write supplies the limit
shared by all Flow instances with the same Flow type and Stream name. Charged
bytes approximate the serialized Value, Flow ID, source, and configured
overhead. A source is required, may contain `#`, and may repeat. Every write is
appended. Step output uses `#<stepExecutionID>` as source metadata.
Reaching the default 90%
trigger starts singleton background FIFO trimming toward the 80% target. A
write that would exceed 100% is not appended; it returns `ResourceExhausted`
after scheduling trim and can be retried later.

## License

[Super Durable Source License 1.0](LICENSE.md), with legacy portions under their
original terms as described in [LEGACY_NOTICES.md](LEGACY_NOTICES.md).
