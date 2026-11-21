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

//! Runs the customer refund example's scenarios against a real Dex server.

use std::process::Command;
use std::sync::Arc;
use std::thread;
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

use dex_examples_rust::products::customer_refund::billing_service::FakeBillingService;
use dex_examples_rust::products::customer_refund::email_service::FakeEmailService;
use dex_examples_rust::products::customer_refund::message_model::MockMessageModel;
use dex_examples_rust::products::customer_refund::refund_config::{
    BillingMode, RefundConfig, open_refund_blob_cache,
};
use dex_examples_rust::products::customer_refund::refund_flow::{
    APPROVE_REFUND, CASE_STATUS, CUSTOMER_MESSAGE, CaseStatus, CustomerRefundFlow, DecisionReason,
    REFUND_ID, REJECT_REFUND, RefundOutcome, RefundRequest, RefundServices,
};
use dex_examples_rust::products::customer_refund::refund_scenarios::RefundScenario;
use dex_examples_rust::products::customer_refund::refund_worker::{
    RunningRefundWorker, open_refund_client, refund_worker_options,
};
use dex_sdk::{AttributeMatch, Client, FlowResult, FlowStatus, WaitForAttributeOptions};
use serde_json::Value;
use tempfile::TempDir;

const WAIT_TIMEOUT: Duration = Duration::from_secs(60);

/// One test's ledgers, cache folders, and Client, shared by its Workers.
struct RefundTestbed {
    directory: TempDir,
    server_address: String,
    services: Arc<RefundServices>,
    flow: CustomerRefundFlow,
    client: Client,
}

impl RefundTestbed {
    fn new() -> Self {
        let directory = tempfile::tempdir().expect("create the test directory");
        let server_address = RefundConfig::from_environment()
            .expect("read the refund settings")
            .server_address;
        let services = new_services(&directory, BillingMode::Normal);
        let flow = CustomerRefundFlow::new(Arc::clone(&services));
        let cache = open_refund_blob_cache(directory.path().join("client-cache"))
            .expect("open the Client cache");
        let client = open_refund_client(flow.clone(), cache, &server_address)
            .expect("create the refund Client");
        client.health_check().expect("reach the Dex server");
        Self {
            directory,
            server_address,
            services,
            flow,
            client,
        }
    }

    fn start_worker(&self, billing_mode: BillingMode) -> RunningRefundWorker {
        let flow = match billing_mode {
            BillingMode::Normal => self.flow.clone(),
            BillingMode::CallMissing => {
                CustomerRefundFlow::new(new_services(&self.directory, billing_mode))
            }
        };
        let cache = open_refund_blob_cache(
            self.directory
                .path()
                .join(format!("worker-cache-{billing_mode}")),
        )
        .expect("open the Worker cache");
        RunningRefundWorker::start(flow, cache, refund_worker_options(&self.server_address))
            .expect("start the refund Worker")
    }

    fn start_refund(&self, flow_id: &str, scenario: RefundScenario) {
        self.start_request(flow_id, scenario.refund_request());
    }

    fn start_request(&self, flow_id: &str, request: RefundRequest) {
        self.client
            .start_flow(&self.flow, flow_id, request)
            .expect("start the refund");
    }

    fn wait_for_manager(&self, flow_id: &str) {
        self.client
            .wait_for_attribute_match(
                flow_id,
                &CASE_STATUS,
                AttributeMatch::equal_to(CaseStatus::AwaitingManager),
                WaitForAttributeOptions::new().request_timeout(WAIT_TIMEOUT),
            )
            .expect("the refund waits for a manager");
    }

    fn decide(&self, flow_id: &str, approved: bool, reason: &str) {
        let rpc = if approved {
            APPROVE_REFUND
        } else {
            REJECT_REFUND
        };
        let decision = DecisionReason {
            reason: reason.to_string(),
        };
        self.client
            .invoke_rpc(flow_id, rpc, decision)
            .expect("record the manager decision");
    }

    fn wait_for_flow(&self, flow_id: &str, timeout: Duration) -> FlowResult {
        self.client
            .wait_for_flow_with_timeout(flow_id, timeout)
            .expect("the refund closes")
    }

    fn read_string(&self, flow_id: &str, attribute: &dex_sdk::Attribute<String>) -> Option<String> {
        let (value,) = self
            .client
            .get_attributes(flow_id, (attribute,))
            .expect("read an Attribute");
        value
    }

    fn case_status(&self, flow_id: &str) -> Option<CaseStatus> {
        let (status,) = self
            .client
            .get_attributes(flow_id, (&*CASE_STATUS,))
            .expect("read case-status");
        status
    }

