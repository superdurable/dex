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

//! Integration tests for the Workflow Patterns Flows. Each test asserts the
//! behavior that `examples/workflow-patterns-mapping.md` requires.

mod worker_harness;

use std::collections::BTreeSet;
use std::sync::Arc;
use std::thread::{self, JoinHandle};
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use dex_examples_rust::create_example_registry;
use dex_examples_rust::patterns::polling::FakeJobService;
use dex_examples_rust::shared::example_pull_worker_options;
use dex_examples_rust::workflow_patterns::TaskInterval;
use dex_examples_rust::workflow_patterns::and_join_and_threads::{
    DELIVER_INVOICE, DELIVER_PAYMENT, GeneralizedAndJoinFlow, InvoicePaymentMatch,
    InvoicePaymentMatching, RecordMatch, ThreadSplitMergeFlow,
};
use dex_examples_rust::workflow_patterns::basic_control_flow::{
    APPROVE_PAYMENT, ExclusiveChoiceSimpleMergeFlow, PackItems, ParallelSplitSynchronizationFlow,
    SequenceFlow,
};
use dex_examples_rust::workflow_patterns::cancellation::{
    CANCEL_COURIER_PICKUP, CANCEL_TRANSLATIONS, COMPLETE_PEER_REVIEWS_EARLY,
    COMPLETE_SANCTIONS_SCREENING, CancelCaseFlow, CancelMultipleInstanceTaskFlow, CancelRegionFlow,
    CancelTaskFlow, CompleteMultipleInstanceTaskFlow, FINISHED_TRANSLATION_COUNT,
    GET_SUBMITTED_PEER_REVIEW_COUNT, NotifyRejection, REJECT_APPLICATION, RELEASE_PEER_REVIEW,
    RELEASE_TRANSLATION, StartApplicationChecks, StartPeerReviews, StartTranslations,
};
use dex_examples_rust::workflow_patterns::iteration::{
    ArbitraryCyclesFlow, ArticleSubmission, BatchProgress, FactorialRecursionFlow,
    StructuredLoopFlow,
};
use dex_examples_rust::workflow_patterns::multi_choice_and_merges::{
    GeneralSynchronizingMergeFlow, MultiChoiceSynchronizingMergeFlow, MultiMergeFlow,
    NotificationChannelSelection,
};
use dex_examples_rust::workflow_patterns::multiple_instances::{
    ADD_BID, ADD_SAMPLE, ApproveBatch, BidJoinProgress, CLOSE_SAMPLE_INTAKE,
    CancellingPartialJoinForMultipleInstancesFlow, ConfirmRegistration,
    DesignTimeMultipleInstancesFlow, DynamicPartialJoinForMultipleInstancesFlow, InspectionPlan,
    MultipleInstancesWithoutSynchronizationFlow, NoPriorKnowledgeMultipleInstancesFlow,
    RELEASE_INSPECTIONS, RELEASE_INVITATIONS, RELEASE_QUORUM_INSPECTIONS,
    RunTimeMultipleInstancesFlow, StaticPartialJoinForMultipleInstancesFlow,
};
use dex_examples_rust::workflow_patterns::partial_joins::{
    BidFromCarrierA, BlockingJoinRound, BlockingPartialJoinFlow, CancellingPartialJoinFlow,
    FiredBranchesRound, PlaceOrder, ProcessRound, QuoteFromSupplierA, RELEASE_CARRIER_BID,
    RELEASE_SUPPLIER_QUOTE, SEND_BRANCH_SIGNAL, StructuredPartialJoinFlow,
};
use dex_examples_rust::workflow_patterns::state_based::{
    ACCEPT_OFFER, APPROVE_RELEASE, CriticalSectionFlow, DECLINE_OFFER, DeferredChoiceFlow,
    FINALIZE_RELEASE, IS_CHANGE_WINDOW_OPEN, InterleavedParallelRoutingFlow,
    InterleavedRoutingFlow, MilestoneFlow, REQUEST_CONFIG_CHANGE,
};
use dex_examples_rust::workflow_patterns::termination::{
    COMPLETE_PROFILE, CreateAccount, ExplicitTerminationFlow, ImplicitTerminationFlow,
    RESOLVE_INCIDENT, SendWelcomeEmail, StartIncident,
};
use dex_examples_rust::workflow_patterns::triggers::{
    ARM_MOTION_SENSOR, IS_AWAITING_MOTION_EVENT, MARK_CARRIER_READY, PersistentTriggerFlow,
    SEND_MOTION_EVENT, SEND_SHIPMENT_LABEL, TransientTriggerFlow,
};
use dex_sdk::{
    Attribute, AttributeMatch, BlobCache, BlobCacheConfig, Client, ClientOptions, Flow, FlowResult,
    FlowStatus, Rpc, SdkError, SdkResult, Step, StepExecutionId, StopFlowOptions, Value,
    WaitForAttributeOptions, WaitForStepCompletionOptions, Worker, WorkerOptions,
};
use tempfile::TempDir;
use worker_harness::await_pull_registration;

const WAIT_TIMEOUT: Duration = Duration::from_secs(30);
const LOCK_CONFLICT_RETRY_INTERVAL: Duration = Duration::from_millis(50);

