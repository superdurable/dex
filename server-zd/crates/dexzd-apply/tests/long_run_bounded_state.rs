// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! A long-lived run's Step rows, checkpoints, and scans stay bounded; the checks count rows, not time.

use std::cell::Cell;
use std::collections::BTreeMap;

use dexzd_apply::{
    ApplyContext, ApplyOutcome, FlowStateReader, StateReadError, StateRow, StateWrite,
    apply_flow_command,
};
use dexzd_keyspace::row_keys::{attempt_record_key, checkpoint_key, prefix_range};
use dexzd_keyspace::{ColumnFamily, PartitionHasher, RunId, RunTable, TenantId, run_table_prefix};
use dexzd_protocol::dex::{
    ExecuteMethodFailurePolicy, InvokeExecuteMethodResponse, StartFlowRequest, StepDecision,
    StepMovement, StepOptions, Value, WorkerErrorResponse, WorkerTarget, value,
};
use dexzd_protocol::dexzd::v1::{
    AttemptRecord, FlowCommand, FlowStartCommand, RunSettings, StepAttemptFailedCommand,
    StepAttemptFailureKind, StepAttemptStartedCommand, StepInvocationKey, StepMethod,
    StepMethodCompletedCommand, command_reply, flow_command::Command,
    step_method_completed_command,
};
use prost::Message;

const FLOW_ID: &str = "long-run";
const RUN_ID: RunId = RunId([4; 16]);
const TOTAL_STEPS: u32 = 3_000;
/// The first Steps of the chain that fail and proceed to the next one, keeping Failed rows.
const FAILED_STEPS: [u32; 2] = [10, 20];
/// LoopStep plus ParkedStep, which waits in WaitFor for the whole run.
const ACTIVE_STEPS: usize = 2;

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
                    let removed: Vec<_> = self
                        .rows
                        .range((*column_family, start.clone())..(*column_family, end.clone()))
                        .map(|(key, _)| key.clone())
                        .collect();
                    for key in removed {
                        self.rows.remove(&key);
                    }
                }
            }
        }
    }
}

/// Counts the StepExecution rows that scans return.
struct CountingReader<'state> {
    state: &'state MemoryState,
    step_prefix: Vec<u8>,
    scanned_step_rows: Cell<usize>,
}

impl FlowStateReader for CountingReader<'_> {
    fn get(
        &self,
        column_family: ColumnFamily,
        key: &[u8],
    ) -> Result<Option<Vec<u8>>, StateReadError> {
        self.state.get(column_family, key)
    }

    fn scan(
        &self,
        column_family: ColumnFamily,
        start: &[u8],
        end: &[u8],
        limit: usize,
    ) -> Result<Vec<StateRow>, StateReadError> {
        let rows = self.state.scan(column_family, start, end, limit)?;
        let step_rows = rows
            .iter()
            .filter(|(key, _)| {
                column_family == ColumnFamily::Flow && key.starts_with(&self.step_prefix)
            })
            .count();
        self.scanned_step_rows
            .set(self.scanned_step_rows.get() + step_rows);
        Ok(rows)
    }
}

struct LongRun {
    state: MemoryState,
    context: ApplyContext,
    now_micros: i64,
    max_scanned_step_rows: usize,
}

impl LongRun {
    fn new() -> Self {
        let hasher = PartitionHasher::new(&[7; 16], 1024).expect("valid partition hasher");
        let flow_hash = hasher.flow_hash(TenantId::DEFAULT, FLOW_ID);
        Self {
            state: MemoryState::default(),
            context: ApplyContext {
                partition: hasher.partition_of(flow_hash),
                flow_hash,
            },
            now_micros: 1_800_000_000_000_000,
            max_scanned_step_rows: 0,
        }
    }

    fn apply(&mut self, body: Command) {
        self.now_micros += 1000;
        let is_start = matches!(body, Command::FlowStart(_));
        let command = FlowCommand {
            flow_id: FLOW_ID.to_owned(),
            run_id: if is_start {
                Vec::new()
            } else {
                RUN_ID.as_bytes().to_vec()
            },
            behavior_version: 1,
            proposed_at_micros: self.now_micros,
            command: Some(body),
            ..FlowCommand::default()
        };
        let reader = CountingReader {
            state: &self.state,
            step_prefix: run_table_prefix(self.context.flow_hash, RUN_ID, RunTable::StepExecution),
            scanned_step_rows: Cell::new(0),
        };
        let outcome =
            apply_flow_command(&self.context, &reader, &command).expect("the command applies");
        assert!(
            !matches!(outcome.reply.reply, Some(command_reply::Reply::Error(_))),
            "{:?}",
            outcome.reply
        );
        self.max_scanned_step_rows = self
            .max_scanned_step_rows
            .max(reader.scanned_step_rows.get());
        self.state.commit(&outcome);
    }

