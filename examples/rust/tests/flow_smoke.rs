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

mod common;

use std::collections::HashMap;
use std::net::{TcpListener, TcpStream};
use std::sync::Arc;
use std::thread::{self, JoinHandle};
use std::time::{Duration, Instant};

use common::{
    FlowSmokeEntry, FlowSmokeFlags, FlowSmokeHttpClient, assert_flow_smoke_no_unexpected_failures,
    assert_flow_smoke_start_step, query, query_with,
};
use dex_examples_rust::create_example_registry;
use dex_examples_rust::server::build_router;
use dex_sdk::{
    BlobCache, BlobCacheConfig, Client, ClientOptions, SdkResult, Worker, WorkerOptions,
};
use tempfile::TempDir;

struct FlowSmokeEnvironment {
    _client: Arc<Client>,
    _worker: Arc<Worker>,
    _worker_thread: Option<JoinHandle<SdkResult<()>>>,
    _cache: Arc<BlobCache>,
    http_base_url: String,
    _server_thread: Option<JoinHandle<()>>,
    _cache_directory: TempDir,
}

impl FlowSmokeEnvironment {
    fn start() -> Self {
        let registry = create_example_registry().expect("register Rust examples");
        let server_address =
            std::env::var("DEX_SERVER_ADDRESS").unwrap_or_else(|_| "127.0.0.1:8801".to_string());
        let worker_port = available_worker_port();
        let worker_address = format!("127.0.0.1:{worker_port}");
        let cache_directory = tempfile::tempdir().expect("create Rust flow smoke cache directory");
        let cache = Arc::new(
            BlobCache::open(
                BlobCacheConfig::new(cache_directory.path(), 64 * 1024 * 1024, 10_000)
                    .expect("valid Rust flow smoke cache config"),
            )
            .expect("open Rust flow smoke cache"),
        );
        let worker = Arc::new(
            Worker::try_new(
                registry.clone(),
                Arc::clone(&cache),
                WorkerOptions::new()
                    .server_address(&server_address)
                    .bind_address(&worker_address),
            )
            .expect("create Rust flow smoke Worker"),
        );
        let thread_worker = Arc::clone(&worker);
        let worker_thread = thread::Builder::new()
            .name("dex-rust-flow-smoke-worker".to_string())
            .spawn(move || thread_worker.start())
            .expect("start Rust flow smoke Worker thread");
        await_worker(worker_port, &worker_thread);
        let client = Arc::new(
            Client::try_new(
                registry,
                Arc::clone(&cache),
                ClientOptions::new()
                    .server_address(server_address)
                    .worker_target(worker.worker_target().clone()),
            )
            .expect("create Rust flow smoke Client"),
        );
        client.health_check().expect("Dex health check");

        let http_port = available_worker_port();
        let router = build_router(Arc::clone(&client));
        let server_thread = thread::spawn(move || {
            let runtime = tokio::runtime::Builder::new_multi_thread()
                .enable_all()
                .build()
                .expect("create flow smoke HTTP runtime");
            runtime.block_on(async move {
                let listener = tokio::net::TcpListener::bind(format!("127.0.0.1:{http_port}"))
                    .await
                    .expect("bind flow smoke HTTP server");
                axum::serve(listener, router)
                    .await
                    .expect("serve flow smoke HTTP");
            });
        });
        await_http(http_port);

        Self {
            _client: client,
            _worker: worker,
            _worker_thread: Some(worker_thread),
            _cache: cache,
            http_base_url: format!("http://127.0.0.1:{http_port}"),
            _server_thread: Some(server_thread),
            _cache_directory: cache_directory,
        }
    }
}

