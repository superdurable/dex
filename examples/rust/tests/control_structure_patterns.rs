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

//! Integration tests for the Deferred Choice, Critical Section, and Cancel
//! Case control-structure pattern Flows.

mod condition_order_probes;
mod worker_harness;

use std::collections::BTreeMap;
use std::sync::Arc;
use std::thread::{self, JoinHandle};
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

use condition_order_probes::{
    ACCEPT_PROBE_OFFER, CANCEL_PROBE_APPLICATION, CancelCaseOrderProbeFlow, ConditionOrderOutcome,
    DECIDE_PROBE_APPLICATION, DeferredChoiceOrderProbeFlow, OPEN_DECISION_PROBE_GATE,
    OPEN_OFFER_PROBE_GATE, REJECT_PROBE_OFFER, register_condition_order_probes,
};
use dex_examples_rust::control_patterns::cancel_case::{
    CANCEL_APPLICATION, DECIDE_APPLICATION, LoanApplication, LoanApplicationFlow, RunCreditCheck,
    STOPPED_CREDIT_CHECK_FLOW_ID,
};
use dex_examples_rust::control_patterns::critical_section::{
    ContractEdit, ContractEditFlow, ContractEditRecord,
};
use dex_examples_rust::control_patterns::deferred_choice::{
    ACCEPT_CUSTOMER_OFFER, AwaitOfferResponse, OfferResponseFlow, REJECT_CUSTOMER_OFFER,
};
use dex_examples_rust::patterns::polling::FakeJobService;
use dex_examples_rust::shared::example_pull_worker_options;
use dex_examples_rust::{create_example_registry, create_worker_registry};
use dex_sdk::{
    BlobCache, BlobCacheConfig, Client, ClientOptions, Flow, FlowResult, FlowStatus, Rpc,
    SdkResult, Step, StepExecutionId, TimerId, Value, WaitForStepCompletionOptions, Worker,
    WorkerOptions,
};
use tempfile::TempDir;
use worker_harness::await_pull_registration;

const WAIT_TIMEOUT: Duration = Duration::from_secs(30);
const POLL_INTERVAL: Duration = Duration::from_millis(50);

struct ControlPatternsEnvironment {
    client: Arc<Client>,
    worker: Arc<Worker>,
    worker_thread: Option<JoinHandle<SdkResult<()>>>,
    cache: Arc<BlobCache>,
    _cache_directory: TempDir,
}

impl ControlPatternsEnvironment {
    fn start() -> Self {
        let polling_jobs = Arc::new(FakeJobService::default());
        let server_address =
            std::env::var("DEX_SERVER_ADDRESS").unwrap_or_else(|_| "127.0.0.1:8801".to_string());
        let cache_directory = tempfile::tempdir().expect("create control patterns cache directory");
        let cache = Arc::new(
            BlobCache::open(
                BlobCacheConfig::new(cache_directory.path(), 64 * 1024 * 1024, 10_000)
                    .expect("valid control patterns cache config"),
            )
            .expect("open control patterns cache"),
        );
        let client = Arc::new(
            Client::try_new(
                create_example_registry(Arc::clone(&polling_jobs))
                    .and_then(register_condition_order_probes)
                    .expect("register Rust examples"),
                Arc::clone(&cache),
                ClientOptions::new().server_address(server_address.clone()),
            )
            .expect("create control patterns Client"),
        );
        let worker_registry = create_worker_registry(Arc::clone(&client), polling_jobs)
            .and_then(register_condition_order_probes)
            .expect("register Rust example Worker");
        let worker_options = WorkerOptions::new()
            .server_address(&server_address)
            .pull(example_pull_worker_options());
        let worker = Arc::new(
            Worker::try_new(worker_registry, Arc::clone(&cache), worker_options)
                .expect("create control patterns Worker"),
        );
        let thread_worker = Arc::clone(&worker);
        let worker_thread = thread::Builder::new()
            .name("dex-control-patterns-worker".to_string())
            .spawn(move || thread_worker.start())
            .expect("start control patterns Worker thread");
        await_pull_registration(&worker);
        client.health_check().expect("Dex health check");
        Self {
            client,
            worker,
            worker_thread: Some(worker_thread),
            cache,
            _cache_directory: cache_directory,
        }
    }

    fn start_flow<SomeFlow: Flow>(
        &self,
        flow: &SomeFlow,
        prefix: &str,
        input: SomeFlow::StartInput,
    ) -> String {
        let flow_id = unique_flow_id(prefix);
        self.client
            .start_flow(flow, &flow_id, input)
            .unwrap_or_else(|error| panic!("start {}: {error}", flow.flow_type()));
        flow_id
    }

