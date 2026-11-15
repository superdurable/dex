// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! CloneForkFlow through a registry over a node with real apply: the owner computes the copy of
//! the source point, stages it on the new Flow's partition, and commits FlowCloneFork there.

use std::collections::BTreeMap;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::Duration;

use dexzd_apply::{ApplyContext, FlowStateReader};
use dexzd_engine::{
    CommitUrgency, FlowCoordinationConfig, FlowCoordinationError, LocalStoreConfig, grpc_code,
};
use dexzd_keyspace::row_keys::{
    decode_timer_index_key, flow_clone_fork_staging_key, history_record_key, prefix_range,
    run_named_key, run_singleton_key,
};
use dexzd_keyspace::{
    ColumnFamily, IndexKind, RunId, RunTable, TenantId, flow_directory_key, run_table_prefix,
};
use dexzd_object_store::ObjectStore;
use dexzd_protocol::dex::{
    AttributeWrite, CloneForkFlowRequest, CloneForkFlowResponse, CloseDecision, CloseDecisionType,
    FlowAlreadyStartedOptions, FlowCloneForkPointType, FlowConfig, FlowResetStepMethod,
    FlowResetType, FlowStartOptions, FlowStatus, GetAttributesRequest, GetHistoryEventsRequest,
    IdReusePolicy, InvokeExecuteMethodResponse, ResetFlowRequest, SetAttributesRequest,
    StartFlowRequest, StepDecision, StepOptions, StopFlowRequest, StopType, Value, WorkerTarget,
    flow_history_event, value,
};
use dexzd_protocol::dexzd::v1::{
    AttributeRecord, FlowCloneForkStageCommand, FlowCloneForkStagedChunk,
    FlowCloneForkStagedTarget, FlowCloneForkStagingExpireCommand, FlowCommand, FlowDirectoryRecord,
    HistoryRecord, RetentionTimerId, RunPurgeCommand, RunRecord, StepAttemptStartedCommand,
    StepExecutionRecord, StepInvocationKey, StepInvocationPhase, StepMethod,
    StepMethodCompletedCommand, TimerId, TimersFiredCommand, flow_command::Command,
    step_method_completed_command, stored_value, timer_id,
};
use prost::Message;

use crate::harness::TestCluster;
use crate::time_travel::{
    ScriptedHistory, TravelNode, segmented_opener, string_value, table_value_id,
};

const SOURCE_FLOW: &str = "clone-source";
const START_STEP: &str = "ChargeStep";
const START_STEP_EXECUTION: &str = "ChargeStep-1";

/// Makes the request IDs of Attribute writes unique across the tests of this module.
static NEXT_REQUEST: AtomicUsize = AtomicUsize::new(0);

impl TravelNode {
    fn context_of(&self, flow_id: &str) -> ApplyContext {
        let flow_hash = self.hasher.flow_hash(TenantId::DEFAULT, flow_id);
        ApplyContext {
            partition: self.hasher.partition_of(flow_hash),
            flow_hash,
        }
    }

    /// Returns a Flow ID whose partition equals the source's when `is_same_partition`.
    fn target_flow_id(&self, label: &str, is_same_partition: bool) -> String {
        let source_partition = self.context_of(SOURCE_FLOW).partition;
        (0..)
            .map(|index| format!("clone-{label}-{index}"))
            .find(|flow_id| {
                (self.context_of(flow_id).partition == source_partition) == is_same_partition
            })
            .expect("some Flow ID has the wanted partition")
    }

    async fn start_source(&self, request: StartFlowRequest) -> RunId {
        self.start_flow_with(StartFlowRequest {
            flow_id: SOURCE_FLOW.to_owned(),
            ..request
        })
        .await
    }

    async fn set_flow_attribute(&self, flow_id: &str, key: &str, value: Value) {
        self.registry
            .set_attributes(
                TenantId::DEFAULT,
                SetAttributesRequest {
                    flow_id: flow_id.to_owned(),
                    attributes: vec![AttributeWrite {
                        key: key.to_owned(),
                        value: Some(value),
                        ..AttributeWrite::default()
                    }],
                    request_id: format!(
                        "set-{flow_id}-{key}-{}",
                        NEXT_REQUEST.fetch_add(1, Ordering::SeqCst)
                    ),
                    ..SetAttributesRequest::default()
                },
            )
            .await
            .expect("the Attribute is set");
    }

    async fn clone_fork(
        &self,
        request: CloneForkFlowRequest,
    ) -> Result<CloneForkFlowResponse, FlowCoordinationError> {
        self.registry
            .clone_fork_flow(TenantId::DEFAULT, request)
            .await
    }

    /// Commits `body` for `flow_id` straight to the log, as an effect or an owner does.
    async fn commit_to(
        &self,
        flow_id: &str,
        run_id: Option<RunId>,
        body: Command,
    ) -> dexzd_engine::CommittedCommand {
        let (_, ticket) = self
            .committer()
            .submit(
                self.context_of(flow_id),
                FlowCommand {
                    flow_id: flow_id.to_owned(),
                    run_id: run_id
                        .map(|run_id| run_id.as_bytes().to_vec())
                        .unwrap_or_default(),
                    command: Some(body),
                    ..FlowCommand::default()
                },
                CommitUrgency::Prompt,
            )
            .expect("the command is submitted");
        ticket.wait().await.expect("the command commits")
    }

    fn record_of(&self, flow_id: &str, run_id: RunId) -> RunRecord {
        let key = run_singleton_key(
            self.context_of(flow_id).flow_hash,
            run_id,
            RunTable::RunRecord,
        );
        RunRecord::decode(
            self.view()
                .get(ColumnFamily::Flow, &key)
                .expect("the run record reads")
                .expect("the run record exists")
                .as_slice(),
        )
        .expect("the run record decodes")
    }

    fn directory_of(&self, flow_id: &str) -> FlowDirectoryRecord {
        let key = flow_directory_key(
            self.context_of(flow_id).flow_hash,
            TenantId::DEFAULT,
            flow_id,
        );
        self.view()
            .get(ColumnFamily::Flow, &key)
            .expect("the directory reads")
            .map(|bytes| FlowDirectoryRecord::decode(bytes.as_slice()).expect("it decodes"))
            .unwrap_or_default()
    }

    /// Returns each Attribute of the run with its complete value, except hard-delete Attributes,
    /// whose values live in objects.
    fn attributes_of(&self, flow_id: &str, run_id: RunId) -> BTreeMap<String, Value> {
        let flow_hash = self.context_of(flow_id).flow_hash;
        let prefix = run_table_prefix(flow_hash, run_id, RunTable::Attribute);
        let (start, end) = prefix_range(&prefix);
        let view = self.view();
        view.scan(ColumnFamily::Flow, &start, &end, usize::MAX)
            .expect("the Attributes scan")
            .into_iter()
            .filter_map(|(key, bytes)| {
                let name = String::from_utf8(key[prefix.len()..].to_vec()).expect("UTF-8 key");
                let record = AttributeRecord::decode(bytes.as_slice()).expect("it decodes");
                let stored = record.value.expect("a set Attribute has a value");
                let value = dexzd_apply::values::load_for_wire(
                    &view,
                    flow_hash,
                    run_id,
                    &match stored.kind.expect("a stored value has a kind") {
                        stored_value::Kind::InlineValue(value) => return Some((name, value)),
                        stored_value::Kind::TableValue(reference) => {
                            let id = reference.value_id.expect("a table value has an ID");
                            dexzd_keyspace::ValueReference {
                                run_id,
                                value_id: dexzd_keyspace::ValueId {
                                    run_seq: id.run_seq,
                                    index_in_command: id.index_in_command,
                                },
                            }
                            .to_string()
                        }
                        stored_value::Kind::HardDeleteObject(_) => return None,
                    },
                )
                .expect("the value reads")
                .expect("the copied value is stored in the new run");
                Some((name, value))
            })
            .collect()
    }

