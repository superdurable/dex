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
mod worker_harness;

use std::collections::HashMap;
use std::net::{TcpListener, TcpStream};
use std::sync::{Arc, Barrier, OnceLock};
use std::thread::{self, JoinHandle};
use std::time::{Duration, Instant};

use common::{
    FlowSmokeEntry, FlowSmokeFlags, FlowSmokeHttpClient, assert_flow_smoke_no_unexpected_failures,
    assert_flow_smoke_start_step, query, query_with,
};
use dex_examples_rust::data_storage::entity_flow::FakeAccountArchive;
use dex_examples_rust::data_storage::entity_flow::controller::open_account_options;
use dex_examples_rust::data_storage::entity_flow::flow::{
    ADJUST_CREDIT_BALANCE, CREDIT_BALANCE, ContactDetails, CreditAdjustment, CustomerAccountFlow,
    DISPLAY_NAME, EMAIL, RETIRE_ACCOUNT, RETIRED, UPDATE_CONTACT_DETAILS, customer_account_flow_id,
};
use dex_examples_rust::patterns::polling::FakeJobService;
use dex_examples_rust::primitives::attribute::flow::AttributeFlow;
use dex_examples_rust::products::customer_refund::billing_service::FakeBillingService;
use dex_examples_rust::products::customer_refund::refund_flow::{CASE_STATUS, CaseStatus};
use dex_examples_rust::server::build_router;
use dex_examples_rust::shared::example_pull_worker_options;
use dex_examples_rust::{create_example_registry, create_worker_registry};
use dex_sdk::{
    AttributeMatch, BlobCache, BlobCacheConfig, Client, ClientOptions, FlowStatus, SdkError,
    SdkResult, WaitForAttributeOptions, Worker, WorkerOptions,
};
use serde_json::{Value, json};
use tempfile::TempDir;
use worker_harness::{await_pull_registration, new_temporary_refund_flow};

struct FlowSmokeEnvironment {
    client: Arc<Client>,
    _worker: Arc<Worker>,
    _worker_thread: Option<JoinHandle<SdkResult<()>>>,
    _cache: Arc<BlobCache>,
    http_base_url: String,
    _server_thread: Option<JoinHandle<()>>,
    _cache_directory: TempDir,
    refund_billing: FakeBillingService,
    _refund_ledger_directory: TempDir,
    account_archive: Arc<FakeAccountArchive>,
}

