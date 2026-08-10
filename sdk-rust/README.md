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
Flows, Steps, Attributes, Channels, RPCs, timers, and waits each have dedicated
modules. Client, Worker, Registry, and each options family are separated as
their own entry points instead of being collected into infrastructure-oriented
files. Handler failures and SDK/service failures are also separate modules.

Single-condition waits read as `Wait::until(condition)`. `Wait::all_of` and
`Wait::any_of` remain available for aggregate conditions. Client failures use
domain-specific `SdkError` variants such as `FlowNotFound`, `FlowNotActive`,
`FlowAlreadyStarted`, `RpcLockConflict`, and `WorkerInvocation` instead of
requiring callers to inspect transport metadata.

Existing-Flow reads (`get_attribute`, `describe_flow`, `wait_for_flow`, and
`reset_flow`) use `FlowNotFound`; operations requiring a running Flow use
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

Format and test the workspace:

```bash
make fmt
make lint
make test
```

Run the SDK integration suite against a fresh local `dexcli dev` stack:

```bash
./run-integration-tests.sh
```

The script creates its own Temporal database, ports, BlobCache directory, and
search attributes, then removes the temporary state.

## License

[Super Durable Source License 1.0](LICENSE), with legacy portions under their
original terms as described in [LEGACY_NOTICES.md](LEGACY_NOTICES.md).