    /// Returns the object that holds the value of the run's hard-delete Attribute `key`.
    fn hard_delete_object_of(&self, flow_id: &str, run_id: RunId, key: &str) -> Option<String> {
        let row_key = run_named_key(
            self.context_of(flow_id).flow_hash,
            run_id,
            RunTable::Attribute,
            key,
        );
        let record = AttributeRecord::decode(
            self.view()
                .get(ColumnFamily::Flow, &row_key)
                .expect("the Attribute reads")
                .expect("the Attribute exists")
                .as_slice(),
        )
        .expect("the Attribute decodes");
        match record.value.and_then(|stored| stored.kind) {
            Some(stored_value::Kind::HardDeleteObject(object)) => Some(object.object_id),
            _ => None,
        }
    }

    fn step_of(
        &self,
        flow_id: &str,
        run_id: RunId,
        step_execution_id: &str,
    ) -> StepExecutionRecord {
        let key = run_named_key(
            self.context_of(flow_id).flow_hash,
            run_id,
            RunTable::StepExecution,
            step_execution_id,
        );
        StepExecutionRecord::decode(
            self.view()
                .get(ColumnFamily::Flow, &key)
                .expect("the Step reads")
                .expect("the Step exists")
                .as_slice(),
        )
        .expect("the Step decodes")
    }

    fn history_of(&self, flow_id: &str, run_id: RunId, seq: u64) -> HistoryRecord {
        let key = history_record_key(self.context_of(flow_id).flow_hash, run_id, seq);
        HistoryRecord::decode(
            self.view()
                .get(ColumnFamily::History, &key)
                .expect("the history record reads")
                .expect("the history record exists")
                .as_slice(),
        )
        .expect("the history record decodes")
    }

    /// Returns the fire time of each Flow timeout of the run in the timer index.
    fn flow_timeouts_of(&self, run_id: RunId) -> Vec<i64> {
        self.view()
            .scan(ColumnFamily::PartitionIndex, &[], &[0xff; 64], usize::MAX)
            .expect("the partition index scans")
            .into_iter()
            .filter_map(|(key, _)| decode_timer_index_key(&key))
            .filter(|entry| entry.run_id == run_id)
            .filter_map(|entry| {
                let timer =
                    TimerId::decode(entry.encoded_timer_id.as_slice()).expect("timer decodes");
                matches!(timer.kind, Some(timer_id::Kind::FlowTimeout(_)))
                    .then_some(entry.fire_at_micros)
            })
            .collect()
    }

    fn count_committed(&self, flow_id: &str, matches: fn(&Command) -> bool) -> usize {
        self.recording
            .committed_commands()
            .iter()
            .filter(|committed| {
                committed.command.flow_id == flow_id
                    && committed.command.command.as_ref().is_some_and(matches)
            })
            .count()
    }

    fn has_staging(&self, flow_id: &str, staging_id: &[u8]) -> bool {
        self.view()
            .get(
                ColumnFamily::Flow,
                &flow_clone_fork_staging_key(self.context_of(flow_id).flow_hash, staging_id),
            )
            .expect("the staging reads")
            .is_some()
    }

    fn staging_index_rows(&self, flow_id: &str) -> usize {
        let partition = self.context_of(flow_id).partition;
        let prefix =
            dexzd_keyspace::partition_index_prefix(partition, IndexKind::FlowCloneForkStaging);
        let (start, end) = prefix_range(&prefix);
        self.view()
            .scan(ColumnFamily::PartitionIndex, &start, &end, usize::MAX)
            .expect("the index scans")
            .len()
    }

    /// Starts and completes the start Step's Execute with `response`, as a Worker does.
    async fn run_execute(
        &self,
        flow_id: &str,
        run_id: RunId,
        step_execution_id: &str,
        response: InvokeExecuteMethodResponse,
    ) {
        self.start_execute(flow_id, run_id, step_execution_id).await;
        self.commit_to(
            flow_id,
            Some(run_id),
            Command::StepMethodCompleted(StepMethodCompletedCommand {
                invocation: Some(execute(step_execution_id)),
                attempt: 1,
                response: Some(step_method_completed_command::Response::Execute(response)),
                app_worker_id: "worker-a".to_owned(),
                ..StepMethodCompletedCommand::default()
            }),
        )
        .await;
    }

    async fn start_execute(&self, flow_id: &str, run_id: RunId, step_execution_id: &str) {
        self.commit_to(
            flow_id,
            Some(run_id),
            Command::StepAttemptStarted(StepAttemptStartedCommand {
                invocation: Some(execute(step_execution_id)),
                attempt: 1,
                task_id: vec![7; 16],
                pull_routing: None,
            }),
        )
        .await;
    }
}

fn execute(step_execution_id: &str) -> StepInvocationKey {
    StepInvocationKey {
        step_execution_id: step_execution_id.to_owned(),
        method: StepMethod::Execute as i32,
    }
}

fn pull_config(task_queue: &str) -> FlowConfig {
    FlowConfig {
        worker_target: Some(WorkerTarget {
            is_pull_mode: true,
            task_queue: task_queue.to_owned(),
        }),
        ..FlowConfig::default()
    }
}

fn source_start(request_id: &str) -> StartFlowRequest {
    StartFlowRequest {
        flow_id: SOURCE_FLOW.to_owned(),
        flow_type: "SessionFlow".to_owned(),
        start_step_type: START_STEP.to_owned(),
        step_options: Some(StepOptions {
            skip_wait_for: true,
            ..StepOptions::default()
        }),
        request_id: request_id.to_owned(),
        flow_start_options: Some(FlowStartOptions {
            flow_config_override: Some(pull_config("source-queue")),
            ..FlowStartOptions::default()
        }),
        ..StartFlowRequest::default()
    }
}

fn clone_request(flow_id: &str, request_id: &str) -> CloneForkFlowRequest {
    CloneForkFlowRequest {
        source_flow_id: SOURCE_FLOW.to_owned(),
        flow_id: flow_id.to_owned(),
        request_id: request_id.to_owned(),
        reason: "clone fork test".to_owned(),
        ..CloneForkFlowRequest::default()
    }
}

fn force_complete() -> InvokeExecuteMethodResponse {
    InvokeExecuteMethodResponse {
        step_decision: Some(StepDecision {
            close_decision: Some(CloseDecision {
                close_decision_type: CloseDecisionType::ForceComplete as i32,
                ..CloseDecision::default()
            }),
            ..StepDecision::default()
        }),
        ..InvokeExecuteMethodResponse::default()
    }
}

fn large_text(fill: char, length: usize) -> Value {
    Value {
        kind: Some(value::Kind::StringValue(fill.to_string().repeat(length))),
    }
}

async fn start_node(config: FlowCoordinationConfig) -> (TestCluster, TravelNode) {
    let cluster = TestCluster::new();
    let node = TravelNode::start(
        &cluster,
        LocalStoreConfig::InMemory,
        segmented_opener(),
        config,
    )
    .await;
    (cluster, node)
}

