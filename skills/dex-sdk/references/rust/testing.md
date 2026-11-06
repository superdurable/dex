# Testing Rust Dex applications

Read [../core/testing.md](../core/testing.md) for what to test and why. Durable
behavior needs a real Dex Server: start one locally with `dexcli dev`. Unit
tests cover only pure helpers and serde mapping; they cannot prove commit
boundaries, retries, Worker replacement, Channel delivery, RPC locks, Timers,
or terminal behavior.

## Harness

Build the production Registry, a temporary `BlobCache`, a Worker on its own
thread, and a Client. Tear them down in `Drop`.

```rust
struct DexTestEnvironment {
    client: Client,
    worker: Arc<Worker>,
    worker_thread: Option<JoinHandle<SdkResult<()>>>,
    cache: Arc<BlobCache>,
    _cache_directory: TempDir,
}

impl DexTestEnvironment {
    fn start() -> Self {
        let server_address =
            std::env::var("DEX_SERVER_ADDRESS").unwrap_or_else(|_| "127.0.0.1:8801".to_string());
        let cache_directory = tempfile::tempdir().expect("create cache directory");
        let cache = Arc::new(
            BlobCache::open(
                BlobCacheConfig::new(cache_directory.path(), 64 * 1024 * 1024, 10_000)
                    .expect("valid cache config"),
            )
            .expect("open cache"),
        );
        let registry = create_registry().expect("register Flows");
        let worker = Arc::new(
            Worker::try_new(
                registry.clone(),
                Arc::clone(&cache),
                WorkerOptions::new()
                    .server_address(&server_address)
                    .pull(PullWorkerOptions::new().max_concurrent_tasks(8)),
            )
            .expect("create Worker"),
        );
        let running_worker = Arc::clone(&worker);
        let worker_thread = thread::spawn(move || running_worker.start());
        tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .expect("create registration runtime")
            .block_on(async {
                tokio::time::timeout(Duration::from_secs(30), worker.wait_pull_registered()).await
            })
            .expect("Worker registered before the timeout")
            .expect("Worker registration");
        let client = Client::try_new(
            registry,
            Arc::clone(&cache),
            ClientOptions::new().server_address(server_address),
        )
        .expect("create Client");
        Self { client, worker, worker_thread: Some(worker_thread), cache, _cache_directory: cache_directory }
    }
}

impl Drop for DexTestEnvironment {
    fn drop(&mut self) {
        self.worker.stop();
        if let Some(worker_thread) = self.worker_thread.take() {
            worker_thread.join().expect("join Worker").expect("stop Worker");
        }
        self.cache.close().expect("close cache");
    }
}
```

A test:

```rust
#[test]
#[ignore = "requires dexcli dev"]
fn approved_order_is_charged() {
    let environment = DexTestEnvironment::start();
    let flow_id = unique_flow_id("order");
    let request = OrderRequest { order_id: flow_id.clone(), amount_cents: 1200 };
    environment
        .client
        .start_flow(&OrderFlow::default(), &flow_id, request)
        .expect("start order");
    environment
        .client
        .invoke_rpc(&flow_id, APPROVE_ORDER, "alice".to_string())
        .expect("approve order");
    let result = environment
        .client
        .wait_for_flow_with_timeout(&flow_id, Duration::from_secs(45))
        .expect("order reaches a terminal status");
    assert_eq!(result.status(), FlowStatus::Completed);
    let output: String = result.single_output().expect("decode output");
    assert_eq!(output, flow_id);
}
```

- Tests may `expect` and `unwrap`; production code may not.
- Give every test a unique Flow ID; never reuse a fixed ID.
- Use `tempfile::TempDir` for the cache; keep it alive as long as the cache.
- Register every Flow the tests start, including SubFlow children.
- Replace external services with fakes injected into the Step structs. Keep
  the Dex Server real.
- Real-server tests in the Dex examples carry `#[ignore = "requires dexcli dev"]`
  and run with `cargo test --test <name> -- --ignored --test-threads=1` from a
  script that starts the server first. Never skip them silently in CI; start
  the server in the CI job.

## Waiting in tests

Wait on Dex state with a deadline. Never use a fixed sleep as the assertion.

| Wait for | Use |
| --- | --- |
| Terminal result | `wait_for_flow_with_timeout`, then check `status()` |
| A Step's commit | `wait_for_step_completion(flow_id, StepExecutionId::of(&Step), options)` |
| A stored value | `wait_for_attribute_match(...)` with a request timeout |
| A computed state | poll a read-only RPC until a deadline |

```rust
fn await_order_status(&self, flow_id: &str, expected: &str) {
    let deadline = Instant::now() + Duration::from_secs(20);
    let mut last_status = String::new();
    while Instant::now() < deadline {
        last_status = self
            .client
            .invoke_rpc_without_input(flow_id, GET_ORDER_STATUS)
            .expect("read order status");
        if last_status == expected {
            return;
        }
        thread::sleep(Duration::from_millis(100));
    }
    panic!("order {flow_id} stayed at {last_status}, expected {expected}");
}
```

Report the last observed value in the panic message.

## Required scenarios

Cover the scenario list in [../core/testing.md](../core/testing.md). In
this language:

- Worker replacement: stop the Worker while the Flow waits or retries, start
  a new Worker with the same Registry, and assert progress continues.
- Timers: assert a generous bound, and use `client.skip_timer` to shorten long ones.
- Failure paths: make the fake dependency fail deterministically and assert
  the recovery Step or the terminal status.

## Running

- Start a local server with `dexcli dev`. A short-lived test server can use the
  faster development durability mode described in
  [../core/testing.md](../core/testing.md).
- Run `cargo fmt --check`, `cargo clippy --all-targets -- -D warnings`, and
  `cargo test`, then the ignored real-server tests.
- The Dex repository's examples show a full suite:
  [tests/dex_integration.rs](https://github.com/superdurable/dex/blob/main/examples/rust/tests/dex_integration.rs)
  and [run-integration-tests.sh](https://github.com/superdurable/dex/blob/main/examples/rust/run-integration-tests.sh).

## Checklist

- [ ] Tests use the production Registry and a real Dex Server.
- [ ] Unique Flow IDs and a temporary cache directory.
- [ ] Waits use Dex APIs or deadline polling, never fixed sleeps.
- [ ] Failure, recovery, Worker replacement, and terminal paths are covered.
- [ ] Assertions check `FlowStatus` before decoding output.
