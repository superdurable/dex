// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Staged TimeTravel results (plan section 6.3): the owner computes and stages the result,
//! apply checks it, restores missing values, and adds the input recorded since.

use super::*;
use dexzd_apply::values::{NoOffloadedValues, OffloadedValues};
use dexzd_apply::{
    ApplyError, ComputedTimeTravel, StateHistorySource, TimeTravelComputation,
    TimeTravelComputationRequest, TimeTravelComputationSources, compute_time_travel,
};
use dexzd_keyspace::row_keys::{time_travel_staging_key, value_garbage_key};
use dexzd_protocol::dexzd::v1::{
    TimeTravelStageCommand, TimeTravelStagedChunk, TimeTravelStagedTarget,
};

/// Rows of these tables hold the attempt state and its values.
const RESTORED_TABLES: [RunTable; 12] = [
    RunTable::AttemptRecord,
    RunTable::Attribute,
    RunTable::ChannelMessage,
    RunTable::ChannelMeta,
    RunTable::StepExecution,
    RunTable::StepCounter,
    RunTable::LockTable,
    RunTable::Waiter,
    RunTable::Timer,
    RunTable::ValueBirth,
    RunTable::ValueHolder,
    RunTable::ValueGarbage,
];

const ABORTED: i32 = 10;
const FAILED_PRECONDITION: i32 = 9;
const INVALID_ARGUMENT: i32 = 3;
const RESOURCE_EXHAUSTED: i32 = 8;
const MINUTE: i64 = 60 * SECOND;

/// Offloaded values that a test removed from the local store.
#[derive(Default)]
struct RemovedValues {
    values: BTreeMap<ValueId, Value>,
}

impl OffloadedValues for RemovedValues {
    fn offloaded_value(
        &self,
        _run_id: RunId,
        value_id: ValueId,
    ) -> Result<Option<Value>, ApplyError> {
        Ok(self.values.get(&value_id).cloned())
    }
}

impl Harness {
    fn travel_command(&mut self, request: ResetFlowRequest) -> FlowCommand {
        self.command(Command::TimeTravel(TimeTravelCommand {
            request: Some(request),
            ..TimeTravelCommand::default()
        }))
    }

    /// Computes `travel` on the current state with `offloaded_values` and `budget_bytes`.
    fn compute_travel(
        &self,
        travel: &FlowCommand,
        offloaded_values: &dyn OffloadedValues,
        budget_bytes: u64,
    ) -> TimeTravelComputation {
        let Some(Command::TimeTravel(time_travel)) = &travel.command else {
            panic!("not a TimeTravel");
        };
        let run_id = self.harness_run_id();
        let history = StateHistorySource::new(&self.state, self.context, run_id);
        compute_time_travel(
            &self.context,
            &TimeTravelComputationSources {
                state: &self.state,
                history: &history,
                offloaded_values,
            },
            &TimeTravelComputationRequest {
                run_id,
                reset: time_travel.request.as_ref().expect("travel has a request"),
                now_micros: travel.proposed_at_micros,
                replay_memory_budget_bytes: budget_bytes,
            },
        )
        .expect("the computation reads valid state")
    }

    /// Applies the Stage commands of `computed` under `staging_id`, then the TimeTravel of
    /// `travel` that names them, and returns the TimeTravel's outcome.
    fn apply_staged(
        &mut self,
        travel: &FlowCommand,
        computed: &ComputedTimeTravel,
        staging_id: &[u8],
    ) -> ApplyOutcome {
        for stage in computed
            .stage_commands(staging_id)
            .expect("the result stages")
        {
            let mut command = self.command(Command::TimeTravelStage(stage));
            command.expected_run_attempt = computed.run_attempt();
            let outcome = self.apply_command(command);
            assert!(
                matches!(outcome.reply.reply, Some(command_reply::Reply::Ok(_))),
                "staging is accepted: {:?}",
                outcome.reply
            );
        }
        let Some(Command::TimeTravel(time_travel)) = &travel.command else {
            panic!("not a TimeTravel");
        };
        let mut command = self.command(Command::TimeTravel(TimeTravelCommand {
            staging_id: staging_id.to_vec(),
            computed_through_seq: computed.computed_through_seq(),
            ..time_travel.clone()
        }));
        command.expected_run_attempt = computed.run_attempt();
        self.apply_command(command)
    }

