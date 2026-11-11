// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Known defect: dashes in Flow IDs and Step types make child Flow IDs ambiguous.
//! Fails until the owner chooses a fix.

use std::collections::BTreeMap;

use dexzd_apply::{
    ApplyContext, ApplyOutcome, FlowStateReader, StateReadError, StateRow, StateWrite,
    apply_flow_command,
};
use dexzd_keyspace::row_keys::{prefix_range, run_singleton_key};
use dexzd_keyspace::{ColumnFamily, PartitionHasher, RunId, RunTable, TenantId, run_table_prefix};
use dexzd_protocol::dex::{
    FlowTimeoutPolicy, InvokeWaitForMethodResponse, StartFlowRequest, StepOptions,
    SubFlowCondition, WaitingCondition, WaitingConditionType, WorkerTarget,
};
use dexzd_protocol::dexzd::v1::{
    ChildFlowStartMessage, FlowCommand, FlowStartCommand, InboxMessageCommand, OutboxRecord,
    RunRecord, RunSettings, StepAttemptStartedCommand, StepInvocationKey, StepMethod,
    StepMethodCompletedCommand, command_reply, flow_command::Command, inbox_message_command,
    outbox_record, step_method_completed_command,
};
use prost::Message;

const SECOND: i64 = 1_000_000;
const FIRST_PARENT_RUN: [u8; 16] = [1; 16];
const SECOND_PARENT_RUN: [u8; 16] = [2; 16];
const FIRST_CHILD_RUN: [u8; 16] = [3; 16];
const SECOND_CHILD_RUN: [u8; 16] = [4; 16];

#[derive(Default)]
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
                StateWrite::Delete { column_family, key }
                | StateWrite::SingleDelete { column_family, key } => {
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
    hasher: PartitionHasher,
    now_micros: i64,
}

impl Harness {
    fn new() -> Self {
        Self {
            state: MemoryState::default(),
            hasher: PartitionHasher::new(&[7; 16], 1024).expect("valid hasher"),
            now_micros: 1_800_000_000 * SECOND,
        }
    }

    fn apply(&mut self, flow_id: &str, run_id: &[u8], body: Command) -> ApplyOutcome {
        self.now_micros += 1000;
        let flow_hash = self.hasher.flow_hash(TenantId::DEFAULT, flow_id);
        let context = ApplyContext {
            partition: self.hasher.partition_of(flow_hash),
            flow_hash,
        };
        let command = FlowCommand {
            flow_id: flow_id.to_owned(),
            run_id: run_id.to_vec(),
            epoch: 1,
            behavior_version: 1,
            proposed_at_micros: self.now_micros,
            command_id: [0; 16].to_vec(),
            command: Some(body),
            ..FlowCommand::default()
        };
        let outcome = apply_flow_command(&context, &self.state, &command).expect("apply succeeds");
        self.state.commit(&outcome);
        outcome
    }

    /// Starts `flow_id` at `start_step_type`, which waits on one SubFlow, and returns its start message.
    fn start_parent_waiting_on_one_child(
        &mut self,
        flow_id: &str,
        run_id: [u8; 16],
        start_step_type: &str,
    ) -> OutboxRecord {
        let started = self.apply(
            flow_id,
            &[],
            Command::FlowStart(FlowStartCommand {
                new_run_id: run_id.to_vec(),
                request: Some(StartFlowRequest {
                    flow_id: flow_id.to_owned(),
                    flow_type: "ParentFlow".to_owned(),
                    start_step_type: start_step_type.to_owned(),
                    request_id: format!("{flow_id}-request"),
                    ..StartFlowRequest::default()
                }),
                settings: Some(settings()),
                resolved_timeout_policy: FlowTimeoutPolicy::Fail as i32,
                worker_target: Some(pull_worker()),
                ..FlowStartCommand::default()
            }),
        );
        assert!(matches!(
            started.reply.reply,
            Some(command_reply::Reply::StartFlow(_))
        ));
        let invocation = StepInvocationKey {
            step_execution_id: format!("{start_step_type}-1"),
            method: StepMethod::WaitFor as i32,
        };
        assert_ok(&self.apply(
            flow_id,
            &run_id,
            Command::StepAttemptStarted(StepAttemptStartedCommand {
                invocation: Some(invocation.clone()),
                attempt: 1,
                task_id: vec![9; 16],
                pull_routing: None,
            }),
        ));
        assert_ok(&self.apply(
            flow_id,
            &run_id,
            Command::StepMethodCompleted(StepMethodCompletedCommand {
                invocation: Some(invocation),
                attempt: 1,
                response: Some(step_method_completed_command::Response::WaitFor(
                    InvokeWaitForMethodResponse {
                        waiting_condition: Some(WaitingCondition {
                            waiting_condition_type: WaitingConditionType::AllCompleted as i32,
                            sub_flow_conditions: vec![SubFlowCondition {
                                condition_id: "child".to_owned(),
                                sub_flow_type: "ChildFlow".to_owned(),
                                start_step_type: "ChildStep".to_owned(),
                                step_options: Some(StepOptions {
                                    skip_wait_for: true,
                                    ..StepOptions::default()
                                }),
                                ..SubFlowCondition::default()
                            }],
                            ..WaitingCondition::default()
                        }),
                        ..InvokeWaitForMethodResponse::default()
                    },
                )),
                ..StepMethodCompletedCommand::default()
            }),
        ));
        let flow_hash = self.hasher.flow_hash(TenantId::DEFAULT, flow_id);
        let (start, end) = prefix_range(&run_table_prefix(
            flow_hash,
            RunId(run_id),
            RunTable::Outbox,
        ));
        let mut outbox: Vec<OutboxRecord> = self
            .state
            .scan(ColumnFamily::Flow, &start, &end, usize::MAX)
            .expect("memory reads")
            .into_iter()
            .map(|(_, bytes)| OutboxRecord::decode(bytes.as_slice()).expect("outbox decodes"))
            .collect();
        assert_eq!(outbox.len(), 1, "one child start");
        outbox.pop().expect("child start")
    }