impl FlowSmokeEnvironment {
    fn start() -> Self {
        let server_address =
            std::env::var("DEX_SERVER_ADDRESS").unwrap_or_else(|_| "127.0.0.1:8801".to_string());
        let cache_directory = tempfile::tempdir().expect("create Rust flow smoke cache directory");
        let cache = Arc::new(
            BlobCache::open(
                BlobCacheConfig::new(cache_directory.path(), 64 * 1024 * 1024, 10_000)
                    .expect("valid Rust flow smoke cache config"),
            )
            .expect("open Rust flow smoke cache"),
        );
        let polling_jobs = Arc::new(FakeJobService::default());
        let account_archive = Arc::new(FakeAccountArchive::default());
        let refund_ledger_directory =
            tempfile::tempdir().expect("create Rust flow smoke refund ledger directory");
        let (customer_refund_flow, refund_billing) =
            new_temporary_refund_flow(refund_ledger_directory.path());
        let client = Arc::new(
            Client::try_new(
                create_example_registry(Arc::clone(&polling_jobs), customer_refund_flow.clone())
                    .expect("register Rust client examples"),
                Arc::clone(&cache),
                ClientOptions::new().server_address(&server_address),
            )
            .expect("create Rust flow smoke Client"),
        );
        let worker_options = WorkerOptions::new()
            .server_address(&server_address)
            .pull(example_pull_worker_options());
        let worker = Arc::new(
            Worker::try_new(
                create_worker_registry(
                    Arc::clone(&client),
                    Arc::clone(&polling_jobs),
                    customer_refund_flow.clone(),
                    Arc::clone(&account_archive),
                )
                .expect("register Rust Worker examples"),
                Arc::clone(&cache),
                worker_options,
            )
            .expect("create Rust flow smoke Worker"),
        );
        let thread_worker = Arc::clone(&worker);
        let worker_thread = thread::Builder::new()
            .name("dex-rust-flow-smoke-worker".to_string())
            .spawn(move || thread_worker.start())
            .expect("start Rust flow smoke Worker thread");
        await_pull_registration(&worker);
        client.health_check().expect("Dex health check");

        let http_port = available_http_port();
        let router = build_router(Arc::clone(&client), polling_jobs, customer_refund_flow);
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
            client,
            _worker: worker,
            _worker_thread: Some(worker_thread),
            _cache: cache,
            http_base_url: format!("http://127.0.0.1:{http_port}"),
            _server_thread: Some(server_thread),
            _cache_directory: cache_directory,
            refund_billing,
            _refund_ledger_directory: refund_ledger_directory,
            account_archive,
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
            name: "products/order-processing",
            path: "/products/order-processing/start",
            query: HashMap::new(),
            flags: FlowSmokeFlags::NONE,
        },
        FlowSmokeEntry {
            name: "products/customer-refund/manager-approval",
            path: "/products/customer-refund/manager-approval/start",
            query: HashMap::from([
                ("flowId".into(), client.new_flow_id("customer-refund")),
                ("scenario".into(), "normal".into()),
            ]),
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
            name: "patterns/polling",
            path: "/patterns/polling/start",
            query: query(&client.new_flow_id("pattern-polling")),
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
            name: "patterns/manual-recovery",
            path: "/patterns/manual-recovery/start",
            query: query(&client.new_flow_id("manual-recovery")),
            flags: FlowSmokeFlags::NONE,
        },
        FlowSmokeEntry {
            name: "patterns/inactiveness-tracker-timer",
            path: "/patterns/inactiveness-tracker-timer/start",
            query: query(&client.new_flow_id("inactiveness-tracker-timer")),
            flags: FlowSmokeFlags::NONE,
        },
        FlowSmokeEntry {
            name: "patterns/parallel/static",
            path: "/patterns/parallel/start/static",
            query: query(&client.new_flow_id("parallel-static")),
            flags: FlowSmokeFlags::NONE,
        },
        FlowSmokeEntry {
            name: "patterns/parallel/dynamic",
            path: "/patterns/parallel/start/dynamic",
            query: query(&client.new_flow_id("parallel-dynamic")),
            flags: FlowSmokeFlags::NONE,
        },
        FlowSmokeEntry {
            name: "patterns/parallel/await",
            path: "/patterns/parallel/start/await",
            query: query(&client.new_flow_id("parallel-await")),
            flags: FlowSmokeFlags::NONE,
        },
        FlowSmokeEntry {
            name: "patterns/parallel/first-win",
            path: "/patterns/parallel/start/first-win",
            query: query(&client.new_flow_id("parallel-first-win")),
            flags: FlowSmokeFlags::NONE,
        },
        FlowSmokeEntry {
            name: "patterns/recovery",
            path: "/patterns/recovery/start",
            query: query_with(&client.new_flow_id("recovery"), &[("itemName", "widget")]),
            flags: FlowSmokeFlags::STEP_START_MAY_FAIL,
        },
        parallel_subflows_entry(client, "basic", "/patterns/parallel-subflows/start/basic"),
        parallel_subflows_entry(
            client,
            "wait-for-half",
            "/patterns/parallel-subflows/start/wait-for-half",
        ),
        parallel_subflows_entry(
            client,
            "long-lived-parent",
            "/patterns/parallel-subflows/start/long-lived-parent",
        ),
        parallel_subflows_entry(
            client,
            "short-lived-parent",
            "/patterns/parallel-subflows/start/short-lived-parent",
        ),
        FlowSmokeEntry {
            name: "patterns/drain-channels/internal",
            path: "/patterns/drain-channels/internal/start",
            query: query(&client.new_flow_id("drain-internal")),
            flags: FlowSmokeFlags::NONE,
        },
        FlowSmokeEntry {
            name: "patterns/drain-channels/external-publishing",
            path: "/patterns/drain-channels/external-publishing/start-or-publish",
            query: query(&client.new_flow_id("drain-external")),
            flags: FlowSmokeFlags::NONE,
        },
        FlowSmokeEntry {
            name: "patterns/wait-for-step-completion",
            path: "/patterns/wait-for-step-completion/start",
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
            name: "primitives/step/custom-retry",
            path: "/primitives/step/custom-retry/start",
            query: query_with(
                &client.new_flow_id("primitive-step-custom-retry"),
                &[("readyAfterAttempt", "1")],
            ),
            flags: FlowSmokeFlags::NONE,
        },
        FlowSmokeEntry {
            name: "primitives/step/durability",
            path: "/primitives/step/durability/start",
            query: query_with(
                &client.new_flow_id("primitive-step-durability"),
                &[("mode", "sync")],
            ),
            flags: FlowSmokeFlags::NONE,
        },
        FlowSmokeEntry {
            name: "primitives/step/heartbeat",
            path: "/primitives/step/heartbeat/start",
            query: query_with(
                &client.new_flow_id("primitive-step-heartbeat"),
                &[("batches", "0")],
            ),
            flags: FlowSmokeFlags::NONE,
        },
        FlowSmokeEntry {
            name: "primitives/step/options-override",
            path: "/primitives/step/options-override/start",
            query: query_with(
                &client.new_flow_id("primitive-step-options-override"),
                &[("input", "smoke")],
            ),
            flags: FlowSmokeFlags::NONE,
        },
        FlowSmokeEntry {
            name: "primitives/step/step-decision",
            path: "/primitives/step/step-decision/start",
            query: query_with(
                &client.new_flow_id("primitive-step-decision"),
                &[("mode", "graceful")],
            ),
            flags: FlowSmokeFlags::NONE,
        },
        FlowSmokeEntry {
            name: "primitives/step/wait-types",
            path: "/primitives/step/wait-types/start",
            query: query_with(
                &client.new_flow_id("primitive-step-wait-types"),
                &[("mode", "any"), ("timeoutSeconds", "1")],
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
            name: "primitives/stream",
            path: "/primitives/stream/start",
            query: query_with(
                &client.new_flow_id("primitive-stream"),
                &[("input", "smoke")],
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
            name: "primitives/rpc-canceling-steps",
            path: "/primitives/rpc-canceling-steps/start",
            query: query_with(
                &client.new_flow_id("primitive-rpc-canceling-steps"),
                &[("reportName", "smoke")],
            ),
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

fn parallel_subflows_entry(
    client: &mut FlowSmokeHttpClient,
    kind: &'static str,
    path: &'static str,
) -> FlowSmokeEntry {
    let name = match kind {
        "basic" => "patterns/parallel-subflows/basic",
        "wait-for-half" => "patterns/parallel-subflows/wait-for-half",
        "long-lived-parent" => "patterns/parallel-subflows/long-lived-parent",
        "short-lived-parent" => "patterns/parallel-subflows/short-lived-parent",
        _ => panic!("unknown Parallel SubFlows kind: {kind}"),
    };
    FlowSmokeEntry {
        name,
        path,
        query: query(&client.new_flow_id(&format!("parallel-subflows-{kind}"))),
        flags: FlowSmokeFlags::NONE,
    }
}

/// Starts the shared environment once so every flow smoke test reuses one Worker.
fn shared_flow_smoke_environment() -> &'static FlowSmokeEnvironment {
    static ENVIRONMENT: OnceLock<FlowSmokeEnvironment> = OnceLock::new();
    ENVIRONMENT.get_or_init(FlowSmokeEnvironment::start)
}

/// Declares one filterable test per catalog entry that some engines reject.
macro_rules! dedicated_flow_smoke_entry_tests {
    ($($test_name:ident => $entry_name:literal,)*) => {
        const DEDICATED_FLOW_SMOKE_ENTRY_NAMES: &[&str] = &[$($entry_name),*];
        $(
            #[test]
            #[ignore = "requires dexcli dev"]
            fn $test_name() {
                run_dedicated_flow_smoke_entry($entry_name);
            }
        )*
    };
}

dedicated_flow_smoke_entry_tests! {
    flow_smoke_products_money_transfer => "products/money-transfer",
    flow_smoke_products_order_processing => "products/order-processing",
    flow_smoke_patterns_polling => "patterns/polling",
    flow_smoke_patterns_entity_store => "patterns/entity-store",
    flow_smoke_patterns_manual_recovery => "patterns/manual-recovery",
    flow_smoke_patterns_recovery => "patterns/recovery",
    flow_smoke_primitives_step_options_override => "primitives/step/options-override",
}

fn run_dedicated_flow_smoke_entry(entry_name: &str) {
    let environment = shared_flow_smoke_environment();
    let mut http_client = FlowSmokeHttpClient::new(environment.http_base_url.clone());
    let entry = flow_smoke_catalog(&mut http_client)
        .into_iter()
        .find(|entry| entry.name == entry_name)
        .unwrap_or_else(|| panic!("flow smoke entry {entry_name} is not in the catalog"));
    run_flow_smoke_entry(&http_client, &entry);
}

#[test]
#[ignore = "requires dexcli dev"]
fn flow_smoke_patterns_entity_flow() {
    let environment = shared_flow_smoke_environment();
    let mut http_client = FlowSmokeHttpClient::new(environment.http_base_url.clone());
    let account_id = http_client.new_flow_id("entity-flow");
    let entry = FlowSmokeEntry {
        name: "patterns/entity-flow",
        path: "/patterns/entity-flow/account",
        query: HashMap::new(),
        flags: FlowSmokeFlags::NO_START_STEP,
    };
    let result = http_client.trigger_post_json(
        entry.path,
        &json!({
            "accountId": account_id,
            "displayName": "Smoke Tester",
            "email": format!("{account_id}@example.com"),
        }),
    );
    assert!(
        !result.flow_id.is_empty(),
        "{}: controller response did not include flowID",
        entry.name
    );
    assert_flow_smoke_no_unexpected_failures(&entry, &result.flow_id, &result.run_id);
}

#[test]
#[ignore = "requires dexcli dev"]
fn entity_flow_keeps_one_customer_account_until_retired() {
    const CONCURRENT_DEPOSITS: i64 = 8;
    let environment = shared_flow_smoke_environment();
    let client = Arc::clone(&environment.client);
    let mut http_client = FlowSmokeHttpClient::new(environment.http_base_url.clone());
    let flow_id = customer_account_flow_id(&http_client.new_flow_id("entity-flow"));
    let contact = ContactDetails {
        display_name: "Ada".to_owned(),
        email: "ada@example.com".to_owned(),
    };
    client
        .start_flow_with_options(
            &CustomerAccountFlow::default(),
            &flow_id,
            (),
            open_account_options(contact.clone()),
        )
        .expect("open customer account");
    let duplicate = client.start_flow_with_options(
        &CustomerAccountFlow::default(),
        &flow_id,
        (),
        open_account_options(contact),
    );
    assert!(matches!(
        duplicate,
        Err(SdkError::FlowAlreadyStarted { .. })
    ));

    client
        .invoke_rpc(
            &flow_id,
            UPDATE_CONTACT_DETAILS,
            ContactDetails {
                display_name: "Ada Lovelace".to_owned(),
                email: "ada@lovelace.example".to_owned(),
            },
        )
        .expect("update contact details");
    let invalid_email = client
        .invoke_rpc(
            &flow_id,
            UPDATE_CONTACT_DETAILS,
            ContactDetails {
                display_name: "Ada".to_owned(),
                email: "not-an-email".to_owned(),
            },
        )
        .expect_err("an email without @ is rejected");
    assert!(invalid_email.to_string().contains("email must contain @"));

    let deposits: Vec<_> = (0..CONCURRENT_DEPOSITS)
        .map(|_| {
            let client = Arc::clone(&client);
            let flow_id = flow_id.clone();
            thread::spawn(move || deposit_credits_with_retry(&client, &flow_id, 5))
        })
        .collect();
    for deposit in deposits {
        deposit.join().expect("deposit thread panicked");
    }
    let overdraft = client
        .invoke_rpc(
            &flow_id,
            ADJUST_CREDIT_BALANCE,
            CreditAdjustment { amount: -1000 },
        )
        .expect_err("an overdraft is rejected");
    assert!(overdraft.to_string().contains("below zero"));

    let (display_name, email, credit_balance) = client
        .get_attributes(&flow_id, (&DISPLAY_NAME, &EMAIL, &CREDIT_BALANCE))
        .expect("read customer account");
    assert_eq!(display_name.as_deref(), Some("Ada Lovelace"));
    assert_eq!(email.as_deref(), Some("ada@lovelace.example"));
    assert_eq!(credit_balance, Some(5 * CONCURRENT_DEPOSITS));

    let retire_with_balance = client
        .invoke_rpc_without_input(&flow_id, RETIRE_ACCOUNT)
        .expect_err("retiring with a balance is rejected");
    assert!(retire_with_balance.to_string().contains("before retiring"));
    assert_eq!(
        client
            .describe_flow(&flow_id)
            .expect("describe account")
            .status,
        FlowStatus::Running
    );

    client
        .invoke_rpc(
            &flow_id,
            ADJUST_CREDIT_BALANCE,
            CreditAdjustment {
                amount: -5 * CONCURRENT_DEPOSITS,
            },
        )
        .expect("spend the remaining credits");
    let archive = Arc::clone(&environment.account_archive);
    archive.make_unavailable(&flow_id);
    client
        .invoke_rpc_without_input(&flow_id, RETIRE_ACCOUNT)
        .expect("retire account");
    assert_entity_flow_running(&client, &flow_id);
    let retired_adjustment = client
        .invoke_rpc(
            &flow_id,
            ADJUST_CREDIT_BALANCE,
            CreditAdjustment { amount: 10 },
        )
        .expect_err("a retired account rejects credit adjustments");
    assert!(
        retired_adjustment
            .to_string()
            .contains("account is retired")
    );
    let retired_contact = client
        .invoke_rpc(
            &flow_id,
            UPDATE_CONTACT_DETAILS,
            ContactDetails {
                display_name: "Ada Byron".to_owned(),
                email: "ada@byron.example".to_owned(),
            },
        )
        .expect_err("a retired account rejects contact updates");
    assert!(retired_contact.to_string().contains("account is retired"));
    client
        .invoke_rpc_without_input(&flow_id, RETIRE_ACCOUNT)
        .expect("a repeated retire is a no-op");
    assert_entity_flow_running(&client, &flow_id);

    archive.make_available(&flow_id);
    let result = client
        .wait_for_flow_with_timeout(&flow_id, Duration::from_secs(45))
        .expect("wait for retired account");
    assert_eq!(result.status(), FlowStatus::Completed);
    assert_eq!(archive.archive_count(&flow_id), 1);
    let (closed_balance, closed_retired) = client
        .get_attributes(&flow_id, (&CREDIT_BALANCE, &RETIRED))
        .expect("read closed account");
    assert_eq!(closed_balance, Some(0));
    assert_eq!(closed_retired, Some(true));

    let reopen = client.start_flow_with_options(
        &CustomerAccountFlow::default(),
        &flow_id,
        (),
        open_account_options(ContactDetails {
            display_name: "Ada".to_owned(),
            email: "ada@example.com".to_owned(),
        }),
    );
    assert!(matches!(reopen, Err(SdkError::FlowAlreadyStarted { .. })));
}

fn assert_entity_flow_running(client: &Client, flow_id: &str) {
    assert_eq!(
        client
            .describe_flow(flow_id)
            .expect("describe account")
            .status,
        FlowStatus::Running
    );
}

fn deposit_credits_with_retry(client: &Client, flow_id: &str, amount: i64) {
    for attempt in 1..=50_u64 {
        match client.invoke_rpc(flow_id, ADJUST_CREDIT_BALANCE, CreditAdjustment { amount }) {
            Ok(_) => return,
            Err(SdkError::RpcLockConflict { .. }) => {
                thread::sleep(Duration::from_millis(attempt * 50));
            }
            Err(error) => panic!("deposit credits: {error}"),
        }
    }
    panic!("credit deposit kept conflicting on the balance lock");
}

#[test]
#[ignore = "requires dexcli dev"]
fn flow_smoke_all_registered_flows_via_controller() {
    let environment = shared_flow_smoke_environment();
    let mut http_client = FlowSmokeHttpClient::new(environment.http_base_url.clone());
    let catalog = flow_smoke_catalog(&mut http_client);
    assert!(!catalog.is_empty(), "flow smoke catalog is empty");
    for entry in catalog
        .iter()
        .filter(|entry| !DEDICATED_FLOW_SMOKE_ENTRY_NAMES.contains(&entry.name))
    {
        run_flow_smoke_entry(&http_client, entry);
    }
    for (name, path) in [
        (
            "patterns/sequentially-chunked-attribute-map",
            "/patterns/sequentially-chunked-attribute-map/start",
        ),
        (
            "patterns/hash-partitioned-attribute-map",
            "/patterns/hash-partitioned-attribute-map/start",
        ),
    ] {
        let entry = FlowSmokeEntry {
            name,
            path,
            query: HashMap::new(),
            flags: FlowSmokeFlags::NO_START_STEP,
        };
        let result = http_client.trigger_post(entry.path);
        assert!(
            !result.flow_id.is_empty(),
            "{}: controller response did not include flowID",
            entry.name
        );
        assert_flow_smoke_no_unexpected_failures(&entry, &result.flow_id, &result.run_id);
    }
}

#[test]
#[ignore = "requires dexcli dev"]
fn attribute_read_returns_committed_values_of_closed_run() {
    let environment = shared_flow_smoke_environment();
    let mut http_client = FlowSmokeHttpClient::new(environment.http_base_url.clone());
    let flow_id = http_client.new_flow_id("primitive-attribute-read");
    environment
        .client
        .start_flow(&AttributeFlow::default(), &flow_id, "read".to_owned())
        .expect("start Attribute Flow");
    let result = environment
        .client
        .wait_for_flow_with_timeout(&flow_id, Duration::from_secs(30))
        .expect("wait for Attribute Flow");
    assert_eq!(result.status(), FlowStatus::Completed);

    let (status, body) = get_attribute_read(&http_client, &[("workflowId", &flow_id)]);
    assert_eq!(status, 200, "{body}");
    let values: Value = serde_json::from_str(&body).expect("parse Attribute read response");
    assert_eq!(
        values,
        json!({
            "status": "completed",
            "paymentProgress": "authorized",
            "email": null,
        })
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn attribute_read_returns_not_found_for_missing_flow() {
    let environment = shared_flow_smoke_environment();
    let mut http_client = FlowSmokeHttpClient::new(environment.http_base_url.clone());
    let flow_id = http_client.new_flow_id("primitive-attribute-read-missing");
    let (status, body) = get_attribute_read(&http_client, &[("workflowId", &flow_id)]);
    assert_eq!(status, 404, "{body}");
    let error_body: serde_json::Map<String, Value> =
        serde_json::from_str(&body).expect("parse Attribute read error");
    assert_eq!(error_body.keys().collect::<Vec<_>>(), vec!["error"]);
    let error = error_body["error"].as_str().expect("error is a string");
    assert!(error.contains(&flow_id), "{body}");
}

#[test]
#[ignore = "requires dexcli dev"]
fn attribute_read_requires_workflow_id() {
    let environment = shared_flow_smoke_environment();
    let http_client = FlowSmokeHttpClient::new(environment.http_base_url.clone());
    let (status, body) = get_attribute_read(&http_client, &[]);
    assert_eq!(status, 400, "{body}");
    let error_body: Value = serde_json::from_str(&body).expect("parse Attribute read error");
    assert_eq!(error_body, json!({"error": "workflowId is required"}));
}

#[test]
#[ignore = "requires dexcli dev"]
fn customer_refund_controller_approves_and_describes() {
    let environment = shared_flow_smoke_environment();
    let http_client = FlowSmokeHttpClient::new(environment.http_base_url.clone());
    let (status, started) = get_customer_refund(&http_client, "start", &[]);
    assert_eq!(status, 200, "{started}");
    let flow_id = started["flowID"]
        .as_str()
        .expect("start returns flowID")
        .to_string();
    assert!(flow_id.starts_with("customer-refund-"), "{started}");
    assert!(
        started["runID"]
            .as_str()
            .is_some_and(|run_id| !run_id.is_empty()),
        "{started}"
    );
    // start returns after CheckPolicy, so the run already waits for the manager.
    let (status, described) =
        get_customer_refund(&http_client, "describe", &[("workflowId", &flow_id)]);
    assert_eq!(status, 200, "{described}");
    assert_eq!(
        described,
        json!({
            "flowID": flow_id,
            "status": "running",
            "caseStatus": "awaiting-manager",
            "refundId": null,
        })
    );

    let (status, body) = get_customer_refund(
        &http_client,
        "decide",
        &[("decision", "approve"), ("reason", "Outage confirmed")],
    );
    assert_eq!(status, 400, "{body}");
    let (status, body) = decide_customer_refund(&http_client, &flow_id, "maybe", "Outage");
    assert_eq!(status, 400, "{body}");
    let (status, body) = decide_customer_refund(&http_client, &flow_id, "approve", "  ");
    assert_eq!(status, 400, "{body}");
    assert_eq!(body, json!({ "error": "reason must not be blank" }));
    let (status, body) =
        decide_customer_refund(&http_client, &flow_id, "approve", "Outage confirmed");
    assert_eq!(status, 200, "{body}");
    assert_eq!(body, json!({ "flowID": flow_id, "decision": "approve" }));

    wait_for_completed_refund(environment, &flow_id);
    let ledger_refund_ids = refund_ids_in_ledger(environment, &flow_id);
    assert_eq!(ledger_refund_ids.len(), 1, "{ledger_refund_ids:?}");
    let (status, described) =
        get_customer_refund(&http_client, "describe", &[("flowId", &flow_id)]);
    assert_eq!(status, 200, "{described}");
    assert_eq!(
        described,
        json!({
            "flowID": flow_id,
            "status": "completed",
            "caseStatus": "closed-refunded",
            "refundId": ledger_refund_ids[0],
        })
    );
    let (status, body) = decide_customer_refund(&http_client, &flow_id, "reject", "Too late");
    assert_eq!(status, 404, "a decision on a closed refund: {body}");
}

#[test]
#[ignore = "requires dexcli dev"]
fn customer_refund_controller_rejects_and_describes() {
    let environment = shared_flow_smoke_environment();
    let http_client = FlowSmokeHttpClient::new(environment.http_base_url.clone());
    let flow_id = start_customer_refund(&http_client, "normal");
    wait_for_refund_manager(environment, &flow_id);
    let (status, body) =
        decide_customer_refund(&http_client, &flow_id, "reject", "No outage on record");
    assert_eq!(status, 200, "{body}");
    assert_eq!(body, json!({ "flowID": flow_id, "decision": "reject" }));
    wait_for_completed_refund(environment, &flow_id);
    assert!(refund_ids_in_ledger(environment, &flow_id).is_empty());
    let (status, described) =
        get_customer_refund(&http_client, "describe", &[("flowId", &flow_id)]);
    assert_eq!(status, 200, "{described}");
    assert_eq!(
        described,
        json!({
            "flowID": flow_id,
            "status": "completed",
            "caseStatus": "closed-denied",
            "refundId": null,
        })
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn customer_refund_controller_starts_runs_that_skip_the_manager() {
    let environment = shared_flow_smoke_environment();
    let http_client = FlowSmokeHttpClient::new(environment.http_base_url.clone());
    let small_flow_id = start_customer_refund(&http_client, "small");
    let deny_flow_id = start_customer_refund(&http_client, "deny");

    wait_for_completed_refund(environment, &small_flow_id);
    let small_refund_ids = refund_ids_in_ledger(environment, &small_flow_id);
    assert_eq!(small_refund_ids.len(), 1, "{small_refund_ids:?}");
    assert!(
        small_refund_ids[0].starts_with("rf_"),
        "{small_refund_ids:?}"
    );
    let (status, described) =
        get_customer_refund(&http_client, "describe", &[("flowId", &small_flow_id)]);
    assert_eq!(status, 200, "{described}");
    assert_eq!(
        described,
        json!({
            "flowID": small_flow_id,
            "status": "completed",
            "caseStatus": "closed-refunded",
            "refundId": small_refund_ids[0],
        })
    );

    wait_for_completed_refund(environment, &deny_flow_id);
    assert!(refund_ids_in_ledger(environment, &deny_flow_id).is_empty());
    let (status, described) =
        get_customer_refund(&http_client, "describe", &[("flowId", &deny_flow_id)]);
    assert_eq!(status, 200, "{described}");
    assert_eq!(
        described,
        json!({
            "flowID": deny_flow_id,
            "status": "completed",
            "caseStatus": "closed-denied",
            "refundId": null,
        })
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn customer_refund_controller_conflicts_when_not_awaiting_manager() {
    let environment = shared_flow_smoke_environment();
    let http_client = FlowSmokeHttpClient::new(environment.http_base_url.clone());
    // IssueRefund then fails twice 5 seconds apart, so the run stays open after approval.
    let flow_id = start_customer_refund(&http_client, "fail-then-succeed");
    wait_for_refund_manager(environment, &flow_id);
    let (status, body) =
        decide_customer_refund(&http_client, &flow_id, "approve", "Outage confirmed");
    assert_eq!(status, 200, "{body}");
    let (status, body) = decide_customer_refund(&http_client, &flow_id, "reject", "Too late");
    assert_eq!(status, 409, "{body}");
    // Closing the run keeps a later suite's Worker from issuing this refund.
    wait_for_completed_refund(environment, &flow_id);
    assert_eq!(refund_ids_in_ledger(environment, &flow_id).len(), 1);
}

#[test]
#[ignore = "requires dexcli dev"]
fn customer_refund_controller_rejects_bad_requests() {
    let environment = shared_flow_smoke_environment();
    let mut http_client = FlowSmokeHttpClient::new(environment.http_base_url.clone());
    let (status, body) = get_customer_refund(&http_client, "start", &[("scenario", "huge")]);
    assert_eq!(status, 400, "{body}");

    let unknown_flow_id = http_client.new_flow_id("customer-refund-unknown");
    let (status, body) = decide_customer_refund(&http_client, &unknown_flow_id, "approve", " ");
    assert_eq!(status, 400, "{body}");
    let (status, body) = decide_customer_refund(
        &http_client,
        &unknown_flow_id,
        "approve",
        "Outage confirmed",
    );
    assert_eq!(status, 404, "{body}");
    let (status, body) =
        get_customer_refund(&http_client, "describe", &[("flowId", &unknown_flow_id)]);
    assert_eq!(status, 404, "{body}");

    let transfer = http_client.trigger_get("/products/money-transfer/start", &HashMap::new());
    let (status, body) =
        get_customer_refund(&http_client, "describe", &[("flowId", &transfer.flow_id)]);
    assert_eq!(status, 404, "describe of another Flow type: {body}");
    assert!(body["error"].is_string(), "{body}");
    let (status, body) = decide_customer_refund(
        &http_client,
        &transfer.flow_id,
        "approve",
        "Outage confirmed",
    );
    assert_eq!(status, 404, "decide on another Flow type: {body}");

    let flow_id = http_client.new_flow_id("customer-refund-duplicate");
    let (status, body) = get_customer_refund(&http_client, "start", &[("flowId", &flow_id)]);
    assert_eq!(status, 200, "{body}");
    let (status, body) = get_customer_refund(
        &http_client,
        "start",
        &[("workflowId", ""), ("flowId", &flow_id), ("scenario", "")],
    );
    assert_eq!(status, 409, "duplicate start: {body}");
    assert!(body["error"].is_string(), "{body}");
}

#[test]
#[ignore = "requires dexcli dev"]
fn customer_refund_controller_lets_one_of_two_racing_decisions_win() {
    let environment = shared_flow_smoke_environment();
    let http_client = FlowSmokeHttpClient::new(environment.http_base_url.clone());
    let flow_id = start_customer_refund(&http_client, "normal");
    let barrier = Barrier::new(2);
    let (approve, reject) = thread::scope(|scope| {
        let decide_after_barrier = |decision: &'static str| {
            let (http_client, flow_id, barrier) = (&http_client, &flow_id, &barrier);
            scope.spawn(move || {
                barrier.wait();
                decide_customer_refund(http_client, flow_id, decision, "Racing decision")
            })
        };
        let approve = decide_after_barrier("approve");
        let reject = decide_after_barrier("reject");
        (
            approve.join().expect("approve thread"),
            reject.join().expect("reject thread"),
        )
    });
    let (winner, loser_status) = match (approve.0, reject.0) {
        (200, status) => ("approve", status),
        (status, 200) => ("reject", status),
        statuses => panic!("exactly one decision wins: {statuses:?} {approve:?} {reject:?}"),
    };
    // The loser meets the lock, the changed case status, or the closed run.
    assert!(
        loser_status == 409 || loser_status == 404,
        "losing decision returned {loser_status}: {approve:?} {reject:?}"
    );
    wait_for_completed_refund(environment, &flow_id);
    let (expected_case_status, expected_refunds) = match winner {
        "approve" => ("closed-refunded", 1),
        _ => ("closed-denied", 0),
    };
    let (status, described) =
        get_customer_refund(&http_client, "describe", &[("flowId", &flow_id)]);
    assert_eq!(status, 200, "{described}");
    assert_eq!(described["caseStatus"], expected_case_status, "{described}");
    assert_eq!(
        refund_ids_in_ledger(environment, &flow_id).len(),
        expected_refunds
    );
}

fn start_customer_refund(http_client: &FlowSmokeHttpClient, scenario: &str) -> String {
    let (status, started) = get_customer_refund(http_client, "start", &[("scenario", scenario)]);
    assert_eq!(status, 200, "{started}");
    started["flowID"]
        .as_str()
        .expect("start returns flowID")
        .to_string()
}

fn decide_customer_refund(
    http_client: &FlowSmokeHttpClient,
    flow_id: &str,
    decision: &str,
    reason: &str,
) -> (u16, Value) {
    get_customer_refund(
        http_client,
        "decide",
        &[
            ("flowId", flow_id),
            ("decision", decision),
            ("reason", reason),
        ],
    )
}

fn wait_for_refund_manager(environment: &FlowSmokeEnvironment, flow_id: &str) {
    environment
        .client
        .wait_for_attribute_match(
            flow_id,
            &CASE_STATUS,
            AttributeMatch::equal_to(CaseStatus::AwaitingManager),
            WaitForAttributeOptions::new().request_timeout(Duration::from_secs(30)),
        )
        .expect("the refund waits for a manager");
}

fn wait_for_completed_refund(environment: &FlowSmokeEnvironment, flow_id: &str) {
    let result = environment
        .client
        .wait_for_flow_with_timeout(flow_id, Duration::from_secs(45))
        .expect("the refund closes");
    assert_eq!(result.status(), FlowStatus::Completed);
}

/// Returns the refund IDs this environment's ledger holds for the run's idempotency key.
fn refund_ids_in_ledger(environment: &FlowSmokeEnvironment, flow_id: &str) -> Vec<String> {
    environment
        .refund_billing
        .list_refunds()
        .expect("list this environment's refunds")
        .into_iter()
        .filter(|refund| refund.idempotency_key == format!("refund-{flow_id}"))
        .map(|refund| refund.refund_id)
        .collect()
}

fn get_customer_refund(
    http_client: &FlowSmokeHttpClient,
    action: &str,
    query: &[(&str, &str)],
) -> (u16, Value) {
    let response = reqwest::blocking::Client::new()
        .get(format!(
            "{}/products/customer-refund/manager-approval/{action}",
            http_client.base_url
        ))
        .query(query)
        .send()
        .expect("GET a customer refund route");
    let status = response.status().as_u16();
    let body = response.json().expect("decode customer refund response");
    (status, body)
}

fn get_attribute_read(http_client: &FlowSmokeHttpClient, query: &[(&str, &str)]) -> (u16, String) {
    let response = reqwest::blocking::Client::new()
        .get(format!(
            "{}/primitives/attribute/read",
            http_client.base_url
        ))
        .query(query)
        .send()
        .expect("GET /primitives/attribute/read");
    let status = response.status().as_u16();
    let body = response.text().expect("read Attribute read response");
    (status, body)
}

fn run_flow_smoke_entry(http_client: &FlowSmokeHttpClient, entry: &FlowSmokeEntry) {
    let result = http_client.trigger_get(entry.path, &entry.query);
    assert!(
        !result.flow_id.is_empty(),
        "{}: controller response did not include flowID",
        entry.name
    );
    assert_flow_smoke_start_step(entry, &result.flow_id, &result.run_id);
    assert_flow_smoke_no_unexpected_failures(entry, &result.flow_id, &result.run_id);
}

fn available_http_port() -> u16 {
    TcpListener::bind("127.0.0.1:0")
        .expect("bind ephemeral port")
        .local_addr()
        .expect("read ephemeral port")
        .port()
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
