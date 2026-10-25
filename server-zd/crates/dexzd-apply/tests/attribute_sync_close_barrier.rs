// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The Attribute Sync close barrier: natural closes wait for the outbox; stops and terminations do not.

use std::collections::BTreeMap;

use dexzd_apply::diagnostics::{current_semantic_point_hash, time_travel_target_hash};
use dexzd_apply::{
    ApplyContext, ApplyEffect, ApplyOutcome, FlowStateReader, StateReadError, StateRow, StateWrite,
    TERMINAL_CLEANUP_MESSAGE, WaitableChange, apply_flow_command,
};
use dexzd_keyspace::row_keys::{
    history_record_key, prefix_range, run_named_key, run_singleton_key,
};
use dexzd_keyspace::{
    ColumnFamily, IndexKind, PartitionHasher, RunId, RunTable, TenantId, flow_directory_key,
    run_table_prefix,
};
use dexzd_protocol::dex::{
    AttributeStoreNames, AttributeSyncConfig, AttributeWrite, ChannelMessage, CloseDecision,
    CloseDecisionType, DeleteChannelMessageRequest, FlowConfig, FlowErrorType, FlowResetType,
    FlowStartOptions, FlowStatus, FlowTimeoutPolicy, IdReusePolicy, InvokeExecuteMethodResponse,
    InvokeRpcRequest, InvokeWorkerRpcResponse, PublishToChannelRequest, ResetFlowRequest,
    RetryPolicy, SetAttributesRequest, SkipTimerRequest, StartFlowRequest, StepDecision,
    StepMovement, StepOptions, StopFlowRequest, StopType, Value, WorkerErrorResponse, WorkerTarget,
    flow_history_event, value,
};
use dexzd_protocol::dexzd::v1::{
    AttributeSyncAckCommand, AttributesSetCommand, ChannelMessageDeleteCommand,
    ChannelPublishCommand, CheckpointRecord, CommandReply, FlowCloseRecord, FlowCommand,
    FlowDirectoryRecord, FlowStartCommand, FlowStopCommand, FlowTimeoutTimerId, HistoryRecord,
    PendingCloseCheckCommand, PendingFlowClose, RetentionTimerId, RpcCommittedCommand, RunRecord,
    RunSettings, StepAttemptFailedCommand, StepAttemptStartedCommand, StepExecutionPhase,
    StepExecutionRecord, StepInvocationKey, StepMethod, StepMethodCompletedCommand,
    TimeTravelCommand, TimerId, TimerSkipCommand, TimersFiredCommand, command_reply,
    flow_command::Command, step_method_completed_command, timer_id,
};
use prost::Message;

const SECOND: i64 = 1_000_000;
const FLOW_ID: &str = "order-1";
const PRIMARY: &str = "primary";
const REPLICA: &str = "replica";

#[derive(Default, Clone)]
struct MemoryState {
    rows: BTreeMap<(ColumnFamily, Vec<u8>), Vec<u8>>,
}

impl FlowStateReader for MemoryState {
    fn get(
        &self,
        column_family: ColumnFamily,
        key: &[u8],
    ) -> Result<Option<Vec<u8>>, StateReadError> {
        Ok(self.rows.get(&(column_family, key.to_vec())).cloned())
    }

    fn scan(
        &self,
        column_family: ColumnFamily,
        start: &[u8],
        end: &[u8],
        limit: usize,
    ) -> Result<Vec<StateRow>, StateReadError> {
        Ok(self
            .rows
            .range((column_family, start.to_vec())..(column_family, end.to_vec()))
            .take(limit)
            .map(|((_, key), value)| (key.clone(), value.clone()))
            .collect())
    }
}

impl MemoryState {
    fn commit(&mut self, outcome: &ApplyOutcome) {
        for write in &outcome.writes {
            match write {
                StateWrite::Put {
                    column_family,
                    key,
                    value,
                } => {
                    self.rows
                        .insert((*column_family, key.clone()), value.clone());
                }
                StateWrite::Delete { column_family, key } => {
                    self.rows.remove(&(*column_family, key.clone()));
                }
                StateWrite::DeleteRange {
                    column_family,
                    start,
                    end,
                } => {
                    let doomed: Vec<_> = self
                        .rows
                        .range((*column_family, start.clone())..(*column_family, end.clone()))
                        .map(|(key, _)| key.clone())
                        .collect();
                    for key in doomed {
                        self.rows.remove(&key);
                    }
                }
            }
        }
    }
}

struct Harness {
    state: MemoryState,
    context: ApplyContext,
    /// Stamps the next commands as the owner does while a transactional RPC is in flight.
    is_transactional_rpc_in_flight: bool,
    now_micros: i64,
    run_id: Vec<u8>,
    settings: RunSettings,
}

impl Harness {
    fn new() -> Self {
        Self::with_settings(settings())
    }

    fn with_settings(settings: RunSettings) -> Self {
        let hasher = PartitionHasher::new(&[7; 16], 1024).expect("valid partition hasher");
        let flow_hash = hasher.flow_hash(TenantId::DEFAULT, FLOW_ID);
        Self {
            state: MemoryState::default(),
            context: ApplyContext {
                partition: hasher.partition_of(flow_hash),
                flow_hash,
            },
            is_transactional_rpc_in_flight: false,
            now_micros: 1_800_000_000 * SECOND,
            run_id: Vec::new(),
            settings,
        }
    }

    fn command(&mut self, body: Command) -> FlowCommand {
        self.now_micros += 1000;
        FlowCommand {
            flow_id: FLOW_ID.to_owned(),
            run_id: if matches!(body, Command::FlowStart(_)) {
                Vec::new()
            } else {
                self.run_id.clone()
            },
            expected_run_attempt: 0,
            epoch: 1,
            behavior_version: 1,
            proposed_at_micros: self.now_micros,
            command_id: (self.now_micros as u128).to_be_bytes().to_vec(),
            tenant_id: 0,
            is_transactional_rpc_in_flight: self.is_transactional_rpc_in_flight,
            command: Some(body),
        }
    }

    /// Applies `body` with the owner's stamp of an admitted transactional RPC still in flight.
    fn apply_while_rpc_in_flight(&mut self, body: Command) -> ApplyOutcome {
        self.is_transactional_rpc_in_flight = true;
        let outcome = self.apply(body);
        self.is_transactional_rpc_in_flight = false;
        outcome
    }

    fn check_pending_close(&mut self) -> ApplyOutcome {
        self.apply(Command::PendingCloseCheck(PendingCloseCheckCommand {}))
    }

    /// Commits an RPC result that writes `writes` and starts no Step.
    fn rpc_with_writes(
        &mut self,
        request_id: &str,
        is_transactional: bool,
        writes: Vec<AttributeWrite>,
    ) -> ApplyOutcome {
        self.apply(Command::RpcCommitted(RpcCommittedCommand {
            request: Some(InvokeRpcRequest {
                flow_id: FLOW_ID.to_owned(),
                rpc_name: "updateOrder".to_owned(),
                request_id: request_id.to_owned(),
                is_transactional,
                ..InvokeRpcRequest::default()
            }),
            response: Some(InvokeWorkerRpcResponse {
                upsert_attributes: writes,
                ..InvokeWorkerRpcResponse::default()
            }),
            app_worker_id: "worker-rpc".to_owned(),
            request_fingerprint: request_id.as_bytes().to_vec(),
            is_transactional,
            ..RpcCommittedCommand::default()
        }))
    }