struct WorkflowPatternsEnvironment {
    client: Client,
    worker: Arc<Worker>,
    worker_thread: Option<JoinHandle<SdkResult<()>>>,
    cache: Arc<BlobCache>,
    _cache_directory: TempDir,
}

impl WorkflowPatternsEnvironment {
    fn start() -> Self {
        let registry = create_example_registry(Arc::new(FakeJobService::default()))
            .expect("register Rust examples");
        let server_address =
            std::env::var("DEX_SERVER_ADDRESS").unwrap_or_else(|_| "127.0.0.1:8801".to_string());
        let cache_directory =
            tempfile::tempdir().expect("create Workflow Patterns cache directory");
        let cache = Arc::new(
            BlobCache::open(
                BlobCacheConfig::new(cache_directory.path(), 64 * 1024 * 1024, 10_000)
                    .expect("valid Workflow Patterns cache config"),
            )
            .expect("open Workflow Patterns cache"),
        );
        let worker_options = WorkerOptions::new()
            .server_address(&server_address)
            .pull(example_pull_worker_options());
        let worker = Arc::new(
            Worker::try_new(registry.clone(), Arc::clone(&cache), worker_options)
                .expect("create Workflow Patterns Worker"),
        );
        let thread_worker = Arc::clone(&worker);
        let worker_thread = thread::Builder::new()
            .name("dex-workflow-patterns-worker".to_string())
            .spawn(move || thread_worker.start())
            .expect("start Workflow Patterns Worker thread");
        await_pull_registration(&worker);
        let client = Client::try_new(
            registry,
            Arc::clone(&cache),
            ClientOptions::new().server_address(server_address),
        )
        .expect("create Workflow Patterns Client");
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

    /// The RPC fails fast while a Step holds the same Attribute lock.
    fn invoke_retrying_lock_conflicts<Input: Value + Clone, Output: Value>(
        &self,
        flow_id: &str,
        rpc: Rpc<Input, Output>,
        input: Input,
    ) -> Output {
        let deadline = std::time::Instant::now() + WAIT_TIMEOUT;
        loop {
            match self.client.invoke_rpc(flow_id, rpc, input.clone()) {
                Ok(output) => return output,
                Err(SdkError::RpcLockConflict { .. }) if std::time::Instant::now() < deadline => {
                    thread::sleep(LOCK_CONFLICT_RETRY_INTERVAL);
                }
                Err(error) => panic!("invoke locking RPC on {flow_id}: {error}"),
            }
        }
    }

    fn wait_for_step<SomeStep: Step>(&self, flow_id: &str, step: &SomeStep, execution: u32) {
        self.client
            .wait_for_step_completion(
                flow_id,
                StepExecutionId::of(step).execution_number(execution),
                WaitForStepCompletionOptions::new().request_timeout(WAIT_TIMEOUT),
            )
            .unwrap_or_else(|error| panic!("wait for {}-{execution}: {error}", step.step_type()));
    }

    fn wait_for_attribute<T: Value + Clone + PartialEq + std::fmt::Debug>(
        &self,
        flow_id: &str,
        attribute: &Attribute<T>,
        expected: T,
    ) {
        let observed = self
            .client
            .wait_for_attribute_match(
                flow_id,
                attribute,
                AttributeMatch::equal_to(expected.clone()),
                WaitForAttributeOptions::new().request_timeout(WAIT_TIMEOUT),
            )
            .unwrap_or_else(|error| {
                panic!("wait for an Attribute of {flow_id} to become {expected:?}: {error}")
            });
        assert_eq!(observed, expected);
    }

    fn assert_running(&self, flow_id: &str) {
        let status = self
            .client
            .describe_flow(flow_id)
            .expect("describe Workflow Patterns Flow")
            .status;
        assert_eq!(status, FlowStatus::Running, "{flow_id} must still run");
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
}

impl Drop for WorkflowPatternsEnvironment {
    fn drop(&mut self) {
        self.worker.stop();
        if let Some(worker_thread) = self.worker_thread.take() {
            worker_thread
                .join()
                .expect("join Workflow Patterns Worker")
                .expect("stop Workflow Patterns Worker");
        }
        self.cache.close().expect("close Workflow Patterns cache");
    }
}

fn unique_flow_id(prefix: &str) -> String {
    let timestamp = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .expect("current time after Unix epoch")
        .as_nanos();
    format!("rust-wcp-{prefix}-{}-{timestamp}", std::process::id())
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

fn single_output_of<Output: Value>(result: &FlowResult, step_type: &str) -> Output {
    let mut outputs = outputs_of(result, step_type);
    assert_eq!(outputs.len(), 1, "exactly one {step_type} completion");
    outputs.remove(0)
}

fn strings(values: &[&str]) -> Vec<String> {
    values.iter().map(|value| (*value).to_string()).collect()
}

fn assert_no_overlap(intervals: &[TaskInterval]) {
    for (index, interval) in intervals.iter().enumerate() {
        for other in &intervals[index + 1..] {
            assert!(!interval.overlaps(other), "{interval:?} overlaps {other:?}");
        }
    }
}

#[test]
#[ignore = "requires dexcli dev"]
fn wcp01_sequence_runs_steps_in_order() {
    let environment = WorkflowPatternsEnvironment::start();
    let flow_id = environment.start_flow(&SequenceFlow::default(), "sequence", Vec::new());
    let result = environment.wait_for_completed_flow(&flow_id);
    assert_eq!(completion_step_types(&result), ["ShipOrder"]);
    let trail: Vec<String> = single_output_of(&result, "ShipOrder");
    assert_eq!(trail, ["ReceiveOrder", "CheckInventory", "ShipOrder"]);
}

#[test]
#[ignore = "requires dexcli dev"]
fn wcp02_wcp03_parallel_split_synchronizes_both_branches() {
    let environment = WorkflowPatternsEnvironment::start();
    let flow_id = environment.start_flow(
        &ParallelSplitSynchronizationFlow::default(),
        "parallel-split",
        (),
    );
    environment.wait_for_step(&flow_id, &PackItems, 1);
    environment.assert_running(&flow_id);
    environment.invoke(&flow_id, APPROVE_PAYMENT, ());
    let result = environment.wait_for_completed_flow(&flow_id);
    let branches: Vec<String> = single_output_of(&result, "SynchronizeFulfillment");
    assert_eq!(branches, ["ChargePayment", "PackItems"]);
}

#[test]
#[ignore = "requires dexcli dev"]
fn wcp04_wcp05_exclusive_choice_takes_one_branch_and_merges() {
    let environment = WorkflowPatternsEnvironment::start();
    for (claim_amount, expected) in [
        (250, ["RouteClaim", "AutoApprove", "CloseClaim"]),
        (5000, ["RouteClaim", "ManualReview", "CloseClaim"]),
    ] {
        let flow_id = environment.start_flow(
            &ExclusiveChoiceSimpleMergeFlow::default(),
            "exclusive-choice",
            claim_amount,
        );
        let result = environment.wait_for_completed_flow(&flow_id);
        assert_eq!(completion_step_types(&result), ["CloseClaim"]);
        let trail: Vec<String> = single_output_of(&result, "CloseClaim");
        assert_eq!(trail, expected);
    }
}

#[test]
#[ignore = "requires dexcli dev"]
fn wcp06_wcp07_wcp37_multi_choice_merges_the_selected_branches() {
    let environment = WorkflowPatternsEnvironment::start();
    let flow_id = environment.start_flow(
        &MultiChoiceSynchronizingMergeFlow::default(),
        "multi-choice",
        NotificationChannelSelection {
            send_email: true,
            send_sms: false,
            send_push: true,
        },
    );
    let result = environment.wait_for_completed_flow(&flow_id);
    let notifications: Vec<String> = single_output_of(&result, "SynchronizingMerge");
    assert_eq!(notifications, ["SendEmail", "SendPush"]);
}

#[test]
#[ignore = "requires dexcli dev"]
fn wcp08_multi_merge_runs_the_merge_step_once_per_branch() {
    let environment = WorkflowPatternsEnvironment::start();
    let flow_id = environment.start_flow(&MultiMergeFlow::default(), "multi-merge", ());
    let result = environment.wait_for_completed_flow(&flow_id);
    let reviewers: BTreeSet<String> = outputs_of(&result, "RecordReview").into_iter().collect();
    assert_eq!(
        reviewers,
        BTreeSet::from(["LegalReview".to_string(), "FinanceReview".to_string()])
    );
    assert_eq!(outputs_of::<String>(&result, "RecordReview").len(), 2);
}

#[test]
#[ignore = "requires dexcli dev"]
fn wcp38_general_synchronizing_merge_waits_for_the_whole_region() {
    let environment = WorkflowPatternsEnvironment::start();
    let flow_id = environment.start_flow(
        &GeneralSynchronizingMergeFlow::default(),
        "general-synchronizing-merge",
        (),
    );
    let result = environment.wait_for_completed_flow(&flow_id);
    let outputs: Vec<String> = single_output_of(&result, "PublishDecision");
    assert_eq!(
        outputs,
        [
            "ReviewProposal-1",
            "TranslateProposal-0",
            "TranslateProposal-1"
        ]
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn wcp09_structured_discriminator_fires_on_the_first_quote() {
    let environment = WorkflowPatternsEnvironment::start();
    let flow_id = environment.start_flow(
        &StructuredPartialJoinFlow::default(),
        "structured-discriminator",
        1,
    );
    environment.invoke(&flow_id, RELEASE_SUPPLIER_QUOTE, "A".to_string());
    environment.wait_for_step(&flow_id, &PlaceOrder, 1);
    environment.invoke(&flow_id, RELEASE_SUPPLIER_QUOTE, "B".to_string());
    environment.invoke(&flow_id, RELEASE_SUPPLIER_QUOTE, "C".to_string());
    let result = environment.wait_for_completed_flow(&flow_id);
    assert_eq!(
        single_output_of::<Vec<String>>(&result, "PlaceOrder"),
        ["A"]
    );
    assert_eq!(
        single_output_of::<Vec<String>>(&result, "ResetQuoteJoin"),
        ["B", "C"]
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn wcp30_structured_partial_join_fires_on_the_second_quote() {
    let environment = WorkflowPatternsEnvironment::start();
    let flow_id = environment.start_flow(
        &StructuredPartialJoinFlow::default(),
        "structured-partial-join",
        2,
    );
    environment.invoke(&flow_id, RELEASE_SUPPLIER_QUOTE, "A".to_string());
    environment.wait_for_step(&flow_id, &QuoteFromSupplierA, 1);
    environment.invoke(&flow_id, RELEASE_SUPPLIER_QUOTE, "B".to_string());
    environment.wait_for_step(&flow_id, &PlaceOrder, 1);
    environment.invoke(&flow_id, RELEASE_SUPPLIER_QUOTE, "C".to_string());
    let result = environment.wait_for_completed_flow(&flow_id);
    assert_eq!(
        single_output_of::<Vec<String>>(&result, "PlaceOrder"),
        ["A", "B"]
    );
    assert_eq!(
        single_output_of::<Vec<String>>(&result, "ResetQuoteJoin"),
        ["C"]
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn wcp28_blocking_discriminator_holds_a_second_signal_until_the_reset() {
    let environment = WorkflowPatternsEnvironment::start();
    let flow_id = environment.start_flow(
        &BlockingPartialJoinFlow::default(),
        "blocking-discriminator",
        BlockingJoinRound {
            required_signals: 1,
            round: 1,
        },
    );
    let signal = |branch: &str| {
        environment.invoke(&flow_id, SEND_BRANCH_SIGNAL, branch.to_string());
    };
    signal("A");
    environment.wait_for_step(&flow_id, &ProcessRound, 1);
    signal("A");
    signal("B");
    signal("C");
    environment.wait_for_step(&flow_id, &ProcessRound, 2);
    signal("B");
    signal("C");
    let result = environment.wait_for_completed_flow(&flow_id);
    let rounds: Vec<FiredBranchesRound> = outputs_of(&result, "ProcessRound");
    assert_eq!(
        rounds,
        [
            FiredBranchesRound {
                round: 1,
                branches: strings(&["A"]),
            },
            FiredBranchesRound {
                round: 2,
                branches: strings(&["A"]),
            },
        ]
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn wcp31_blocking_partial_join_holds_second_round_signals_until_the_reset() {
    let environment = WorkflowPatternsEnvironment::start();
    let flow_id = environment.start_flow(
        &BlockingPartialJoinFlow::default(),
        "blocking-partial-join",
        BlockingJoinRound {
            required_signals: 2,
            round: 1,
        },
    );
    let signal = |branch: &str| {
        environment.invoke(&flow_id, SEND_BRANCH_SIGNAL, branch.to_string());
    };
    signal("A");
    signal("B");
    environment.wait_for_step(&flow_id, &ProcessRound, 1);
    signal("A");
    signal("B");
    signal("C");
    environment.wait_for_step(&flow_id, &ProcessRound, 2);
    signal("C");
    let result = environment.wait_for_completed_flow(&flow_id);
    let rounds: Vec<FiredBranchesRound> = outputs_of(&result, "ProcessRound");
    assert_eq!(
        rounds,
        [
            FiredBranchesRound {
                round: 1,
                branches: strings(&["A", "B"]),
            },
            FiredBranchesRound {
                round: 2,
                branches: strings(&["A", "B"]),
            },
        ]
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn wcp29_cancelling_discriminator_cancels_the_other_bids() {
    let environment = WorkflowPatternsEnvironment::start();
    let flow_id = environment.start_flow(
        &CancellingPartialJoinFlow::default(),
        "cancelling-discriminator",
        1,
    );
    environment.invoke(&flow_id, RELEASE_CARRIER_BID, "B".to_string());
    let result = environment.wait_for_completed_flow(&flow_id);
    assert_eq!(completion_step_types(&result), ["AcceptBids"]);
    assert_eq!(
        single_output_of::<Vec<String>>(&result, "AcceptBids"),
        ["B"]
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn wcp32_cancelling_partial_join_cancels_the_remaining_bid() {
    let environment = WorkflowPatternsEnvironment::start();
    let flow_id = environment.start_flow(
        &CancellingPartialJoinFlow::default(),
        "cancelling-partial-join",
        2,
    );
    environment.invoke(&flow_id, RELEASE_CARRIER_BID, "A".to_string());
    environment.wait_for_step(&flow_id, &BidFromCarrierA, 1);
    environment.invoke(&flow_id, RELEASE_CARRIER_BID, "C".to_string());
    let result = environment.wait_for_completed_flow(&flow_id);
    assert_eq!(completion_step_types(&result), ["AcceptBids"]);
    assert_eq!(
        single_output_of::<Vec<String>>(&result, "AcceptBids"),
        ["A", "C"]
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn wcp33_generalized_and_join_keeps_extra_messages_for_the_next_firing() {
    let environment = WorkflowPatternsEnvironment::start();
    let flow_id = environment.start_flow(
        &GeneralizedAndJoinFlow::default(),
        "generalized-and-join",
        InvoicePaymentMatching {
            expected_matches: 2,
            matched_count: 0,
        },
    );
    environment.invoke(&flow_id, DELIVER_INVOICE, "I1".to_string());
    environment.invoke(&flow_id, DELIVER_INVOICE, "I2".to_string());
    environment.invoke(&flow_id, DELIVER_PAYMENT, "P1".to_string());
    environment.wait_for_step(&flow_id, &RecordMatch, 1);
    environment.assert_running(&flow_id);
    environment.invoke(&flow_id, DELIVER_PAYMENT, "P2".to_string());
    let result = environment.wait_for_completed_flow(&flow_id);
    let matches: Vec<InvoicePaymentMatch> = outputs_of(&result, "RecordMatch");
    let invoice_payment_match = |invoice_id: &str, payment_id: &str| InvoicePaymentMatch {
        invoice_id: invoice_id.to_string(),
        payment_id: payment_id.to_string(),
    };
    assert_eq!(
        matches,
        [
            invoice_payment_match("I1", "P1"),
            invoice_payment_match("I2", "P2")
        ]
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn wcp41_wcp42_thread_split_merges_every_parcel() {
    let environment = WorkflowPatternsEnvironment::start();
    let flow_id = environment.start_flow(&ThreadSplitMergeFlow::default(), "thread-split", 3);
    let result = environment.wait_for_completed_flow(&flow_id);
    assert_eq!(completion_step_types(&result), ["MergeParcels"]);
    assert_eq!(
        single_output_of::<Vec<usize>>(&result, "MergeParcels"),
        [0, 1, 2]
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn wcp12_multiple_instances_run_without_synchronization() {
    let environment = WorkflowPatternsEnvironment::start();
    let flow_id = environment.start_flow(
        &MultipleInstancesWithoutSynchronizationFlow::default(),
        "instances-without-synchronization",
        strings(&["ana", "ben"]),
    );
    environment.wait_for_step(&flow_id, &ConfirmRegistration, 1);
    environment.assert_running(&flow_id);
    environment.invoke(&flow_id, RELEASE_INVITATIONS, 2);
    let result = environment.wait_for_completed_flow(&flow_id);
    let attendees: BTreeSet<String> = outputs_of(&result, "SendInvitation").into_iter().collect();
    assert_eq!(
        attendees,
        BTreeSet::from(["ana".to_string(), "ben".to_string()])
    );
    assert_eq!(
        outputs_of::<String>(&result, "ConfirmRegistration"),
        ["ConfirmRegistration"]
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn wcp13_design_time_instances_join_all_reviews() {
    let environment = WorkflowPatternsEnvironment::start();
    let flow_id = environment.start_flow(
        &DesignTimeMultipleInstancesFlow::default(),
        "instances-design-time",
        (),
    );
    let result = environment.wait_for_completed_flow(&flow_id);
    assert_eq!(
        single_output_of::<Vec<String>>(&result, "CollectReviews"),
        ["finance", "legal", "security"]
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn wcp14_run_time_instances_join_all_reviews() {
    let environment = WorkflowPatternsEnvironment::start();
    let flow_id = environment.start_flow(
        &RunTimeMultipleInstancesFlow::default(),
        "instances-run-time",
        strings(&["ana", "ben", "cy", "dee"]),
    );
    let result = environment.wait_for_completed_flow(&flow_id);
    assert_eq!(
        single_output_of::<Vec<String>>(&result, "CollectReviews"),
        ["ana", "ben", "cy", "dee"]
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn wcp15_instances_without_prior_knowledge_join_once_intake_closes() {
    let environment = WorkflowPatternsEnvironment::start();
    let flow_id = environment.start_flow(
        &NoPriorKnowledgeMultipleInstancesFlow::default(),
        "instances-no-prior-knowledge",
        Vec::new(),
    );
    for sample_id in ["s1", "s2", "s3"] {
        let is_added =
            environment.invoke_retrying_lock_conflicts(&flow_id, ADD_SAMPLE, sample_id.to_string());
        assert!(is_added, "{sample_id} must be added");
    }
    environment.invoke_retrying_lock_conflicts(&flow_id, CLOSE_SAMPLE_INTAKE, ());
    let result = environment.wait_for_completed_flow(&flow_id);
    assert_eq!(
        single_output_of::<Vec<String>>(&result, "ReportSamples"),
        ["s1", "s2", "s3"]
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn wcp34_static_partial_join_for_instances_resets_after_the_rest() {
    let environment = WorkflowPatternsEnvironment::start();
    let flow_id = environment.start_flow(
        &StaticPartialJoinForMultipleInstancesFlow::default(),
        "instances-static-partial-join",
        InspectionPlan {
            inspection_count: 3,
            required_inspections: 2,
        },
    );
    environment.invoke(&flow_id, RELEASE_INSPECTIONS, 2);
    environment.wait_for_step(&flow_id, &ApproveBatch, 1);
    environment.assert_running(&flow_id);
    environment.invoke(&flow_id, RELEASE_INSPECTIONS, 1);
    let result = environment.wait_for_completed_flow(&flow_id);
    let approved: Vec<usize> = single_output_of(&result, "ApproveBatch");
    let ignored: Vec<usize> = single_output_of(&result, "ResetInspectionJoin");
    assert_eq!(approved.len(), 2);
    assert_eq!(ignored.len(), 1);
    let every_unit: BTreeSet<usize> = approved.into_iter().chain(ignored).collect();
    assert_eq!(every_unit, BTreeSet::from([0, 1, 2]));
}

#[test]
#[ignore = "requires dexcli dev"]
fn wcp35_cancelling_partial_join_for_instances_cancels_the_rest() {
    let environment = WorkflowPatternsEnvironment::start();
    let flow_id = environment.start_flow(
        &CancellingPartialJoinForMultipleInstancesFlow::default(),
        "instances-cancelling-partial-join",
        InspectionPlan {
            inspection_count: 3,
            required_inspections: 2,
        },
    );
    environment.invoke(&flow_id, RELEASE_QUORUM_INSPECTIONS, 2);
    let result = environment.wait_for_completed_flow(&flow_id);
    assert_eq!(completion_step_types(&result), ["AcceptQuorumInspections"]);
    let inspected: BTreeSet<usize> =
        single_output_of::<Vec<usize>>(&result, "AcceptQuorumInspections")
            .into_iter()
            .collect();
    assert_eq!(inspected.len(), 2);
}

#[test]
#[ignore = "requires dexcli dev"]
fn wcp36_dynamic_partial_join_for_instances_completes_after_two_accepted_bids() {
    let environment = WorkflowPatternsEnvironment::start();
    let flow_id = environment.start_flow(
        &DynamicPartialJoinForMultipleInstancesFlow::default(),
        "instances-dynamic-partial-join",
        BidJoinProgress {
            required_accepted_bids: 2,
            accepted_bids: Vec::new(),
        },
    );
    for amount in [500, 1500, 700] {
        let is_admitted = environment.invoke_retrying_lock_conflicts(&flow_id, ADD_BID, amount);
        assert!(is_admitted, "bid {amount} must be admitted");
    }
    let result = environment.wait_for_completed_flow(&flow_id);
    assert_eq!(
        single_output_of::<Vec<i64>>(&result, "AwardContract"),
        [500, 700]
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn wcp16_deferred_choice_runs_only_the_first_response_branch() {
    let environment = WorkflowPatternsEnvironment::start();
    for (rpc, expected) in [
        (DECLINE_OFFER, "ArchiveOffer"),
        (ACCEPT_OFFER, "FulfillOffer"),
    ] {
        let flow_id = environment.start_flow(&DeferredChoiceFlow::default(), "deferred-choice", ());
        environment.invoke(&flow_id, rpc, ());
        let result = environment.wait_for_completed_flow(&flow_id);
        assert_eq!(completion_step_types(&result), [expected]);
    }
}

#[test]
#[ignore = "requires dexcli dev"]
fn wcp17_interleaved_parallel_routing_never_overlaps_and_keeps_order() {
    let environment = WorkflowPatternsEnvironment::start();
    let flow_id = environment.start_flow(
        &InterleavedParallelRoutingFlow::default(),
        "interleaved-parallel-routing",
        (),
    );
    let result = environment.wait_for_completed_flow(&flow_id);
    let intervals: Vec<TaskInterval> = single_output_of(&result, "CompleteOnboardingTasks");
    let tasks: BTreeSet<&str> = intervals
        .iter()
        .map(|interval| interval.task.as_str())
        .collect();
    assert_eq!(
        tasks,
        BTreeSet::from(["PrepareContract", "RunCreditCheck", "SignContract"])
    );
    assert_eq!(intervals.len(), 3);
    assert_no_overlap(&intervals);
    let interval_of = |task: &str| {
        intervals
            .iter()
            .find(|interval| interval.task == task)
            .unwrap_or_else(|| panic!("missing {task} interval"))
    };
    assert!(
        interval_of("PrepareContract").finished_at_millis
            <= interval_of("SignContract").started_at_millis
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn wcp18_milestone_allows_changes_only_while_the_window_is_open() {
    let environment = WorkflowPatternsEnvironment::start();
    let flow_id = environment.start_flow(&MilestoneFlow::default(), "milestone", ());
    environment.wait_for_attribute(&flow_id, &IS_CHANGE_WINDOW_OPEN, true);
    assert!(environment.invoke(&flow_id, REQUEST_CONFIG_CHANGE, "raise-limit".to_string()));
    environment.invoke(&flow_id, APPROVE_RELEASE, ());
    environment.wait_for_attribute(&flow_id, &IS_CHANGE_WINDOW_OPEN, false);
    assert!(!environment.invoke(&flow_id, REQUEST_CONFIG_CHANGE, "late-change".to_string()));
    environment.invoke(&flow_id, FINALIZE_RELEASE, ());
    let result = environment.wait_for_completed_flow(&flow_id);
    assert_eq!(
        outputs_of::<String>(&result, "ApplyConfigChange"),
        ["raise-limit"]
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn wcp39_critical_section_runs_one_section_at_a_time() {
    let environment = WorkflowPatternsEnvironment::start();
    let flow_id = environment.start_flow(&CriticalSectionFlow::default(), "critical-section", ());
    let result = environment.wait_for_completed_flow(&flow_id);
    let intervals: Vec<TaskInterval> = single_output_of(&result, "FinishCriticalSections");
    let sections: BTreeSet<&str> = intervals
        .iter()
        .map(|interval| interval.task.as_str())
        .collect();
    assert_eq!(sections, BTreeSet::from(["inventory", "pricing"]));
    assert_eq!(intervals.len(), 2);
    assert_no_overlap(&intervals);
}

#[test]
#[ignore = "requires dexcli dev"]
fn wcp40_interleaved_routing_runs_audits_one_at_a_time() {
    let environment = WorkflowPatternsEnvironment::start();
    let flow_id = environment.start_flow(
        &InterleavedRoutingFlow::default(),
        "interleaved-routing",
        (),
    );
    let result = environment.wait_for_completed_flow(&flow_id);
    let intervals: Vec<TaskInterval> = single_output_of(&result, "CompleteAudits");
    let audits: BTreeSet<&str> = intervals
        .iter()
        .map(|interval| interval.task.as_str())
        .collect();
    assert_eq!(
        audits,
        BTreeSet::from(["AuditExpenses", "AuditPayroll", "AuditTaxes"])
    );
    assert_eq!(intervals.len(), 3);
    assert_no_overlap(&intervals);
}

#[test]
#[ignore = "requires dexcli dev"]
fn wcp19_cancel_task_replaces_the_pickup_with_self_delivery() {
    let environment = WorkflowPatternsEnvironment::start();
    let flow_id = environment.start_flow(&CancelTaskFlow::default(), "cancel-task", ());
    environment.invoke(&flow_id, CANCEL_COURIER_PICKUP, ());
    let result = environment.wait_for_completed_flow(&flow_id);
    assert_eq!(completion_step_types(&result), ["ArrangeSelfDelivery"]);
}

#[test]
#[ignore = "requires dexcli dev"]
fn wcp20_cancel_case_stops_the_whole_flow() {
    let environment = WorkflowPatternsEnvironment::start();
    let flow_id = environment.start_flow(&CancelCaseFlow::default(), "cancel-case", ());
    environment
        .client
        .stop_flow(
            &flow_id,
            StopFlowOptions::cancel().reason("applicant withdrew"),
        )
        .expect("cancel CancelCaseFlow");
    let result = environment.wait_for_closed_flow(&flow_id, FlowStatus::Canceled);
    assert!(result.completions().is_empty());
}

#[test]
#[ignore = "requires dexcli dev"]
fn wcp25_cancel_region_cancels_two_checks_and_keeps_the_third() {
    let environment = WorkflowPatternsEnvironment::start();
    let flow_id = environment.start_flow(&CancelRegionFlow::default(), "cancel-region", ());
    environment.wait_for_step(&flow_id, &StartApplicationChecks, 1);
    environment.invoke(&flow_id, REJECT_APPLICATION, ());
    environment.wait_for_step(&flow_id, &NotifyRejection, 1);
    environment.assert_running(&flow_id);
    environment.invoke(&flow_id, COMPLETE_SANCTIONS_SCREENING, ());
    let result = environment.wait_for_completed_flow(&flow_id);
    let step_types: BTreeSet<String> = completion_step_types(&result).into_iter().collect();
    assert_eq!(
        step_types,
        BTreeSet::from(["NotifyRejection".to_string(), "ScreenSanctions".to_string()])
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn wcp26_cancel_multiple_instance_task_keeps_finished_translations() {
    let environment = WorkflowPatternsEnvironment::start();
    let flow_id = environment.start_flow(
        &CancelMultipleInstanceTaskFlow::default(),
        "cancel-instances",
        3,
    );
    environment.wait_for_step(&flow_id, &StartTranslations, 1);
    environment.invoke(&flow_id, RELEASE_TRANSLATION, ());
    environment.wait_for_attribute(&flow_id, &FINISHED_TRANSLATION_COUNT, 1);
    environment.invoke(&flow_id, CANCEL_TRANSLATIONS, ());
    let result = environment.wait_for_completed_flow(&flow_id);
    assert_eq!(outputs_of::<usize>(&result, "TranslateDocument").len(), 1);
    assert_eq!(single_output_of::<i64>(&result, "ReportTranslations"), 1);
}

#[test]
#[ignore = "requires dexcli dev"]
fn wcp27_complete_multiple_instance_task_continues_with_submitted_reviews() {
    let environment = WorkflowPatternsEnvironment::start();
    let flow_id = environment.start_flow(
        &CompleteMultipleInstanceTaskFlow::default(),
        "complete-instances",
        3,
    );
    environment.wait_for_step(&flow_id, &StartPeerReviews, 1);
    environment.invoke(&flow_id, RELEASE_PEER_REVIEW, ());
    let deadline = std::time::Instant::now() + WAIT_TIMEOUT;
    while environment.invoke(&flow_id, GET_SUBMITTED_PEER_REVIEW_COUNT, ()) != 1 {
        assert!(
            std::time::Instant::now() < deadline,
            "one peer review was never submitted"
        );
        thread::yield_now();
    }
    environment.invoke(&flow_id, COMPLETE_PEER_REVIEWS_EARLY, ());
    let result = environment.wait_for_completed_flow(&flow_id);
    assert_eq!(completion_step_types(&result), ["PublishReviewResults"]);
    assert_eq!(
        single_output_of::<Vec<usize>>(&result, "PublishReviewResults").len(),
        1
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn wcp10_arbitrary_cycles_enter_the_cycle_at_either_step() {
    let environment = WorkflowPatternsEnvironment::start();
    for (has_draft, expected) in [
        (
            false,
            strings(&[
                "RouteSubmission",
                "DraftArticle",
                "EditArticle",
                "DraftArticle",
                "EditArticle",
                "PublishArticle",
            ]),
        ),
        (
            true,
            strings(&[
                "RouteSubmission",
                "EditArticle",
                "DraftArticle",
                "EditArticle",
                "PublishArticle",
            ]),
        ),
    ] {
        let flow_id = environment.start_flow(
            &ArbitraryCyclesFlow::default(),
            "arbitrary-cycles",
            ArticleSubmission { has_draft },
        );
        let result = environment.wait_for_completed_flow(&flow_id);
        assert_eq!(
            single_output_of::<Vec<String>>(&result, "PublishArticle"),
            expected
        );
    }
}

#[test]
#[ignore = "requires dexcli dev"]
fn wcp21_structured_loop_tests_before_each_iteration() {
    let environment = WorkflowPatternsEnvironment::start();
    for (remaining_batches, expected) in [(3, 3), (0, 0)] {
        let flow_id = environment.start_flow(
            &StructuredLoopFlow::default(),
            "structured-loop",
            BatchProgress {
                remaining_batches,
                processed_batches: 0,
            },
        );
        let result = environment.wait_for_completed_flow(&flow_id);
        assert_eq!(single_output_of::<u32>(&result, "FinishBatches"), expected);
    }
}

#[test]
#[ignore = "requires dexcli dev"]
fn wcp22_recursion_computes_a_factorial_through_sub_flows() {
    let environment = WorkflowPatternsEnvironment::start();
    let flow_id =
        environment.start_flow(&FactorialRecursionFlow::default(), "factorial-recursion", 4);
    let result = environment.wait_for_completed_flow(&flow_id);
    assert_eq!(single_output_of::<i64>(&result, "ComputeFactorial"), 24);
}

#[test]
#[ignore = "requires dexcli dev"]
fn wcp11_implicit_termination_waits_for_every_active_step() {
    let environment = WorkflowPatternsEnvironment::start();
    let flow_id = environment.start_flow(
        &ImplicitTerminationFlow::default(),
        "implicit-termination",
        (),
    );
    environment.wait_for_step(&flow_id, &CreateAccount, 1);
    environment.wait_for_step(&flow_id, &SendWelcomeEmail, 1);
    environment.assert_running(&flow_id);
    environment.invoke(&flow_id, COMPLETE_PROFILE, ());
    let result = environment.wait_for_completed_flow(&flow_id);
    let step_types: BTreeSet<String> = completion_step_types(&result).into_iter().collect();
    assert_eq!(
        step_types,
        BTreeSet::from([
            "AwaitProfileCompletion".to_string(),
            "CreateAccount".to_string(),
            "SendWelcomeEmail".to_string(),
        ])
    );
}

#[test]
#[ignore = "requires dexcli dev"]
fn wcp43_explicit_termination_cancels_the_waiting_steps() {
    let environment = WorkflowPatternsEnvironment::start();
    let flow_id = environment.start_flow(
        &ExplicitTerminationFlow::default(),
        "explicit-termination",
        (),
    );
    environment.wait_for_step(&flow_id, &StartIncident, 1);
    environment.invoke(&flow_id, RESOLVE_INCIDENT, ());
    let result = environment.wait_for_completed_flow(&flow_id);
    assert_eq!(completion_step_types(&result), ["ResolveIncident"]);
}

#[test]
#[ignore = "requires dexcli dev"]
fn wcp23_transient_trigger_drops_events_nobody_waits_for() {
    let environment = WorkflowPatternsEnvironment::start();
    let flow_id = environment.start_flow(&TransientTriggerFlow::default(), "transient-trigger", ());
    assert!(!environment.invoke(&flow_id, SEND_MOTION_EVENT, 1));
    environment.invoke(&flow_id, ARM_MOTION_SENSOR, ());
    environment.wait_for_attribute(&flow_id, &IS_AWAITING_MOTION_EVENT, true);
    assert!(environment.invoke(&flow_id, SEND_MOTION_EVENT, 2));
    let result = environment.wait_for_completed_flow(&flow_id);
    assert_eq!(single_output_of::<i64>(&result, "AwaitMotionEvent"), 2);
}

#[test]
#[ignore = "requires dexcli dev"]
fn wcp24_persistent_trigger_keeps_an_early_label() {
    let environment = WorkflowPatternsEnvironment::start();
    let flow_id =
        environment.start_flow(&PersistentTriggerFlow::default(), "persistent-trigger", ());
    environment.invoke(&flow_id, SEND_SHIPMENT_LABEL, "label-1".to_string());
    environment.invoke(&flow_id, MARK_CARRIER_READY, ());
    let result = environment.wait_for_completed_flow(&flow_id);
    assert_eq!(
        single_output_of::<String>(&result, "AwaitShipmentLabel"),
        "label-1"
    );
}
