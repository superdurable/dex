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

use std::sync::Arc;
use std::thread;

use dex_examples_rust::server::build_router;
use dex_examples_rust::{create_example_registry, create_worker_registry};
use dex_sdk::{
    BlobCache, BlobCacheConfig, Client, ClientOptions, Worker, WorkerOptions, WorkerTarget,
};

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let server_address =
        std::env::var("DEX_SERVER_ADDRESS").unwrap_or_else(|_| "127.0.0.1:8801".to_string());
    let worker_bind_address =
        std::env::var("DEX_WORKER_BIND_ADDRESS").unwrap_or_else(|_| "127.0.0.1:8803".to_string());
    let http_address =
        std::env::var("DEX_EXAMPLES_HTTP_ADDRESS").unwrap_or_else(|_| "127.0.0.1:8080".to_string());
    let cache_directory = std::env::var("DEX_BLOB_CACHE_DIR")
        .unwrap_or_else(|_| "/tmp/dex-rust-examples-blobs".to_string());

    let cache = Arc::new(BlobCache::open(BlobCacheConfig::new(
        cache_directory,
        64 * 1024 * 1024,
        10_000,
    )?)?);
    let worker_target = WorkerTarget::new(worker_bind_address.clone());
    let client = Arc::new(Client::try_new(
        create_example_registry()?,
        Arc::clone(&cache),
        ClientOptions::new()
            .server_address(server_address.clone())
            .worker_target(worker_target.clone()),
    )?);
    let worker = Worker::try_new(
        create_worker_registry(Arc::clone(&client))?,
        Arc::clone(&cache),
        WorkerOptions::new()
            .server_address(server_address.clone())
            .bind_address(worker_bind_address.clone())
            .worker_target(worker_target),
    )?;
    let worker_handle = thread::spawn(move || worker.start());

    let router = build_router(client);
    let listener = tokio::net::TcpListener::bind(&http_address).await?;
    println!("Dex Rust examples listening on http://{http_address} (worker {worker_bind_address})");
    axum::serve(listener, router).await?;

    if let Err(error) = worker_handle.join().expect("worker thread panicked") {
        return Err(error.into());
    }
    cache.close()?;
    Ok(())
}