    fn apply(&mut self, body: Command) -> ApplyOutcome {
        let command = self.command(body);
        let outcome =
            apply_flow_command(&self.context, &self.state, &command).expect("apply succeeds");
        let again =
            apply_flow_command(&self.context, &self.state, &command).expect("apply succeeds");
        assert_eq!(outcome.writes, again.writes, "apply must be deterministic");
        assert_eq!(
            outcome.effects, again.effects,
            "effects must be deterministic"
        );
        self.state.commit(&outcome);
        outcome
    }

    /// Starts a run of ChargeStep (Execute only) whose Flow config names `store_names`.
    fn start(
        &mut self,
        store_names: &[&str],
        attributes: Vec<AttributeWrite>,
        flow_timeout_seconds: i32,
    ) -> ApplyOutcome {
        self.start_run(
            vec![9; 16],
            store_names,
            attributes,
            flow_timeout_seconds,
            IdReusePolicy::Unspecified,
        )
    }

    fn start_run(
        &mut self,
        new_run_id: Vec<u8>,
        store_names: &[&str],
        attributes: Vec<AttributeWrite>,
        flow_timeout_seconds: i32,
        id_reuse_policy: IdReusePolicy,
    ) -> ApplyOutcome {
        let flow_config = FlowConfig {
            attribute_store_names: (!store_names.is_empty()).then(|| AttributeStoreNames {
                names: store_names.iter().map(|name| (*name).to_owned()).collect(),
            }),
            ..FlowConfig::default()
        };
        let outcome = self.apply(Command::FlowStart(FlowStartCommand {
            new_run_id,
            request: Some(StartFlowRequest {
                flow_id: FLOW_ID.to_owned(),
                flow_type: "OrderFlow".to_owned(),
                start_step_type: "ChargeStep".to_owned(),
                step_options: Some(StepOptions {
                    skip_wait_for: true,
                    execute_retry_policy: Some(RetryPolicy {
                        maximum_attempts: 1,
                        ..RetryPolicy::default()
                    }),
                    ..StepOptions::default()
                }),
                flow_timeout_seconds,
                request_id: format!("start-{}", self.now_micros),
                flow_start_options: Some(FlowStartOptions {
                    attributes,
                    id_reuse_policy: id_reuse_policy as i32,
                    ..FlowStartOptions::default()
                }),
                ..StartFlowRequest::default()
            }),
            settings: Some(self.settings),
            resolved_timeout_policy: FlowTimeoutPolicy::Fail as i32,
            worker_target: Some(WorkerTarget {
                is_pull_mode: true,
                ..WorkerTarget::default()
            }),
            effective_flow_config: Some(flow_config),
            ..FlowStartCommand::default()
        }));
        let Some(command_reply::Reply::StartFlow(reply)) = &outcome.reply.reply else {
            panic!("the Flow starts: {:?}", outcome.reply);
        };
        self.run_id = reply.run_id.clone();
        outcome
    }

    fn start_execute(&mut self, step_execution_id: &str) {
        let outcome = self.apply(Command::StepAttemptStarted(StepAttemptStartedCommand {
            invocation: Some(key(step_execution_id)),
            attempt: 1,
            task_id: vec![1; 16],
            pull_routing: None,
        }));
        assert!(!outcome.writes.is_empty(), "{step_execution_id} starts");
    }

    fn complete_execute(
        &mut self,
        step_execution_id: &str,
        decision: StepDecision,
        writes: Vec<AttributeWrite>,
    ) -> ApplyOutcome {
        let outcome = self.apply(Command::StepMethodCompleted(StepMethodCompletedCommand {
            invocation: Some(key(step_execution_id)),
            attempt: 1,
            response: Some(step_method_completed_command::Response::Execute(
                InvokeExecuteMethodResponse {
                    step_decision: Some(decision),
                    upsert_attributes: writes,
                    ..InvokeExecuteMethodResponse::default()
                },
            )),
            app_worker_id: "worker-a".to_owned(),
            ..StepMethodCompletedCommand::default()
        }));
        assert!(!outcome.writes.is_empty(), "{step_execution_id} completes");
        outcome
    }

    /// Runs ChargeStep to a ForceComplete decision.
    fn force_complete(&mut self, writes: Vec<AttributeWrite>) -> ApplyOutcome {
        self.start_execute("ChargeStep-1");
        self.complete_execute("ChargeStep-1", force_complete(), writes)
    }

    fn set(&mut self, writes: Vec<AttributeWrite>, request_id: &str) -> ApplyOutcome {
        self.apply(Command::AttributesSet(AttributesSetCommand {
            request: Some(SetAttributesRequest {
                attributes: writes,
                request_id: request_id.to_owned(),
                ..SetAttributesRequest::default()
            }),
            hard_delete_objects: Vec::new(),
            request_fingerprint: request_id.as_bytes().to_vec(),
        }))
    }

    fn publish(&mut self, message_id: &str) -> ApplyOutcome {
        self.apply(Command::ChannelPublish(ChannelPublishCommand {
            request: Some(PublishToChannelRequest {
                messages: vec![ChannelMessage {
                    channel_name: "orders".to_owned(),
                    value: Some(string_value(message_id)),
                    message_id: message_id.to_owned(),
                }],
                ..PublishToChannelRequest::default()
            }),
        }))
    }

    fn rpc(&mut self, request_id: &str, is_transactional: bool) -> ApplyOutcome {
        self.apply(Command::RpcCommitted(RpcCommittedCommand {
            request: Some(InvokeRpcRequest {
                flow_id: FLOW_ID.to_owned(),
                rpc_name: "updateOrder".to_owned(),
                request_id: request_id.to_owned(),
                is_transactional,
                ..InvokeRpcRequest::default()
            }),
            response: Some(InvokeWorkerRpcResponse {
                upsert_attributes: vec![synced("rpc", "written")],
                step_decision: Some(StepDecision {
                    next_steps: vec![StepMovement {
                        step_type: "ShipStep".to_owned(),
                        ..StepMovement::default()
                    }],
                    ..StepDecision::default()
                }),
                ..InvokeWorkerRpcResponse::default()
            }),
            app_worker_id: "worker-rpc".to_owned(),
            request_fingerprint: request_id.as_bytes().to_vec(),
            is_transactional,
            ..RpcCommittedCommand::default()
        }))
    }

    fn ack(&mut self, store_name: &str, acked_seq: u64) -> ApplyOutcome {
        self.apply(Command::AttributeSyncAck(AttributeSyncAckCommand {
            store_name: store_name.to_owned(),
            acked_seq,
            attribute_keys: Vec::new(),
            is_dropped: false,
        }))
    }

    fn stop(&mut self, stop_type: StopType) -> ApplyOutcome {
        self.apply(Command::FlowStop(FlowStopCommand {
            request: Some(StopFlowRequest {
                stop_type: stop_type as i32,
                ..StopFlowRequest::default()
            }),
        }))
    }

