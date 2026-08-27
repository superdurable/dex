# Dex Rust SDK

[![Rust SDK CI](https://github.com/superdurable/dex/actions/workflows/sdk-rust-ci.yml/badge.svg?branch=main)](https://github.com/superdurable/dex/actions/workflows/sdk-rust-ci.yml)

This workspace contains the synchronous Rust SDK, the shared DXBC BlobCache,
and the generated Rust protocol used by the native bindings.

The crates are:

- `dex-sdk`: strongly typed synchronous Client, Worker, Flow, Step, RPC, and persistence APIs.
- `dex-blob-cache`: transport-neutral, Go-compatible disk cache.
- `dex-blob-cache-jni`: Java 8-compatible binding containing only cache APIs.
- `dex-blob-cache-python`: PyO3 binding for the Python SDK.
- `dex-blob-cache-node`: Node-API binding for the TypeScript SDK.
- `dex-protocol`: generated Rust protobuf and gRPC protocol.

The architecture is defined in
[Multi-language Rust SDK Core](../docs/design/multi-language-rust-sdk-core.md).
The public Rust API is defined in
[Rust SDK User Interface](../docs/design/rust-sdk-user-interface.md).

The `dex-sdk` source layout follows the application developer's mental model:
Flows, Steps, Attributes, Channels, Streams, RPCs, timers, and waits each have dedicated
modules. Client, Worker, Registry, and each options family are separated as
their own entry points instead of being collected into infrastructure-oriented
files. Handler failures and SDK/service failures are also separate modules.

Streams carry best-effort resumable progress messages. Register a cloned
definition in exactly one Flow schema; clones retain identity, and the
approximate byte budget is shared across all instances of that Flow type.

```rust
let progress = Stream::<String>::new("progress", 10 * 1024 * 1024);
let schema = PersistenceSchema::new().stream(&progress);

progress.write(context, "running".to_owned())?;
client.write_stream(flow_id, &progress, "frontend/1", "starting".to_owned())?;
let message = client.read_stream_with_timeout(
    flow_id, &progress, resume_token, Duration::from_secs(30)
)?;
```

Step writes are immediate, use `runID#stepExecutionID`, and allow one write per
Stream per invocation. Client keys cannot contain `#`. Reads return the decoded
value, resume token, creation time, and idempotency key.

Single-condition waits read as `Wait::until(condition)`. `Wait::all_of` and
`Wait::any_of` remain available for aggregate conditions. Client failures use
domain-specific `SdkError` variants such as `FlowNotFound`, `FlowNotActive`,
`FlowAlreadyStarted`, `RpcLockConflict`, and `WorkerInvocation` instead of
requiring callers to inspect transport metadata.

Flat waits may contain unnamed Conditions and send an empty Condition ID.
`Wait::any_combination_of` requires a non-empty user ID on every Condition; a
cloned Condition retains its identity and may be reused across combinations.

Client-side map reads and writes use `get_attribute_map_instance` and
`set_attribute_map_instance`. `Client::wait_for_attribute_equal` and
`Client::wait_for_attribute_map_instance_equal` target the current run and
accept only string, bool, integer, or double wire values. JSON, bytes, and null
return a local `InvalidArgument`. `AttributeMap::map_size/all_instance_keys` include
buffered sets and deletes. The matching `ChannelMap` methods are RPC-only,
include buffered publishes, and omit empty instances. Keys are decoded and
sorted. Conditional completion is
`StepDecision::force_complete_if_channels_empty`.

`Client::wait_for_flow` and `wait_for_flow_with_timeout` return a
`FlowResult` after hydrating every output-bearing completion. Use the
strict helper for a single-output Flow:

```rust
let output: OrderResult = client.wait_for_flow(flow_id)?.single_output()?;

let result = client.wait_for_flow(flow_id)?;
for completion in result.completions() {
    if completion.step_type == "ChargeCard" {
        let receipt: Receipt = completion.decode()?;
    }
}
```

The completion slice preserves server collection order, but parallel branch
order is not deterministic. No-output Flows return an empty slice;
`single_output` returns `SdkError::InvalidArgument` for zero or multiple
completions. Every terminal status returns a `FlowResult`; inspect `status`,
`error_type`, and `error_message` for unsuccessful completion.

SubFlows are normal, independently addressable Flows used as durable Conditions:

```rust
fn wait_for(&self, _context: &mut Context, input: ChargeInput) -> HandlerResult<Wait> {
    let condition = SubFlow::run(&ChargeFlow::new(), input)
        .map_err(|error| HandlerError::new(error.to_string()))?;
    Ok(Wait::until(condition))
}

fn execute(&self, context: &mut Context, _input: ChargeInput) -> HandlerResult<StepDecision> {
    let receipt: Receipt = SubFlow::condition_result(context)?
        .single_output()
        .map_err(|error| HandlerError::new(error.to_string()))?;
    Ok(StepDecision::graceful_complete(receipt))
}
```

`SubFlow::flow_id_at(context, index)` remains available for a running `any_of` loser.
`SubFlowOptions` configures timing, timeout policy, retry, initial target Attributes,
Flow config, Condition ID, and reuse. Parent completion does not cancel an unfinished SubFlow.

Existing-Flow reads (`get_attribute`, `describe_flow`, `wait_for_flow`, and
`time_travel`) use `FlowNotFound`; operations requiring a running Flow use
`FlowNotActive`. Each remote variant owns a `ServiceError`, available through
`SdkError::service_error()`, with gRPC code, Dex sub-status, detail, operation,
Flow ID, and the original `tonic::Status` source. `WorkerInvocation` also owns
a `WorkerError` with the original worker code, type, and detail.

```rust
match client.publish(flow_id, &orders.approved, order_id) {
    Err(SdkError::FlowNotActive { service }) => {
        eprintln!("{} failed for {:?}", service.operation(), service.flow_id());
    }
    result => result?,
}
```

`Registry::register` reports invalid definitions as `SdkError::FlowDefinition`.
Value conversion and invalid Step results use `ValueMapping` and
`InvalidStepResult`.

### Soft Flow timeout

A Flow registers its optional handler through `timeout_handler`. Its presence
makes a positive timeout use handler policy by default:

```rust
fn handle_timeout(&self, context: &mut Context) -> HandlerResult<StepDecision> {
    notify_expiration(context)?;
    Ok(StepDecision::force_complete("expired".to_string()))
}

fn timeout_handler(&self) -> Option<FlowTimeoutHandler<Self>> {
    Some(Self::handle_timeout)
}

let options = StartFlowOptions::new()
    .timeout(Duration::from_secs(30 * 60))
    .timeout_policy(FlowTimeoutPolicy::Handler);
```

`Fail` produces `FlowErrorType::FlowTimeout` and permits Flow retry; `Cancel`
cancels without retry. Continue-as-new preserves the deadline and handler
execution, while retry runs receive a fresh budget. An absent or zero
timeout disables the feature.

## Rust SDK runtime

Flows bind their start input and every Step binds its own input at compile time:

```rust
use dex_sdk::{Context, Flow, HandlerResult, Step, StepDecision, StepList};

struct GreetingFlow {
    greet: Greet,
}

impl Flow for GreetingFlow {
    type StartInput = String;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.greet)
    }
}

struct Greet;

impl Step for Greet {
    type Input = String;

    fn execute(
        &self,
        _context: &mut Context,
        name: String,
    ) -> HandlerResult<StepDecision> {
        Ok(StepDecision::graceful_complete(format!("Hello, {name}!")))
    }
}
```

`Client` calls block and return their final result. `Worker::start` serves until
another thread calls `Worker::stop`. Synchronous user handlers run on Tokio's
blocking executor, so they do not occupy gRPC I/O tasks. Long-running handlers
can call `Context::wait_for_cancellation` or poll `Context::is_cancelled` to
observe method deadlines and disconnected callers.

### Canceling Step executions

A successful Step can cancel queued or active executions while continuing with
its normal decision:

```rust
Ok(StepDecision::go_to(&self.record_quote, quote)
    .cancel_sibling_step(&self.carrier_a)
    .cancel_sibling_step(&self.carrier_b)
    .cancel_step(&self.global_quote_timeout))
```

`cancel_step` selects every current execution of one registered Step type.
`cancel_sibling_step` selects only executions whose
`Context::from_step_execution_id()` matches the current execution. Chain the
builders to select multiple types; Flow-wide selection wins for the same type.
The Worker rejects unregistered Step types as invalid results.

Dex resolves one snapshot after the current execution succeeds. Completed,
already-canceled, and absent targets are no-ops. Next Steps created by that
decision are outside the snapshot. Dex immediately applies the next or close
action; late decisions, writes, retries, and recovery Steps are discarded.

Set `StepOptions::heartbeat_timeout` on long-running regular Steps so
cancellation reaches the Worker promptly. Zero disables heartbeats, and
positive values must be whole seconds in the signed int32 range. Local
activities ignore the setting, while an ASYNC fallback uses it. Blocking
handlers can call `Context::wait_for_cancellation`; batch handlers may check
`Context::is_cancelled()` at natural boundaries.

`RpcResult::cancel_step` provides Flow-wide selection for RPCs. RPCs do not
support sibling selection because they have no Step execution lineage.

Opt an Attribute or AttributeMap into Attribute Store synchronization and
select Server-configured Stores for the Flow:

```rust
use dex_sdk::{Attribute, FlowConfig};

let email = Attribute::<String>::new("customer-email").sync_to_attribute_store();
let config = FlowConfig::new().attribute_store_names(vec!["profiles".into(), "audit".into()]);
```

Stores are asynchronous latest-state projections. Every enabled Attribute write
is sent to every selected Store. Deletion writes SQL `NULL`, and projection
failures do not roll back Flow Attributes. Omitting the builder preserves
current targets; passing an empty vector disables future synchronization while
retaining protocol presence.

## Blob cache

The shared cache keeps opaque payload bytes on disk and uses Stretto only for
metadata admission and eviction:

```rust
use dex_blob_cache::{BlobCache, BlobCacheConfig};

let config = BlobCacheConfig::new("./blob-cache", 256 * 1024 * 1024, 10_000)?;
let cache = BlobCache::open(config)?;
let retained = cache.put("server-minted-blob-id", b"opaque bytes")?;
let cached_payload = cache.get("server-minted-blob-id")?;
if !retained || cached_payload.is_none() {
    // Keep using the freshly loaded bytes.
}
cache.close()?;
```

The calls are synchronous. Event-loop bridges must dispatch them through a
bounded blocking executor. Concurrent reads are supported; mutations and close
coordinate inside the cache. An orderly `close` does not delete committed
files, so they can be reused after restart; use `delete_all` before `close` for
ephemeral storage. The cache is not authoritative: policy rejection, a miss,
or a cache error must fall back to fresh server data.

Build the Java cache binding as an optimized native library:

```bash
cargo build --release -p dex-blob-cache-jni --locked
```

Build the Node cache binding and stage it into the TypeScript package:

```bash
cargo build --release -p dex-blob-cache-node --locked
# or from sdk-typescript:
npm run build:native
```

## Development

Rust 1.97 or newer is required.

Format and test the workspace:

```bash
make fmt
make lint
make test
make docs-check
```

`docs-check` denies missing rustdoc and rustdoc warnings for the hand-written
`dex-sdk` and `dex-blob-cache` public APIs, then compiles every documentation
example as a doctest. Generated protocol and language-binding crates are not
part of that public SDK surface. Generated HTML starts at
`target/doc/dex_sdk/index.html` and `target/doc/dex_blob_cache/index.html`.

Run the SDK integration suite against a fresh local `dexcli dev` stack:

```bash
./run-integration-tests.sh
```

The script creates its own Dex environment, ports, and BlobCache directory,
then removes the temporary state. Each Worker synchronizes its registered
Indexed Attributes before listening; failure or the default two-minute
deadline aborts startup.

### Measure integration coverage

Install `cargo-llvm-cov`, then run the same integration suite with Rust source
coverage:

```bash
cargo install cargo-llvm-cov --locked
./run-integration-tests.sh --coverage
```

Only the `integ` and `cross_sdk` integration test targets contribute execution
data. Test sources and dependencies are excluded from the report by
`cargo-llvm-cov`; coverage is reported for the production `dex-sdk` crate.

The LCOV report is written to `coverage/lcov.info`, and the browser report
starts at `coverage/html/index.html`. CI uploads the LCOV report with the
`sdk-rust-integration` flag and retains the full report as the
`sdk-rust-integration-coverage` Actions artifact.

## Releases

Publish `dex-sdk`, `dex-blob-cache`, and `dex-protocol` by creating a GitHub
Release whose tag is `sdk-rust/vX.Y.Z`. The release workflow stamps all crate
versions and internal registry requirements from the tag in its temporary
checkout, then publishes the crates in dependency order. No version-bump commit
is required before publishing.

The workflow also supports a manual validation run. Manual publishing is
restricted to `main` and requires the `CRATES_IO_TOKEN` repository secret.

## License

[Super Durable Source License 1.0](LICENSE), with legacy portions under their
original terms as described in [LEGACY_NOTICES.md](LEGACY_NOTICES.md).