/// A clone of the current point holds the source's Attributes, values, and Steps, on the same
/// partition and on another one; the two runs then change independently.
#[tokio::test(start_paused = true)]
async fn a_current_point_clone_copies_the_state_and_the_source_keeps_running() {
    let (_cluster, node) = start_node(FlowCoordinationConfig::default()).await;
    let source_run = node.start_source(source_start("start-source")).await;
    node.set_flow_attribute(SOURCE_FLOW, "status", string_value("paid"))
        .await;
    node.set_flow_attribute(SOURCE_FLOW, "transcript", large_text('t', 500))
        .await;
    let source_before = node.record_of(SOURCE_FLOW, source_run);
    for (label, is_same_partition) in [("same", true), ("other", false)] {
        let target = node.target_flow_id(label, is_same_partition);
        let response = node
            .clone_fork(clone_request(&target, &format!("clone-{label}")))
            .await
            .expect("the clone fork applies");
        assert_eq!(response.source_run_id, source_run.to_string());
        assert_eq!(response.source_run_attempt, 1);
        assert_eq!(
            response.source_event_id,
            source_before.next_event_id - 1,
            "the current point includes every source event"
        );
        let clone_run: RunId = response.run_id.parse().expect("the run ID parses");
        let clone_record = node.record_of(&target, clone_run);
        assert_eq!(clone_record.status, FlowStatus::Running as i32);
        assert_eq!(clone_record.flow_type, "SessionFlow");
        assert_eq!(clone_record.current_attempt, 1);
        assert_eq!(clone_record.start_request_id, format!("clone-{label}"));
        assert_eq!(clone_record.parent, None);
        assert!(clone_record.sticky_app_worker_id.is_empty());
        assert_eq!(
            clone_record
                .clone_fork_source
                .as_ref()
                .map(|source| source.flow_id.as_str()),
            Some(SOURCE_FLOW)
        );
        assert_eq!(
            node.attributes_of(&target, clone_run),
            node.attributes_of(SOURCE_FLOW, source_run),
            "the clone holds every Attribute value of the source"
        );
        let copied_step = node.step_of(&target, clone_run, START_STEP_EXECUTION);
        let source_step = node.step_of(SOURCE_FLOW, source_run, START_STEP_EXECUTION);
        assert_eq!(copied_step.phase, source_step.phase);
        assert_eq!(copied_step.input, source_step.input);
        let genesis = node.history_of(&target, clone_run, clone_record.window_start_seq);
        let Some(flow_history_event::Payload::FlowStartedOrContinued(started)) =
            genesis.events[0].payload.as_ref()
        else {
            panic!("the clone's first event starts it: {genesis:?}");
        };
        assert_eq!(genesis.events[0].event_id, 1);
        assert_eq!(
            started
                .clone_fork_source
                .as_ref()
                .map(|source| source.event_id),
            Some(response.source_event_id)
        );
        node.set_flow_attribute(&target, "status", string_value("refunded"))
            .await;
    }
    assert_eq!(
        node.record_of(SOURCE_FLOW, source_run).next_run_seq,
        source_before.next_run_seq,
        "a clone fork writes nothing to the source run"
    );
    node.set_flow_attribute(SOURCE_FLOW, "status", string_value("shipped"))
        .await;
    assert_eq!(
        node.attributes_of(SOURCE_FLOW, source_run)["status"],
        string_value("shipped")
    );
    node.shutdown().await;
}

/// Each historical point type copies the Attributes the source held at that point.
#[tokio::test(start_paused = true)]
async fn historical_points_copy_the_state_at_that_point() {
    let (_cluster, node) = start_node(FlowCoordinationConfig::default()).await;
    let source_run = node.start_source(source_start("start-source")).await;
    node.set_flow_attribute(SOURCE_FLOW, "stage", string_value("first"))
        .await;
    let first_record = node.record_of(SOURCE_FLOW, source_run);
    let first_event_id = first_record.next_event_id - 1;
    let first_seq = first_record.next_run_seq - 1;
    let first_time = node
        .history_of(SOURCE_FLOW, source_run, first_seq)
        .timestamp_micros;
    tokio::time::advance(Duration::from_secs(1)).await;
    node.set_flow_attribute(SOURCE_FLOW, "stage", string_value("second"))
        .await;
    let base = |label: &str, point_type: FlowCloneForkPointType| CloneForkFlowRequest {
        point_type: point_type as i32,
        ..clone_request(&format!("clone-{label}"), &format!("request-{label}"))
    };
    let cases = [
        (base("beginning", FlowCloneForkPointType::Beginning), None),
        (
            CloneForkFlowRequest {
                event_id: first_event_id,
                ..base("event", FlowCloneForkPointType::EventId)
            },
            Some("first"),
        ),
        (
            CloneForkFlowRequest {
                history_event_time: (i128::from(first_time) * 1000).to_string(),
                ..base("time", FlowCloneForkPointType::HistoryEventTime)
            },
            Some("first"),
        ),
        (
            CloneForkFlowRequest {
                step_type: START_STEP.to_owned(),
                ..base("step-type", FlowCloneForkPointType::StepType)
            },
            None,
        ),
        (
            CloneForkFlowRequest {
                step_execution_id: START_STEP_EXECUTION.to_owned(),
                step_method: FlowResetStepMethod::Execute as i32,
                ..base("step-execution", FlowCloneForkPointType::StepExecutionId)
            },
            None,
        ),
        (
            CloneForkFlowRequest {
                run_attempt: 1,
                ..base("current", FlowCloneForkPointType::Current)
            },
            Some("never"),
        ),
    ];
    for (request, expected_stage) in cases {
        let flow_id = request.flow_id.clone();
        let result = node.clone_fork(request).await;
        if expected_stage == Some("never") {
            let error = result.expect_err("CURRENT takes no run attempt");
            assert_eq!(error.grpc_code(), grpc_code::INVALID_ARGUMENT);
            continue;
        }
        let response = result.expect("the clone fork applies");
        let clone_run: RunId = response.run_id.parse().expect("the run ID parses");
        let attributes = node.attributes_of(&flow_id, clone_run);
        assert_eq!(
            attributes.get("stage"),
            expected_stage.map(string_value).as_ref(),
            "{flow_id} copies the stage of its point"
        );
    }
    let outside = node
        .clone_fork(CloneForkFlowRequest {
            event_id: first_record.next_event_id + 100,
            ..base("missing", FlowCloneForkPointType::EventId)
        })
        .await
        .expect_err("an event the source never recorded is no point");
    assert_eq!(outside.grpc_code(), grpc_code::FAILED_PRECONDITION);
    node.shutdown().await;
}

/// A closed source run copies an earlier point, whose state is RUNNING, but not its closed
/// current state.
#[tokio::test(start_paused = true)]
async fn a_closed_source_copies_an_earlier_running_point_but_not_its_closed_state() {
    let (_cluster, node) = start_node(FlowCoordinationConfig::default()).await;
    let source_run = node.start_source(source_start("start-source")).await;
    node.set_flow_attribute(SOURCE_FLOW, "stage", string_value("before close"))
        .await;
    let running_event_id = node.record_of(SOURCE_FLOW, source_run).next_event_id - 1;
    node.registry
        .stop_flow(
            TenantId::DEFAULT,
            StopFlowRequest {
                flow_id: SOURCE_FLOW.to_owned(),
                stop_type: StopType::Terminate as i32,
                reason: "done".to_owned(),
                ..StopFlowRequest::default()
            },
        )
        .await
        .expect("the source stops");
    let closed = node
        .clone_fork(clone_request("clone-closed", "request-closed"))
        .await
        .expect_err("a closed state is not copied");
    assert_eq!(closed.grpc_code(), grpc_code::FAILED_PRECONDITION);
    assert!(node.directory_of("clone-closed").runs.is_empty());
    let response = node
        .clone_fork(CloneForkFlowRequest {
            point_type: FlowCloneForkPointType::EventId as i32,
            event_id: running_event_id,
            ..clone_request("clone-earlier", "request-earlier")
        })
        .await
        .expect("an earlier running point is copied");
    let clone_run: RunId = response.run_id.parse().expect("the run ID parses");
    assert_eq!(
        node.record_of("clone-earlier", clone_run).status,
        FlowStatus::Running as i32
    );
    assert_eq!(
        node.attributes_of("clone-earlier", clone_run)["stage"],
        string_value("before close")
    );
    node.shutdown().await;
}