    fn travel_to(&mut self, request: ResetFlowRequest) -> ApplyOutcome {
        let outcome = self.apply(Command::TimeTravel(TimeTravelCommand {
            request: Some(request),
        }));
        assert!(
            matches!(
                outcome.reply.reply,
                Some(command_reply::Reply::TimeTravel(_))
            ),
            "time travel applies: {:?}",
            outcome.reply
        );
        outcome
    }

    fn run(&self) -> RunId {
        RunId::from_slice(&self.run_id).expect("the harness has a run")
    }

    fn get<RowMessage: Message + Default>(
        &self,
        column_family: ColumnFamily,
        key: &[u8],
    ) -> Option<RowMessage> {
        self.state
            .rows
            .get(&(column_family, key.to_vec()))
            .map(|bytes| RowMessage::decode(bytes.as_slice()).expect("row decodes"))
    }

    fn run_record(&self) -> RunRecord {
        let key = run_singleton_key(self.context.flow_hash, self.run(), RunTable::RunRecord);
        self.get(ColumnFamily::Flow, &key)
            .expect("the run record exists")
    }

    fn last_seq(&self) -> u64 {
        self.run_record().next_run_seq - 1
    }

    fn history_record(&self, seq: u64) -> HistoryRecord {
        let key = history_record_key(self.context.flow_hash, self.run(), seq);
        self.get(ColumnFamily::History, &key)
            .expect("the history record exists")
    }

    fn directory_status(&self) -> i32 {
        let key = flow_directory_key(self.context.flow_hash, TenantId::DEFAULT, FLOW_ID);
        let directory: FlowDirectoryRecord = self
            .get(ColumnFamily::Flow, &key)
            .expect("the directory exists");
        directory
            .runs
            .iter()
            .find(|run| run.run_id == self.run_id)
            .expect("the directory lists the run")
            .status
    }

    fn step(&self, step_execution_id: &str) -> StepExecutionRecord {
        let key = run_named_key(
            self.context.flow_hash,
            self.run(),
            RunTable::StepExecution,
            step_execution_id,
        );
        self.get(ColumnFamily::Flow, &key)
            .expect("the Step execution exists")
    }

    fn has_step(&self, step_execution_id: &str) -> bool {
        let key = run_named_key(
            self.context.flow_hash,
            self.run(),
            RunTable::StepExecution,
            step_execution_id,
        );
        self.state.rows.contains_key(&(ColumnFamily::Flow, key))
    }

    fn has_timer(&self, timer: &TimerId) -> bool {
        let key = dexzd_keyspace::row_keys::timer_key(
            self.context.flow_hash,
            self.run(),
            &timer.encode_to_vec(),
        );
        self.state.rows.contains_key(&(ColumnFamily::Flow, key))
    }

    fn outbox_entry_count(&self) -> usize {
        let prefix = run_table_prefix(
            self.context.flow_hash,
            self.run(),
            RunTable::AttributeSyncOutbox,
        );
        let (start, end) = prefix_range(&prefix);
        self.state
            .rows
            .range((ColumnFamily::Flow, start)..(ColumnFamily::Flow, end))
            .count()
    }

    fn has_active_work_index(&self) -> bool {
        self.state.rows.keys().any(|(column_family, key)| {
            *column_family == ColumnFamily::PartitionIndex
                && key.get(2) == Some(&(IndexKind::ActiveWork as u8))
        })
    }

    fn semantic_point_hash(&self) -> [u8; 16] {
        let history = self.history_record(self.last_seq());
        assert!(history.is_semantic, "the last command is a semantic point");
        current_semantic_point_hash(
            &self.context,
            &self.state,
            self.run(),
            &history.command.expect("history command").command_id,
        )
        .expect("hash the current point")
        .expect("semantic point")
        .state_hash
    }
}

fn settings() -> RunSettings {
    RunSettings {
        history_window_semantic_events: 10_000,
        history_prune_chunk_semantic_events: 1000,
        max_retained_superseded_bytes: 256 << 20,
        retention_after_close_micros: 60 * SECOND,
        wire_inline_threshold_bytes: 100,
        max_value_bytes: 16 << 20,
        checkpoint_min_written_bytes: 1 << 20,
        checkpoint_max_commands: 10_000,
        request_dedup_capacity: 1000,
        max_run_state_bytes: 1 << 30,
        max_active_step_executions: 10_000,
    }
}

fn key(step_execution_id: &str) -> StepInvocationKey {
    StepInvocationKey {
        step_execution_id: step_execution_id.to_owned(),
        method: StepMethod::Execute as i32,
    }
}

fn string_value(text: &str) -> Value {
    Value {
        kind: Some(value::Kind::StringValue(text.to_owned())),
    }
}

fn synced(attribute_key: &str, text: &str) -> AttributeWrite {
    AttributeWrite {
        key: attribute_key.to_owned(),
        value: Some(string_value(text)),
        sync_config: Some(AttributeSyncConfig { enabled: true }),
        ..AttributeWrite::default()
    }
}

fn force_complete() -> StepDecision {
    StepDecision {
        close_decision: Some(CloseDecision {
            close_decision_type: CloseDecisionType::ForceComplete as i32,
            close_input: Some(string_value("done")),
            ..CloseDecision::default()
        }),
        ..StepDecision::default()
    }
}

fn next_steps(step_types: &[&str]) -> StepDecision {
    StepDecision {
        next_steps: step_types
            .iter()
            .map(|step_type| StepMovement {
                step_type: (*step_type).to_owned(),
                step_options: Some(StepOptions {
                    skip_wait_for: true,
                    ..StepOptions::default()
                }),
                ..StepMovement::default()
            })
            .collect(),
        ..StepDecision::default()
    }
}

fn flow_timeout_timer(run_attempt: u32) -> TimerId {
    TimerId {
        run_attempt,
        kind: Some(timer_id::Kind::FlowTimeout(FlowTimeoutTimerId {})),
    }
}

fn retention_timer() -> TimerId {
    TimerId {
        run_attempt: 0,
        kind: Some(timer_id::Kind::Retention(RetentionTimerId {})),
    }
}

fn is_held_by_rpcs(outcome: &ApplyOutcome) -> bool {
    outcome
        .effects
        .iter()
        .any(|effect| matches!(effect, ApplyEffect::PendingCloseHeldByRpcs { .. }))
}

fn wakes(outcome: &ApplyOutcome) -> Vec<WaitableChange> {
    outcome
        .effects
        .iter()
        .filter_map(|effect| match effect {
            ApplyEffect::WakeWaiters { change, .. } => Some(change.clone()),
            _ => None,
        })
        .collect()
}

fn has_flow_closed_event(history: &HistoryRecord) -> bool {
    history.events.iter().any(|event| {
        matches!(
            event.payload,
            Some(flow_history_event::Payload::FlowClosed(_))
        )
    })
}

fn is_ok(reply: &CommandReply) -> bool {
    matches!(reply.reply, Some(command_reply::Reply::Ok(_)))
}

