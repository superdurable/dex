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
use dex_examples_rust::patterns::recovery::FailureRecoveryFlow;
use dex_examples_rust::workflow::engagement::{
    ENGAGEMENT_ACCEPT, ENGAGEMENT_DESCRIBE, EngagementFlow, EngagementRequest, EngagementStatus,
};
use dex_examples_rust::workflow::microservices::{
    ORCHESTRATION_READY, ORCHESTRATION_SWAP, OrchestrationFlow,
};
use dex_examples_rust::workflow::money_transfer::{MoneyTransferFlow, TransferRequest};
use dex_examples_rust::workflow::polling::{POLLING_COMPLETE_TASK, PollingFlow};
use dex_examples_rust::workflow::subscription::{
    SUBSCRIPTION_CANCEL, SUBSCRIPTION_DESCRIBE, SUBSCRIPTION_UPDATE_CHARGE, SubscriptionFlow,
    SubscriptionRequest, SubscriptionState,
};
use dex_sdk::{
    Attribute, BlobCache, BlobCacheConfig, Client, ClientOptions, FlowStatus, SdkResult, Worker,
    WorkerOptions,
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

    fn await_engagement_status(&self, flow_id: &str, expected: &str) -> EngagementStatus {
        let deadline = Instant::now() + Duration::from_secs(20);
        while Instant::now() < deadline {
            let status = self
                .client
                .invoke_rpc_without_input(flow_id, ENGAGEMENT_DESCRIBE)
                .expect("describe Rust Engagement Flow");
            if status.status == expected {
                return status;
            }
            thread::yield_now();
        }
        panic!("Rust Engagement Flow did not reach {expected}");
    }

    fn await_subscription_charge(&self, flow_id: &str, expected: i64) -> SubscriptionState {
        let deadline = Instant::now() + Duration::from_secs(20);
        while Instant::now() < deadline {
            let state = self
                .client
                .invoke_rpc_without_input(flow_id, SUBSCRIPTION_DESCRIBE)
                .expect("describe Rust Subscription Flow");
            if state.charge_cents == expected {
                return state;
            }
            thread::yield_now();
        }
        panic!("Rust Subscription Flow charge did not reach {expected}");
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
    let flow_id = unique_flow_id("money-transfer");
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
        .expect("complete Rust Money Transfer Flow")
        .single_output()
        .expect("decode Rust Money Transfer output");
    assert_eq!(output.from_account, "released-sdk-source");
    assert_eq!(output.to_account, "released-sdk-destination");
    assert_eq!(output.amount_cents, 4_200);
    assert_eq!(
        environment
            .client
            .describe_flow(&flow_id)
            .expect("describe completed Rust Money Transfer Flow")
            .status,
        FlowStatus::Completed
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn engagement_invokes_rpcs_and_completes() {
    let environment = DexEnvironment::start();
    let flow = EngagementFlow::default();
    let flow_id = unique_flow_id("engagement");
    let run_id = environment
        .client
        .start_flow(
            &flow,
            &flow_id,
            EngagementRequest {
                employer_id: "released-sdk-employer".to_string(),
                candidate_id: "released-sdk-candidate".to_string(),
            },
        )
        .expect("start Rust Engagement Flow");
    assert!(!run_id.is_empty());

    let pending = environment.await_engagement_status(&flow_id, "pending");
    assert!(pending.notes.is_empty());
    environment
        .client
        .invoke_rpc(
            &flow_id,
            ENGAGEMENT_ACCEPT,
            "accepted in integration test".to_string(),
        )
        .expect("accept Rust Engagement Flow");
    environment
        .client
        .wait_for_flow_with_timeout(&flow_id, Duration::from_secs(30))
        .expect("complete Rust Engagement Flow");
    assert_eq!(
        environment
            .client
            .describe_flow(&flow_id)
            .expect("describe completed Rust Engagement Flow")
            .status,
        FlowStatus::Completed
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn microservice_swaps_data_and_completes_when_ready() {
    let environment = DexEnvironment::start();
    let flow = OrchestrationFlow::default();
    let flow_id = unique_flow_id("microservice");
    let run_id = environment
        .client
        .start_flow(&flow, &flow_id, "initial-data".to_string())
        .expect("start Rust Microservice Flow");
    assert!(!run_id.is_empty());

    let data = Attribute::<String>::new("orchestration-data");
    environment
        .client
        .wait_for_attribute_equal(
            &flow_id,
            &data,
            "initial-data".to_string(),
            Duration::from_secs(20),
        )
        .expect("wait for initial Rust Microservice data");
    let previous = environment
        .client
        .invoke_rpc(&flow_id, ORCHESTRATION_SWAP, "updated-data".to_string())
        .expect("swap Rust Microservice data");
    assert_eq!(previous, "initial-data");
    environment
        .client
        .wait_for_attribute_equal(
            &flow_id,
            &data,
            "updated-data".to_string(),
            Duration::from_secs(20),
        )
        .expect("wait for updated Rust Microservice data");
    environment
        .client
        .invoke_rpc_without_input::<()>(&flow_id, ORCHESTRATION_READY)
        .expect("release Rust Microservice Flow");
    environment
        .client
        .wait_for_flow_with_timeout(&flow_id, Duration::from_secs(30))
        .expect("complete Rust Microservice Flow");
}

#[test]
#[ignore = "requires dexcli dev"]
fn polling_completes_all_tasks() {
    let environment = DexEnvironment::start();
    let flow = PollingFlow::default();
    let flow_id = unique_flow_id("polling");
    let run_id = environment
        .client
        .start_flow(&flow, &flow_id, 1)
        .expect("start Rust Polling Flow");
    assert!(!run_id.is_empty());
    environment
        .client
        .invoke_rpc(&flow_id, POLLING_COMPLETE_TASK, "a".to_string())
        .expect("complete Rust Polling task a");
    environment
        .client
        .invoke_rpc(&flow_id, POLLING_COMPLETE_TASK, "b".to_string())
        .expect("complete Rust Polling task b");

    let result = environment
        .client
        .wait_for_flow_with_timeout(&flow_id, Duration::from_secs(30))
        .expect("complete Rust Polling Flow");
    assert_eq!(result.status(), FlowStatus::Completed);
    let output: String = result
        .completions()
        .last()
        .expect("polling Flow has completions")
        .decode()
        .expect("decode Rust Polling output");
    assert_eq!(output, "task-c");
    assert_eq!(
        environment
            .client
            .describe_flow(&flow_id)
            .expect("describe completed Rust Polling Flow")
            .status,
        FlowStatus::Completed
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn subscription_updates_charge_and_cancels() {
    let environment = DexEnvironment::start();
    let flow = SubscriptionFlow::default();
    let flow_id = unique_flow_id("subscription");
    let run_id = environment
        .client
        .start_flow(
            &flow,
            &flow_id,
            SubscriptionRequest {
                customer_id: "released-sdk-customer".to_string(),
                charge_cents: 100,
                billing_periods: 2,
            },
        )
        .expect("start Rust Subscription Flow");
    assert!(!run_id.is_empty());

    let initial = environment.await_subscription_charge(&flow_id, 100);
    assert_eq!(initial.periods_charged, 0);
    assert!(!initial.cancelled);
    environment
        .client
        .invoke_rpc(&flow_id, SUBSCRIPTION_UPDATE_CHARGE, 250)
        .expect("update Rust Subscription charge");
    let updated = environment.await_subscription_charge(&flow_id, 250);
    assert!(!updated.cancelled);
    environment
        .client
        .invoke_rpc_without_input::<()>(&flow_id, SUBSCRIPTION_CANCEL)
        .expect("cancel Rust Subscription Flow");

    let output: SubscriptionState = environment
        .client
        .wait_for_flow_with_timeout(&flow_id, Duration::from_secs(30))
        .expect("complete Rust Subscription Flow")
        .single_output()
        .expect("decode Rust Subscription output");
    assert_eq!(output.charge_cents, 250);
    assert_eq!(output.periods_charged, 0);
    assert!(output.cancelled);
}

#[test]
#[ignore = "requires dexcli dev"]
fn failure_recovery_retries_and_compensates() {
    let environment = DexEnvironment::start();
    let flow = FailureRecoveryFlow::default();
    let flow_id = unique_flow_id("failure-recovery");
    let run_id = environment
        .client
        .start_flow(&flow, &flow_id, "released-sdk-reservation".to_string())
        .expect("start Rust Failure Recovery Flow");
    assert!(!run_id.is_empty());

    let output: String = environment
        .client
        .wait_for_flow_with_timeout(&flow_id, Duration::from_secs(30))
        .expect("complete Rust Failure Recovery Flow")
        .single_output()
        .expect("decode Rust Failure Recovery output");
    assert_eq!(output, "compensated");
    assert_eq!(
        environment
            .client
            .describe_flow(&flow_id)
            .expect("describe completed Rust Failure Recovery Flow")
            .status,
        FlowStatus::Completed
    );
}

fn unique_flow_id(prefix: &str) -> String {
    let timestamp = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .expect("current time after Unix epoch")
        .as_nanos();
    format!("rust-examples-{prefix}-{}-{timestamp}", std::process::id())
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