fn flow_smoke_catalog(client: &mut FlowSmokeHttpClient) -> Vec<FlowSmokeEntry> {
    vec![
        FlowSmokeEntry {
            name: "products/engagement",
            path: "/products/engagement/start",
            query: HashMap::new(),
            flags: FlowSmokeFlags::NONE,
        },
        FlowSmokeEntry {
            name: "products/microservices",
            path: "/products/microservices/start",
            query: query(&client.new_flow_id("microservices")),
            flags: FlowSmokeFlags::NONE,
        },
        FlowSmokeEntry {
            name: "products/money-transfer",
            path: "/products/money-transfer/start",
            query: HashMap::from([
                ("amount".into(), "100".into()),
                ("fromAccount".into(), "from-smoke".into()),
                ("toAccount".into(), "to-smoke".into()),
                ("notes".into(), "smoke".into()),
            ]),
            flags: FlowSmokeFlags::NONE,
        },
        FlowSmokeEntry {
            name: "products/polling",
            path: "/products/polling/start",
            query: query_with(
                &client.new_flow_id("product-polling"),
                &[("pollingCompletionThreshold", "3")],
            ),
            flags: FlowSmokeFlags::NONE,
        },
        FlowSmokeEntry {
            name: "products/subscription",
            path: "/products/subscription/start",
            query: HashMap::new(),
            flags: FlowSmokeFlags::NONE,
        },
        FlowSmokeEntry {
            name: "products/signup",
            path: "/products/signup/start",
            query: HashMap::new(),
            flags: FlowSmokeFlags::NONE,
        },
        FlowSmokeEntry {
            name: "products/job-post",
            path: "/products/job-post/start",
            query: HashMap::new(),
            flags: FlowSmokeFlags::NONE,
        },
        FlowSmokeEntry {
            name: "products/shortlist-candidates",
            path: "/products/shortlist-candidates/start",
            query: HashMap::new(),
            flags: FlowSmokeFlags::NONE,
        },
        FlowSmokeEntry {
            name: "patterns/polling/simple",
            path: "/patterns/polling/start/simple",
            query: query(&client.new_flow_id("pattern-polling-simple")),
            flags: FlowSmokeFlags::NONE,
        },
        FlowSmokeEntry {
            name: "patterns/polling/backoff",
            path: "/patterns/polling/start/backoff",
            query: query(&client.new_flow_id("pattern-polling-backoff")),
            flags: FlowSmokeFlags::NONE,
        },
        FlowSmokeEntry {
            name: "patterns/interruptible",
            path: "/patterns/interruptible/start",
            query: query(&client.new_flow_id("interruptible")),
            flags: FlowSmokeFlags::NONE,
        },
        FlowSmokeEntry {
            name: "patterns/reminders",
            path: "/patterns/reminders/start",
            query: HashMap::new(),
            flags: FlowSmokeFlags::NONE,
        },
        FlowSmokeEntry {
            name: "patterns/entity-store",
            path: "/patterns/entity-store/start",
            query: HashMap::new(),
            flags: FlowSmokeFlags::NO_START_STEP,
        },
        FlowSmokeEntry {
            name: "patterns/intervention",
            path: "/patterns/intervention/start",
            query: query(&client.new_flow_id("intervention")),
            flags: FlowSmokeFlags::NONE,
        },
        FlowSmokeEntry {
            name: "patterns/resettable-timer",
            path: "/patterns/resettable-timer/start",
            query: query(&client.new_flow_id("resettable-timer")),
            flags: FlowSmokeFlags::NONE,
        },
        FlowSmokeEntry {
            name: "patterns/parallel/simple",
            path: "/patterns/parallel/start/simple",
            query: query(&client.new_flow_id("parallel-simple")),
            flags: FlowSmokeFlags::NONE,
        },
        FlowSmokeEntry {
            name: "patterns/parallel/with-await",
            path: "/patterns/parallel/start/withAwait",
            query: query(&client.new_flow_id("parallel-await")),
            flags: FlowSmokeFlags::NONE,
        },
        FlowSmokeEntry {
            name: "patterns/recovery",
            path: "/patterns/recovery/start",
            query: query_with(&client.new_flow_id("recovery"), &[("itemName", "widget")]),
            flags: FlowSmokeFlags::STEP_START_MAY_FAIL,
        },
        FlowSmokeEntry {
            name: "patterns/scalable-parallel",
            path: "/patterns/scalable-parallel/start",
            query: query_with(
                &client.new_flow_id("scalable-parallel"),
                &[("numOfChildWfs", "1")],
            ),
            flags: FlowSmokeFlags::NONE,
        },
        FlowSmokeEntry {
            name: "patterns/parent-child",
            path: "/patterns/parent-child/start",
            query: query_with(
                &client.new_flow_id("parent-child"),
                &[("numOfChildWfs", "1")],
            ),
            flags: FlowSmokeFlags::NONE,
        },
        FlowSmokeEntry {
            name: "patterns/drain-channels/internal",
            path: "/patterns/drain-channels/internal/start",
            query: query(&client.new_flow_id("drain-internal")),
            flags: FlowSmokeFlags::NONE,
        },
        FlowSmokeEntry {
            name: "patterns/drain-channels/signal",
            path: "/patterns/drain-channels/signal/startorsignal",
            query: query(&client.new_flow_id("drain-signal")),
            flags: FlowSmokeFlags::NONE,
        },
        FlowSmokeEntry {
            name: "patterns/wait-for-state-completion",
            path: "/patterns/wait-for-state-completion/start",
            query: query(&client.new_flow_id("wait-for-state")),
            flags: FlowSmokeFlags::NONE,
        },
        FlowSmokeEntry {
            name: "patterns/timeout",
            path: "/patterns/timeout/start",
            query: query_with(
                &client.new_flow_id("timeout"),
                &[("successfulWorkflow", "true")],
            ),
            flags: FlowSmokeFlags::NONE,
        },
        FlowSmokeEntry {
            name: "primitives/step",
            path: "/primitives/step/start",
            query: query_with(&client.new_flow_id("primitive-step"), &[("inputNum", "1")]),
            flags: FlowSmokeFlags::NONE,
        },
        FlowSmokeEntry {
            name: "primitives/step/retry",
            path: "/primitives/step/retry/start",
            query: query_with(
                &client.new_flow_id("primitive-step-retry"),
                &[("readyAfterAttempt", "2")],
            ),
            flags: FlowSmokeFlags::NONE,
        },
        FlowSmokeEntry {
            name: "primitives/attribute",
            path: "/primitives/attribute/start",
            query: query_with(
                &client.new_flow_id("primitive-attribute"),
                &[("message", "smoke")],
            ),
            flags: FlowSmokeFlags::NONE,
        },
        FlowSmokeEntry {
            name: "primitives/channel",
            path: "/primitives/channel/start",
            query: query_with(
                &client.new_flow_id("primitive-channel"),
                &[("inputNum", "1")],
            ),
            flags: FlowSmokeFlags::NONE,
        },
        FlowSmokeEntry {
            name: "primitives/timer",
            path: "/primitives/timer/start",
            query: query_with(&client.new_flow_id("primitive-timer"), &[("seconds", "1")]),
            flags: FlowSmokeFlags::NONE,
        },
        FlowSmokeEntry {
            name: "primitives/rpc",
            path: "/primitives/rpc/start",
            query: query(&client.new_flow_id("primitive-rpc")),
            flags: FlowSmokeFlags::NONE,
        },
        FlowSmokeEntry {
            name: "primitives/subflow",
            path: "/primitives/subflow/start",
            query: query_with(
                &client.new_flow_id("primitive-subflow"),
                &[("inputNum", "1")],
            ),
            flags: FlowSmokeFlags::NONE,
        },
        FlowSmokeEntry {
            name: "primitives/client-apis",
            path: "/primitives/client-apis/start",
            query: query_with(
                &client.new_flow_id("primitive-client-apis"),
                &[("keyword", "smoke")],
            ),
            flags: FlowSmokeFlags::NONE,
        },
    ]
}