fn assert_error(reply: &CommandReply, grpc_code: i32, message: &str) {
    match &reply.reply {
        Some(command_reply::Reply::Error(error)) => {
            assert_eq!(error.grpc_code, grpc_code, "{error:?}");
            assert_eq!(error.message, message);
        }
        other => panic!("expected an error reply, got {other:?}"),
    }
}

fn reset(reset_type: FlowResetType) -> ResetFlowRequest {
    ResetFlowRequest {
        flow_id: FLOW_ID.to_owned(),
        reset_type: reset_type as i32,
        reason: "test".to_owned(),
        skip_writes_reapply: true,
        ..ResetFlowRequest::default()
    }
}

fn reset_to_time(timestamp_micros: i64) -> ResetFlowRequest {
    ResetFlowRequest {
        run_attempt: 1,
        history_event_time: (i128::from(timestamp_micros) * 1000).to_string(),
        ..reset(FlowResetType::HistoryEventTime)
    }
}

#[test]
fn natural_close_waits_for_outbox_entries_and_closes_at_once_without_them() {
    let mut without_stores = Harness::new();
    without_stores.start(&[], vec![synced("status", "opened")], 60);
    let closed = without_stores.force_complete(Vec::new());
    assert_eq!(
        without_stores.run_record().status,
        FlowStatus::Completed as i32
    );
    assert!(wakes(&closed).contains(&WaitableChange::FlowClosed));
    assert!(has_flow_closed_event(
        &without_stores.history_record(without_stores.last_seq())
    ));

    let mut harness = Harness::new();
    harness.start(&[PRIMARY], Vec::new(), 60);
    harness.start_execute("ChargeStep-1");
    harness.complete_execute(
        "ChargeStep-1",
        next_steps(&["ShipStep", "PackStep"]),
        Vec::new(),
    );
    harness.start_execute("ShipStep-1");
    // The deciding response's own synced write must hold the close.
    let deferred = harness.complete_execute(
        "ShipStep-1",
        force_complete(),
        vec![synced("status", "shipped")],
    );
    let record = harness.run_record();
    assert_eq!(record.status, FlowStatus::Running as i32);
    assert_eq!(record.closed_at_micros, 0);
    assert_eq!(
        record.pending_close,
        Some(PendingFlowClose {
            status: FlowStatus::Completed as i32,
            ..PendingFlowClose::default()
        })
    );
    assert!(!record.is_graceful_completion_requested);
    assert_eq!(
        harness.step("PackStep-1").phase,
        StepExecutionPhase::Canceled as i32,
        "the first half cancels active Steps"
    );
    assert!(!harness.has_timer(&flow_timeout_timer(1)));
    assert!(!harness.has_timer(&retention_timer()));
    assert!(harness.has_active_work_index());
    assert_eq!(harness.directory_status(), FlowStatus::Running as i32);
    assert_eq!(harness.outbox_entry_count(), 1);
    let deferred_wakes = wakes(&deferred);
    assert!(deferred_wakes.contains(&WaitableChange::FlowCleanupStarted));
    assert!(!deferred_wakes.contains(&WaitableChange::FlowClosed));
    assert!(
        !deferred
            .effects
            .iter()
            .any(|effect| matches!(effect, ApplyEffect::TimerArmed { .. })),
        "retention is not armed yet"
    );
    assert!(!has_flow_closed_event(
        &harness.history_record(harness.last_seq())
    ));
}

#[test]
fn the_last_ack_across_two_stores_finalizes_the_close_and_records_flow_closed() {
    let mut harness = Harness::new();
    harness.start(&[PRIMARY, REPLICA], vec![synced("status", "opened")], 60);
    harness.force_complete(Vec::new());
    let deferred_seq = harness.last_seq();
    assert!(!has_flow_closed_event(
        &harness.history_record(deferred_seq)
    ));

    let first = harness.ack(PRIMARY, deferred_seq);
    assert!(is_ok(&first.reply));
    assert_eq!(harness.run_record().status, FlowStatus::Running as i32);
    let first_history = harness.history_record(harness.last_seq());
    assert!(!first_history.is_semantic);
    assert!(!has_flow_closed_event(&first_history));
    assert!(!wakes(&first).contains(&WaitableChange::FlowClosed));

    let last = harness.ack(REPLICA, deferred_seq);
    let record = harness.run_record();
    assert_eq!(record.status, FlowStatus::Completed as i32);
    assert_eq!(record.closed_at_micros, harness.now_micros);
    assert_eq!(record.pending_close, None);
    assert_eq!(
        record.close.expect("close details").results.len(),
        1,
        "the close keeps the deciding Step's output"
    );
    assert_eq!(harness.directory_status(), FlowStatus::Completed as i32);
    assert!(harness.has_timer(&retention_timer()));
    assert!(!harness.has_active_work_index());
    assert!(wakes(&last).contains(&WaitableChange::FlowClosed));
    let last_history = harness.history_record(harness.last_seq());
    assert!(last_history.is_semantic);
    assert!(has_flow_closed_event(&last_history));
}

#[test]
fn writes_during_cleanup_are_accepted_and_extend_the_wait() {
    let mut harness = Harness::new();
    harness.start(&[PRIMARY], vec![synced("status", "opened")], 60);
    harness.force_complete(Vec::new());
    let deferred_seq = harness.last_seq();

    let set = harness.set(vec![synced("status", "verified")], "late-set");
    assert!(is_ok(&set.reply), "SetAttributes is a signal in Go");
    let set_seq = harness.last_seq();
    assert!(is_ok(&harness.publish("m1").reply));
    for (request_id, is_transactional) in [("late-rpc", false), ("admitted-locked-rpc", true)] {
        let rpc = harness.rpc(request_id, is_transactional);
        assert!(
            matches!(rpc.reply.reply, Some(command_reply::Reply::Rpc(_))),
            "an RPC result is accepted during cleanup: {:?}",
            rpc.reply
        );
    }
    assert!(!harness.has_step("ShipStep-1"), "no Step starts in cleanup");
    let rpc_seq = harness.last_seq();

    harness.ack(PRIMARY, deferred_seq);
    assert_eq!(
        harness.run_record().status,
        FlowStatus::Running as i32,
        "the later write is still pending"
    );
    harness.ack(PRIMARY, set_seq);
    assert_eq!(harness.run_record().status, FlowStatus::Running as i32);
    harness.ack(PRIMARY, rpc_seq);
    assert_eq!(harness.run_record().status, FlowStatus::Completed as i32);
}

#[test]
fn update_class_requests_fail_during_cleanup() {
    let mut harness = Harness::new();
    harness.start(&[PRIMARY], vec![synced("status", "opened")], 60);
    assert!(is_ok(&harness.publish("m1").reply));
    harness.force_complete(Vec::new());

    let deleted = harness.apply(Command::ChannelMessageDelete(ChannelMessageDeleteCommand {
        request: Some(DeleteChannelMessageRequest {
            flow_id: FLOW_ID.to_owned(),
            channel_name: "orders".to_owned(),
            message_id: "m1".to_owned(),
            ..DeleteChannelMessageRequest::default()
        }),
        request_fingerprint: Vec::new(),
    }));
    assert_error(&deleted.reply, 9, TERMINAL_CLEANUP_MESSAGE);
    assert!(deleted.writes.is_empty());
    assert_eq!(harness.run_record().status, FlowStatus::Running as i32);
}