    fn refund_keys(&self) -> Vec<String> {
        let refunds = self.services.billing.list_refunds().expect("list refunds");
        refunds
            .into_iter()
            .map(|refund| refund.idempotency_key)
            .collect()
    }

    fn email_keys(&self) -> Vec<String> {
        self.services
            .email
            .list_sent_keys()
            .expect("list sent emails")
    }
}

fn new_services(directory: &TempDir, billing_mode: BillingMode) -> Arc<RefundServices> {
    Arc::new(RefundServices {
        billing: FakeBillingService::new(
            directory.path().join("billing-refunds.json"),
            billing_mode,
        ),
        email: FakeEmailService::new(directory.path().join("sent-emails.json")),
        model: MockMessageModel,
    })
}

fn new_flow_id() -> String {
    let timestamp = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .expect("current time after Unix epoch")
        .as_nanos();
    format!("refund-{timestamp:x}")
}

fn assert_outcome(result: &FlowResult, expected: RefundOutcome) {
    assert_eq!(result.status(), FlowStatus::Completed);
    let outcome: RefundOutcome = result.single_output().expect("decode the refund outcome");
    assert_eq!(outcome, expected);
}

#[test]
#[ignore = "requires dexcli dev"]
fn manager_approves_the_northstar_refund() {
    let testbed = RefundTestbed::new();
    let flow_id = new_flow_id();
    let result = {
        let _worker = testbed.start_worker(BillingMode::Normal);
        testbed.start_refund(&flow_id, RefundScenario::Normal);
        testbed.wait_for_manager(&flow_id);
        testbed.decide(&flow_id, true, "Outage confirmed");
        testbed.wait_for_flow(&flow_id, WAIT_TIMEOUT)
    };

    assert_outcome(&result, RefundOutcome::Refunded);
    let refunds = testbed.services.billing.list_refunds().unwrap();
    assert_eq!(refunds.len(), 1);
    assert_eq!(refunds[0].order_id, "INV-1249");
    assert_eq!(refunds[0].amount_cents, 124_900);
    assert_eq!(
        testbed.read_string(&flow_id, &REFUND_ID),
        Some(refunds[0].refund_id.clone())
    );
    assert_eq!(testbed.email_keys(), vec![format!("email-{flow_id}")]);
}

#[test]
#[ignore = "requires dexcli dev"]
fn manager_rejects_the_refund() {
    let testbed = RefundTestbed::new();
    let flow_id = new_flow_id();
    let result = {
        let _worker = testbed.start_worker(BillingMode::Normal);
        testbed.start_refund(&flow_id, RefundScenario::Normal);
        testbed.wait_for_manager(&flow_id);
        testbed.decide(&flow_id, false, "Credit offered instead");
        testbed.wait_for_flow(&flow_id, WAIT_TIMEOUT)
    };

    assert_outcome(&result, RefundOutcome::Denied);
    assert!(testbed.refund_keys().is_empty());
    let message = testbed
        .read_string(&flow_id, &CUSTOMER_MESSAGE)
        .expect("a customer message");
    assert!(message.contains("Credit offered instead"), "{message}");
}

#[test]
#[ignore = "requires dexcli dev"]
fn policy_denies_an_old_charge() {
    let testbed = RefundTestbed::new();
    let flow_id = new_flow_id();
    let result = {
        let _worker = testbed.start_worker(BillingMode::Normal);
        testbed.start_refund(&flow_id, RefundScenario::Deny);
        testbed.wait_for_flow(&flow_id, WAIT_TIMEOUT)
    };

    assert_outcome(&result, RefundOutcome::Denied);
    assert_eq!(
        testbed.case_status(&flow_id),
        Some(CaseStatus::ClosedDenied)
    );
    assert!(testbed.refund_keys().is_empty());
    assert_eq!(testbed.email_keys(), vec![format!("email-{flow_id}")]);
    let message = testbed
        .read_string(&flow_id, &CUSTOMER_MESSAGE)
        .expect("a customer message");
    assert!(message.contains(EXPIRED_WINDOW_NOTE), "{message}");
    assert!(message.contains(NO_OUTAGE_NOTE), "{message}");
}

const EXPIRED_WINDOW_NOTE: &str = "outside the 30-day refund window";
const NO_OUTAGE_NOTE: &str = "No service outage was recorded";