    fn invoke<Input: Value, Output: Value>(
        &self,
        flow_id: &str,
        rpc: Rpc<Input, Output>,
        input: Input,
    ) -> Output {
        self.client
            .invoke_rpc(flow_id, rpc, input)
            .unwrap_or_else(|error| panic!("invoke RPC on {flow_id}: {error}"))
    }

    fn wait_for_step<SomeStep: Step>(&self, flow_id: &str, step: &SomeStep) {
        self.client
            .wait_for_step_completion(
                flow_id,
                StepExecutionId::of(step).execution_number(1),
                WaitForStepCompletionOptions::new().request_timeout(WAIT_TIMEOUT),
            )
            .unwrap_or_else(|error| panic!("wait for {}-1: {error}", step.step_type()));
    }

    fn wait_for_closed_flow(&self, flow_id: &str, expected: FlowStatus) -> FlowResult {
        let result = self
            .client
            .wait_for_flow_with_timeout(flow_id, WAIT_TIMEOUT)
            .unwrap_or_else(|error| panic!("wait for {flow_id}: {error}"));
        assert_eq!(
            result.status(),
            expected,
            "{flow_id} closed with {:?}: {:?}",
            result.error_type(),
            result.error_message()
        );
        result
    }

    fn wait_for_completed_flow(&self, flow_id: &str) -> FlowResult {
        self.wait_for_closed_flow(flow_id, FlowStatus::Completed)
    }

    /// Retries until the Timer of `AwaitOfferResponse` is registered.
    fn skip_offer_expiry(&self, flow_id: &str) {
        let deadline = Instant::now() + WAIT_TIMEOUT;
        loop {
            match self.client.skip_timer(
                flow_id,
                StepExecutionId::of(&AwaitOfferResponse),
                TimerId::by_condition_index(0),
            ) {
                Ok(()) => return,
                Err(error) => assert!(
                    Instant::now() < deadline,
                    "skip the offer expiry of {flow_id}: {error}"
                ),
            }
            thread::sleep(POLL_INTERVAL);
        }
    }

    fn poll_flow_status_until(&self, flow_id: &str, expected: FlowStatus) {
        let deadline = Instant::now() + WAIT_TIMEOUT;
        loop {
            let status = self
                .client
                .describe_flow(flow_id)
                .unwrap_or_else(|error| panic!("describe {flow_id}: {error}"))
                .status;
            if status == expected {
                return;
            }
            assert!(
                Instant::now() < deadline,
                "{flow_id} stayed {status:?}, expected {expected:?}"
            );
            thread::sleep(POLL_INTERVAL);
        }
    }

    fn contract_edit_records(&self, flow_id: &str) -> Vec<ContractEditRecord> {
        let result = self.wait_for_completed_flow(flow_id);
        let mut outputs = outputs_of::<Vec<ContractEditRecord>>(&result, "FinishContractEdits");
        assert_eq!(
            outputs.len(),
            1,
            "exactly one FinishContractEdits completion"
        );
        outputs.remove(0)
    }

    /// Asserts that legal ended with `legal_outcome`, finance still saved,
    /// and the edits never overlapped.
    fn assert_contract_edit_outcomes(&self, flow_id: &str, legal_outcome: &str) {
        let records = self.contract_edit_records(flow_id);
        let outcomes = records
            .iter()
            .map(|record| (record.reviewer.as_str(), record.outcome.as_str()))
            .collect::<BTreeMap<_, _>>();
        assert_eq!(
            outcomes,
            BTreeMap::from([("finance", "saved"), ("legal", legal_outcome)])
        );
        assert_contract_edits_do_not_overlap(&records);
    }
}

impl Drop for ControlPatternsEnvironment {
    fn drop(&mut self) {
        self.worker.stop();
        if let Some(worker_thread) = self.worker_thread.take() {
            worker_thread
                .join()
                .expect("join control patterns Worker")
                .expect("stop control patterns Worker");
        }
        self.cache.close().expect("close control patterns cache");
    }
}

#[derive(Clone, Copy, Debug)]
enum OfferResponse {
    Accept,
    Reject,
    Expire,
}

#[test]
#[ignore = "requires dexcli dev"]
fn deferred_choice_routes_on_the_customer_response() {
    let environment = ControlPatternsEnvironment::start();
    for (response, expected_step, expected_output) in [
        (OfferResponse::Accept, "FulfillOffer", "fulfilled offer-1"),
        (
            OfferResponse::Reject,
            "CloseRejectedOffer",
            "rejected offer-1",
        ),
        (OfferResponse::Expire, "ExpireOffer", "expired offer-1"),
    ] {
        let flow_id = environment.start_flow(
            &OfferResponseFlow::default(),
            "deferred-choice",
            "offer-1".to_string(),
        );
        match response {
            OfferResponse::Accept => {
                environment.invoke(&flow_id, ACCEPT_CUSTOMER_OFFER, "looks good".to_string());
            }
            OfferResponse::Reject => {
                environment.invoke(&flow_id, REJECT_CUSTOMER_OFFER, "too expensive".to_string());
            }
            OfferResponse::Expire => environment.skip_offer_expiry(&flow_id),
        }
        let result = environment.wait_for_completed_flow(&flow_id);
        assert_eq!(
            completion_step_types(&result),
            [expected_step],
            "{response:?}"
        );
        assert_eq!(
            outputs_of::<String>(&result, expected_step),
            [expected_output],
            "{response:?}"
        );
    }
}