#[test]
fn stop_flow_during_cleanup_closes_at_once_with_the_stop_result() {
    for (stop_type, status) in [
        (StopType::Cancel, FlowStatus::Canceled),
        (StopType::Fail, FlowStatus::Failed),
    ] {
        let mut harness = Harness::new();
        harness.start(&[PRIMARY], vec![synced("status", "opened")], 60);
        harness.force_complete(Vec::new());
        let deferred_seq = harness.last_seq();
        let stopped = harness.stop(stop_type);
        assert!(is_ok(&stopped.reply));
        let record = harness.run_record();
        assert_eq!(record.status, status as i32, "{stop_type:?}");
        assert_eq!(record.pending_close, None);
        if stop_type == StopType::Fail {
            let close = record.close.expect("close details");
            assert_eq!(close.error_type, FlowErrorType::ClientApiFailingFlow as i32);
            assert_eq!(close.error_message, "fail by client");
        }
        assert!(wakes(&stopped).contains(&WaitableChange::FlowClosed));
        assert!(has_flow_closed_event(
            &harness.history_record(harness.last_seq())
        ));
        assert_eq!(harness.outbox_entry_count(), 1, "entries keep syncing");
        let late_set = harness.set(vec![synced("status", "late")], "late-set");
        assert!(
            matches!(&late_set.reply.reply, Some(command_reply::Reply::Error(error)) if error.grpc_code == 5),
            "a closed run rejects SetAttributes with NOT_FOUND"
        );
        harness.ack(PRIMARY, deferred_seq);
        assert_eq!(harness.outbox_entry_count(), 0);
        assert_eq!(harness.run_record().status, status as i32);
    }
}

#[test]
fn terminations_close_at_once_despite_outbox_entries() {
    let mut harness = Harness::new();
    harness.start(&[PRIMARY], vec![synced("status", "opened")], 60);
    harness.stop(StopType::Terminate);
    assert_eq!(harness.run_record().status, FlowStatus::Terminated as i32);
    assert_eq!(harness.outbox_entry_count(), 1);

    let mut reused = Harness::new();
    reused.start(&[PRIMARY], vec![synced("status", "opened")], 60);
    reused.force_complete(Vec::new());
    let first_run_id = reused.run_id.clone();
    reused.start_run(
        vec![8; 16],
        &[PRIMARY],
        Vec::new(),
        60,
        IdReusePolicy::AllowTerminateIfRunning,
    );
    assert_ne!(reused.run_id, first_run_id);
    reused.run_id = first_run_id;
    let record = reused.run_record();
    assert_eq!(record.status, FlowStatus::Terminated as i32);
    assert_eq!(record.pending_close, None);
}

#[test]
fn flow_timeouts_and_exhausted_retries_wait_for_the_outbox() {
    let mut timed_out = Harness::new();
    timed_out.start(&[PRIMARY], vec![synced("status", "opened")], 30);
    timed_out.apply(Command::TimersFired(TimersFiredCommand {
        timers: vec![flow_timeout_timer(1)],
    }));
    let record = timed_out.run_record();
    assert_eq!(record.status, FlowStatus::Running as i32);
    assert_eq!(
        record.pending_close.expect("pending close").status,
        FlowStatus::Failed as i32
    );
    timed_out.ack(PRIMARY, timed_out.last_seq());
    let close = timed_out.run_record().close.expect("close details");
    assert_eq!(close.error_type, FlowErrorType::FlowTimeout as i32);
    assert_eq!(close.error_message, "Flow timed out after 30 seconds");

    let mut failed = Harness::new();
    failed.start(&[PRIMARY], vec![synced("status", "opened")], 60);
    failed.start_execute("ChargeStep-1");
    failed.apply(Command::StepAttemptFailed(StepAttemptFailedCommand {
        invocation: Some(key("ChargeStep-1")),
        attempt: 1,
        kind: 1,
        worker_error: Some(WorkerErrorResponse {
            detail: "boom".to_owned(),
            ..WorkerErrorResponse::default()
        }),
        ..StepAttemptFailedCommand::default()
    }));
    let pending = failed.run_record().pending_close.expect("pending close");
    assert_eq!(pending.status, FlowStatus::Failed as i32);
    assert_eq!(pending.error_type, FlowErrorType::WorkerApiFail as i32);
    failed.ack(PRIMARY, failed.last_seq());
    assert_eq!(failed.run_record().status, FlowStatus::Failed as i32);
}

#[test]
fn time_travel_clears_the_pending_close_and_the_run_continues() {
    let mut harness = Harness::new();
    harness.start(&[PRIMARY], vec![synced("status", "opened")], 60);
    harness.force_complete(Vec::new());
    assert!(harness.run_record().pending_close.is_some());

    harness.travel_to(reset(FlowResetType::Beginning));
    let record = harness.run_record();
    assert_eq!(record.status, FlowStatus::Running as i32);
    assert_eq!(record.pending_close, None);
    assert_eq!(
        harness.step("ChargeStep-1").phase,
        StepExecutionPhase::Execute as i32
    );
    assert!(harness.has_timer(&flow_timeout_timer(2)));

    harness.start_execute("ChargeStep-1");
    harness.complete_execute("ChargeStep-1", force_complete(), Vec::new());
    assert!(
        harness.run_record().pending_close.is_some(),
        "re-marked entries hold the new close"
    );
    harness.ack(PRIMARY, harness.last_seq());
    assert_eq!(harness.run_record().status, FlowStatus::Completed as i32);
}

#[test]
fn restored_natural_close_waits_for_remarks_then_records_flow_closed() {
    let mut harness = Harness::new();
    harness.start(&[PRIMARY], vec![synced("status", "opened")], 60);
    harness.ack(PRIMARY, harness.last_seq());
    harness.force_complete(Vec::new());
    let close_time = harness.history_record(harness.last_seq()).timestamp_micros;
    assert_eq!(harness.run_record().status, FlowStatus::Completed as i32);

    harness.travel_to(reset(FlowResetType::Beginning));
    let travel = harness.travel_to(reset_to_time(close_time));
    let pending = harness.run_record();
    assert_eq!(pending.status, FlowStatus::Running as i32);
    assert_eq!(
        pending.pending_close.as_ref().map(|close| close.status),
        Some(FlowStatus::Completed as i32)
    );
    assert_eq!(harness.directory_status(), FlowStatus::Running as i32);
    assert!(harness.has_active_work_index());
    assert!(harness.outbox_entry_count() > 0);
    assert!(!has_flow_closed_event(
        &harness.history_record(harness.last_seq())
    ));
    assert!(!wakes(&travel).contains(&WaitableChange::FlowClosed));

    let rejected = harness.apply(Command::ChannelMessageDelete(ChannelMessageDeleteCommand {
        request: Some(DeleteChannelMessageRequest {
            flow_id: FLOW_ID.to_owned(),
            channel_name: "orders".to_owned(),
            message_id: "new-write".to_owned(),
            ..DeleteChannelMessageRequest::default()
        }),
        request_fingerprint: Vec::new(),
    }));
    assert_error(&rejected.reply, 9, TERMINAL_CLEANUP_MESSAGE);

    let ack = harness.ack(PRIMARY, harness.last_seq());
    assert!(wakes(&ack).contains(&WaitableChange::FlowClosed));
    let final_record = harness.run_record();
    assert_eq!(final_record.status, FlowStatus::Completed as i32);
    assert!(final_record.pending_close.is_none());
    assert_eq!(harness.directory_status(), FlowStatus::Completed as i32);
    assert!(!harness.has_active_work_index());
    assert!(has_flow_closed_event(
        &harness.history_record(harness.last_seq())
    ));
}

