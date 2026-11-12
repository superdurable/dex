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

use std::io::{Read, Write};
use std::net::TcpStream;
use std::path::Path;
use std::sync::{Arc, Mutex, MutexGuard};
use std::thread::{self, JoinHandle};
use std::time::{Duration, Instant};

use dex_quick_start::config::{QuickStartConfig, open_blob_cache};
use dex_quick_start::order_flow::{OrderFlow, SHIP_DELAY_TIMER};
use dex_quick_start::order_service::{OrderService, order_routes};
use dex_quick_start::shop_api::ShopApi;
use dex_sdk::{
    Client, ClientOptions, FlowStatus, Registry, SdkResult, StepExecutionId, TimerId,
    WaitForStepCompletionOptions, Worker, WorkerOptions,
};

const WAIT_TIMEOUT: Duration = Duration::from_secs(60);

/// Tests that run Workers share one Flow type, so they take turns.
static WORKER_TESTS: Mutex<()> = Mutex::new(());

fn one_worker_test_at_a_time() -> MutexGuard<'static, ()> {
    WORKER_TESTS
        .lock()
        .unwrap_or_else(std::sync::PoisonError::into_inner)
}

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
    let _turn = one_worker_test_at_a_time();
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
    assert_eq!(
        calls_for(&order_id, first_shop.charged()),
        std::slice::from_ref(&order_id)
    );
    assert!(calls_for(&order_id, first_shop.shipped()).is_empty());

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
    assert!(calls_for(&order_id, second_shop.charged()).is_empty());
    assert_eq!(calls_for(&order_id, second_shop.shipped()), [order_id]);
}

#[test]
fn shop_api_drops_a_repeated_key_across_processes() {
    let directory = tempfile::tempdir().unwrap();
    let keys_path = directory.path().join("keys");
    let first_process = ShopApi::with_keys_file(&keys_path, Duration::ZERO);
    let second_process = ShopApi::with_keys_file(&keys_path, Duration::ZERO);

    first_process.charge("order-1", "charge-order-1").unwrap();
    second_process.charge("order-1", "charge-order-1").unwrap();

    assert_eq!(first_process.charged(), ["order-1"]);
    assert!(second_process.charged().is_empty());
    assert_eq!(second_process.repeated_keys(), ["charge-order-1"]);
}

#[test]
fn service_starts_one_flow_for_a_repeated_request() {
    let _turn = one_worker_test_at_a_time();
    let directory = tempfile::tempdir().unwrap();
    let order_id = format!("order-{:016x}", fastrand::u64(..));
    let shop = ShopApi::default();
    let client_flow = OrderFlow::new(ShopApi::default());
    let Some(client) = connected_client(&client_flow, &directory.path().join("client")) else {
        return;
    };
    let client = Arc::new(client);
    let _worker = RunningWorker::start(
        OrderFlow::new(shop.clone()),
        &directory.path().join("worker"),
    );
    let runtime = tokio::runtime::Builder::new_multi_thread()
        .enable_all()
        .build()
        .unwrap();
    let listener = runtime
        .block_on(tokio::net::TcpListener::bind(("127.0.0.1", 0)))
        .unwrap();
    let port = listener.local_addr().unwrap().port();
    let routes = order_routes(OrderService {
        client: Arc::clone(&client),
        order_flow: client_flow.clone(),
    });
    let server = runtime.spawn(async move { axum::serve(listener, routes).await });

    for _ in 0..2 {
        let reply = post_order(port, &order_id);
        assert!(reply.starts_with("HTTP/1.1 200"), "reply: {reply}");
        assert!(
            reply.ends_with(&format!(r#"{{"charged":true,"flowId":"{order_id}"}}"#)),
            "reply: {reply}"
        );
    }
    server.abort();
    skip_ship_delay(&client, &client_flow, &order_id);
    client
        .wait_for_flow_with_timeout(&order_id, WAIT_TIMEOUT)
        .unwrap();
    assert_eq!(calls_for(&order_id, shop.charged()), [order_id]);
}

/// Sends POST /orders over a plain connection and returns the whole reply.
fn post_order(port: u16, order_id: &str) -> String {
    let body = format!(r#"{{"orderId": "{order_id}"}}"#);
    let mut stream = TcpStream::connect(("127.0.0.1", port)).expect("connect to the service");
    write!(
        stream,
        "POST /orders HTTP/1.1\r\nHost: 127.0.0.1\r\nContent-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}",
        body.len()
    )
    .expect("send the request");
    let mut reply = String::new();
    stream.read_to_string(&mut reply).expect("read the reply");
    reply
}

/// Keeps this test's order; a Flow left open by an earlier run can also reach a Worker.
fn calls_for(order_id: &str, calls: Vec<String>) -> Vec<String> {
    calls.into_iter().filter(|call| call == order_id).collect()
}

/// Skips the 60-second Timer once Ship waits on it.
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