/// Runs one refund for `order_id` to completion and returns the customer message.
fn denied_message_for(order_id: &str) -> String {
    let testbed = RefundTestbed::new();
    let flow_id = new_flow_id();
    let result = {
        let _worker = testbed.start_worker(BillingMode::Normal);
        let request = RefundRequest {
            order_id: order_id.to_string(),
            ..RefundScenario::Normal.refund_request()
        };
        testbed.start_request(&flow_id, request);
        testbed.wait_for_flow(&flow_id, WAIT_TIMEOUT)
    };
    assert_outcome(&result, RefundOutcome::Denied);
    assert!(testbed.refund_keys().is_empty());
    testbed
        .read_string(&flow_id, &CUSTOMER_MESSAGE)
        .expect("a customer message")
}

#[test]
#[ignore = "requires dexcli dev"]
fn policy_names_only_the_expired_window() {
    let message = denied_message_for("INV-0412");
    assert!(message.contains(EXPIRED_WINDOW_NOTE), "{message}");
    assert!(!message.contains(NO_OUTAGE_NOTE), "{message}");
}

#[test]
#[ignore = "requires dexcli dev"]
fn policy_names_only_the_missing_outage() {
    let message = denied_message_for("INV-0517");
    assert!(message.contains(NO_OUTAGE_NOTE), "{message}");
    assert!(!message.contains(EXPIRED_WINDOW_NOTE), "{message}");
}

#[test]
#[ignore = "requires dexcli dev"]
fn blank_reason_is_rejected_and_a_later_decision_is_accepted() {
    let testbed = RefundTestbed::new();
    let flow_id = new_flow_id();
    let result = {
        let _worker = testbed.start_worker(BillingMode::Normal);
        testbed.start_refund(&flow_id, RefundScenario::Normal);
        testbed.wait_for_manager(&flow_id);
        let blank = DecisionReason {
            reason: "   ".to_string(),
        };
        let error = testbed
            .client
            .invoke_rpc(&flow_id, APPROVE_REFUND, blank)
            .expect_err("a blank reason is rejected");
        assert!(error.to_string().contains("needs a reason"), "{error}");
        assert_eq!(
            testbed.case_status(&flow_id),
            Some(CaseStatus::AwaitingManager)
        );
        testbed.decide(&flow_id, true, "Outage confirmed");
        testbed.wait_for_flow(&flow_id, WAIT_TIMEOUT)
    };
    assert_outcome(&result, RefundOutcome::Refunded);
    assert_eq!(testbed.refund_keys(), vec![format!("refund-{flow_id}")]);
}

#[test]
#[ignore = "requires dexcli dev"]
fn small_refund_needs_no_manager() {
    let testbed = RefundTestbed::new();
    let flow_id = new_flow_id();
    let result = {
        let _worker = testbed.start_worker(BillingMode::Normal);
        testbed.start_refund(&flow_id, RefundScenario::Small);
        testbed.wait_for_flow(&flow_id, WAIT_TIMEOUT)
    };

    assert_outcome(&result, RefundOutcome::Refunded);
    let message = testbed
        .read_string(&flow_id, &CUSTOMER_MESSAGE)
        .expect("a customer message");
    assert!(message.contains("refunded 49.00 USD"), "{message}");
}