    fn start(&mut self) {
        let request = StartFlowRequest {
            flow_id: FLOW_ID.to_owned(),
            flow_type: "LongRunFlow".to_owned(),
            start_step_type: "LoopStep".to_owned(),
            step_input: Some(Value {
                kind: Some(value::Kind::StringValue("input".to_owned())),
            }),
            step_options: Some(loop_options()),
            request_id: "start-1".to_owned(),
            ..StartFlowRequest::default()
        };
        self.apply(Command::FlowStart(FlowStartCommand {
            new_run_id: RUN_ID.as_bytes().to_vec(),
            request: Some(request),
            settings: Some(RunSettings {
                wire_inline_threshold_bytes: 100,
                max_value_bytes: 16 << 20,
                retention_after_close_micros: 7 * 24 * 3600 * 1_000_000,
                request_dedup_capacity: 1000,
                max_run_state_bytes: 1 << 30,
                max_active_step_executions: 10_000,
                history_window_events: 10_000,
                history_prune_chunk_events: 1000,
                max_retained_superseded_bytes: 256 << 20,
                checkpoint_command_threshold: 1000,
                checkpoint_row_write_bytes_threshold: 1 << 20,
                ..RunSettings::default()
            }),
            worker_target: Some(WorkerTarget {
                is_pull_mode: true,
                ..WorkerTarget::default()
            }),
            ..FlowStartCommand::default()
        }));
    }

    /// Runs LoopStep-`number`, which moves to the next LoopStep, or fails and proceeds to it.
    fn run_loop_step(&mut self, number: u32) {
        let step_execution_id = format!("LoopStep-{number}");
        let invocation = StepInvocationKey {
            step_execution_id,
            method: StepMethod::Execute as i32,
        };
        self.apply(Command::StepAttemptStarted(StepAttemptStartedCommand {
            invocation: Some(invocation.clone()),
            attempt: 1,
            task_id: vec![2; 16],
            pull_routing: None,
        }));
        if FAILED_STEPS.contains(&number) {
            self.apply(Command::StepAttemptFailed(StepAttemptFailedCommand {
                invocation: Some(invocation),
                attempt: 1,
                kind: StepAttemptFailureKind::WorkerError as i32,
                worker_error: Some(WorkerErrorResponse {
                    detail: "failed".to_owned(),
                    ..WorkerErrorResponse::default()
                }),
                is_non_retryable: true,
                ..StepAttemptFailedCommand::default()
            }));
            return;
        }
        let mut next_steps = vec![StepMovement {
            step_type: "LoopStep".to_owned(),
            step_options: Some(loop_options()),
            ..StepMovement::default()
        }];
        if number == 1 {
            next_steps.push(StepMovement {
                step_type: "ParkedStep".to_owned(),
                ..StepMovement::default()
            });
        }
        self.apply(Command::StepMethodCompleted(StepMethodCompletedCommand {
            invocation: Some(invocation),
            attempt: 1,
            response: Some(step_method_completed_command::Response::Execute(
                InvokeExecuteMethodResponse {
                    step_decision: Some(StepDecision {
                        next_steps,
                        ..StepDecision::default()
                    }),
                    ..InvokeExecuteMethodResponse::default()
                },
            )),
            ..StepMethodCompletedCommand::default()
        }));
    }

    fn step_row_count(&self) -> usize {
        let prefix = run_table_prefix(self.context.flow_hash, RUN_ID, RunTable::StepExecution);
        let (start, end) = prefix_range(&prefix);
        self.state
            .rows
            .range((ColumnFamily::Flow, start)..(ColumnFamily::Flow, end))
            .count()
    }

    fn latest_checkpoint_bytes(&self) -> usize {
        let attempt_key = attempt_record_key(self.context.flow_hash, RUN_ID, 1);
        let attempt =
            AttemptRecord::decode(self.state.rows[&(ColumnFamily::Flow, attempt_key)].as_slice())
                .expect("valid attempt row");
        let seq = *attempt.checkpoint_seqs.last().expect("a checkpoint exists");
        let key = checkpoint_key(self.context.flow_hash, RUN_ID, 1, seq);
        self.state.rows[&(ColumnFamily::History, key)].len()
    }
}

fn loop_options() -> StepOptions {
    StepOptions {
        skip_wait_for: true,
        execute_failure_policy: ExecuteMethodFailurePolicy::ProceedToConfiguredStep as i32,
        execute_failure_proceed_step_type: "LoopStep".to_owned(),
        execute_failure_proceed_step_options: Some(Box::new(StepOptions {
            skip_wait_for: true,
            execute_failure_policy: ExecuteMethodFailurePolicy::ProceedToConfiguredStep as i32,
            execute_failure_proceed_step_type: "LoopStep".to_owned(),
            ..StepOptions::default()
        })),
        ..StepOptions::default()
    }
}

#[test]
fn step_rows_and_checkpoints_stay_bounded_by_active_and_failed_steps() {
    let mut run = LongRun::new();
    run.start();
    let mut checkpoint_bytes_at = BTreeMap::new();
    for number in 1..=TOTAL_STEPS {
        run.run_loop_step(number);
        if number % 500 == 0 {
            assert_eq!(
                run.step_row_count(),
                ACTIVE_STEPS + FAILED_STEPS.len(),
                "Step rows after {number} Steps: only active and Failed Steps keep rows"
            );
            checkpoint_bytes_at.insert(number, run.latest_checkpoint_bytes());
        }
    }
    let early = checkpoint_bytes_at[&1_000];
    let late = checkpoint_bytes_at[&TOTAL_STEPS];
    assert!(
        late.abs_diff(early) * 10 <= early,
        "the checkpoint does not grow with total Steps: {checkpoint_bytes_at:?}"
    );
    assert!(
        run.max_scanned_step_rows <= 2 * (ACTIVE_STEPS + FAILED_STEPS.len()),
        "a command scanned {} StepExecution rows",
        run.max_scanned_step_rows
    );
}
