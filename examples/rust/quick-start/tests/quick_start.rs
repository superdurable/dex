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

//! Runs the Quick Start against a real Dex server.
//!
//! Point DEX_FLOW_SERVICE_ADDRESS at the server (default 127.0.0.1:8801). Without
//! a server the test fails, unless DEX_EXAMPLES_ALLOW_SKIP=1 asks it to skip.

use std::path::Path;
use std::sync::Arc;
use std::thread::{self, JoinHandle};
use std::time::{Duration, Instant};

use dex_quick_start::config::{QuickStartConfig, open_blob_cache};
use dex_quick_start::order_flow::{OrderFlow, SHIP_DELAY_TIMER};
use dex_quick_start::shop_api::ShopApi;
use dex_sdk::{
    Client, ClientOptions, FlowStatus, Registry, SdkResult, StepExecutionId, TimerId,
    WaitForStepCompletionOptions, Worker, WorkerOptions,
};

const WAIT_TIMEOUT: Duration = Duration::from_secs(60);

struct RunningWorker {
    worker: Arc<Worker>,
    thread: Option<JoinHandle<SdkResult<()>>>,
}

impl RunningWorker {
    fn start(order_flow: OrderFlow, cache_dir: &Path) -> Self {
        let config = QuickStartConfig::from_environment();
        let registry = Registry::new()
            .register(order_flow)
            .expect("register OrderFlow");
        let options = WorkerOptions::new().server_address(config.server_address);
        let worker = Arc::new(
            Worker::try_new(
                registry,
                open_blob_cache(cache_dir).expect("open the Worker cache"),
                options,
            )
            .expect("create the Worker"),
        );
        let running_worker = Arc::clone(&worker);
        let thread = thread::spawn(move || running_worker.start());
        let runtime = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .expect("create the registration runtime");
        runtime
            .block_on(async {
                tokio::time::timeout(Duration::from_secs(30), worker.wait_pull_registered()).await
            })
            .expect("the Worker registered in time")
            .expect("register the pull Worker");
        Self {
            worker,
            thread: Some(thread),
        }
    }
}

impl Drop for RunningWorker {
    fn drop(&mut self) {
        self.worker.stop();
        let Some(thread) = self.thread.take() else {
            return;
        };
        let stopped = thread.join();
        // A second panic while the test unwinds would abort and hide the first.
        if !thread::panicking() {
            stopped
                .expect("the Worker thread does not panic")
                .expect("stop the Worker");
        }
    }
}

/// Returns a Client, or None when there is no server and skipping is allowed.
fn connected_client(order_flow: &OrderFlow, cache_dir: &Path) -> Option<Client> {
    let config = QuickStartConfig::from_environment();
    let registry = Registry::new()
        .register(order_flow.clone())
        .expect("register OrderFlow");
    let options = ClientOptions::new().server_address(&config.server_address);
    let client = Client::try_new(
        registry,
        open_blob_cache(cache_dir).expect("open the Client cache"),
        options,
    )
    .expect("create the Client");
    let deadline = Instant::now() + Duration::from_secs(20);
    while client.health_check().is_err() {
        if Instant::now() >= deadline {
            let message = format!(
                "no Dex server at {}; set DEX_FLOW_SERVICE_ADDRESS",
                config.server_address
            );
            if std::env::var("DEX_EXAMPLES_ALLOW_SKIP").as_deref() == Ok("1") {
                eprintln!("skipped: {message}");
                return None;
            }
            panic!("{message}");
        }
        thread::sleep(Duration::from_millis(250));
    }
    Some(client)
}

#[test]
fn restarted_worker_ships_without_charging_again() {
    let directory = tempfile::tempdir().unwrap();
    let order_id = format!("order-{:016x}", fastrand::u64(..));
    let first_shop = ShopApi::default();
    let second_shop = ShopApi::default();
    let client_flow = OrderFlow::new(ShopApi::default());
    let Some(client) = connected_client(&client_flow, &directory.path().join("client")) else {
        return;
    };

    {
        let _worker = RunningWorker::start(
            OrderFlow::new(first_shop.clone()),
            &directory.path().join("worker-1"),
        );
        client
            .start_flow(&client_flow, &order_id, order_id.clone())
            .unwrap();
        client
            .wait_for_step_completion(
                &order_id,
                StepExecutionId::of(&client_flow.charge),
                WaitForStepCompletionOptions::new().request_timeout(WAIT_TIMEOUT),
            )
            .unwrap();
    }
    assert_eq!(first_shop.charged(), std::slice::from_ref(&order_id));
    assert!(first_shop.shipped().is_empty());

    let result = {
        let _worker = RunningWorker::start(
            OrderFlow::new(second_shop.clone()),
            &directory.path().join("worker-2"),
        );
        skip_ship_delay(&client, &client_flow, &order_id);
        client
            .wait_for_flow_with_timeout(&order_id, WAIT_TIMEOUT)
            .unwrap()
    };

    assert_eq!(result.status(), FlowStatus::Completed);
    assert!(second_shop.charged().is_empty());
    assert_eq!(second_shop.shipped(), [order_id]);
}

/// Skips the 30-second Timer once Ship waits on it.
fn skip_ship_delay(client: &Client, order_flow: &OrderFlow, order_id: &str) {
    let deadline = Instant::now() + WAIT_TIMEOUT;
    loop {
        let skipped = client.skip_timer(
            order_id,
            StepExecutionId::of(&order_flow.ship),
            TimerId::by_condition_id(SHIP_DELAY_TIMER),
        );
        match skipped {
            Ok(()) => return,
            Err(error) if Instant::now() >= deadline => panic!("skip the ship delay: {error}"),
            Err(_) => thread::sleep(Duration::from_millis(250)),
        }
    }
}