    /// Returns a harness over a copy of this state.
    fn twin(&self) -> Self {
        Self {
            state: self.state.clone(),
            context: self.context,
            now_micros: self.now_micros,
            flow_id: self.flow_id.clone(),
            run_id: self.run_id.clone(),
            is_storage_recount_checked: self.is_storage_recount_checked,
        }
    }

    /// Applies the request of `travel` with a TimeTravel proposed at `proposed_at_micros`, the
    /// way the harness stages it, and returns the restored rows.
    fn rows_after_travel_at(
        &mut self,
        proposed_at_micros: i64,
        travel: &FlowCommand,
    ) -> BTreeMap<(ColumnFamily, Vec<u8>), Vec<u8>> {
        self.now_micros = proposed_at_micros - 1000;
        let mut command = self.command(travel.command.clone().expect("travel has a body"));
        command.proposed_at_micros = proposed_at_micros;
        assert!(is_time_travel(&self.apply_command(command).reply));
        self.restored_rows()
    }

    fn apply_stage(&mut self, stage: TimeTravelStageCommand) -> ApplyOutcome {
        self.apply(Command::TimeTravelStage(stage))
    }

    fn staging_chunk_indexes(&self) -> Vec<u32> {
        let prefix = run_table_prefix(
            self.context.flow_hash,
            self.harness_run_id(),
            RunTable::TimeTravelStaging,
        );
        let (start, end) = prefix_range(&prefix);
        self.state
            .rows
            .range((ColumnFamily::Value, start)..(ColumnFamily::Value, end))
            .map(|((_, key), _)| {
                u32::from_be_bytes(key[prefix.len()..].try_into().expect("4-byte chunk index"))
            })
            .collect()
    }

    /// Rows of the restored tables and of the value table, by column family and key.
    fn restored_rows(&self) -> BTreeMap<(ColumnFamily, Vec<u8>), Vec<u8>> {
        let run_id = self.harness_run_id();
        self.state
            .rows
            .iter()
            .filter(|((column_family, key), _)| {
                let Ok(parsed) = parse_run_key(key) else {
                    return false;
                };
                parsed.run_id == run_id
                    && ((*column_family == ColumnFamily::Flow
                        && RESTORED_TABLES.contains(&parsed.table))
                        || (*column_family == ColumnFamily::Value
                            && parsed.table == RunTable::Value))
            })
            .map(|(key, value)| (key.clone(), value.clone()))
            .collect()
    }

    /// Removes every row of a superseded value from the local store and returns the value.
    fn remove_superseded_value(&mut self, value_id: ValueId) -> Value {
        let run_id = self.harness_run_id();
        let flow_hash = self.context.flow_hash;
        let garbage_seq = self.value_birth(value_id).garbage_seq;
        assert!(garbage_seq > 0, "only a superseded value leaves the store");
        let value_row = self
            .state
            .rows
            .remove(&(
                ColumnFamily::Value,
                value_row_key(flow_hash, run_id, RunTable::Value, value_id),
            ))
            .expect("the value row exists");
        for key in [
            value_row_key(flow_hash, run_id, RunTable::ValueBirth, value_id),
            value_garbage_key(flow_hash, run_id, garbage_seq, value_id),
        ] {
            self.state
                .rows
                .remove(&(ColumnFamily::Flow, key))
                .expect("the value's row exists");
        }
        self.restate_storage_totals();
        dexzd_protocol::dexzd::v1::ValueRecord::decode(value_row.as_slice())
            .expect("the value row decodes")
            .value
            .expect("the value row holds a value")
    }

    /// Removes the history record at `seq` from the local store.
    fn remove_history_record(&mut self, seq: u64) {
        let key = history_record_key(self.context.flow_hash, self.harness_run_id(), seq);
        self.state
            .rows
            .remove(&(ColumnFamily::History, key))
            .expect("the history record exists");
        self.restate_storage_totals();
    }