#[test]
#[ignore = "requires dexcli dev"]
fn refund_succeeds_after_payment_timeouts() {
    let testbed = RefundTestbed::new();
    let flow_id = new_flow_id();
    let result = {
        let _worker = testbed.start_worker(BillingMode::Normal);
        testbed.start_refund(&flow_id, RefundScenario::FailThenSucceed);
        testbed.wait_for_manager(&flow_id);
        testbed.decide(&flow_id, true, "Outage confirmed");
        testbed.wait_for_flow(&flow_id, Duration::from_secs(90))
    };

    assert_outcome(&result, RefundOutcome::Refunded);
    assert_eq!(testbed.services.billing.payment_timeouts(), 2);
    assert_eq!(testbed.refund_keys(), vec![format!("refund-{flow_id}")]);
    let completed = issue_refund_completion(&testbed.server_address, &flow_id);
    assert_eq!(completed["context"]["finalAttempt"], 3, "{completed}");
    assert_eq!(
        completed["context"]["lastFailureInfo"]["attempt"], 2,
        "{completed}"
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn time_travel_after_a_refund_reuses_the_refund() {
    let testbed = RefundTestbed::new();
    let flow_id = new_flow_id();
    let worker = testbed.start_worker(BillingMode::Normal);
    testbed.start_refund(&flow_id, RefundScenario::Small);
    let first = testbed.wait_for_flow(&flow_id, WAIT_TIMEOUT);
    assert_outcome(&first, RefundOutcome::Refunded);
    let refund_id = testbed.read_string(&flow_id, &REFUND_ID);

    time_travel_with_dexcli(&testbed.server_address, &flow_id, "IssueRefund");
    wait_until(|| testbed.services.billing.reused_refunds() == 1);
    let again = testbed.wait_for_flow(&flow_id, WAIT_TIMEOUT);
    worker.stop().expect("stop the refund Worker");

    assert_outcome(&again, RefundOutcome::Refunded);
    assert_eq!(testbed.refund_keys(), vec![format!("refund-{flow_id}")]);
    assert_eq!(testbed.read_string(&flow_id, &REFUND_ID), refund_id);
    assert_eq!(testbed.email_keys(), vec![format!("email-{flow_id}")]);
}

fn wait_until(condition: impl Fn() -> bool) {
    let deadline = Instant::now() + WAIT_TIMEOUT;
    while !condition() {
        assert!(
            Instant::now() < deadline,
            "the condition did not hold in time"
        );
        thread::sleep(Duration::from_millis(100));
    }
}

/// Returns the run's StepExecuteCompleted event for IssueRefund, from `dexcli flow history`.
fn issue_refund_completion(server_address: &str, flow_id: &str) -> Value {
    let output = run_dexcli(&[
        "flow",
        "history",
        flow_id,
        "--server",
        server_address,
        "--all",
    ]);
    let history: Value = serde_json::from_slice(&output).expect("decode the history JSON");
    history["events"]
        .as_array()
        .expect("history events")
        .iter()
        .find(|event| {
            event["type"] == "StepExecuteCompleted"
                && event["payload"]["context"]["stepType"] == "IssueRefund"
        })
        .map(|event| event["payload"].clone())
        .expect("an IssueRefund StepExecuteCompleted event")
}

#[test]
#[ignore = "requires dexcli dev"]
fn time_travel_issues_the_refund_a_missing_call_skipped() {
    let testbed = RefundTestbed::new();
    let flow_id = new_flow_id();
    let broken = {
        let _worker = testbed.start_worker(BillingMode::CallMissing);
        testbed.start_refund(&flow_id, RefundScenario::Normal);
        testbed.wait_for_manager(&flow_id);
        testbed.decide(&flow_id, true, "Outage confirmed");
        testbed.wait_for_flow(&flow_id, WAIT_TIMEOUT)
    };
    assert_outcome(&broken, RefundOutcome::Refunded);
    assert_eq!(
        testbed.read_string(&flow_id, &REFUND_ID),
        Some(String::new())
    );
    assert!(testbed.refund_keys().is_empty());

    let fixed = {
        let _worker = testbed.start_worker(BillingMode::Normal);
        time_travel_with_dexcli(&testbed.server_address, &flow_id, "IssueRefund");
        wait_for_refund_id(&testbed, &flow_id);
        testbed.wait_for_flow(&flow_id, WAIT_TIMEOUT)
    };

    assert_outcome(&fixed, RefundOutcome::Refunded);
    let refunds = testbed.services.billing.list_refunds().unwrap();
    assert_eq!(refunds.len(), 1);
    assert_eq!(refunds[0].idempotency_key, format!("refund-{flow_id}"));
    assert_eq!(
        testbed.read_string(&flow_id, &REFUND_ID),
        Some(refunds[0].refund_id.clone())
    );
    assert_eq!(testbed.email_keys(), vec![format!("email-{flow_id}")]);
}

/// Waits until the new run records a refund, so the Flow wait reads the new run's result.
fn wait_for_refund_id(testbed: &RefundTestbed, flow_id: &str) {
    testbed
        .client
        .wait_for_attribute_match(
            flow_id,
            &REFUND_ID,
            AttributeMatch::not_equal_to(String::new()),
            WaitForAttributeOptions::new().request_timeout(WAIT_TIMEOUT),
        )
        .expect("the time-traveled run records a refund ID");
}

/// Runs the README's time travel command.
fn time_travel_with_dexcli(server_address: &str, flow_id: &str, step_type: &str) {
    run_dexcli(&[
        "flow",
        "time-travel",
        flow_id,
        "--server",
        server_address,
        "--type",
        "step-type",
        "--target",
        step_type,
        "--reason",
        "billing call restored",
        "--yes",
    ]);
}

/// Runs dexcli from `DEXCLI_PATH` or `PATH` and returns its standard output.
fn run_dexcli(arguments: &[&str]) -> Vec<u8> {
    let dexcli = std::env::var("DEXCLI_PATH").unwrap_or_else(|_| "dexcli".to_string());
    let output = Command::new(&dexcli)
        .args(arguments)
        .output()
        .unwrap_or_else(|error| panic!("run {dexcli}; set DEXCLI_PATH: {error}"));
    assert!(
        output.status.success(),
        "dexcli {} failed: {}",
        arguments.join(" "),
        String::from_utf8_lossy(&output.stderr)
    );
    output.stdout
}