#[test]
#[ignore = "requires dexcli dev"]
fn flow_smoke_all_registered_flows_via_controller() {
    let environment = FlowSmokeEnvironment::start();
    let mut http_client = FlowSmokeHttpClient::new(environment.http_base_url.clone());
    let catalog = flow_smoke_catalog(&mut http_client);
    assert!(!catalog.is_empty(), "flow smoke catalog is empty");
    for entry in catalog {
        let result = http_client.trigger_get(entry.path, &entry.query);
        assert!(
            !result.flow_id.is_empty(),
            "{}: controller response did not include flowID",
            entry.name
        );
        assert_flow_smoke_start_step(&entry, &result.flow_id, &result.run_id);
        assert_flow_smoke_no_unexpected_failures(&entry, &result.flow_id, &result.run_id);
    }
}

fn available_worker_port() -> u16 {
    TcpListener::bind("127.0.0.1:0")
        .expect("bind ephemeral port")
        .local_addr()
        .expect("read ephemeral port")
        .port()
}

fn await_worker(worker_port: u16, worker_thread: &JoinHandle<SdkResult<()>>) {
    let deadline = Instant::now() + Duration::from_secs(10);
    while Instant::now() < deadline {
        if worker_thread.is_finished() {
            panic!("Worker exited before becoming ready");
        }
        if TcpStream::connect(format!("127.0.0.1:{worker_port}")).is_ok() {
            return;
        }
        thread::sleep(Duration::from_millis(50));
    }
    panic!("Worker did not become ready");
}

fn await_http(http_port: u16) {
    let deadline = Instant::now() + Duration::from_secs(10);
    while Instant::now() < deadline {
        if TcpStream::connect(format!("127.0.0.1:{http_port}")).is_ok() {
            return;
        }
        thread::sleep(Duration::from_millis(50));
    }
    panic!("HTTP server did not become ready");
}
