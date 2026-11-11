// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! A Step type containing `-` cannot start SubFlows, because the child Flow ID joins parts with `-`.

use std::collections::BTreeMap;

use dexzd_apply::{
    ApplyContext, ApplyOutcome, FlowStateReader, StateReadError, StateRow, StateWrite,
    apply_flow_command,
};
use dexzd_keyspace::row_keys::{prefix_range, run_named_key, run_singleton_key};
use dexzd_keyspace::{ColumnFamily, PartitionHasher, RunId, RunTable, TenantId, run_table_prefix};
use dexzd_protocol::dex::{
    FlowStatus, FlowTimeoutPolicy, InvokeWaitForMethodResponse, StartFlowRequest, StepOptions,
    SubFlowCondition, TimerCondition, WaitingCondition, WaitingConditionType, WorkerTarget,
};
use dexzd_protocol::dexzd::v1::{
    FlowCommand, FlowStartCommand, OutboxRecord, RunRecord, RunSettings, StepAttemptStartedCommand,
    StepExecutionPhase, StepExecutionRecord, StepInvocationKey, StepMethod,
    StepMethodCompletedCommand, command_reply, flow_command::Command, outbox_record,
    step_method_completed_command,
};
use prost::Message;

const SECOND: i64 = 1_000_000;
const FIRST_PARENT_RUN: [u8; 16] = [1; 16];
const SECOND_PARENT_RUN: [u8; 16] = [2; 16];

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

    /// Starts `flow_id` at `start_step_type` and completes its WaitFor with `condition`.
    fn start_parent_and_complete_wait_for(
        &mut self,
        flow_id: &str,
        run_id: [u8; 16],
        start_step_type: &str,
        condition: WaitingCondition,
    ) -> ApplyOutcome {
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
        self.apply(
            flow_id,
            &run_id,
            Command::StepMethodCompleted(StepMethodCompletedCommand {
                invocation: Some(invocation),
                attempt: 1,
                response: Some(step_method_completed_command::Response::WaitFor(
                    InvokeWaitForMethodResponse {
                        waiting_condition: Some(condition),
                        ..InvokeWaitForMethodResponse::default()
                    },
                )),
                ..StepMethodCompletedCommand::default()
            }),
        )
    }

    fn outbox(&self, flow_id: &str, run_id: [u8; 16]) -> Vec<OutboxRecord> {
        let flow_hash = self.hasher.flow_hash(TenantId::DEFAULT, flow_id);
        let (start, end) = prefix_range(&run_table_prefix(
            flow_hash,
            RunId(run_id),
            RunTable::Outbox,
        ));
        self.state
            .scan(ColumnFamily::Flow, &start, &end, usize::MAX)
            .expect("memory reads")
            .into_iter()
            .map(|(_, bytes)| OutboxRecord::decode(bytes.as_slice()).expect("outbox decodes"))
            .collect()
    }

    fn record(&self, flow_id: &str, run_id: [u8; 16]) -> RunRecord {
        let flow_hash = self.hasher.flow_hash(TenantId::DEFAULT, flow_id);
        let key = run_singleton_key(flow_hash, RunId(run_id), RunTable::RunRecord);
        let bytes = self
            .state
            .get(ColumnFamily::Flow, &key)
            .expect("memory reads")
            .expect("run exists");
        RunRecord::decode(bytes.as_slice()).expect("run decodes")
    }

    fn step(
        &self,
        flow_id: &str,
        run_id: [u8; 16],
        step_execution_id: &str,
    ) -> StepExecutionRecord {
        let flow_hash = self.hasher.flow_hash(TenantId::DEFAULT, flow_id);
        let key = run_named_key(
            flow_hash,
            RunId(run_id),
            RunTable::StepExecution,
            step_execution_id,
        );
        let bytes = self
            .state
            .get(ColumnFamily::Flow, &key)
            .expect("memory reads")
            .expect("Step exists");
        StepExecutionRecord::decode(bytes.as_slice()).expect("Step decodes")
    }
}

fn one_sub_flow() -> WaitingCondition {
    WaitingCondition {
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
    }
}

fn child_flow_id(outbox: &[OutboxRecord]) -> String {
    let [record] = outbox else {
        panic!("expected one child start, got {}", outbox.len())
    };
    match record.payload.as_ref() {
        Some(outbox_record::Payload::ChildStart(start)) => start.child_flow_id.clone(),
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

/// Parents `a-b` (Step `C`) and `a` (Step `b-C`) would both derive `SubFlow:a-b-C-1-0`.
#[test]
fn a_step_type_with_a_dash_cannot_start_sub_flows() {
    let mut harness = Harness::new();
    assert_ok(&harness.start_parent_and_complete_wait_for(
        "a-b",
        FIRST_PARENT_RUN,
        "C",
        one_sub_flow(),
    ));
    assert_eq!(
        child_flow_id(&harness.outbox("a-b", FIRST_PARENT_RUN)),
        "SubFlow:a-b-C-1-0"
    );

    harness.start_parent_and_complete_wait_for("a", SECOND_PARENT_RUN, "b-C", one_sub_flow());

    assert!(
        harness.outbox("a", SECOND_PARENT_RUN).is_empty(),
        "the rejected result starts no child"
    );
    let step = harness.step("a", SECOND_PARENT_RUN, "b-C-1");
    assert_eq!(step.phase, StepExecutionPhase::Failed as i32);
    let record = harness.record("a", SECOND_PARENT_RUN);
    assert_eq!(record.status, FlowStatus::Failed as i32);
    let error_message = record.close.expect("the run is closed").error_message;
    assert!(
        error_message.contains(
            "Step type \"b-C\" contains \"-\" and cannot start SubFlows: the child Flow ID joins"
        ),
        "{error_message}"
    );
}

#[test]
fn a_step_type_with_a_dash_may_wait_without_sub_flows() {
    let mut harness = Harness::new();
    let timer_only = WaitingCondition {
        waiting_condition_type: WaitingConditionType::AllCompleted as i32,
        timer_conditions: vec![TimerCondition {
            duration_seconds: 60,
            ..TimerCondition::default()
        }],
        ..WaitingCondition::default()
    };
    assert_ok(&harness.start_parent_and_complete_wait_for(
        "a",
        FIRST_PARENT_RUN,
        "b-C",
        timer_only,
    ));
    let step = harness.step("a", FIRST_PARENT_RUN, "b-C-1");
    assert_eq!(step.phase, StepExecutionPhase::Waiting as i32);
}

/// Without a dash in the Step type the ID splits uniquely from the right.
#[test]
fn parents_with_dashed_flow_ids_and_plain_step_types_get_distinct_children() {
    let mut harness = Harness::new();
    assert_ok(&harness.start_parent_and_complete_wait_for(
        "a-b",
        FIRST_PARENT_RUN,
        "C",
        one_sub_flow(),
    ));
    assert_ok(&harness.start_parent_and_complete_wait_for(
        "a",
        SECOND_PARENT_RUN,
        "C",
        one_sub_flow(),
    ));
    assert_ne!(
        child_flow_id(&harness.outbox("a-b", FIRST_PARENT_RUN)),
        child_flow_id(&harness.outbox("a", SECOND_PARENT_RUN))
    );
}