#[test]
fn restored_stop_ignores_remarked_attribute_sync_entries() {
    for (stop_type, expected_status) in [
        (StopType::Fail, FlowStatus::Failed),
        (StopType::Cancel, FlowStatus::Canceled),
        (StopType::Terminate, FlowStatus::Terminated),
    ] {
        let mut harness = Harness::new();
        harness.start(&[PRIMARY], vec![synced("status", "opened")], 60);
        let stopped = harness.stop(stop_type);
        let stop_time = harness.history_record(harness.last_seq()).timestamp_micros;
        assert!(wakes(&stopped).contains(&WaitableChange::FlowClosed));
        harness.travel_to(reset(FlowResetType::Beginning));
        let travel = harness.travel_to(reset_to_time(stop_time));
        assert_eq!(harness.run_record().status, expected_status as i32);
        assert!(harness.run_record().pending_close.is_none());
        assert_eq!(
            harness.run_record().close.expect("close").is_closed_at_once,
            Some(true)
        );
        assert!(harness.outbox_entry_count() > 0);
        assert!(wakes(&travel).contains(&WaitableChange::FlowClosed));
        assert!(has_flow_closed_event(
            &harness.history_record(harness.last_seq())
        ));
    }
}

#[test]
fn rebuilt_cleanup_points_equal_their_recorded_states() {
    let mut harness = Harness::with_settings(RunSettings {
        checkpoint_max_commands: 2,
        ..settings()
    });
    let mut points = Vec::new();
    harness.start(&[PRIMARY], vec![synced("status", "opened")], 60);
    points.push((harness.now_micros, harness.semantic_point_hash()));
    harness.force_complete(Vec::new());
    points.push((harness.now_micros, harness.semantic_point_hash()));
    let set = harness.set(vec![synced("status", "late")], "late-set");
    assert!(is_ok(&set.reply));
    points.push((harness.now_micros, harness.semantic_point_hash()));
    assert!(is_ok(&harness.publish("m1").reply));
    points.push((harness.now_micros, harness.semantic_point_hash()));
    harness.ack(PRIMARY, harness.last_seq());
    assert_eq!(harness.run_record().status, FlowStatus::Completed as i32);
    points.push((harness.now_micros, harness.semantic_point_hash()));

    for (timestamp, expected) in &points {
        let command = harness.command(Command::TimeTravel(TimeTravelCommand {
            request: Some(reset_to_time(*timestamp)),
        }));
        let rebuilt = time_travel_target_hash(&harness.context, &harness.state, &command)
            .expect("rebuild the point")
            .expect("the point exists");
        assert_eq!(rebuilt.state_hash, *expected, "point at {timestamp}");
    }

    // Traveling into cleanup restores the pending close, and re-marked entries hold it.
    let (cleanup_time, _) = points[2];
    harness.travel_to(reset_to_time(cleanup_time));
    let record = harness.run_record();
    assert_eq!(record.status, FlowStatus::Running as i32);
    assert!(record.pending_close.is_some());
    assert_eq!(harness.directory_status(), FlowStatus::Running as i32);
    harness.ack(PRIMARY, harness.last_seq());
    assert_eq!(harness.run_record().status, FlowStatus::Completed as i32);
}

#[test]
fn an_admitted_transactional_rpc_holds_a_close_without_outbox_entries() {
    let mut harness = Harness::new();
    harness.start(&[], Vec::new(), 60);
    harness.start_execute("ChargeStep-1");
    let held = harness.apply_while_rpc_in_flight(Command::StepMethodCompleted(
        StepMethodCompletedCommand {
            invocation: Some(key("ChargeStep-1")),
            attempt: 1,
            response: Some(step_method_completed_command::Response::Execute(
                InvokeExecuteMethodResponse {
                    step_decision: Some(force_complete()),
                    ..InvokeExecuteMethodResponse::default()
                },
            )),
            app_worker_id: "worker-a".to_owned(),
            ..StepMethodCompletedCommand::default()
        },
    ));
    let record = harness.run_record();
    assert_eq!(record.status, FlowStatus::Running as i32);
    assert!(record.pending_close.is_some());
    assert_eq!(harness.outbox_entry_count(), 0);
    assert!(is_held_by_rpcs(&held));
    assert!(!wakes(&held).contains(&WaitableChange::FlowClosed));

    harness.is_transactional_rpc_in_flight = true;
    let early_check = harness.check_pending_close();
    harness.is_transactional_rpc_in_flight = false;
    assert!(
        early_check.writes.is_empty(),
        "a check during the RPC changes nothing"
    );

    let checked = harness.check_pending_close();
    assert!(is_ok(&checked.reply));
    assert!(wakes(&checked).contains(&WaitableChange::FlowClosed));
    assert_eq!(harness.run_record().status, FlowStatus::Completed as i32);
    let history = harness.history_record(harness.last_seq());
    assert!(history.is_semantic);
    assert!(has_flow_closed_event(&history));
    let late_check = harness.check_pending_close();
    assert!(
        late_check.writes.is_empty(),
        "a check after the close changes nothing"
    );
}

#[test]
fn the_last_ack_waits_for_an_admitted_transactional_rpc_whose_writes_join_the_outbox() {
    let mut harness = Harness::new();
    harness.start(&[PRIMARY], vec![synced("status", "opened")], 60);
    harness.force_complete(Vec::new());
    let deferred_seq = harness.last_seq();
    let held_ack =
        harness.apply_while_rpc_in_flight(Command::AttributeSyncAck(AttributeSyncAckCommand {
            store_name: PRIMARY.to_owned(),
            acked_seq: deferred_seq,
            attribute_keys: Vec::new(),
            is_dropped: false,
        }));
    assert!(is_held_by_rpcs(&held_ack));
    assert_eq!(harness.outbox_entry_count(), 0);
    assert_eq!(harness.run_record().status, FlowStatus::Running as i32);

    let rpc = harness.rpc_with_writes("admitted-rpc", true, vec![synced("rpc", "written")]);
    assert!(
        matches!(rpc.reply.reply, Some(command_reply::Reply::Rpc(_))),
        "the admitted RPC commits: {:?}",
        rpc.reply
    );
    assert_eq!(
        harness.outbox_entry_count(),
        1,
        "its synced write joins the outbox"
    );
    assert_eq!(harness.run_record().status, FlowStatus::Running as i32);
    harness.ack(PRIMARY, harness.last_seq());
    assert_eq!(harness.run_record().status, FlowStatus::Completed as i32);
}