    /// Writes the recounted storage totals into the run record after a test removed rows
    /// behind apply's back, so later commands account from consistent totals.
    fn restate_storage_totals(&mut self) {
        let run_id = self.harness_run_id();
        let key = run_singleton_key(self.context.flow_hash, run_id, RunTable::RunRecord);
        loop {
            let mut record = self.run_record();
            let usage = self.recount_storage();
            if record.storage.as_ref() == Some(&usage) {
                return;
            }
            record.storage = Some(usage);
            self.state
                .rows
                .insert((ColumnFamily::Flow, key.clone()), record.encode_to_vec());
        }
    }

    fn history_timestamp(&self, seq: u64) -> i64 {
        let key = history_record_key(self.context.flow_hash, self.harness_run_id(), seq);
        let bytes = self
            .state
            .rows
            .get(&(ColumnFamily::History, key))
            .expect("the history record exists");
        HistoryRecord::decode(bytes.as_slice())
            .expect("the history record decodes")
            .timestamp_micros
    }
}

fn reset_to(reset_type: FlowResetType, is_reapplying: bool) -> ResetFlowRequest {
    ResetFlowRequest {
        flow_id: "order-1".to_owned(),
        reset_type: reset_type as i32,
        reason: "staging test".to_owned(),
        skip_writes_reapply: !is_reapplying,
        ..ResetFlowRequest::default()
    }
}

fn reset_to_time(timestamp_micros: i64) -> ResetFlowRequest {
    ResetFlowRequest {
        history_event_time: (i128::from(timestamp_micros) * 1000).to_string(),
        ..reset_to(FlowResetType::HistoryEventTime, false)
    }
}

fn computed(computation: TimeTravelComputation) -> ComputedTimeTravel {
    match computation {
        TimeTravelComputation::Computed(computed) => *computed,
        TimeTravelComputation::Rejected(reply) => panic!("the computation rejected: {reply:?}"),
    }
}

fn error_message(reply: &CommandReply) -> &str {
    match &reply.reply {
        Some(command_reply::Reply::Error(error)) => &error.message,
        other => panic!("expected an error reply, got {other:?}"),
    }
}

fn is_time_travel(reply: &CommandReply) -> bool {
    matches!(reply.reply, Some(command_reply::Reply::TimeTravel(_)))
}

fn large_text(fill: char, bytes: usize) -> Value {
    string_value(&fill.to_string().repeat(bytes))
}

/// Logs of older binaries hold TimeTravel commands without a staged result; apply computes
/// those with the owner's code, so both paths restore the same rows.
#[test]
fn an_unstaged_log_record_restores_what_its_staged_result_restores() {
    let mut harness = Harness::new("order-1");
    harness.start(start_request("start"));
    harness.set_attribute("note", string_value("before"), "set-1");
    harness.set_attribute("report", large_text('r', 300), "set-2");
    harness.set_attribute("report", large_text('s', 300), "set-3");
    let mut unstaged_state = harness.state.clone();
    let travel = harness.travel_command(reset_to(FlowResetType::Beginning, true));
    let unstaged = apply_flow_command(&harness.context, &unstaged_state, &travel)
        .expect("an unstaged TimeTravel applies");
    unstaged_state.commit(&unstaged);
    let staged = harness.apply_command(travel);
    assert!(is_time_travel(&unstaged.reply), "{:?}", unstaged.reply);
    assert_eq!(staged.reply, unstaged.reply);
    assert_eq!(staged.effects, unstaged.effects);
    let unstaged_rows = Harness {
        state: unstaged_state,
        context: harness.context,
        now_micros: harness.now_micros,
        flow_id: harness.flow_id.clone(),
        run_id: harness.run_id.clone(),
        is_storage_recount_checked: true,
    }
    .restored_rows();
    assert_eq!(harness.restored_rows(), unstaged_rows);
    assert!(harness.run_record().time_travel_staging.is_none());
    assert!(harness.staging_chunk_indexes().is_empty());
}

