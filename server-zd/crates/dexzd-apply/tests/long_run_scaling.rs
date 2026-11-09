// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Per-command reads stay constant as a long run executes more Steps.

use std::cell::Cell;
use std::collections::BTreeMap;

use dexzd_apply::{
    ApplyContext, ApplyOutcome, FlowStateReader, StateReadError, StateRow, StateWrite,
    apply_flow_command,
};
use dexzd_keyspace::{ColumnFamily, PartitionHasher, RunTable, TenantId, parse_run_key};
use dexzd_protocol::dex::{
    InvokeExecuteMethodResponse, StartFlowRequest, StepDecision, StepMovement, StepOptions, Value,
    WorkerTarget, value,
};
use dexzd_protocol::dexzd::v1::{
    FlowCommand, FlowStartCommand, RunSettings, StepAttemptStartedCommand, StepInvocationKey,
    StepMethod, StepMethodCompletedCommand, command_reply, flow_command::Command,
    step_method_completed_command,
};

const SECOND: i64 = 1_000_000;
const STEP_TYPE: &str = "ChainStep";
/// Steps sampled at each milestone.
const SAMPLED_STEPS: u64 = 40;
/// Active Step executions of the chain: the completing Step and the one it moves to.
const MAX_ACTIVE_STEPS: u64 = 2;

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

/// Reads one command makes, counted by kind.
#[derive(Debug, Default, Clone, Copy, PartialEq, Eq)]
struct CommandReads {
    gets: u64,
    scanned_rows: u64,
    step_execution_gets: u64,
    scanned_step_execution_rows: u64,
}

/// Counts the reads of the commands applied through it.
struct CountingReader<'state> {
    state: &'state MemoryState,
    reads: Cell<CommandReads>,
}

impl FlowStateReader for CountingReader<'_> {
    fn get(
        &self,
        column_family: ColumnFamily,
        key: &[u8],
    ) -> Result<Option<Vec<u8>>, StateReadError> {
        let mut reads = self.reads.get();
        reads.gets += 1;
        if is_step_execution_key(column_family, key) {
            reads.step_execution_gets += 1;
        }
        self.reads.set(reads);
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
        let mut reads = self.reads.get();
        for (key, _) in &rows {
            reads.scanned_rows += 1;
            if is_step_execution_key(column_family, key) {
                reads.scanned_step_execution_rows += 1;
            }
        }
        self.reads.set(reads);
        Ok(rows)
    }
}

fn is_step_execution_key(column_family: ColumnFamily, key: &[u8]) -> bool {
    column_family == ColumnFamily::Flow
        && parse_run_key(key).is_ok_and(|parsed| parsed.table == RunTable::StepExecution)
}

/// A run whose Step moves to a new Step of the same type, so completed Steps pile up.
struct ChainRun {
    state: MemoryState,
    context: ApplyContext,
    now_micros: i64,
    run_id: Vec<u8>,
    next_step_number: u64,
}

impl ChainRun {
    fn start() -> Self {
        let hasher = PartitionHasher::new(&[7; 16], 1024).expect("valid partition hasher");
        let flow_hash = hasher.flow_hash(TenantId::DEFAULT, "long-run-1");
        let mut run = Self {
            state: MemoryState::default(),
            context: ApplyContext {
                partition: hasher.partition_of(flow_hash),
                flow_hash,
            },
            now_micros: 1_800_000_000 * SECOND,
            run_id: Vec::new(),
            next_step_number: 1,
        };
        let outcome = run.apply(Command::FlowStart(FlowStartCommand {
            new_run_id: vec![9; 16],
            request: Some(StartFlowRequest {
                flow_id: "long-run-1".to_owned(),
                flow_type: "ChainFlow".to_owned(),
                start_step_type: STEP_TYPE.to_owned(),
                step_input: Some(step_input(0)),
                step_options: Some(chain_options()),
                request_id: "start-1".to_owned(),
                ..StartFlowRequest::default()
            }),
            settings: Some(settings()),
            resolved_timeout_policy: dexzd_protocol::dex::FlowTimeoutPolicy::Fail as i32,
            worker_target: Some(WorkerTarget { is_pull_mode: true }),
            ..FlowStartCommand::default()
        }));
        let Some(command_reply::Reply::StartFlow(reply)) = &outcome.reply.reply else {
            panic!("the run starts: {:?}", outcome.reply);
        };
        run.run_id = reply.run_id.clone();
        run
    }

    fn command(&mut self, body: Command) -> FlowCommand {
        self.now_micros += 1000;
        FlowCommand {
            flow_id: "long-run-1".to_owned(),
            run_id: if matches!(body, Command::FlowStart(_)) {
                Vec::new()
            } else {
                self.run_id.clone()
            },
            expected_run_attempt: 0,
            epoch: 1,
            behavior_version: 1,
            proposed_at_micros: self.now_micros,
            command_id: u128::try_from(self.now_micros)
                .expect("command time is positive")
                .to_be_bytes()
                .to_vec(),
            tenant_id: 0,
            is_transactional_rpc_in_flight: false,
            command: Some(body),
        }
    }

    fn apply(&mut self, body: Command) -> ApplyOutcome {
        let command = self.command(body);
        let outcome =
            apply_flow_command(&self.context, &self.state, &command).expect("apply succeeds");
        assert!(
            !outcome.writes.is_empty(),
            "the command applies: {:?}",
            outcome.reply
        );
        self.state.commit(&outcome);
        outcome
    }