#[test]
fn the_commit_of_the_last_admitted_rpc_records_a_held_close() {
    let mut harness = Harness::new();
    harness.start(&[], Vec::new(), 60);
    harness.start_execute("ChargeStep-1");
    harness.is_transactional_rpc_in_flight = true;
    harness.complete_execute("ChargeStep-1", force_complete(), Vec::new());
    harness.is_transactional_rpc_in_flight = false;
    assert!(harness.run_record().pending_close.is_some());
    let rpc = harness.rpc_with_writes("admitted-rpc", true, Vec::new());
    assert!(wakes(&rpc).contains(&WaitableChange::FlowClosed));
    let record = harness.run_record();
    assert_eq!(record.status, FlowStatus::Completed as i32);
    assert_eq!(record.pending_close, None);
    assert!(has_flow_closed_event(
        &harness.history_record(harness.last_seq())
    ));
}

#[test]
fn rebuilt_rpc_held_cleanup_points_equal_their_recorded_states() {
    for is_finalized_by_check in [true, false] {
        let mut harness = Harness::with_settings(RunSettings {
            checkpoint_max_commands: 2,
            ..settings()
        });
        let mut points = Vec::new();
        harness.start(&[], vec![synced("status", "opened")], 60);
        points.push((harness.now_micros, harness.semantic_point_hash()));
        harness.start_execute("ChargeStep-1");
        harness.is_transactional_rpc_in_flight = true;
        harness.complete_execute("ChargeStep-1", force_complete(), Vec::new());
        harness.is_transactional_rpc_in_flight = false;
        points.push((harness.now_micros, harness.semantic_point_hash()));
        assert!(is_ok(&harness.publish("m1").reply));
        points.push((harness.now_micros, harness.semantic_point_hash()));
        if is_finalized_by_check {
            harness.check_pending_close();
        } else {
            harness.rpc_with_writes("admitted-rpc", true, vec![synced("rpc", "written")]);
        }
        assert_eq!(harness.run_record().status, FlowStatus::Completed as i32);
        points.push((harness.now_micros, harness.semantic_point_hash()));

        for (timestamp, expected) in &points {
            let command = harness.command(Command::TimeTravel(TimeTravelCommand {
                request: Some(reset_to_time(*timestamp)),
            }));
            let rebuilt = time_travel_target_hash(&harness.context, &harness.state, &command)
                .expect("rebuild the point")
                .expect("the point exists");
            assert_eq!(
                rebuilt.state_hash, *expected,
                "point at {timestamp}, finalized by check: {is_finalized_by_check}"
            );
        }
    }
}

fn start_with_cancel_timeout(harness: &mut Harness) {
    let outcome = harness.apply(Command::FlowStart(FlowStartCommand {
        new_run_id: vec![9; 16],
        request: Some(StartFlowRequest {
            flow_id: FLOW_ID.to_owned(),
            flow_type: "OrderFlow".to_owned(),
            start_step_type: "ChargeStep".to_owned(),
            step_options: Some(StepOptions {
                skip_wait_for: true,
                ..StepOptions::default()
            }),
            flow_timeout_seconds: 30,
            request_id: "start-cancel-timeout".to_owned(),
            flow_start_options: Some(FlowStartOptions {
                attributes: vec![synced("status", "opened")],
                ..FlowStartOptions::default()
            }),
            ..StartFlowRequest::default()
        }),
        settings: Some(harness.settings),
        resolved_timeout_policy: FlowTimeoutPolicy::Cancel as i32,
        worker_target: Some(WorkerTarget {
            is_pull_mode: true,
            ..WorkerTarget::default()
        }),
        effective_flow_config: Some(FlowConfig {
            attribute_store_names: Some(AttributeStoreNames {
                names: vec![PRIMARY.to_owned()],
            }),
            ..FlowConfig::default()
        }),
        ..FlowStartCommand::default()
    }));
    let Some(command_reply::Reply::StartFlow(reply)) = &outcome.reply.reply else {
        panic!("the Flow starts: {:?}", outcome.reply);
    };
    harness.run_id = reply.run_id.clone();
}

#[test]
fn live_flow_timeout_cancel_waits_for_attribute_sync() {
    let mut harness = Harness::new();
    start_with_cancel_timeout(&mut harness);
    harness.apply(Command::TimersFired(TimersFiredCommand {
        timers: vec![flow_timeout_timer(1)],
    }));
    let record = harness.run_record();
    assert_eq!(record.status, FlowStatus::Running as i32);
    assert_eq!(
        record.pending_close.expect("C31 waits").status,
        FlowStatus::Canceled as i32
    );
}

#[test]
fn restored_flow_timeout_cancel_waits_for_remarked_attributes() {
    let mut harness = Harness::new();
    start_with_cancel_timeout(&mut harness);
    harness.ack(PRIMARY, harness.last_seq());
    harness.apply(Command::TimersFired(TimersFiredCommand {
        timers: vec![flow_timeout_timer(1)],
    }));
    assert_eq!(harness.run_record().status, FlowStatus::Canceled as i32);
    let close_time = harness.history_record(harness.last_seq()).timestamp_micros;
    harness.travel_to(reset(FlowResetType::Beginning));
    harness.travel_to(reset_to_time(close_time));
    assert!(harness.outbox_entry_count() > 0, "re-marked entries exist");
    let record = harness.run_record();
    assert_eq!(
        record.status,
        FlowStatus::Running as i32,
        "a restored Flow timeout CANCEL must wait for Attribute Sync"
    );
    assert_eq!(
        record.pending_close.expect("C31 waits").status,
        FlowStatus::Canceled as i32
    );
}

fn skip_flow_timeout() -> Command {
    Command::TimerSkip(TimerSkipCommand {
        request: Some(SkipTimerRequest {
            flow_id: FLOW_ID.to_owned(),
            step_execution_id: "sys:timeout_handler-1".to_owned(),
            timer_condition_index: Some(0),
            ..SkipTimerRequest::default()
        }),
    })
}

fn close_origin(record: &RunRecord) -> Option<bool> {
    record
        .close
        .as_ref()
        .and_then(|close| close.is_closed_at_once)
}

fn attribute_sync_pending_count(outcome: &ApplyOutcome) -> usize {
    outcome
        .effects
        .iter()
        .filter(|effect| matches!(effect, ApplyEffect::AttributeSyncPending { .. }))
        .count()
}