/// Input after the target larger than a log record is staged in chunks of one entry each.
#[test]
fn a_result_larger_than_a_log_record_is_staged_in_chunks() {
    let mut harness = Harness::new("order-1");
    harness.start(start_request("start"));
    for (index, fill) in ['a', 'b', 'c'].into_iter().enumerate() {
        harness.set_attribute(
            &format!("document-{index}"),
            large_text(fill, 12 << 20),
            &format!("set-{index}"),
        );
    }
    let travel = harness.travel_command(reset_to(FlowResetType::Beginning, true));
    let computed = computed(harness.compute_travel(&travel, &NoOffloadedValues, u64::MAX));
    let stage_commands = computed
        .stage_commands(b"large-staging")
        .expect("the result stages");
    let chunk_sizes: Vec<usize> = stage_commands
        .iter()
        .map(|stage| stage.chunk.len())
        .collect();
    assert_eq!(stage_commands.len(), 4, "the target, then one input each");
    assert!(
        chunk_sizes.iter().sum::<usize>() > 32 << 20,
        "{chunk_sizes:?}"
    );
    assert!(
        chunk_sizes
            .iter()
            .all(|size| *size < (32 << 20) - (64 << 10)),
        "{chunk_sizes:?}"
    );
    assert!(
        stage_commands
            .iter()
            .enumerate()
            .all(|(index, stage)| stage.is_last_chunk == (index == 3))
    );
    let outcome = harness.apply_staged(&travel, &computed, b"large-staging");
    assert!(is_time_travel(&outcome.reply), "{:?}", outcome.reply);
    for (index, _) in ['a', 'b', 'c'].into_iter().enumerate() {
        assert!(
            harness.attribute(&format!("document-{index}")).is_some(),
            "reapplied input {index} is restored"
        );
    }
    assert!(harness.staging_chunk_indexes().is_empty());
}

/// A value offloaded after the owner computed the result: apply names it, the owner computes
/// again, stages the value from object storage, and apply restores it.
#[test]
fn a_value_missing_from_the_staged_result_is_rejected_by_id_then_restored() {
    let mut harness = Harness::new("order-1");
    harness.start(start_request("start"));
    harness.set_attribute("report", large_text('o', 300), "set-old");
    let old_seq = harness.run_record().next_run_seq - 1;
    let old_value = table_value_id(
        harness
            .attribute("report")
            .expect("the report exists")
            .value
            .as_ref(),
    );
    harness.set_attribute("report", large_text('n', 300), "set-new");
    let mut twin = harness.twin();
    let travel = harness.travel_command(reset_to_time(harness.history_timestamp(old_seq)));
    let first = computed(harness.compute_travel(&travel, &NoOffloadedValues, u64::MAX));
    assert!(
        first.unstored_value_ids_for_test().is_empty(),
        "the value is local"
    );
    let mut removed = RemovedValues::default();
    removed
        .values
        .insert(old_value, harness.remove_superseded_value(old_value));
    let rejected = harness.apply_staged(&travel, &first, b"first-staging");
    assert!(is_error(&rejected.reply, ABORTED), "{:?}", rejected.reply);
    let old_reference = harness.value_reference(old_value.run_seq, old_value.index_in_command);
    assert!(
        error_message(&rejected.reply).contains(&old_reference),
        "{}",
        error_message(&rejected.reply)
    );
    assert!(rejected.writes.is_empty());
    // The replayed write of the value holds its content, so the next result stages it.
    let second = computed(harness.compute_travel(&travel, &NoOffloadedValues, u64::MAX));
    assert_eq!(second.unstored_value_ids_for_test(), vec![old_value]);
    let restored = harness.apply_staged(&travel, &second, b"second-staging");
    assert!(is_time_travel(&restored.reply), "{:?}", restored.reply);
    assert_eq!(harness.holder_count(old_value), 1);
    assert!(harness.garbage_seqs(old_value).is_empty());
    assert_eq!(
        harness.restored_rows(),
        twin.rows_after_travel_at(harness.now_micros, &travel)
    );
}

