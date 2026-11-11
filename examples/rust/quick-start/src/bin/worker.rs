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

//! The Dex Quick Start in Rust: an order Flow that charges, waits on a 30-second Timer, and ships.
//!
//! Runs the Worker that executes OrderFlow Steps. Stop it with Ctrl+C.

use std::error::Error;
use std::sync::Arc;
use std::thread;
use std::time::Duration;

use dex_quick_start::config::{QuickStartConfig, open_blob_cache};
use dex_quick_start::order_flow::OrderFlow;
use dex_quick_start::shop_api::ShopApi;
use dex_sdk::{Registry, Worker, WorkerOptions};

fn main() -> Result<(), Box<dyn Error>> {
    let config = QuickStartConfig::from_environment();
    let registry = Registry::new().register(OrderFlow::new(ShopApi::default()))?;
    let cache = open_blob_cache(".dex-blob-cache/worker")?;
    let options = WorkerOptions::new().server_address(config.server_address);
    let worker = Arc::new(Worker::try_new(registry, cache, options)?);
    let running_worker = Arc::clone(&worker);
    let worker_thread = thread::spawn(move || running_worker.start());

    let runtime = tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()?;
    let outcome = runtime.block_on(async {
        tokio::time::timeout(Duration::from_secs(30), worker.wait_pull_registered()).await??;
        println!("Worker is ready. Press Ctrl+C to stop it.");
        tokio::signal::ctrl_c().await?;
        println!("Stopping the Worker.");
        Ok::<(), Box<dyn Error>>(())
    });

    worker.stop();
    let stopped = worker_thread
        .join()
        .map_err(|_| "the Worker thread panicked")?;
    outcome?;
    Ok(stopped?)
}
