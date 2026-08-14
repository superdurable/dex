// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

use std::net::{TcpListener, TcpStream};
use std::sync::Arc;
use std::thread::{self, JoinHandle};
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

use dex_examples_rust::create_example_registry;
use dex_examples_rust::workflow::money_transfer::{MoneyTransferFlow, TransferRequest};
use dex_sdk::{
    BlobCache, BlobCacheConfig, Client, ClientOptions, SdkResult, Worker, WorkerOptions,
};
use tempfile::TempDir;

struct DexEnvironment {
    client: Client,
    worker: Arc<Worker>,
    worker_thread: Option<JoinHandle<SdkResult<()>>>,
    cache: Arc<BlobCache>,
    _cache_directory: TempDir,
}

impl DexEnvironment {
    fn start() -> Self {
        let registry = create_example_registry().expect("register Rust examples");
        let server_address =
            std::env::var("DEX_SERVER_ADDRESS").unwrap_or_else(|_| "127.0.0.1:8801".to_string());
        let worker_port = available_worker_port();
        let worker_address = format!("127.0.0.1:{worker_port}");
        let cache_directory = tempfile::tempdir().expect("create Rust examples cache directory");
        let cache = Arc::new(
            BlobCache::open(
                BlobCacheConfig::new(cache_directory.path(), 64 * 1024 * 1024, 10_000)
                    .expect("valid Rust examples cache config"),
            )
            .expect("open Rust examples cache"),
        );
        let worker = Arc::new(
            Worker::try_new(
                registry.clone(),
                Arc::clone(&cache),
                WorkerOptions::new()
                    .server_address(&server_address)
                    .bind_address(&worker_address),
            )
            .expect("create Rust examples Worker"),
        );
        let thread_worker = Arc::clone(&worker);
        let worker_thread = thread::Builder::new()
            .name("dex-rust-examples-worker".to_string())
            .spawn(move || thread_worker.start())
            .expect("start Rust examples Worker thread");
        await_worker(worker_port, &worker_thread);
        let client = Client::try_new(
            registry,
            Arc::clone(&cache),
            ClientOptions::new()
                .server_address(server_address)
                .worker_target(worker.worker_target().clone()),
        )
        .expect("create Rust examples Client");
        client.health_check().expect("Dex health check");
        Self {
            client,
            worker,
            worker_thread: Some(worker_thread),
            cache,
            _cache_directory: cache_directory,
        }
    }
}

impl Drop for DexEnvironment {
    fn drop(&mut self) {
        self.worker.stop();
        if let Some(worker_thread) = self.worker_thread.take() {
            worker_thread
                .join()
                .expect("join Rust examples Worker")
                .expect("stop Rust examples Worker");
        }
        self.cache.close().expect("close Rust examples cache");
    }
}

#[test]
#[ignore = "requires dexcli dev"]
fn money_transfer_completes_with_released_sdk() {
    let environment = DexEnvironment::start();
    let flow = MoneyTransferFlow::default();
    let flow_id = unique_flow_id();
    let input = TransferRequest {
        from_account: "released-sdk-source".to_string(),
        to_account: "released-sdk-destination".to_string(),
        amount_cents: 4_200,
        notes: "examples/rust integration".to_string(),
    };
    environment
        .client
        .start_flow(&flow, &flow_id, input)
        .expect("start Rust Money Transfer Flow");
    let output: TransferRequest = environment
        .client
        .wait_for_flow_with_timeout(&flow_id, Duration::from_secs(30))
        .expect("complete Rust Money Transfer Flow");
    assert_eq!(output.from_account, "released-sdk-source");
    assert_eq!(output.to_account, "released-sdk-destination");
    assert_eq!(output.amount_cents, 4_200);
}

fn unique_flow_id() -> String {
    let timestamp = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .expect("current time after Unix epoch")
        .as_nanos();
    format!(
        "rust-examples-money-transfer-{}-{timestamp}",
        std::process::id()
    )
}

fn available_worker_port() -> u16 {
    TcpListener::bind("127.0.0.1:0")
        .expect("bind an available Worker port")
        .local_addr()
        .expect("read available Worker port")
        .port()
}

fn await_worker(worker_port: u16, worker_thread: &JoinHandle<SdkResult<()>>) {
    let deadline = Instant::now() + Duration::from_secs(10);
    while Instant::now() < deadline {
        assert!(
            !worker_thread.is_finished(),
            "Rust examples Worker exited before becoming ready"
        );
        if TcpStream::connect(("127.0.0.1", worker_port)).is_ok() {
            return;
        }
        thread::yield_now();
    }
    panic!("Rust examples Worker did not become ready");
}