/// A value from before the replayed range that no source holds aborts the computation instead
/// of restoring a stand-in; with object storage holding it, the result stages it.
#[test]
fn a_value_no_source_holds_aborts_the_computation() {
    let mut harness = Harness::new("order-1");
    harness.start_with_settings(
        start_request("start"),
        RunSettings {
            checkpoint_command_threshold: 2,
            ..settings()
        },
    );
    harness.set_attribute("report", large_text('o', 300), "set-old");
    let old_value = table_value_id(
        harness
            .attribute("report")
            .expect("the report exists")
            .value
            .as_ref(),
    );
    harness.set_attribute("note", string_value("1"), "set-note-1");
    harness.set_attribute("note", string_value("2"), "set-note-2");
    let target_seq = harness.run_record().next_run_seq - 1;
    let checkpoint_seq = harness.run_record().last_checkpoint_seq;
    assert!(
        old_value.run_seq < checkpoint_seq && checkpoint_seq <= target_seq,
        "the value was born before the checkpoint the replay starts from"
    );
    harness.set_attribute("report", large_text('n', 300), "set-new");
    let mut twin = harness.twin();
    let mut removed = RemovedValues::default();
    removed
        .values
        .insert(old_value, harness.remove_superseded_value(old_value));
    let travel = harness.travel_command(reset_to_time(harness.history_timestamp(target_seq)));
    let TimeTravelComputation::Rejected(abort) =
        harness.compute_travel(&travel, &NoOffloadedValues, u64::MAX)
    else {
        panic!("a computation must not restore a stand-in value");
    };
    assert!(is_error(&abort, FAILED_PRECONDITION), "{abort:?}");
    let old_reference = harness.value_reference(old_value.run_seq, old_value.index_in_command);
    assert!(
        error_message(&abort).contains(&old_reference),
        "{}",
        error_message(&abort)
    );
    let staged = computed(harness.compute_travel(&travel, &removed, u64::MAX));
    assert_eq!(staged.unstored_value_ids_for_test(), vec![old_value]);
    let restored = harness.apply_staged(&travel, &staged, b"offloaded-staging");
    assert!(is_time_travel(&restored.reply), "{:?}", restored.reply);
    assert_eq!(
        harness.restored_rows(),
        twin.rows_after_travel_at(harness.now_micros, &travel)
    );
}

/// External input committed after the computation is reapplied from the local tail; input
/// that left the local store makes the owner compute again.
#[test]
fn input_recorded_after_the_computation_is_reapplied_from_the_local_tail() {
    let mut harness = Harness::new("order-1");
    harness.start(start_request("start"));
    harness.set_attribute("early", string_value("1"), "set-early");
    let travel = harness.travel_command(reset_to(FlowResetType::Beginning, true));
    let computed_result = computed(harness.compute_travel(&travel, &NoOffloadedValues, u64::MAX));
    harness.set_attribute("late", string_value("2"), "set-late");
    let outcome = harness.apply_staged(&travel, &computed_result, b"tail-staging");
    assert!(is_time_travel(&outcome.reply), "{:?}", outcome.reply);
    assert!(harness.attribute("early").is_some());
    assert!(
        harness.attribute("late").is_some(),
        "the later input is reapplied"
    );
    let fork_seq = harness.run_record().next_run_seq - 1;
    let key = history_record_key(
        harness.context.flow_hash,
        harness.harness_run_id(),
        fork_seq,
    );
    let fork = HistoryRecord::decode(harness.state.rows[&(ColumnFamily::History, key)].as_slice())
        .expect("the fork record decodes");
    assert_eq!(fork.reapplied_inputs.len(), 2, "both inputs are recorded");

    let mut offloaded = Harness::new("order-1");
    offloaded.start(start_request("start"));
    let travel = offloaded.travel_command(reset_to(FlowResetType::Beginning, true));
    let computed_result = computed(offloaded.compute_travel(&travel, &NoOffloadedValues, u64::MAX));
    offloaded.set_attribute("late", string_value("2"), "set-late");
    let late_seq = offloaded.run_record().next_run_seq - 1;
    offloaded.remove_history_record(late_seq);
    let rejected = offloaded.apply_staged(&travel, &computed_result, b"offloaded-tail");
    assert!(is_error(&rejected.reply, ABORTED), "{:?}", rejected.reply);
    assert!(
        error_message(&rejected.reply).contains(&format!("run seq {late_seq}")),
        "{}",
        error_message(&rejected.reply)
    );
}