#[test]
fn a_reapplied_flow_timeout_skip_waits_for_remarked_attributes() {
    let mut harness = Harness::with_settings(RunSettings {
        checkpoint_max_commands: 2,
        ..settings()
    });
    harness.start(&[PRIMARY], vec![synced("status", "opened")], 60);
    harness.ack(PRIMARY, harness.last_seq());
    let skipped = harness.apply(skip_flow_timeout());
    assert!(is_ok(&skipped.reply), "{:?}", skipped.reply);
    assert_eq!(harness.run_record().status, FlowStatus::Failed as i32);
    assert_eq!(harness.outbox_entry_count(), 0);

    let travel = harness.travel_to(ResetFlowRequest {
        skip_writes_reapply: false,
        ..reset(FlowResetType::Beginning)
    });
    let record = harness.run_record();
    assert_eq!(
        record.status,
        FlowStatus::Running as i32,
        "the reapplied Flow timeout must wait for the re-marked Attributes"
    );
    let pending = record.pending_close.expect("C31 waits");
    assert_eq!(pending.status, FlowStatus::Failed as i32);
    assert_eq!(pending.error_type, FlowErrorType::FlowTimeout as i32);
    assert!(harness.outbox_entry_count() > 0);
    assert_eq!(harness.directory_status(), FlowStatus::Running as i32);
    assert_eq!(attribute_sync_pending_count(&travel), 1);
    assert!(!wakes(&travel).contains(&WaitableChange::FlowClosed));
    assert!(!has_flow_closed_event(
        &harness.history_record(harness.last_seq())
    ));
    let mut points = vec![(harness.now_micros, harness.semantic_point_hash())];

    let ack = harness.ack(PRIMARY, harness.last_seq());
    assert!(wakes(&ack).contains(&WaitableChange::FlowClosed));
    let closed = harness.run_record();
    assert_eq!(closed.status, FlowStatus::Failed as i32);
    assert_eq!(close_origin(&closed), Some(false));
    assert!(has_flow_closed_event(
        &harness.history_record(harness.last_seq())
    ));
    points.push((harness.now_micros, harness.semantic_point_hash()));

    for (timestamp, expected) in &points {
        let command = harness.command(Command::TimeTravel(TimeTravelCommand {
            request: Some(ResetFlowRequest {
                run_attempt: 2,
                ..reset_to_time(*timestamp)
            }),
        }));
        let rebuilt = time_travel_target_hash(&harness.context, &harness.state, &command)
            .expect("rebuild the point")
            .expect("the point exists");
        assert_eq!(rebuilt.state_hash, *expected, "point at {timestamp}");
    }
}

#[test]
fn a_deferred_natural_close_records_its_origin_and_waits_again_after_a_reset() {
    let mut harness = Harness::new();
    harness.start(&[PRIMARY], vec![synced("status", "opened")], 60);
    harness.force_complete(Vec::new());
    assert!(harness.run_record().pending_close.is_some());
    harness.ack(PRIMARY, harness.last_seq());
    let finalized = harness.run_record();
    assert_eq!(finalized.status, FlowStatus::Completed as i32);
    assert_eq!(close_origin(&finalized), Some(false));
    let close_time = harness.history_record(harness.last_seq()).timestamp_micros;

    harness.travel_to(reset(FlowResetType::Beginning));
    harness.travel_to(reset_to_time(close_time));
    let restored = harness.run_record();
    assert_eq!(restored.status, FlowStatus::Running as i32);
    assert_eq!(
        restored.pending_close.map(|close| close.status),
        Some(FlowStatus::Completed as i32),
        "a restored deferred close waits for the re-marked Attributes"
    );
    assert!(harness.outbox_entry_count() > 0);
    harness.ack(PRIMARY, harness.last_seq());
    let closed = harness.run_record();
    assert_eq!(closed.status, FlowStatus::Completed as i32);
    assert_eq!(close_origin(&closed), Some(false));
}

impl Harness {
    /// Clears the close origin and FlowClosed message, as records written before them lack both.
    fn clear_stored_close_origins(&mut self) {
        let clear = |close: &mut Option<FlowCloseRecord>| {
            if let Some(close) = close.as_mut() {
                close.is_closed_at_once = None;
                close.history_message = None;
            }
        };
        let record_key = run_singleton_key(self.context.flow_hash, self.run(), RunTable::RunRecord);
        let mut record = self.run_record();
        clear(&mut record.close);
        self.state
            .rows
            .insert((ColumnFamily::Flow, record_key), record.encode_to_vec());
        let prefix = run_table_prefix(self.context.flow_hash, self.run(), RunTable::Checkpoint);
        let (start, end) = prefix_range(&prefix);
        let checkpoint_keys: Vec<Vec<u8>> = self
            .state
            .rows
            .range((ColumnFamily::History, start)..(ColumnFamily::History, end))
            .map(|((_, key), _)| key.clone())
            .collect();
        assert!(!checkpoint_keys.is_empty(), "the run has checkpoints");
        for key in checkpoint_keys {
            let mut checkpoint: CheckpointRecord = self
                .get(ColumnFamily::History, &key)
                .expect("the checkpoint exists");
            if let Some(run_status) = checkpoint.run_status.as_mut() {
                clear(&mut run_status.close);
            }
            self.state
                .rows
                .insert((ColumnFamily::History, key), checkpoint.encode_to_vec());
        }
    }
}

#[test]
fn restored_closes_without_a_stored_origin_follow_the_status_heuristic() {
    const REASON: &str = "operator ended the order";
    for (stop_type, expected_status) in [
        (None, FlowStatus::Completed),
        (Some(StopType::Cancel), FlowStatus::Canceled),
        (Some(StopType::Terminate), FlowStatus::Terminated),
    ] {
        let mut harness = Harness::with_settings(RunSettings {
            checkpoint_max_commands: 1,
            ..settings()
        });
        harness.start(&[PRIMARY], vec![synced("status", "opened")], 60);
        harness.ack(PRIMARY, harness.last_seq());
        match stop_type {
            None => {
                harness.force_complete(Vec::new());
            }
            Some(stop_type) => {
                harness.apply(Command::FlowStop(FlowStopCommand {
                    request: Some(StopFlowRequest {
                        stop_type: stop_type as i32,
                        reason: REASON.to_owned(),
                        ..StopFlowRequest::default()
                    }),
                }));
            }
        }
        assert_eq!(harness.run_record().status, expected_status as i32);
        let close_time = harness.history_record(harness.last_seq()).timestamp_micros;
        harness.clear_stored_close_origins();

        harness.travel_to(reset(FlowResetType::Beginning));
        let travel = harness.travel_to(reset_to_time(close_time));
        assert!(harness.outbox_entry_count() > 0, "re-marked entries exist");
        let restored = harness.run_record();
        if stop_type.is_none() {
            assert_eq!(
                restored.status,
                FlowStatus::Running as i32,
                "an old natural close waits for Attribute Sync"
            );
            harness.ack(PRIMARY, harness.last_seq());
            let closed = harness.run_record();
            assert_eq!(closed.status, FlowStatus::Completed as i32);
            assert_eq!(close_origin(&closed), Some(false));
            continue;
        }
        assert_eq!(
            restored.status, expected_status as i32,
            "an old {stop_type:?} closes at once"
        );
        assert_eq!(
            close_origin(&restored),
            Some(true),
            "the heuristic result is stored"
        );
        assert!(wakes(&travel).contains(&WaitableChange::FlowClosed));
        let closed_messages: Vec<String> = harness
            .history_record(harness.last_seq())
            .events
            .into_iter()
            .filter_map(|event| match event.payload {
                Some(flow_history_event::Payload::FlowClosed(closed)) => Some(closed.error_message),
                _ => None,
            })
            .collect();
        let expected_message = if stop_type == Some(StopType::Terminate) {
            REASON
        } else {
            ""
        };
        assert_eq!(
            closed_messages,
            vec![expected_message.to_owned()],
            "{stop_type:?} keeps its FlowClosed message from history"
        );
    }
}