/// The Flow timeout restarts at the clone fork: with the source's duration by default, or with
/// the requested one; a source deadline that already passed does not fire in the new run.
#[tokio::test(start_paused = true)]
async fn the_flow_timeout_restarts_at_the_clone_fork() {
    let (cluster, node) = start_node(FlowCoordinationConfig::default()).await;
    let source_run = node
        .start_source(StartFlowRequest {
            flow_timeout_seconds: 60,
            ..source_start("start-source")
        })
        .await;
    let source_deadline = node
        .record_of(SOURCE_FLOW, source_run)
        .timeout_deadline_micros;
    // The source's deadline passes; no timer service runs in this node.
    cluster.clock.shift(90_000_000);
    let inherited = node
        .clone_fork(clone_request("clone-inherited", "request-inherited"))
        .await
        .expect("the clone fork applies");
    let inherited_run: RunId = inherited.run_id.parse().expect("the run ID parses");
    let record = node.record_of("clone-inherited", inherited_run);
    assert!(record.timeout_deadline_micros > source_deadline);
    assert_eq!(
        record.timeout_deadline_micros - record.started_at_micros,
        60_000_000
    );
    assert_eq!(record.timeout_duration_micros, 60_000_000);
    assert_eq!(
        node.flow_timeouts_of(inherited_run),
        vec![record.timeout_deadline_micros]
    );
    let requested = node
        .clone_fork(CloneForkFlowRequest {
            flow_timeout_seconds: 5,
            ..clone_request("clone-requested", "request-requested")
        })
        .await
        .expect("the clone fork applies");
    let requested_run: RunId = requested.run_id.parse().expect("the run ID parses");
    let record = node.record_of("clone-requested", requested_run);
    assert_eq!(
        record.timeout_deadline_micros - record.started_at_micros,
        5_000_000
    );
    assert_eq!(
        node.flow_timeouts_of(requested_run),
        vec![record.timeout_deadline_micros],
        "only the restarted Flow timeout is armed"
    );
    node.shutdown().await;
}

/// A copy larger than one log record stages in several FlowCloneForkStage commands, and its
/// values arrive complete.
#[tokio::test(start_paused = true)]
async fn a_copy_larger_than_a_log_record_is_staged_in_several_commands() {
    let defaults = FlowCoordinationConfig::default();
    let (_cluster, node) = start_node(FlowCoordinationConfig {
        run_settings: dexzd_protocol::dexzd::v1::RunSettings {
            max_run_state_bytes: 256 * 1024 * 1024,
            ..defaults.run_settings
        },
        ..defaults
    })
    .await;
    let source_run = node.start_source(source_start("start-source")).await;
    for index in 0..5 {
        let fill = char::from(b'a' + index);
        node.set_flow_attribute(
            SOURCE_FLOW,
            &format!("part-{index}"),
            large_text(fill, 7 << 20),
        )
        .await;
    }
    let response = node
        .clone_fork(clone_request("clone-large", "request-large"))
        .await
        .expect("the clone fork applies");
    let clone_run: RunId = response.run_id.parse().expect("the run ID parses");
    assert!(
        node.count_committed("clone-large", |command| matches!(
            command,
            Command::FlowCloneForkStage(_)
        )) >= 5,
        "every 7 MiB value takes its own chunk"
    );
    assert_eq!(
        node.attributes_of("clone-large", clone_run),
        node.attributes_of(SOURCE_FLOW, source_run)
    );
    node.shutdown().await;
}

/// The target's state at the clone fork.
#[derive(Debug, Clone, Copy)]
enum TargetState {
    Absent,
    Running,
    Completed,
    Failed,
}

/// Every ID reuse policy decides on the target as StartFlow does on a Flow in the same state,
/// and ALLOW_TERMINATE_IF_RUNNING terminates the running target in the same change.
#[tokio::test(start_paused = true)]
async fn id_reuse_policies_decide_as_for_start_flow() {
    let (_cluster, node) = start_node(FlowCoordinationConfig::default()).await;
    node.start_source(source_start("start-source")).await;
    let policies = [
        IdReusePolicy::Unspecified,
        IdReusePolicy::AllowIfPreviousExistsAbnormally,
        IdReusePolicy::AllowIfNoRunning,
        IdReusePolicy::DisallowReuse,
        IdReusePolicy::AllowTerminateIfRunning,
    ];
    let states = [
        TargetState::Absent,
        TargetState::Running,
        TargetState::Completed,
        TargetState::Failed,
    ];
    for policy in policies {
        for state in states {
            let label = format!("{}-{state:?}", policy.as_str_name()).to_lowercase();
            let clone_target = format!("clone-{label}");
            let start_target = format!("start-{label}");
            for flow_id in [&clone_target, &start_target] {
                prepare_target(&node, flow_id, state).await;
            }
            let clone_result = node
                .clone_fork(CloneForkFlowRequest {
                    id_reuse_policy: policy as i32,
                    ..clone_request(&clone_target, &format!("clone-request-{label}"))
                })
                .await;
            let start_result = node
                .registry
                .start_flow(
                    TenantId::DEFAULT,
                    StartFlowRequest {
                        flow_id: start_target.clone(),
                        request_id: format!("start-request-{label}"),
                        flow_start_options: Some(FlowStartOptions {
                            id_reuse_policy: policy as i32,
                            flow_config_override: Some(pull_config("")),
                            ..FlowStartOptions::default()
                        }),
                        ..source_start("")
                    },
                )
                .await;
            assert_eq!(
                clone_result
                    .as_ref()
                    .map(|_| ())
                    .map_err(FlowCoordinationError::grpc_code),
                start_result
                    .as_ref()
                    .map(|_| ())
                    .map_err(FlowCoordinationError::grpc_code),
                "{label}: CloneForkFlow decides as StartFlow"
            );
            if let Err(error) = &clone_result {
                assert_eq!(error.grpc_code(), grpc_code::ALREADY_EXISTS, "{label}");
            }
            let clone_runs = node.directory_of(&clone_target).runs;
            let start_runs = node.directory_of(&start_target).runs;
            let statuses = |runs: &[dexzd_protocol::dexzd::v1::FlowDirectoryRun]| {
                runs.iter().map(|run| run.status).collect::<Vec<_>>()
            };
            assert_eq!(statuses(&clone_runs), statuses(&start_runs), "{label}");
        }
    }
    node.shutdown().await;
}

async fn prepare_target(node: &TravelNode, flow_id: &str, state: TargetState) {
    let run_id = match state {
        TargetState::Absent => return,
        TargetState::Running | TargetState::Completed | TargetState::Failed => {
            node.start_flow_with(StartFlowRequest {
                flow_id: flow_id.to_owned(),
                request_id: format!("prepare-{flow_id}"),
                ..source_start("")
            })
            .await
        }
    };
    match state {
        TargetState::Completed => {
            node.run_execute(flow_id, run_id, START_STEP_EXECUTION, force_complete())
                .await;
        }
        TargetState::Failed => {
            node.registry
                .stop_flow(
                    TenantId::DEFAULT,
                    StopFlowRequest {
                        flow_id: flow_id.to_owned(),
                        stop_type: StopType::Fail as i32,
                        ..StopFlowRequest::default()
                    },
                )
                .await
                .expect("the target fails");
        }
        TargetState::Absent | TargetState::Running => {}
    }
}

/// A retry of a committed request returns its run without copying again, or ALREADY_EXISTS
/// when the request does not ignore the already-started error, as StartFlow does.
#[tokio::test(start_paused = true)]
async fn a_retry_returns_the_same_run_without_copying_again() {
    let (_cluster, node) = start_node(FlowCoordinationConfig::default()).await;
    node.start_source(source_start("start-source")).await;
    let request = CloneForkFlowRequest {
        flow_already_started_options: Some(FlowAlreadyStartedOptions {
            ignore_already_started_error: true,
        }),
        ..clone_request("clone-retried", "request-retried")
    };
    let first = node
        .clone_fork(request.clone())
        .await
        .expect("the clone fork applies");
    let stages = node.count_committed("clone-retried", |command| {
        matches!(command, Command::FlowCloneForkStage(_))
    });
    node.set_flow_attribute(SOURCE_FLOW, "later", string_value("not copied"))
        .await;
    let retried = node
        .clone_fork(request.clone())
        .await
        .expect("the retry returns the run");
    assert_eq!(
        retried, first,
        "a retry reports the run and its source point"
    );
    assert_eq!(
        node.count_committed("clone-retried", |command| matches!(
            command,
            Command::FlowCloneForkStage(_)
        )),
        stages,
        "a retry stages nothing"
    );
    let rejected = node
        .clone_fork(CloneForkFlowRequest {
            flow_already_started_options: None,
            ..request
        })
        .await
        .expect_err("without the option a retry is ALREADY_EXISTS");
    assert_eq!(rejected.grpc_code(), grpc_code::ALREADY_EXISTS);
    assert_eq!(node.directory_of("clone-retried").runs.len(), 1);
    node.shutdown().await;
}

