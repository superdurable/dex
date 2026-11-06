# Rust applications

Load this page first for any Rust Dex work. It covers crate shape,
registration, typing, the Worker and Client lifecycle, and Rust-specific rules.
Concepts live in core; this page only says how to express them in Rust.

| Task | Read next |
| --- | --- |
| Write Flows, Steps, waits, state, RPCs, Streams, SubFlows, Client calls | [primitives.md](primitives.md) |
| Handler errors, retries, recovery, `SdkError` handling | [error-handling.md](error-handling.md) |
| Integration tests against a real Dex Server | [testing.md](testing.md) |
| Choose a Flow shape | [../core/patterns.md](../core/patterns.md), then the pattern table in [primitives.md](primitives.md#patterns-in-rust) |

## Version and API details

1. Find the resolved `dex-sdk` version in `Cargo.lock`, or run
   `cargo tree -p dex-sdk --depth 0`.
2. Open that minor version in the
   [Rust Developer reference](https://docs.superdurable.io/reference/rust/)
   with the left-sidebar version selector. It holds option lists, defaults,
   limits, the supported Rust toolchain, version differences, and the link to
   the generated crate documentation.
3. Check the Server requirement for that SDK minor in the
   [compatibility matrix](https://docs.superdurable.io/reference/).

This skill gives stable rules and skeletons. Signatures and defaults come from
the Developer reference and the crate's doc comments. Never translate a Java
annotation, Python coroutine, or Go option literally into Rust, and never
invent an API; if the installed crate lacks a feature, say so.

Runnable examples: [examples/rust](https://github.com/superdurable/dex/tree/main/examples/rust).

## Crate shape

```text
src/
  orders/flow.rs        # schema statics, RPC consts, Flow, Steps
  orders/controller.rs  # Client calls at the service boundary
  orders/mod.rs
  registry.rs           # Registry composition
  main.rs               # BlobCache, Client, Worker, HTTP bootstrap
tests/
  order_integration.rs  # real Dex Server tests
```

- Controllers own Flow ID choice and request IDs. Flows own durable state and
  decisions. Steps do bounded external work.
- Dependencies (HTTP clients, repositories, an `Arc<Client>`) are fields of the
  Step structs, set in the Flow's constructor.

## Registration rules

- A Flow implements `Flow` with `type StartInput`. A Step implements `Step`
  with `type Input`.
- The Flow owns its Step values as fields. `steps()` borrows them:
  `StepList::start(&self.first).and(&self.second)`. List every Step the Flow
  can reach: decision targets, RPC movements, recovery Steps, and fallback
  movements. An RPC-only Flow keeps the default empty list; an RPC-driven Flow
  that still has Steps uses `StepList::empty().and(&self.work)`.
- `persistence()` returns `PersistenceSchema::new().attribute(&X).attribute_map(&Y).channel(&Z).channel_map(&W).stream(&S)`
  with every definition the Flow uses.
- `rpcs()` returns an `RpcList` binding each RPC const to a method of the Flow.
- `timeout_handler()` returns `Some(Self::handle_timeout)` when the Flow has one.
- Build the Registry with chained `register` calls and propagate the
  `SdkResult`:

```rust
pub fn create_registry() -> SdkResult<Registry> {
    Registry::new()
        .register(OrderFlow::default())?
        .register(ShipmentFlow::default())
}
```

- Register every Flow the process starts or executes, including SubFlow
  children. Registration validates names, the Step graph, persistence
  declarations, loads, locks, and RPCs; treat failure as a startup failure.
- If a Step needs the `Client` (for example to call another Flow), build the
  Client's Registry first, then construct the Worker's Registry with the
  `Arc<Client>`. Do not wire it in after construction.

### Names are durable identifiers

| Name | Default | Rule |
| --- | --- | --- |
| Flow type | last segment of the Rust type name | Name the type after its domain, such as `OrderFlow`. Never `Flow`. The module path does not disambiguate two types with the same name. |
| Step type | last segment of the type name | Unique within the Flow. |
| RPC name | the string in `Rpc::new("...")` | Start with a verb: `GetOrderStatus`, `ApproveOrder`. |
| Attribute, Channel, Stream names | the `new` argument | Stable strings, unique within the Flow. Use maps for dynamic keys. |

Open Flows keep running against new code. Renaming a type changes its default
Flow or Step type. Override `flow_type()` or `step_type()` with the old name,
or keep the old type registered while open Flows can reach it. The same holds
for definition names, RPC names, and serde field names. See
[../core/application.md](../core/application.md) for versioning.

### Schema definitions

Define every Attribute, AttributeMap, Channel, ChannelMap, and Stream as a
module-level `static LazyLock<T>`. Define each RPC as a `pub const`.

```rust
pub static ORDER_STATUS: LazyLock<Attribute<String>> = LazyLock::new(|| {
    Attribute::new("order-status").indexed(AttributeIndex::keyword().with_key("keyword1"))
});
pub static ORDER_NOTES: LazyLock<AttributeMap<String>> =
    LazyLock::new(|| AttributeMap::new("order-notes"));
pub static ORDER_APPROVALS: LazyLock<Channel<String>> =
    LazyLock::new(|| Channel::new("order-approvals"));

pub const APPROVE_ORDER: Rpc<String, ()> = Rpc::new("ApproveOrder");
pub const GET_ORDER_STATUS: Rpc<(), String> = Rpc::new("GetOrderStatus");
```

- Never build a definition in a function that returns a fresh value on each
  call. Reuse the static.
- Clone a definition only when an owned field truly needs it. Never clone
  durable data to avoid ownership questions.
- Builder calls (`indexed`, `sync_to_attribute_store`, `hard_delete_on_null`)
  consume and return the definition; put them inside the `LazyLock`.

## Types and serialization

- Every value that crosses Dex (Flow input, Step input, Attribute value,
  Channel message, RPC input and output, Stream message, output) implements
  `Serialize + DeserializeOwned + Send + Sync + 'static`. Derive
  `Clone, Debug, Deserialize, Serialize` on DTOs.
- Use owned types (`String`, `Vec<T>`, owned structs) at durable boundaries.
  Never put handles, clients, sockets, or references in them.
- Use `()` for no input or no output, with the `*_without_input` and
  `procedure*` registrations. Do not wrap it in `Option<()>`.
- Keep serialized field names stable while open Flows can decode them. Use
  `#[serde(rename = "...")]` to keep a wire name when you rename a field, and
  `#[serde(default)]` when you add one.
- Remove a value with `delete`. A value that serializes to JSON `null`
  also deletes, and a `null` completion output is discarded. Return an
  explicit result type when "no output" and "empty result" must differ.

## Worker and Client bootstrap

One process usually hosts one Worker, one Client, and one shared
`Arc<BlobCache>`. Every Worker is a pull Worker: it opens outbound connections
to the Dex Server and needs no inbound port.

```rust
pub async fn start_dex(
    server_address: &str,
    cache_directory: PathBuf,
) -> Result<DexRuntime, Box<dyn std::error::Error>> {
    let cache = Arc::new(BlobCache::open(BlobCacheConfig::new(
        cache_directory,
        64 * 1024 * 1024,
        10_000,
    )?)?);
    let registry = create_registry()?;
    let worker = Arc::new(Worker::try_new(
        registry.clone(),
        Arc::clone(&cache),
        WorkerOptions::new()
            .server_address(server_address)
            .pull(PullWorkerOptions::new().max_concurrent_tasks(16)),
    )?);
    let client = Arc::new(Client::try_new(
        registry,
        Arc::clone(&cache),
        ClientOptions::new().server_address(server_address),
    )?);
    let running_worker = Arc::clone(&worker);
    let worker_thread = thread::spawn(move || running_worker.start());
    let registered: Result<(), Box<dyn std::error::Error>> =
        match tokio::time::timeout(Duration::from_secs(30), worker.wait_pull_registered()).await {
            Ok(result) => result.map_err(Into::into),
            Err(elapsed) => Err(elapsed.into()),
        };
    if let Err(error) = registered {
        worker.stop();
        if let Err(start_error) = worker_thread.join().expect("Worker thread panicked") {
            tracing::error!(error = %start_error, "pull Worker stopped");
        }
        return Err(error);
    }
    Ok(DexRuntime { client, worker, worker_thread: Some(worker_thread), cache })
}
```

Shutdown: `worker.stop()`, join the Worker thread and check its
`SdkResult`, then `cache.close()`.

Rules:

- `Worker::start()` blocks until the Worker stops. Call it once, on a
  dedicated OS thread, and keep the `JoinHandle`. A start error must fail the
  service.
- A Worker starts only once. To replace or restart one, build a new Worker
  over the same Registry and cache, start it, and wait for its registration.
- `wait_pull_registered()` is async. Wrap it in `tokio::time::timeout` and
  await it before reporting the service ready. Outside an async context, build
  a current-thread Tokio runtime and `block_on` it.
- `worker.pull_registration_state().is_ready` is the readiness probe.
- `stop()` lets running handlers finish and send their results.
- Prefer `Worker::try_new` and `Client::try_new`; `new` panics on invalid
  options.
- Share one `Arc<BlobCache>` between the Worker and Client. Never open one per
  request.
- `max_concurrent_tasks` bounds parallel handlers in this process. Optional
  reserved RPC slots keep RPCs responsive while Steps run. Sizing, readiness,
  and shutdown details are in the
  [Rust Workers reference](https://docs.superdurable.io/reference/rust/workers).
- Install a `tracing` subscriber at startup, such as
  `tracing_subscriber::fmt().init()`. The SDK reports Worker errors and
  warnings only through `tracing`; without a subscriber they are invisible.

### Blocking calls

- Step and RPC handlers are synchronous functions. Do blocking I/O directly,
  bounded by the Step method timeout. Never spawn a future from a handler and
  drop it.
- Every `Client` method blocks the calling thread. In an async server, call it through
  `tokio::task::spawn_blocking`, never directly on an executor thread.
  `tokio::task::block_in_place` panics on a current-thread runtime, which is
  the `#[tokio::test]` default, so use it only on a multi-thread runtime. The examples use a `run_blocking` helper in
  [http_response.rs](https://github.com/superdurable/dex/blob/main/examples/rust/src/server/http_response.rs).

## Pull Workers and Dex credentials

- The Worker and Client take the same Dex Server address. There is no Worker
  bind address and no Worker routing configuration.
- A Worker serves only the Flow types in its Registry. During a rolling
  deploy, old and new Workers serve the same Flow type at once, so every
  change must stay compatible with open Flows.
- When the Server requires authentication, set `credential(Arc<dyn Credential>)`
  on both `ClientOptions` and `WorkerOptions`. Use `FileCredential` for a
  rotated token file, or `ApiKeyCredential::builder(token_url, server_id,
  api_key)` when the application holds an API key. Share one `Arc`.
- The API-key credential performs its first token exchange in
  `build().await`, so a bad key fails before the Client or Worker exists. It
  refreshes on a background Tokio task; keep that runtime alive and running
  while the credential is in use. It needs the crate's default API-key feature.
- TLS and credential options, refresh, and error behavior are in the
  [Rust Developer reference](https://docs.superdurable.io/reference/rust/) and
  the [Server authentication reference](https://docs.superdurable.io/reference/server/authentication).
- Read [../core/application.md](../core/application.md) for routing, retries
  across Worker restarts, heartbeats, and shutdown guarantees.

## What Rust does not have

- There is no Rust Connector SDK. Do not invent a loader for Dex Web Connector
  configuration, and never put provider credentials in Flow input or an
  ordinary Attribute. Keep them behind the application's secret management. A
  short-lived secret the Flow itself must hold belongs in a hard-delete
  Attribute. See [../core/connector.md](../core/connector.md).
- The FDG 2.0 management metadata (Summary, Display, Action, Indexed
  Attribute, group, explanation) is read from Go source only. Do not invent
  Rust attributes for it. Rust Flows still work with Dex Web, the CLI, and the
  Client.
- The SDK does not propagate application tracing spans. Do not claim it does.

## Rust gotchas

- `StepDecision::go_to(&ChargePayment, input)` uses the Step value only for its
  type. A fresh unit-struct value works, but the type must be in `steps()`.
- Never store `&mut Context`, a `BufferedTextStream`, or loaded values in a
  Flow or Step. They belong to one invocation.
- `SdkError`, `ErrorSubStatus`, and `StepAttemptProgress` are
  `#[non_exhaustive]`. Every `match` needs a wildcard arm.
- There is no `From<SdkError>` for `HandlerError`. Map with
  `.map_err(HandlerError::from_error)?`, for example after `SubFlow::run`.
- `thread::sleep` in a handler holds a Worker slot. Use a Timer for business
  time that can be long.

## Self-check

- [ ] Flow type named after its domain; RPC names start with a verb.
- [ ] Definitions are `static LazyLock`; RPCs are `const`.
- [ ] Every reachable Step in `steps()`; every definition in `persistence()`.
- [ ] Durable values are owned serde types with stable field names.
- [ ] Worker on a dedicated thread; startup waits for registration and fails on error.
- [ ] Client calls stay off async executor threads.
- [ ] A `tracing` subscriber is installed.
- [ ] One shared `Arc<BlobCache>`; Worker stopped and joined, cache closed.
- [ ] External effects in Steps are idempotent (see [error-handling.md](error-handling.md)).
- [ ] Integration tests run against a real Dex Server (see [testing.md](testing.md)).