    fn deliver_child_start(
        &mut self,
        start: &ChildFlowStartMessage,
        message_id: &[u8],
        new_run_id: [u8; 16],
    ) -> ApplyOutcome {
        self.apply(
            &start.child_flow_id,
            &[],
            Command::InboxMessage(InboxMessageCommand {
                message_id: message_id.to_vec(),
                source: start
                    .parent
                    .as_ref()
                    .and_then(|parent| parent.parent.clone()),
                payload: Some(inbox_message_command::Payload::ChildStart(start.clone())),
                new_run_id: new_run_id.to_vec(),
                settings: Some(settings()),
            }),
        )
    }

    fn record(&self, flow_id: &str, run_id: [u8; 16]) -> Option<RunRecord> {
        let flow_hash = self.hasher.flow_hash(TenantId::DEFAULT, flow_id);
        let key = run_singleton_key(flow_hash, RunId(run_id), RunTable::RunRecord);
        self.state
            .get(ColumnFamily::Flow, &key)
            .expect("memory reads")
            .map(|bytes| RunRecord::decode(bytes.as_slice()).expect("run decodes"))
    }
}

fn child_start(outbox: &OutboxRecord) -> ChildFlowStartMessage {
    match outbox.payload.clone() {
        Some(outbox_record::Payload::ChildStart(start)) => start,
        other => panic!("expected a child start, got {other:?}"),
    }
}

fn pull_worker() -> WorkerTarget {
    WorkerTarget { is_pull_mode: true }
}

fn settings() -> RunSettings {
    RunSettings {
        retention_after_close_micros: 7 * 24 * 3600 * SECOND,
        wire_inline_threshold_bytes: 100,
        max_value_bytes: 16 << 20,
        request_dedup_capacity: 1000,
        max_run_state_bytes: 1 << 30,
        max_active_step_executions: 10_000,
        history_window_events: 10_000,
        history_prune_chunk_events: 1000,
        max_retained_superseded_bytes: 256 << 20,
        ..RunSettings::default()
    }
}

fn assert_ok(outcome: &ApplyOutcome) {
    assert!(
        matches!(outcome.reply.reply, Some(command_reply::Reply::Ok(_))),
        "unexpected reply {:?}",
        outcome.reply.reply
    );
}

/// Parents `a-b` (Step `C`) and `a` (Step `b-C`) both derive `SubFlow:a-b-C-1-0`.
#[test]
fn known_defect_distinct_parents_with_dashes_share_one_child_flow_id() {
    let mut harness = Harness::new();
    let first_outbox = harness.start_parent_waiting_on_one_child("a-b", FIRST_PARENT_RUN, "C");
    let second_outbox = harness.start_parent_waiting_on_one_child("a", SECOND_PARENT_RUN, "b-C");
    let first_start = child_start(&first_outbox);
    let second_start = child_start(&second_outbox);
    assert_eq!(first_start.child_flow_id, "SubFlow:a-b-C-1-0");
    assert_eq!(second_start.child_flow_id, first_start.child_flow_id);
    assert_ne!(first_start.parent, second_start.parent);

    assert_ok(&harness.deliver_child_start(
        &first_start,
        &first_outbox.message_id,
        FIRST_CHILD_RUN,
    ));
    assert_ok(&harness.deliver_child_start(
        &second_start,
        &second_outbox.message_id,
        SECOND_CHILD_RUN,
    ));

    let first_child = harness
        .record(&first_start.child_flow_id, FIRST_CHILD_RUN)
        .expect("the first parent's child run exists");
    assert_eq!(first_child.parent, first_start.parent);
    let second_parent = second_start.parent.expect("parent reference");
    assert!(
        !first_child.attached_parents.contains(&second_parent),
        "parent `a` (Step `b-C`) attached to the child of parent `a-b` (Step `C`) \
         and will receive that child's result"
    );
}

/// Without a dash in the Step type the ID splits uniquely from the right.
#[test]
fn parents_with_dashed_flow_ids_and_plain_step_types_get_distinct_children() {
    let mut harness = Harness::new();
    let first_outbox = harness.start_parent_waiting_on_one_child("a-b", FIRST_PARENT_RUN, "C");
    let second_outbox = harness.start_parent_waiting_on_one_child("a", SECOND_PARENT_RUN, "C");
    assert_ne!(
        child_start(&first_outbox).child_flow_id,
        child_start(&second_outbox).child_flow_id
    );
}