#[test]
#[ignore = "requires dexcli dev"]
fn deferred_choice_consumes_only_the_winning_response() {
    let environment = ControlPatternsEnvironment::start();
    let flow_id = environment.start_flow(
        &OfferResponseFlow::default(),
        "deferred-choice-race",
        "offer-2".to_string(),
    );
    environment.invoke(&flow_id, ACCEPT_CUSTOMER_OFFER, "yes".to_string());
    environment.invoke(&flow_id, REJECT_CUSTOMER_OFFER, "no".to_string());
    let result = environment.wait_for_completed_flow(&flow_id);
    let step_types = completion_step_types(&result);
    assert_eq!(step_types.len(), 1, "{step_types:?}");
    assert!(
        ["FulfillOffer", "CloseRejectedOffer"].contains(&step_types[0].as_str()),
        "{step_types:?}"
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn critical_section_runs_one_edit_at_a_time() {
    let environment = ControlPatternsEnvironment::start();
    let flow_id = environment.start_flow(
        &ContractEditFlow::default(),
        "critical-section",
        vec![
            contract_edit("legal"),
            contract_edit("finance"),
            contract_edit("procurement"),
        ],
    );
    let records = environment.contract_edit_records(&flow_id);
    let mut reviewers = records
        .iter()
        .map(|record| record.reviewer.as_str())
        .collect::<Vec<_>>();
    reviewers.sort_unstable();
    assert_eq!(reviewers, ["finance", "legal", "procurement"]);
    for record in &records {
        assert_eq!(record.outcome, "saved", "{record:?}");
    }
    assert_contract_edits_do_not_overlap(&records);
}

#[test]
#[ignore = "requires dexcli dev"]
fn critical_section_returns_the_token_when_an_edit_fails() {
    let environment = ControlPatternsEnvironment::start();
    let flow_id = environment.start_flow(
        &ContractEditFlow::default(),
        "critical-section-failure",
        vec![
            ContractEdit {
                should_fail_edit: true,
                ..contract_edit("legal")
            },
            contract_edit("finance"),
        ],
    );
    environment.assert_contract_edit_outcomes(&flow_id, "failed");
}

#[test]
#[ignore = "requires dexcli dev"]
fn critical_section_returns_the_token_when_a_handoff_fails() {
    let environment = ControlPatternsEnvironment::start();
    let flow_id = environment.start_flow(
        &ContractEditFlow::default(),
        "critical-section-handoff-failure",
        vec![
            ContractEdit {
                should_fail_handoff: true,
                ..contract_edit("legal")
            },
            contract_edit("finance"),
        ],
    );
    environment.assert_contract_edit_outcomes(&flow_id, "failed");
}

#[test]
#[ignore = "requires dexcli dev"]
fn critical_section_returns_the_token_on_an_early_exit() {
    let environment = ControlPatternsEnvironment::start();
    let flow_id = environment.start_flow(
        &ContractEditFlow::default(),
        "critical-section-early-exit",
        vec![
            ContractEdit {
                should_withdraw: true,
                ..contract_edit("legal")
            },
            contract_edit("finance"),
        ],
    );
    environment.assert_contract_edit_outcomes(&flow_id, "withdrawn");
}

#[test]
#[ignore = "requires dexcli dev"]
fn cancel_case_stops_the_running_child_flow() {
    let environment = ControlPatternsEnvironment::start();
    let flow_id = environment.start_flow(
        &LoanApplicationFlow::default(),
        "cancel-case",
        loan_application("applicant-1", 3600),
    );
    environment.invoke(
        &flow_id,
        CANCEL_APPLICATION,
        "applicant withdrew".to_string(),
    );
    let result = environment.wait_for_closed_flow(&flow_id, FlowStatus::Failed);
    assert_canceled_by_the_applicant(&result);
    let (child_flow_id,) = environment
        .client
        .get_attributes(&flow_id, (&*STOPPED_CREDIT_CHECK_FLOW_ID,))
        .expect("read the stopped credit check Flow ID");
    let child_flow_id = child_flow_id.expect("a credit check Flow was stopped");
    assert!(!child_flow_id.is_empty());
    environment.poll_flow_status_until(&child_flow_id, FlowStatus::Canceled);
}

#[test]
#[ignore = "requires dexcli dev"]
fn cancel_case_after_the_child_flow_finished() {
    let environment = ControlPatternsEnvironment::start();
    let flow_id = environment.start_flow(
        &LoanApplicationFlow::default(),
        "cancel-case-late",
        loan_application("applicant-2", 1),
    );
    environment.wait_for_step(&flow_id, &RunCreditCheck::default());
    environment.invoke(
        &flow_id,
        CANCEL_APPLICATION,
        "applicant withdrew".to_string(),
    );
    let result = environment.wait_for_closed_flow(&flow_id, FlowStatus::Failed);
    assert_canceled_by_the_applicant(&result);
}

#[test]
#[ignore = "requires dexcli dev"]
fn cancel_case_completes_with_the_underwriter_decision() {
    let environment = ControlPatternsEnvironment::start();
    let flow_id = environment.start_flow(
        &LoanApplicationFlow::default(),
        "cancel-case-decided",
        loan_application("applicant-3", 1),
    );
    environment.wait_for_step(&flow_id, &RunCreditCheck::default());
    environment.invoke(&flow_id, DECIDE_APPLICATION, "approved".to_string());
    let result = environment.wait_for_completed_flow(&flow_id);
    assert_eq!(
        outputs_of::<String>(&result, "AwaitUnderwriterDecision"),
        ["approved after credit report for applicant-3"]
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn deferred_choice_declared_first_response_wins_when_both_are_pending() {
    let environment = ControlPatternsEnvironment::start();
    let flow_id = environment.start_flow(
        &DeferredChoiceOrderProbeFlow::default(),
        "deferred-choice-order",
        (),
    );
    // Publish the later-declared response first: declaration order wins.
    environment.invoke(&flow_id, REJECT_PROBE_OFFER, "no".to_string());
    environment.invoke(&flow_id, ACCEPT_PROBE_OFFER, "yes".to_string());
    environment.invoke(&flow_id, OPEN_OFFER_PROBE_GATE, ());
    let result = environment.wait_for_completed_flow(&flow_id);
    assert_eq!(
        outputs_of::<ConditionOrderOutcome>(&result, "AwaitOfferProbeResponse"),
        [ConditionOrderOutcome::new("accepted", 1)]
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn cancel_case_cancel_wins_when_both_are_pending() {
    let environment = ControlPatternsEnvironment::start();
    let flow_id = environment.start_flow(
        &CancelCaseOrderProbeFlow::default(),
        "cancel-case-order",
        (),
    );
    environment.invoke(&flow_id, DECIDE_PROBE_APPLICATION, "approved".to_string());
    environment.invoke(
        &flow_id,
        CANCEL_PROBE_APPLICATION,
        "applicant withdrew".to_string(),
    );
    environment.invoke(&flow_id, OPEN_DECISION_PROBE_GATE, ());
    let result = environment.wait_for_completed_flow(&flow_id);
    assert_eq!(
        outputs_of::<ConditionOrderOutcome>(&result, "AwaitProbeDecision"),
        [ConditionOrderOutcome::new("canceled", 1)]
    );
}

fn unique_flow_id(prefix: &str) -> String {
    let timestamp = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .expect("current time after Unix epoch")
        .as_nanos();
    format!("rust-pattern-{prefix}-{}-{timestamp}", std::process::id())
}

fn contract_edit(reviewer: &str) -> ContractEdit {
    ContractEdit {
        reviewer: reviewer.to_string(),
        ..ContractEdit::default()
    }
}

fn loan_application(applicant_id: &str, credit_report_delay_seconds: u64) -> LoanApplication {
    LoanApplication {
        applicant_id: applicant_id.to_string(),
        credit_report_delay_seconds,
    }
}

fn completion_step_types(result: &FlowResult) -> Vec<String> {
    result
        .completions()
        .iter()
        .map(|completion| completion.step_type.clone())
        .collect()
}

/// Decodes the outputs of one Step type in execution order.
fn outputs_of<Output: Value>(result: &FlowResult, step_type: &str) -> Vec<Output> {
    result
        .completions()
        .iter()
        .filter(|completion| completion.step_type == step_type)
        .map(|completion| {
            completion
                .decode()
                .unwrap_or_else(|error| panic!("decode {step_type} output: {error}"))
        })
        .collect()
}

fn assert_canceled_by_the_applicant(result: &FlowResult) {
    let error_message = result.error_message().unwrap_or_default();
    assert!(
        error_message.contains("application canceled: applicant withdrew"),
        "{error_message}"
    );
}

fn assert_contract_edits_do_not_overlap(records: &[ContractEditRecord]) {
    for (index, left) in records.iter().enumerate() {
        for right in &records[index + 1..] {
            let is_disjoint = left.finished_at_millis <= right.started_at_millis
                || right.finished_at_millis <= left.started_at_millis;
            assert!(is_disjoint, "{left:?} overlaps {right:?}");
        }
    }
}
