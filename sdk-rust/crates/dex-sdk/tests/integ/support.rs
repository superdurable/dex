// Portions of this file are derived from indeedeng/iwf-java-sdk.
// Those portions are licensed under the Apache License, Version 2.0.
// See LICENSES/Apache-2.0.txt and LEGACY_NOTICES.md.
//
// Modifications Copyright (c) 2026 Super Durable, Inc.
//
// Modifications are licensed under the Super Durable Source License 1.0.
// Third-Party Materials remain under the Apache License, Version 2.0.
// See LICENSE and LEGACY_NOTICES.md.

use std::net::{TcpListener, TcpStream};
use std::sync::{Arc, Mutex};
use std::thread::{self, JoinHandle};
use std::time::{Duration, Instant};

use dex_sdk::{
    BlobCache, BlobCacheConfig, Client, ClientOptions, Registry, SdkResult, Worker, WorkerOptions,
};
use tempfile::TempDir;

pub(crate) struct DexDevTestEnvironment {
    pub(crate) client: Client,
    worker: Arc<Worker>,
    worker_thread: Option<JoinHandle<()>>,
    worker_failure: Arc<Mutex<Option<String>>>,
    cache: Arc<BlobCache>,
    _cache_directory: TempDir,
}

impl DexDevTestEnvironment {
    pub(crate) fn start(registry: SdkResult<Registry>) -> Self {
        let registry = registry.expect("register integration test Flow");
        let server_address =
            std::env::var("DEX_SERVER_ADDRESS").unwrap_or_else(|_| "127.0.0.1:8801".to_string());
        let worker_port = available_port();
        let worker_address = format!("127.0.0.1:{worker_port}");
        let cache_directory = tempfile::tempdir().expect("create Rust SDK test cache directory");
        let cache = Arc::new(
            BlobCache::open(
                BlobCacheConfig::new(cache_directory.path(), 64 * 1024 * 1024, 10_000)
                    .expect("valid Rust SDK test cache config"),
            )
            .expect("open Rust SDK test cache"),
        );
        let worker = Arc::new(Worker::new(
            registry.clone(),
            Arc::clone(&cache),
            WorkerOptions::new()
                .bind_address(&worker_address)
                .server_address(&server_address),
        ));
        let worker_failure = Arc::new(Mutex::new(None));
        let thread_worker = Arc::clone(&worker);
        let thread_failure = Arc::clone(&worker_failure);
        let worker_thread = thread::Builder::new()
            .name("dex-rust-integration-worker".to_string())
            .spawn(move || {
                if let Err(error) = thread_worker.start() {
                    *thread_failure.lock().expect("worker failure lock") = Some(error.to_string());
                }
            })
            .expect("start Rust integration Worker thread");
        await_worker(worker_port, &worker_failure);
        let client = Client::new(
            registry,
            Arc::clone(&cache),
            ClientOptions::new()
                .server_address(server_address)
                .worker_target(worker.worker_target().clone()),
        );
        client.health_check().expect("Dex server health check");
        Self {
            client,
            worker,
            worker_thread: Some(worker_thread),
            worker_failure,
            cache,
            _cache_directory: cache_directory,
        }
    }
}

impl Drop for DexDevTestEnvironment {
    fn drop(&mut self) {
        self.worker.stop();
        if let Some(worker_thread) = self.worker_thread.take() {
            worker_thread.join().expect("join Rust integration Worker");
        }
        self.cache.close().expect("close Rust SDK test cache");
        if let Some(failure) = self
            .worker_failure
            .lock()
            .expect("worker failure lock")
            .take()
        {
            panic!("Rust integration Worker failed: {failure}");
        }
    }
}

pub(crate) fn flow_id(prefix: &str) -> String {
    format!("{prefix}-{}", uuid::Uuid::new_v4())
}

fn available_port() -> u16 {
    TcpListener::bind("127.0.0.1:0")
        .expect("bind an available Worker port")
        .local_addr()
        .expect("read available Worker port")
        .port()
}

fn await_worker(worker_port: u16, failure: &Mutex<Option<String>>) {
    let deadline = Instant::now() + Duration::from_secs(10);
    while Instant::now() < deadline {
        if let Some(failure) = failure.lock().expect("worker failure lock").as_ref() {
            panic!("Rust integration Worker failed: {failure}");
        }
        if TcpStream::connect(("127.0.0.1", worker_port)).is_ok() {
            return;
        }
        thread::yield_now();
    }
    panic!("Rust integration Worker did not become ready");
}