    /// Applies the command once through a counting reader, then commits it.
    ///
    /// Returns the command's reads and whether it wrote a checkpoint.
    fn apply_counted(&mut self, body: Command) -> (CommandReads, bool) {
        let command = self.command(body);
        let reader = CountingReader {
            state: &self.state,
            reads: Cell::new(CommandReads::default()),
        };
        let outcome = apply_flow_command(&self.context, &reader, &command).expect("apply succeeds");
        let reads = reader.reads.get();
        assert!(
            !outcome.writes.is_empty(),
            "the command applies: {:?}",
            outcome.reply
        );
        let has_checkpoint = outcome.writes.iter().any(|write| {
            matches!(
                write,
                StateWrite::Put { column_family: ColumnFamily::History, key, .. }
                    if parse_run_key(key).is_ok_and(|parsed| parsed.table == RunTable::Checkpoint)
            )
        });
        self.state.commit(&outcome);
        (reads, has_checkpoint)
    }

    /// Starts and completes the current Step, which moves to the next one.
    ///
    /// Returns the completion's reads, or `None` when it wrote a checkpoint, whose reads still
    /// grow with the Step count.
    fn run_step(&mut self, is_counted: bool) -> Option<CommandReads> {
        let number = self.next_step_number;
        self.next_step_number += 1;
        let invocation = StepInvocationKey {
            step_execution_id: format!("{STEP_TYPE}-{number}"),
            method: StepMethod::Execute as i32,
        };
        let task_id = u128::from(number).to_be_bytes().to_vec();
        self.apply(Command::StepAttemptStarted(StepAttemptStartedCommand {
            invocation: Some(invocation.clone()),
            attempt: 1,
            task_id: task_id.clone(),
            pull_routing: None,
        }));
        let completion = Command::StepMethodCompleted(StepMethodCompletedCommand {
            invocation: Some(invocation),
            attempt: 1,
            task_id,
            response: Some(step_method_completed_command::Response::Execute(
                InvokeExecuteMethodResponse {
                    step_decision: Some(StepDecision {
                        next_steps: vec![StepMovement {
                            step_type: STEP_TYPE.to_owned(),
                            step_input: Some(step_input(number)),
                            step_options: Some(chain_options()),
                            ..StepMovement::default()
                        }],
                        ..StepDecision::default()
                    }),
                    ..InvokeExecuteMethodResponse::default()
                },
            )),
            app_worker_id: "worker-a".to_owned(),
            ..StepMethodCompletedCommand::default()
        });
        if !is_counted {
            self.apply(completion);
            return None;
        }
        let (reads, has_checkpoint) = self.apply_counted(completion);
        (!has_checkpoint).then_some(reads)
    }

    /// Runs Steps until `executed_steps` have completed, then samples the next ones.
    ///
    /// Returns the reads of every sampled completion that wrote no checkpoint.
    fn sample_at(&mut self, executed_steps: u64) -> Vec<CommandReads> {
        while self.next_step_number <= executed_steps {
            self.run_step(false);
        }
        (0..SAMPLED_STEPS)
            .filter_map(|_| self.run_step(true))
            .collect()
    }
}

fn settings() -> RunSettings {
    RunSettings {
        // A small window prunes history in every sample, as in a long run.
        history_window_events: 200,
        history_prune_chunk_events: 50,
        max_retained_superseded_bytes: 256 << 20,
        retention_after_close_micros: 7 * 24 * 3600 * SECOND,
        wire_inline_threshold_bytes: 100,
        max_value_bytes: 16 << 20,
        checkpoint_row_write_bytes_threshold: 1 << 20,
        checkpoint_command_threshold: 1000,
        request_dedup_capacity: 1000,
        max_run_state_bytes: 1 << 30,
        max_active_step_executions: 10_000,
        max_child_flow_result_bytes: 16 << 20,
        history_segment_target_bytes: 4 << 20,
        delete_expired_history_objects: false,
        superseded_value_grace_micros: 0,
    }
}

fn chain_options() -> StepOptions {
    StepOptions {
        skip_wait_for: true,
        ..StepOptions::default()
    }
}

fn step_input(number: u64) -> Value {
    Value {
        kind: Some(value::Kind::StringValue(format!("{{\"step\":{number}}}"))),
    }
}

/// Completions once scanned every Step execution row to refresh search, so their reads grew
/// with executed Steps.
#[test]
fn step_completion_reads_stay_constant_as_the_run_ages() {
    let mut run = ChainRun::start();
    let young = run.sample_at(1_000);
    let old = run.sample_at(4_000);
    for (executed_steps, samples) in [(1_000, &young), (4_000, &old)] {
        assert!(
            samples.len() as u64 > SAMPLED_STEPS / 2,
            "most sampled completions after {executed_steps} Steps write no checkpoint"
        );
        for reads in samples {
            assert!(
                reads.scanned_step_execution_rows <= MAX_ACTIVE_STEPS,
                "a completion after {executed_steps} Steps scanned Step executions beyond the \
                 active ones: {reads:?}"
            );
        }
    }
    let young_most = young
        .iter()
        .copied()
        .fold(CommandReads::default(), max_reads);
    let old_most = old.iter().copied().fold(CommandReads::default(), max_reads);
    assert_eq!(
        young_most, old_most,
        "the most reads of a completion grew from 1,000 to 4,000 executed Steps"
    );
}

fn max_reads(left: CommandReads, right: CommandReads) -> CommandReads {
    CommandReads {
        gets: left.gets.max(right.gets),
        scanned_rows: left.scanned_rows.max(right.scanned_rows),
        step_execution_gets: left.step_execution_gets.max(right.step_execution_gets),
        scanned_step_execution_rows: left
            .scanned_step_execution_rows
            .max(right.scanned_step_execution_rows),
    }
}