/// A staging an interrupted owner left is replaced by the retry, and a staging no chunk extends
/// for 10 minutes expires.
#[tokio::test(start_paused = true)]
async fn an_interrupted_staging_is_replaced_and_an_idle_one_expires() {
    let (cluster, node) = start_node(FlowCoordinationConfig::default()).await;
    node.start_source(source_start("start-source")).await;
    // A chunk 0 that announces more chunks, as an owner that stopped mid-staging leaves.
    let abandoned_id = vec![3; 16];
    let partial_stage = |staging_id: Vec<u8>, request_id: &str| {
        Command::FlowCloneForkStage(FlowCloneForkStageCommand {
            staging_id,
            chunk_index: 0,
            is_last_chunk: false,
            chunk: FlowCloneForkStagedChunk {
                target: Some(FlowCloneForkStagedTarget {
                    request: Some(clone_request("clone-interrupted", request_id)),
                    source: Some(dexzd_protocol::dex::FlowCloneForkSource::default()),
                    source_run_seq: 1,
                    first_run_seq: 2,
                    flow_type: "SessionFlow".to_owned(),
                    run_status: Some(dexzd_protocol::dexzd::v1::CheckpointRunStatus {
                        status: FlowStatus::Running as i32,
                        ..Default::default()
                    }),
                }),
                ..FlowCloneForkStagedChunk::default()
            }
            .encode_to_vec(),
        })
    };
    node.commit_to(
        "clone-interrupted",
        None,
        partial_stage(abandoned_id.clone(), "lost"),
    )
    .await;
    assert!(node.has_staging("clone-interrupted", &abandoned_id));
    let response = node
        .clone_fork(clone_request("clone-interrupted", "request-interrupted"))
        .await
        .expect("the clone fork applies beside the abandoned staging");
    assert!(!response.run_id.is_empty());
    let expire = || {
        Command::FlowCloneForkStagingExpire(FlowCloneForkStagingExpireCommand {
            staging_id: abandoned_id.clone(),
        })
    };
    cluster.clock.shift(9 * 60 * 1_000_000);
    node.commit_to("clone-interrupted", None, expire()).await;
    assert!(
        node.has_staging("clone-interrupted", &abandoned_id),
        "a staging idle for less than 10 minutes stays"
    );
    // Proposal times may run ahead of the paused clock by a microsecond per command.
    cluster.clock.shift(61 * 1_000_000);
    let expired = node.commit_to("clone-interrupted", None, expire()).await;
    assert!(
        !expired.outcome.writes.is_empty(),
        "the expiry deletes the staging"
    );
    assert!(!node.has_staging("clone-interrupted", &abandoned_id));
    assert_eq!(
        node.staging_index_rows("clone-interrupted"),
        0,
        "the clone fork deleted its own staging and the expiry the abandoned one"
    );
    node.shutdown().await;
}

/// A Step running at the point becomes pending again in the new run, with its attempt, while
/// the source's attempt keeps running.
#[tokio::test(start_paused = true)]
async fn a_running_step_is_dispatched_again_in_the_clone() {
    let (_cluster, node) = start_node(FlowCoordinationConfig::default()).await;
    let source_run = node.start_source(source_start("start-source")).await;
    node.start_execute(SOURCE_FLOW, source_run, START_STEP_EXECUTION)
        .await;
    let response = node
        .clone_fork(clone_request("clone-running", "request-running"))
        .await
        .expect("the clone fork applies");
    let clone_run: RunId = response.run_id.parse().expect("the run ID parses");
    let copied = node
        .step_of("clone-running", clone_run, START_STEP_EXECUTION)
        .invocation
        .expect("the copied Step has its call");
    assert_eq!(copied.phase, StepInvocationPhase::Pending as i32);
    assert_eq!(copied.attempt, 1);
    assert!(copied.task_id.is_empty());
    let source = node
        .step_of(SOURCE_FLOW, source_run, START_STEP_EXECUTION)
        .invocation
        .expect("the source Step has its call");
    assert_eq!(source.phase, StepInvocationPhase::Running as i32);
    assert_eq!(source.task_id, vec![7; 16]);
    node.shutdown().await;
}

/// The task queue override routes the new run's Steps; an empty one keeps the source's.
#[tokio::test(start_paused = true)]
async fn the_task_queue_override_routes_the_new_run() {
    let (_cluster, node) = start_node(FlowCoordinationConfig::default()).await;
    node.start_source(source_start("start-source")).await;
    let task_queue_of = |flow_id: &str, response: &CloneForkFlowResponse| {
        let run: RunId = response.run_id.parse().expect("the run ID parses");
        node.record_of(flow_id, run)
            .flow_config
            .and_then(|config| config.worker_target)
            .map(|target| target.task_queue)
            .unwrap_or_default()
    };
    let overridden = node
        .clone_fork(CloneForkFlowRequest {
            flow_config_override: Some(pull_config("clone-queue")),
            ..clone_request("clone-queue", "request-queue")
        })
        .await
        .expect("the clone fork applies");
    assert_eq!(task_queue_of("clone-queue", &overridden), "clone-queue");
    let inherited = node
        .clone_fork(CloneForkFlowRequest {
            flow_config_override: Some(pull_config("")),
            ..clone_request("clone-inherit", "request-inherit")
        })
        .await
        .expect("the clone fork applies");
    assert_eq!(task_queue_of("clone-inherit", &inherited), "source-queue");
    node.shutdown().await;
}

/// A time travel of the new run to its beginning returns to the copied point.
#[tokio::test(start_paused = true)]
async fn a_time_travel_to_the_beginning_returns_to_the_copied_point() {
    let (_cluster, node) = start_node(FlowCoordinationConfig::default()).await;
    node.start_source(source_start("start-source")).await;
    node.set_flow_attribute(SOURCE_FLOW, "report", large_text('r', 400))
        .await;
    let response = node
        .clone_fork(clone_request("clone-traveler", "request-traveler"))
        .await
        .expect("the clone fork applies");
    let clone_run: RunId = response.run_id.parse().expect("the run ID parses");
    let copied = node.attributes_of("clone-traveler", clone_run);
    node.set_flow_attribute("clone-traveler", "report", string_value("replaced"))
        .await;
    node.registry
        .reset_flow(
            TenantId::DEFAULT,
            ResetFlowRequest {
                flow_id: "clone-traveler".to_owned(),
                reset_type: FlowResetType::Beginning as i32,
                skip_writes_reapply: true,
                ..ResetFlowRequest::default()
            },
        )
        .await
        .expect("the new run travels to its beginning");
    assert_eq!(node.attributes_of("clone-traveler", clone_run), copied);
    node.shutdown().await;
}

/// Requests that name no valid clone fork are rejected before anything is read.
#[tokio::test(start_paused = true)]
async fn invalid_requests_are_rejected() {
    let (_cluster, node) = start_node(FlowCoordinationConfig::default()).await;
    node.start_source(source_start("start-source")).await;
    let same = node
        .clone_fork(clone_request(SOURCE_FLOW, "request-same"))
        .await
        .expect_err("the new Flow ID must differ");
    assert_eq!(same.grpc_code(), grpc_code::INVALID_ARGUMENT);
    let missing = node
        .clone_fork(CloneForkFlowRequest {
            source_flow_id: "no-such-flow".to_owned(),
            ..clone_request("clone-missing", "request-missing")
        })
        .await
        .expect_err("the source must exist");
    assert_eq!(missing.grpc_code(), grpc_code::NOT_FOUND);
    let no_request_id = node
        .clone_fork(clone_request("clone-no-request", ""))
        .await
        .expect_err("a request ID is required");
    assert_eq!(no_request_id.grpc_code(), grpc_code::INVALID_ARGUMENT);
    node.shutdown().await;
}

