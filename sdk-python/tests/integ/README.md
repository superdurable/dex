# Python SDK integration tests

This directory mirrors the Java integration inventory. Its executable
scenarios preserve the Java workflows, client operations, and assertions while
using idiomatic Python contracts.

Run the complete suite against a fresh `dexcli dev` environment:

```shell
./run-integration-tests.sh
```

The compile-oriented call-site modules remain part of strict static checking:

```shell
uv run --frozen mypy tests/integ
uv run --frozen pyright tests/integ
```

## Programming experience

- Python annotations derive Step and RPC codecs; Attribute and Channel values
  declare ordinary Python types.
- `Wait.until(Timer.by_duration(...))` and handle methods keep nested calls
  readable.
- RPC methods remain normal bound methods and are passed directly to
  `Client.invoke_rpc`.
- `PersistenceSchema.of(...)` groups attributes and channels without parallel
  keyword tuples.
- `StartFlowOptions.with_attribute(...)` binds Attribute and AttributeMap
  values without a public wrapper type.
- Attribute-map locks retain the instance through
  `items.lock("order-1")`.
- Synchronous Client shapes match Java and Go while preserving Python naming.
- Sync and async suites cover singleton Attribute and AttributeMap instance equality waits;
  local contracts cover Condition IDs and map introspection.
- Stream integration is pending the server-streaming Worker API update.

## Error coverage

| Scenario | Error |
| --- | --- |
| Duplicate start | `FlowAlreadyStartedError` |
| Missing describe, attribute read, or Flow wait | `FlowNotFoundError` |
| Missing or closed mutation/RPC | `FlowNotActiveError` |
| Worker handler failure | `WorkerInvocationError` |
| Locking RPC contention | `RpcLockConflictError` |
| Long-poll expiry | `LongPollTimeoutError` |
| Terminal Flow result | `FlowResult` for every terminal status |
| Durable SubFlow condition | Identity, all/any, reuse, reset, and continue-as-new |

Local contract tests also cover malformed rich details, registration failures,
value mapping, and invalid Step results.
