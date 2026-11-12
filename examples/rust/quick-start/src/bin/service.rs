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

//! The Dex Quick Start in Rust: an order Flow that charges, waits on a 60-second Timer, and ships.
//!
//! Serves POST /orders and runs the Worker in the same process. Stop it with Ctrl+C.

use std::error::Error;
use std::sync::Arc;
use std::thread;
use std::time::Duration;

use dex_quick_start::config::{QuickStartConfig, open_blob_cache};
use dex_quick_start::order_flow::OrderFlow;
use dex_quick_start::order_service::{OrderService, order_routes};
use dex_sdk::{Client, ClientOptions, Registry, Worker, WorkerOptions};

fn main() -> Result<(), Box<dyn Error>> {
    let config = QuickStartConfig::from_environment();
    let order_flow = OrderFlow::new(config.new_shop_api());
    let worker_registry = Registry::new().register(order_flow.clone())?;
    let client_registry = Registry::new().register(order_flow.clone())?;
    let worker = Arc::new(Worker::try_new(
        worker_registry,
        open_blob_cache(".dex-blob-cache/worker")?,
        WorkerOptions::new().server_address(&config.server_address),
    )?);
    let client = Arc::new(Client::try_new(
        client_registry,
        open_blob_cache(".dex-blob-cache/client")?,
        ClientOptions::new().server_address(&config.server_address),
    )?);
    let running_worker = Arc::clone(&worker);
    let worker_thread = thread::spawn(move || running_worker.start());

    let runtime = tokio::runtime::Builder::new_multi_thread()
        .enable_all()
        .build()?;
    let outcome = runtime.block_on(async {
        tokio::time::timeout(Duration::from_secs(30), worker.wait_pull_registered()).await??;
        let listener = tokio::net::TcpListener::bind(("127.0.0.1", config.http_port)).await?;
        println!(
            "Serving POST /orders on port {} (process {}).",
            config.http_port,
            std::process::id()
        );
        let routes = order_routes(OrderService { client, order_flow });
        axum::serve(listener, routes)
            .with_graceful_shutdown(async {
                if tokio::signal::ctrl_c().await.is_ok() {
                    println!("Stopping the service.");
                }
            })
            .await?;
        Ok::<(), Box<dyn Error>>(())
    });

    worker.stop();
    let stopped = worker_thread
        .join()
        .map_err(|_| "the Worker thread panicked")?;
    outcome?;
    Ok(stopped?)
}