/// A value the point holds that offload already deleted locally is read from its source and
/// stored in the new run, which keeps it whatever happens to the source.
#[tokio::test(start_paused = true)]
async fn an_offloaded_value_of_the_point_is_copied_into_the_new_run() {
    // The scripted history serves the value, as object storage would; offloading helpers name
    // the time travel tests' Flow.
    const OFFLOADED_SOURCE: &str = "travel-flow";
    let history = std::sync::Arc::new(ScriptedHistory::default());
    let cluster = TestCluster::new();
    let node = TravelNode::start(
        &cluster,
        LocalStoreConfig::InMemory,
        history.clone(),
        FlowCoordinationConfig::default(),
    )
    .await;
    let source_run = node
        .start_flow_with(StartFlowRequest {
            flow_id: OFFLOADED_SOURCE.to_owned(),
            ..source_start("start-offloaded")
        })
        .await;
    node.set_flow_attribute(OFFLOADED_SOURCE, "report", large_text('a', 400))
        .await;
    let point_event_id = node.record_of(OFFLOADED_SOURCE, source_run).next_event_id - 1;
    let first_value_id = table_value_id(
        &node
            .attribute(source_run, "report")
            .expect("the report is set"),
    );
    node.set_flow_attribute(OFFLOADED_SOURCE, "report", large_text('b', 400))
        .await;
    let removed = node.offload_superseded_value(source_run, first_value_id);
    history
        .removed_values
        .lock()
        .expect("value lock")
        .insert(first_value_id, removed);
    let response = node
        .clone_fork(CloneForkFlowRequest {
            source_flow_id: OFFLOADED_SOURCE.to_owned(),
            point_type: FlowCloneForkPointType::EventId as i32,
            event_id: point_event_id,
            ..clone_request("clone-offloaded", "request-offloaded")
        })
        .await
        .expect("the clone fork applies");
    let clone_run: RunId = response.run_id.parse().expect("the run ID parses");
    history.removed_values.lock().expect("value lock").clear();
    assert_eq!(
        node.attributes_of("clone-offloaded", clone_run)["report"],
        large_text('a', 400),
        "the new run stores the value the source no longer stores locally"
    );
    node.shutdown().await;
}

/// A point whose history exists only in uploaded segments is read from them, and a point of an
/// earlier run attempt is copied after a time travel of the source.
#[tokio::test(start_paused = true)]
async fn points_in_uploaded_segments_and_earlier_attempts_are_copied() {
    // The offloading helpers name the time travel tests' Flow.
    const TRAVELED_SOURCE: &str = "travel-flow";
    let cluster = TestCluster::new();
    let node = TravelNode::start(
        &cluster,
        LocalStoreConfig::InMemory,
        segmented_opener(),
        crate::time_travel::offloading_config(),
    )
    .await;
    node.enable_history_offload().await;
    let source_run = node.start_flow(None).await;
    node.set_attribute("stage", string_value("paid")).await;
    let paid_event_id = node.run_record(source_run).next_event_id - 1;
    for index in 0..3 {
        node.set_attribute("report", string_value(&format!("{index}").repeat(400)))
            .await;
    }
    node.set_attribute("stage", string_value("shipped")).await;
    let uploaded_through_seq = node.offload_sealed_history(source_run).await;
    assert_eq!(
        uploaded_through_seq,
        node.run_record(source_run).next_run_seq - 1
    );
    let reads_before = node.history_object_reads();
    let from_segments = node
        .clone_fork(CloneForkFlowRequest {
            source_flow_id: TRAVELED_SOURCE.to_owned(),
            point_type: FlowCloneForkPointType::EventId as i32,
            event_id: paid_event_id,
            ..clone_request("clone-from-segments", "request-from-segments")
        })
        .await
        .expect("the clone fork applies");
    assert!(
        node.history_object_reads() > reads_before,
        "the computation read uploaded segments"
    );
    let clone_run: RunId = from_segments.run_id.parse().expect("the run ID parses");
    let copied = node.attributes_of("clone-from-segments", clone_run);
    assert_eq!(copied["stage"], string_value("paid"));
    assert!(!copied.contains_key("report"));

    node.reset(ResetFlowRequest {
        flow_id: TRAVELED_SOURCE.to_owned(),
        reset_type: FlowResetType::Beginning as i32,
        skip_writes_reapply: true,
        ..ResetFlowRequest::default()
    })
    .await
    .expect("the source travels to its beginning");
    let from_attempt_one = node
        .clone_fork(CloneForkFlowRequest {
            source_flow_id: TRAVELED_SOURCE.to_owned(),
            point_type: FlowCloneForkPointType::EventId as i32,
            event_id: paid_event_id,
            run_attempt: 1,
            ..clone_request("clone-from-attempt-one", "request-from-attempt-one")
        })
        .await
        .expect("a point of run attempt 1 is copied");
    assert_eq!(from_attempt_one.source_run_attempt, 1);
    let clone_run: RunId = from_attempt_one.run_id.parse().expect("the run ID parses");
    assert_eq!(
        node.attributes_of("clone-from-attempt-one", clone_run)["stage"],
        string_value("paid")
    );
    node.shutdown().await;
}

/// The retry of a request whose owner stopped mid-staging replaces that partial staging, which
/// has the same staging ID, and creates the run.
#[tokio::test(start_paused = true)]
async fn a_retry_replaces_the_partial_staging_of_its_own_request() {
    let (_cluster, node) = start_node(FlowCoordinationConfig::default()).await;
    node.start_source(source_start("start-source")).await;
    let request = clone_request("clone-resumed", "request-resumed");
    let mut identity = u64::try_from(request.flow_id.len())
        .expect("a short Flow ID")
        .to_be_bytes()
        .to_vec();
    identity.extend_from_slice(request.flow_id.as_bytes());
    identity.extend_from_slice(request.request_id.as_bytes());
    let staging_id = xxhash_rust::xxh3::xxh3_128(&identity)
        .to_be_bytes()
        .to_vec();
    node.commit_to(
        "clone-resumed",
        None,
        Command::FlowCloneForkStage(FlowCloneForkStageCommand {
            staging_id: staging_id.clone(),
            chunk_index: 0,
            is_last_chunk: false,
            chunk: FlowCloneForkStagedChunk {
                target: Some(FlowCloneForkStagedTarget {
                    request: Some(request.clone()),
                    source: Some(dexzd_protocol::dex::FlowCloneForkSource::default()),
                    source_run_seq: 1,
                    first_run_seq: 2,
                    flow_type: "SessionFlow".to_owned(),
                    run_status: Some(dexzd_protocol::dexzd::v1::CheckpointRunStatus {
                        status: FlowStatus::Running as i32,
                        ..Default::default()
                    }),
                }),
                ..FlowCloneForkStagedChunk::default()
            }
            .encode_to_vec(),
        }),
    )
    .await;
    assert!(node.has_staging("clone-resumed", &staging_id));
    let response = node
        .clone_fork(request)
        .await
        .expect("the retry stages again and applies");
    let clone_run: RunId = response.run_id.parse().expect("the run ID parses");
    assert_eq!(
        node.record_of("clone-resumed", clone_run).status,
        FlowStatus::Running as i32
    );
    assert!(!node.has_staging("clone-resumed", &staging_id));
    assert_eq!(node.staging_index_rows("clone-resumed"), 0);
    node.shutdown().await;
}

/// Hard-delete objects in memory: the source run's uploads, then the clone fork's.
#[derive(Default)]
struct MemoryHardDeleteObjects {
    values: std::sync::Mutex<BTreeMap<String, Value>>,
    next_object: AtomicUsize,
    /// The run of each upload call and the keys of its writes.
    uploads: std::sync::Mutex<Vec<(RunId, Vec<String>)>>,
    finished: std::sync::Mutex<Vec<(Vec<String>, dexzd_engine::HardDeleteUploadOutcome)>>,
}