/// A target the window left after the computation makes the owner compute again.
#[test]
fn a_target_that_left_the_window_is_rejected_as_out_of_date() {
    let mut harness = Harness::new("order-1");
    harness.start_with_settings(
        start_request("start"),
        RunSettings {
            history_window_events: 4,
            history_prune_chunk_events: 1,
            checkpoint_command_threshold: 2,
            ..settings()
        },
    );
    harness.set_attribute("value", string_value("0"), "set-0");
    let target_seq = harness.run_record().next_run_seq - 1;
    let travel = harness.travel_command(reset_to_time(harness.history_timestamp(target_seq)));
    let computed_result = computed(harness.compute_travel(&travel, &NoOffloadedValues, u64::MAX));
    for index in 1..12 {
        harness.set_attribute(
            "value",
            string_value(&index.to_string()),
            &format!("set-{index}"),
        );
    }
    assert!(harness.run_record().window_start_seq > target_seq);
    let rejected = harness.apply_staged(&travel, &computed_result, b"stale-staging");
    assert!(is_error(&rejected.reply, ABORTED), "{:?}", rejected.reply);
    assert!(error_message(&rejected.reply).contains("no longer a time travel target"));
}

/// The replay holds at most the configured bytes; a larger replay fails with an explicit error.
#[test]
fn a_replay_over_its_memory_budget_is_rejected() {
    let mut harness = Harness::new("order-1");
    harness.start(start_request("start"));
    harness.set_attribute("note", large_text('m', 4096), "set-1");
    let travel = harness.travel_command(reset_to_time(
        harness.history_timestamp(harness.run_record().next_run_seq - 1),
    ));
    let TimeTravelComputation::Rejected(reply) =
        harness.compute_travel(&travel, &NoOffloadedValues, 1024)
    else {
        panic!("a 1 KiB budget cannot replay the run");
    };
    assert!(is_error(&reply, RESOURCE_EXHAUSTED), "{reply:?}");
    assert!(error_message(&reply).contains("history.timeTravelReplayMemoryBytes"));
    assert!(matches!(
        harness.compute_travel(&travel, &NoOffloadedValues, 1 << 30),
        TimeTravelComputation::Computed(_)
    ));
}

fn stage_chunk(staging_id: &[u8], chunk_index: u32, is_last_chunk: bool) -> TimeTravelStageCommand {
    let chunk = TimeTravelStagedChunk {
        target: (chunk_index == 0).then(TimeTravelStagedTarget::default),
        ..TimeTravelStagedChunk::default()
    };
    TimeTravelStageCommand {
        staging_id: staging_id.to_vec(),
        chunk_index,
        is_last_chunk,
        chunk: chunk.encode_to_vec(),
    }
}