impl dexzd_engine::HardDeleteValueReader for MemoryHardDeleteObjects {
    fn read_hard_delete_value<'a>(
        &'a self,
        _flow: &'a dexzd_engine::FlowAddress,
        _run_id: RunId,
        object: &'a dexzd_protocol::dexzd::v1::HardDeleteObjectReference,
    ) -> futures::future::BoxFuture<'a, Result<Option<Value>, dexzd_engine::HardDeleteReadError>>
    {
        let value = self
            .values
            .lock()
            .expect("value lock")
            .get(&object.object_id)
            .cloned();
        Box::pin(async move { Ok(value) })
    }
}

impl dexzd_engine::HardDeleteValueUploader for MemoryHardDeleteObjects {
    fn upload_hard_delete_values<'a>(
        &'a self,
        _flow: &'a dexzd_engine::FlowAddress,
        run_id: RunId,
        writes: &'a mut [AttributeWrite],
    ) -> futures::future::BoxFuture<
        'a,
        Result<
            dexzd_engine::FlowCoordinationHardDeleteUploads,
            dexzd_engine::HardDeleteUploadError,
        >,
    > {
        self.uploads.lock().expect("upload lock").push((
            run_id,
            writes.iter().map(|write| write.key.clone()).collect(),
        ));
        let mut uploads = dexzd_engine::FlowCoordinationHardDeleteUploads::default();
        for (write_index, write) in writes.iter_mut().enumerate() {
            let Some(value) = write.value.clone().filter(|_| write.is_hard_delete_on_null) else {
                continue;
            };
            let object_id = format!(
                "object-{}",
                self.next_object.fetch_add(1, Ordering::SeqCst) + 1
            );
            uploads
                .references
                .push(dexzd_protocol::dexzd::v1::HardDeleteObjectReference {
                    write_index: u32::try_from(write_index).expect("few writes"),
                    object_id: object_id.clone(),
                    size_bytes: u64::try_from(value.encoded_len()).expect("small value"),
                    is_string_value: matches!(value.kind, Some(value::Kind::StringValue(_))),
                });
            uploads.object_keys.push(object_id.clone());
            self.values
                .lock()
                .expect("value lock")
                .insert(object_id, value);
            write.value = Some(Value { kind: None });
        }
        Box::pin(async move { Ok(uploads) })
    }

    fn finish_hard_delete_uploads(
        &self,
        uploads: dexzd_engine::FlowCoordinationHardDeleteUploads,
        outcome: dexzd_engine::HardDeleteUploadOutcome,
    ) -> futures::future::BoxFuture<'_, Result<(), dexzd_engine::HardDeleteUploadError>> {
        self.finished
            .lock()
            .expect("finish lock")
            .push((uploads.object_keys, outcome));
        Box::pin(async { Ok(()) })
    }
}

fn hard_delete_write(key: &str, text: &str) -> AttributeWrite {
    AttributeWrite {
        key: key.to_owned(),
        value: Some(string_value(text)),
        is_hard_delete_on_null: true,
        ..AttributeWrite::default()
    }
}

/// The source's current hard-delete Attributes reach the new run as objects of its own, beside
/// the request's writes; a clone fork that ID reuse rejects leaves no object behind.
#[tokio::test(start_paused = true)]
async fn hard_delete_attributes_are_uploaded_again_for_the_new_run() {
    let objects = std::sync::Arc::new(MemoryHardDeleteObjects::default());
    let cluster = TestCluster::new();
    let mut config = FlowCoordinationConfig::default();
    config.validation.has_blob_store = true;
    let node = TravelNode::start_with_hard_delete_ports(
        &cluster,
        LocalStoreConfig::InMemory,
        vec![segmented_opener()],
        config,
        dexzd_engine::HistoryReadsConfig::default(),
        crate::time_travel::HardDeletePorts {
            reader: objects.clone(),
            uploader: objects.clone(),
        },
    )
    .await;
    let source_run = node
        .start_source(StartFlowRequest {
            flow_start_options: Some(FlowStartOptions {
                flow_config_override: Some(pull_config("source-queue")),
                attributes: vec![hard_delete_write("secret", "source secret")],
                ..FlowStartOptions::default()
            }),
            ..source_start("start-source")
        })
        .await;
    let request = CloneForkFlowRequest {
        attributes: vec![
            AttributeWrite {
                key: "lease".to_owned(),
                value: Some(string_value("released")),
                ..AttributeWrite::default()
            },
            hard_delete_write("token", "clone token"),
        ],
        ..clone_request("clone-secret", "request-secret")
    };
    let response = node
        .clone_fork(request.clone())
        .await
        .expect("the clone fork applies");
    let clone_run: RunId = response.run_id.parse().expect("the run ID parses");
    let uploads = objects.uploads.lock().expect("upload lock").clone();
    assert_eq!(
        uploads.last(),
        Some(&(
            clone_run,
            vec!["secret".to_owned(), "lease".to_owned(), "token".to_owned()]
        )),
        "the copied hard-delete Attributes upload first, under the new run"
    );
    let object_of =
        |flow_id: &str, run_id: RunId, key: &str| node.hard_delete_object_of(flow_id, run_id, key);
    let source_object = object_of(SOURCE_FLOW, source_run, "secret").expect("an object");
    let copied_object = object_of("clone-secret", clone_run, "secret").expect("an object");
    assert_ne!(copied_object, source_object, "the new run owns its object");
    assert_eq!(
        objects.values.lock().expect("value lock")[&copied_object],
        string_value("source secret")
    );
    let token_object = object_of("clone-secret", clone_run, "token").expect("an object");
    assert_eq!(
        objects.values.lock().expect("value lock")[&token_object],
        string_value("clone token")
    );
    assert_eq!(object_of("clone-secret", clone_run, "lease"), None);
    assert_eq!(
        objects
            .finished
            .lock()
            .expect("finish lock")
            .last()
            .map(|(_, outcome)| *outcome),
        Some(dexzd_engine::HardDeleteUploadOutcome::Committed)
    );

    let conflict = node
        .clone_fork(CloneForkFlowRequest {
            request_id: "request-secret-again".to_owned(),
            id_reuse_policy: IdReusePolicy::DisallowReuse as i32,
            ..request
        })
        .await
        .expect_err("the new Flow ID already has a run");
    assert_eq!(conflict.grpc_code(), grpc_code::ALREADY_EXISTS);
    assert_eq!(
        objects
            .finished
            .lock()
            .expect("finish lock")
            .last()
            .map(|(_, outcome)| *outcome),
        Some(dexzd_engine::HardDeleteUploadOutcome::NotCommitted),
        "the rejected clone fork's uploads are deleted"
    );
    node.shutdown().await;
}

/// The new run's synced Attributes are marked for its Attribute Stores, so the stores receive
/// rows of the new Flow.
#[tokio::test(start_paused = true)]
async fn synced_attributes_are_marked_for_the_new_flow() {
    let defaults = FlowCoordinationConfig::default();
    let mut validation = defaults.validation.clone();
    validation
        .attribute_store_names
        .insert("customer-store".to_owned());
    let (_cluster, node) = start_node(FlowCoordinationConfig {
        validation,
        ..defaults
    })
    .await;
    node.start_source(StartFlowRequest {
        flow_start_options: Some(FlowStartOptions {
            flow_config_override: Some(FlowConfig {
                attribute_store_names: Some(dexzd_protocol::dex::AttributeStoreNames {
                    names: vec!["customer-store".to_owned()],
                }),
                ..pull_config("source-queue")
            }),
            ..FlowStartOptions::default()
        }),
        ..source_start("start-source")
    })
    .await;
    node.registry
        .set_attributes(
            TenantId::DEFAULT,
            SetAttributesRequest {
                flow_id: SOURCE_FLOW.to_owned(),
                attributes: vec![AttributeWrite {
                    key: "customer".to_owned(),
                    value: Some(string_value("ada")),
                    sync_config: Some(dexzd_protocol::dex::AttributeSyncConfig { enabled: true }),
                    ..AttributeWrite::default()
                }],
                request_id: "set-synced".to_owned(),
                ..SetAttributesRequest::default()
            },
        )
        .await
        .expect("the synced Attribute is set");
    let response = node
        .clone_fork(clone_request("clone-synced", "request-synced"))
        .await
        .expect("the clone fork applies");
    let clone_run: RunId = response.run_id.parse().expect("the run ID parses");
    let prefix = run_table_prefix(
        node.context_of("clone-synced").flow_hash,
        clone_run,
        RunTable::AttributeSyncOutbox,
    );
    let (start, end) = prefix_range(&prefix);
    let entries: Vec<_> = node
        .view()
        .scan(ColumnFamily::Flow, &start, &end, usize::MAX)
        .expect("the outbox scans")
        .into_iter()
        .map(|(_, bytes)| {
            dexzd_protocol::dexzd::v1::AttributeSyncOutboxRecord::decode(bytes.as_slice())
                .expect("the entry decodes")
        })
        .collect();
    assert_eq!(entries.len(), 1, "{entries:?}");
    assert_eq!(entries[0].attribute_key, "customer");
    assert_eq!(entries[0].store_name, "customer-store");
    node.shutdown().await;
}