/// Chunks of one staging arrive in order; a new staging drops the old one; a TimeTravel needs
/// its complete staging.
#[test]
fn staging_chunks_follow_in_order_and_a_new_staging_replaces_the_old() {
    let mut harness = Harness::new("order-1");
    harness.start(start_request("start"));
    let next_seq = harness.run_record().next_run_seq;
    let accepted = harness.apply_stage(stage_chunk(b"first", 0, false));
    assert!(matches!(
        accepted.reply.reply,
        Some(command_reply::Reply::Ok(_))
    ));
    assert_eq!(
        harness.run_record().next_run_seq,
        next_seq,
        "staging takes no seq"
    );
    assert_eq!(harness.staging_chunk_indexes(), vec![0]);
    let out_of_order = harness.apply_stage(stage_chunk(b"first", 2, true));
    assert!(
        is_error(&out_of_order.reply, ABORTED),
        "{:?}",
        out_of_order.reply
    );
    let unknown = harness.apply_stage(stage_chunk(b"other", 1, true));
    assert!(is_error(&unknown.reply, ABORTED), "{:?}", unknown.reply);
    let mut missing_target = stage_chunk(b"first", 1, true);
    missing_target.chunk = TimeTravelStagedChunk {
        target: Some(TimeTravelStagedTarget::default()),
        ..TimeTravelStagedChunk::default()
    }
    .encode_to_vec();
    assert!(is_error(
        &harness.apply_stage(missing_target).reply,
        INVALID_ARGUMENT
    ));
    let mut undecodable = stage_chunk(b"first", 1, true);
    undecodable.chunk = vec![0xff, 0xff, 0xff];
    assert!(is_error(
        &harness.apply_stage(undecodable).reply,
        INVALID_ARGUMENT
    ));
    let travel = Command::TimeTravel(TimeTravelCommand {
        request: Some(reset_to(FlowResetType::Beginning, false)),
        staging_id: b"first".to_vec(),
        computed_through_seq: next_seq - 1,
        ..TimeTravelCommand::default()
    });
    let incomplete = harness.apply(travel);
    assert!(
        is_error(&incomplete.reply, ABORTED),
        "{:?}",
        incomplete.reply
    );
    assert!(error_message(&incomplete.reply).contains("is not complete"));
    assert!(
        harness
            .apply_stage(stage_chunk(b"first", 1, false))
            .writes
            .len()
            > 1
    );
    assert_eq!(harness.staging_chunk_indexes(), vec![0, 1]);
    let replacing = harness.apply_stage(stage_chunk(b"second", 0, true));
    assert!(matches!(
        replacing.reply.reply,
        Some(command_reply::Reply::Ok(_))
    ));
    assert_eq!(harness.staging_chunk_indexes(), vec![0]);
    let staging = harness
        .run_record()
        .time_travel_staging
        .expect("the run has a staging");
    assert_eq!(staging.staging_id, b"second".to_vec());
    assert!(staging.is_complete);
    assert_eq!(staging.chunk_count, 1);
    let after_complete = harness.apply_stage(stage_chunk(b"second", 1, true));
    assert!(is_error(&after_complete.reply, ABORTED));
}

/// A staging no chunk extended for 10 minutes of command time goes with the run's next
/// history-recording command; a younger one stays.
#[test]
fn an_abandoned_staging_is_deleted_after_ten_idle_minutes() {
    let mut harness = Harness::new("order-1");
    harness.start(start_request("start"));
    harness.apply_stage(stage_chunk(b"abandoned", 0, false));
    harness.now_micros += 9 * MINUTE;
    harness.set_attribute("note", string_value("9 minutes"), "set-9");
    assert_eq!(harness.staging_chunk_indexes(), vec![0]);
    assert!(harness.run_record().time_travel_staging.is_some());
    harness.now_micros += MINUTE;
    harness.set_attribute("note", string_value("10 minutes"), "set-10");
    assert!(harness.staging_chunk_indexes().is_empty());
    assert!(harness.run_record().time_travel_staging.is_none());
}

/// Run purge deletes every key of the run, the staging rows in the value column family too.
#[test]
fn run_purge_deletes_the_staging() {
    let mut harness = Harness::new("order-1");
    harness.start(start_request("start"));
    harness.apply_stage(stage_chunk(b"abandoned", 0, false));
    harness.apply(Command::FlowStop(FlowStopCommand {
        request: Some(StopFlowRequest {
            flow_id: "order-1".to_owned(),
            stop_type: StopType::Cancel as i32,
            ..StopFlowRequest::default()
        }),
    }));
    harness.apply(Command::TimersFired(TimersFiredCommand {
        timers: vec![TimerId {
            run_attempt: 0,
            kind: Some(timer_id::Kind::Retention(RetentionTimerId {})),
        }],
    }));
    assert_eq!(harness.staging_chunk_indexes(), vec![0]);
    let purge = harness.command(Command::RunPurge(RunPurgeCommand {}));
    let purged =
        apply_flow_command(&harness.context, &harness.state, &purge).expect("purge applies");
    harness.state.commit(&purged);
    assert!(harness.staging_chunk_indexes().is_empty());
    let staging_key =
        time_travel_staging_key(harness.context.flow_hash, harness.harness_run_id(), 0);
    assert!(
        !harness
            .state
            .rows
            .contains_key(&(ColumnFamily::Value, staging_key))
    );
}