/// The source run's retention purges it after the clone fork. The new run keeps its copied
/// values, including an offloaded one and a hard-delete one, its history, and its beginning.
#[tokio::test(start_paused = true)]
async fn the_new_run_outlives_the_purge_of_its_source() {
    // Offloading helpers name the time travel tests' Flow.
    const PURGED_SOURCE: &str = "travel-flow";
    const CLONE: &str = "clone-of-purged";
    const RETENTION_MICROS: i64 = 60 * 1_000_000;
    let objects = std::sync::Arc::new(MemoryHardDeleteObjects::default());
    let cluster = TestCluster::new();
    let mut config = crate::time_travel::offloading_config();
    config.validation.has_blob_store = true;
    config.run_settings.retention_after_close_micros = RETENTION_MICROS;
    let node = TravelNode::start_with_hard_delete_ports(
        &cluster,
        LocalStoreConfig::InMemory,
        vec![segmented_opener()],
        config,
        dexzd_engine::HistoryReadsConfig::default(),
        crate::time_travel::HardDeletePorts {
            reader: objects.clone(),
            uploader: objects.clone(),
        },
    )
    .await;
    node.enable_history_offload().await;
    let source_run = node
        .start_flow_with(StartFlowRequest {
            flow_id: PURGED_SOURCE.to_owned(),
            flow_start_options: Some(FlowStartOptions {
                flow_config_override: Some(pull_config("source-queue")),
                attributes: vec![hard_delete_write("secret", "source secret")],
                ..FlowStartOptions::default()
            }),
            ..source_start("start-purged")
        })
        .await;
    node.set_flow_attribute(PURGED_SOURCE, "report", large_text('a', 400))
        .await;
    let point_event_id = node.record_of(PURGED_SOURCE, source_run).next_event_id - 1;
    let first_value_id = table_value_id(
        &node
            .attribute(source_run, "report")
            .expect("the report is set"),
    );
    node.set_flow_attribute(PURGED_SOURCE, "report", large_text('b', 400))
        .await;
    // The first report lives on only in an uploaded segment.
    node.offload_sealed_history(source_run).await;
    node.offload_superseded_value(source_run, first_value_id);
    let reads_before = node.history_object_reads();
    let response = node
        .clone_fork(CloneForkFlowRequest {
            source_flow_id: PURGED_SOURCE.to_owned(),
            point_type: FlowCloneForkPointType::EventId as i32,
            event_id: point_event_id,
            ..clone_request(CLONE, "request-purged")
        })
        .await
        .expect("the clone fork applies");
    assert!(
        node.history_object_reads() > reads_before,
        "the clone fork read the uploaded segments"
    );
    let clone_run: RunId = response.run_id.parse().expect("the run ID parses");

    // The source closes; its retention fires, its owner deletes its objects, then it purges.
    node.registry
        .stop_flow(
            TenantId::DEFAULT,
            StopFlowRequest {
                flow_id: PURGED_SOURCE.to_owned(),
                stop_type: StopType::Terminate as i32,
                ..StopFlowRequest::default()
            },
        )
        .await
        .expect("the source stops");
    // Retention postpones while a segment is SEALED.
    node.offload_sealed_history(source_run).await;
    cluster.clock.shift(RETENTION_MICROS + 1_000_000);
    node.commit_to(
        PURGED_SOURCE,
        Some(source_run),
        Command::TimersFired(TimersFiredCommand {
            timers: vec![TimerId {
                run_attempt: 0,
                kind: Some(timer_id::Kind::Retention(RetentionTimerId {})),
            }],
        }),
    )
    .await;
    assert!(
        node.record_of(PURGED_SOURCE, source_run).is_purging,
        "the retention requests the purge"
    );
    let source_object = node
        .hard_delete_object_of(PURGED_SOURCE, source_run, "secret")
        .expect("the source's secret is an object");
    objects
        .values
        .lock()
        .expect("value lock")
        .remove(&source_object);
    // Only the source uploaded history, so every history object is the source's.
    let history_objects = cluster
        .object_store
        .list("history/", None, 1000)
        .await
        .expect("the history objects list");
    assert!(
        !history_objects.objects.is_empty(),
        "the source's history was uploaded"
    );
    for object in history_objects.objects {
        cluster
            .object_store
            .delete(&object.key)
            .await
            .expect("the source's history object is deleted");
    }
    node.commit_to(
        PURGED_SOURCE,
        Some(source_run),
        Command::RunPurge(RunPurgeCommand {}),
    )
    .await;
    let source_record_key = run_singleton_key(
        node.context_of(PURGED_SOURCE).flow_hash,
        source_run,
        RunTable::RunRecord,
    );
    assert!(
        node.view()
            .get(ColumnFamily::Flow, &source_record_key)
            .expect("the run record reads")
            .is_none(),
        "the source run is purged"
    );

    let copied = node.attributes_of(CLONE, clone_run);
    assert_eq!(copied["report"], large_text('a', 400));
    let read_secret = || async {
        node.registry
            .get_attributes(
                TenantId::DEFAULT,
                GetAttributesRequest {
                    flow_id: CLONE.to_owned(),
                    keys: vec!["secret".to_owned()],
                    ..GetAttributesRequest::default()
                },
            )
            .await
            .expect("the new run's Attributes read")
            .attributes
    };
    let secret = read_secret().await;
    assert_eq!(secret.len(), 1, "{secret:?}");
    assert_eq!(secret[0].value, Some(string_value("source secret")));
    let events = node
        .registry
        .get_history_events(
            TenantId::DEFAULT,
            GetHistoryEventsRequest {
                flow_id: CLONE.to_owned(),
                ..GetHistoryEventsRequest::default()
            },
        )
        .await
        .expect("the new run's history reads")
        .events;
    let Some(flow_history_event::Payload::FlowStartedOrContinued(started)) =
        events.first().and_then(|event| event.payload.as_ref())
    else {
        panic!("the new run starts with its start event: {events:?}");
    };
    let source = started
        .clone_fork_source
        .as_ref()
        .expect("the start event names its source");
    assert_eq!(source.flow_id, PURGED_SOURCE);
    assert_eq!(source.run_id, source_run.to_string());

    node.set_flow_attribute(CLONE, "report", string_value("replaced"))
        .await;
    node.registry
        .reset_flow(
            TenantId::DEFAULT,
            ResetFlowRequest {
                flow_id: CLONE.to_owned(),
                reset_type: FlowResetType::Beginning as i32,
                skip_writes_reapply: true,
                ..ResetFlowRequest::default()
            },
        )
        .await
        .expect("the new run travels to its beginning without its source");
    assert_eq!(node.attributes_of(CLONE, clone_run), copied);
    assert_eq!(read_secret().await, secret);
    assert_eq!(
        node.record_of(CLONE, clone_run).status,
        FlowStatus::Running as i32
    );
    node.shutdown().await;
}
