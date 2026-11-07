// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Randomized checks that TimeTravel target resolution from History record metadata selects
//! exactly what resolution from the records selects, and that history pages read through a
//! non-local history source match local reads.
//!
//! Set `DEXZD_TARGET_SEEDS` and `DEXZD_TARGET_COMMANDS` for a longer budget.

use std::collections::{BTreeMap, BTreeSet};
use std::ops::RangeInclusive;

use dexzd_apply::diagnostics::{
    TargetResolutionForTest, TimeTravelTargetForTest, resolve_time_travel_target_for_test,
};
use dexzd_apply::read_model::{get_history_events, get_history_events_from_source};
use dexzd_apply::{
    ApplyContext, ApplyError, BoundedRowSeek, FlowStateReader, HistoryRecords, HistorySource,
    StateReadError, StateRow, StateWrite,
};
use dexzd_keyspace::{ColumnFamily, RunId, RunTable, run_table_prefix};
use dexzd_protocol::dex::{FlowResetStepMethod, FlowResetType, ResetFlowRequest};
use dexzd_protocol::dexzd::v1::{
    CheckpointRecord, CommandReply, HistoryRecord, HistoryRowMetadata, RunRecord,
};
use dexzd_simulation::{
    ATTRIBUTE_SYNC_FLOW_ID, ApplyChecker, CommandGenerator, FLOW_IDS, FlowView, MemoryFlowState,
    RunSelection, RunView, STEP_FAILURE_FLOW_IDS,
};
use prost::Message;

const DEFAULT_SEED_COUNT: u64 = 16;
const DEFAULT_COMMANDS_PER_SEED: usize = 250;
/// First seed of the fixed set; any constant keeps the set reproducible.
const FIRST_SEED: u64 = 0xb4_7a29_0000;
/// Most request values of one kind drawn from a run's retained records.
const MAX_VALUES_PER_KIND: usize = 6;
/// Page sizes compared between local and non-local history reads; zero is the default size.
const COMPARED_PAGE_SIZES: [i32; 2] = [3, 0];
/// Response byte limits compared: none, and one small enough to end pages early or reject.
const COMPARED_RESPONSE_BYTE_LIMITS: [usize; 2] = [usize::MAX, 4096];
const ONE_DAY_NANOS: i128 = 86_400 * 1_000_000_000;

type TargetResult = Result<TimeTravelTargetForTest, CommandReply>;

#[test]
fn metadata_and_record_target_resolution_agree_over_random_histories() {
    let seed_count = environment_number("DEXZD_TARGET_SEEDS").unwrap_or(DEFAULT_SEED_COUNT);
    let command_count = environment_number("DEXZD_TARGET_COMMANDS")
        .map_or(DEFAULT_COMMANDS_PER_SEED, |count| count as usize);
    let mut coverage = Coverage::default();
    for seed in FIRST_SEED..FIRST_SEED + seed_count {
        run_seed(seed, command_count, &mut coverage);
    }
    println!("time travel target coverage: {coverage:#?}");
    coverage.assert_exercised();
}

fn environment_number(name: &str) -> Option<u64> {
    let text = std::env::var(name).ok()?;
    Some(
        text.parse()
            .unwrap_or_else(|error| panic!("{name}={text:?} is not a number: {error}")),
    )
}

/// Applies the seed's commands, comparing every resolution after each one, then checks the
/// fallback for older metadata on the final state.
fn run_seed(seed: u64, command_count: usize, coverage: &mut Coverage) {
    let mut checker = ApplyChecker::new();
    let mut checked_runs = BTreeMap::new();
    let mut generator = CommandGenerator::new(seed);
    let mut side_streams = [
        CommandGenerator::attribute_sync_stream(seed),
        CommandGenerator::step_failure_stream(seed),
    ];
    for command_index in 0..command_count {
        let mut commands = vec![
            generator
                .next_command(&checker)
                .unwrap_or_else(|violation| {
                    panic!("seed {seed} command {command_index}: {violation}")
                }),
        ];
        for side_stream in &mut side_streams {
            if side_stream.should_propose() {
                side_stream.follow_clock(&generator);
                commands.push(
                    side_stream
                        .next_command(&checker)
                        .unwrap_or_else(|violation| {
                            panic!("seed {seed} command {command_index}: {violation}")
                        }),
                );
            }
        }
        for command in commands {
            checker
                .apply_and_check(&command)
                .unwrap_or_else(|violation| {
                    panic!("seed {seed} command {command_index}: {violation}")
                });
            let site = format!("seed {seed} command {command_index}");
            let view = checker
                .view(&command.flow_id, RunSelection::All)
                .unwrap_or_else(|violation| panic!("{site}: {violation}"));
            let changed_runs = changed_runs(&view, &mut checked_runs);
            let flow = CheckedFlow {
                checker: &checker,
                flow_id: &command.flow_id,
                view: &view,
                changed_runs: &changed_runs,
                site: &site,
            };
            flow.check_targets(command.proposed_at_micros, coverage);
            flow.check_history_pages(coverage);
        }
    }
    let now_micros = generator_now_micros(&checker);
    for flow_id in all_flow_ids() {
        let site = format!("seed {seed} final state");
        check_older_metadata_fallback(&checker, flow_id, now_micros, &site, coverage);
    }
}

fn all_flow_ids() -> impl Iterator<Item = &'static str> {
    FLOW_IDS
        .iter()
        .chain(STEP_FAILURE_FLOW_IDS)
        .copied()
        .chain(std::iter::once(ATTRIBUTE_SYNC_FLOW_ID))
}

/// A fixed resolution time after every recorded command; relative times resolve against it.
fn generator_now_micros(checker: &ApplyChecker) -> i64 {
    all_flow_ids()
        .filter_map(|flow_id| checker.view(flow_id, RunSelection::All).ok())
        .flat_map(|view| view.runs.into_values())
        .flat_map(|run| {
            run.history
                .into_iter()
                .map(|(_, record)| record.timestamp_micros)
        })
        .max()
        .unwrap_or(0)
        .saturating_add(1_000_000)
}

/// What decides whether a run's resolutions can differ from its last check.
type RunVersion = (u64, u32, u64, bool);

/// Returns the runs whose history or window changed since `checked_runs` last saw them.
fn changed_runs(
    view: &FlowView,
    checked_runs: &mut BTreeMap<RunId, RunVersion>,
) -> BTreeSet<RunId> {
    let mut changed = BTreeSet::new();
    for (run_id, run) in &view.runs {
        let Some(record) = &run.record else {
            continue;
        };
        let version = (
            record.next_run_seq,
            record.current_attempt,
            record.window_start_seq,
            record.is_purging,
        );
        if checked_runs.insert(*run_id, version) != Some(version) {
            changed.insert(*run_id);
        }
    }
    changed
}

/// One paged GetHistoryEvents read.
#[derive(Debug)]
struct PageQuery {
    run_id: RunId,
    run_attempt: i32,
    page_size: i32,
    max_response_bytes: usize,
}

/// The runs of one Flow to check after a command.
struct CheckedFlow<'check> {
    checker: &'check ApplyChecker,
    flow_id: &'check str,
    view: &'check FlowView,
    changed_runs: &'check BTreeSet<RunId>,
    site: &'check str,
}

impl CheckedFlow<'_> {
    /// Requires every resolution of every request on every attempt of each changed run to match
    /// the record oracle.
    fn check_targets(&self, now_micros: i64, coverage: &mut Coverage) {
        let site = self.site;
        let context = self.checker.context(self.flow_id);
        let state = self.checker.state();
        for (run_id, run, record) in self.changed() {
            for attempt in 1..=record.current_attempt {
                for request in target_requests(run, attempt) {
                    let oracle = resolve(
                        &context,
                        state,
                        run_id,
                        now_micros,
                        &request,
                        TargetResolutionForTest::RecordOracle,
                        site,
                    );
                    for resolution in [
                        TargetResolutionForTest::Apply,
                        TargetResolutionForTest::ApplyWithoutHistoryReads,
                        TargetResolutionForTest::Metadata,
                        TargetResolutionForTest::Records,
                    ] {
                        let resolved = resolve(
                            &context, state, run_id, now_micros, &request, resolution, site,
                        );
                        assert_eq!(
                            resolved, oracle,
                            "{site}: {resolution:?} differs from the record oracle on run \
                             {run_id} for {request:?}"
                        );
                    }
                    coverage.record_target(run, attempt, &request, &oracle);
                }
            }
        }
    }

    /// Requires every history page of each changed run read through an in-memory history source
    /// to match local reads.
    fn check_history_pages(&self, coverage: &mut Coverage) {
        let site = self.site;
        let context = self.checker.context(self.flow_id);
        let state = self.checker.state();
        for (run_id, run, record) in self.changed() {
            // Reads of a purging run answer NotFound before reading history.
            if record.is_purging {
                continue;
            }
            let history = MemoryHistorySource::of_run(state, &context, run_id, run);
            // The sourced read must take every History row from `history`, never from the store.
            let state_without_history = WithoutHistoryColumn { state };
            let local_without_history = get_history_events(
                &state_without_history,
                context.flow_hash,
                run_id,
                0,
                0,
                &[],
                0,
                usize::MAX,
            );
            assert!(
                local_without_history.is_err(),
                "{site}: a local read of run {run_id} passed without History rows"
            );
            let attempts = [
                0,
                1,
                i32::try_from(record.current_attempt).unwrap_or(i32::MAX),
            ];
            for run_attempt in attempts {
                for page_size in COMPARED_PAGE_SIZES {
                    for max_response_bytes in COMPARED_RESPONSE_BYTE_LIMITS {
                        let page = PageQuery {
                            run_id,
                            run_attempt,
                            page_size,
                            max_response_bytes,
                        };
                        self.compare_pages(&page, &history, &state_without_history, coverage);
                    }
                }
            }
        }
    }

    /// Pages through `page` locally and through `history`, requiring identical pages or errors.
    fn compare_pages(
        &self,
        page: &PageQuery,
        history: &MemoryHistorySource,
        state_without_history: &WithoutHistoryColumn<'_>,
        coverage: &mut Coverage,
    ) {
        let site = self.site;
        let context = self.checker.context(self.flow_id);
        let mut token = Vec::new();
        loop {
            let local = get_history_events(
                self.checker.state(),
                context.flow_hash,
                page.run_id,
                0,
                page.page_size,
                &token,
                page.run_attempt,
                page.max_response_bytes,
            );
            let from_source = get_history_events_from_source(
                state_without_history,
                history,
                context.flow_hash,
                page.run_id,
                0,
                page.page_size,
                &token,
                page.run_attempt,
                page.max_response_bytes,
            );
            coverage.compared_history_pages += 1;
            let local = match (local, from_source) {
                (Ok(local), Ok(from_source)) => {
                    assert_eq!(
                        from_source, local,
                        "{site}: {page:?} differs when read through a history source"
                    );
                    let requested_events = if page.page_size == 0 {
                        100
                    } else {
                        usize::try_from(page.page_size).unwrap_or(usize::MAX)
                    };
                    if !local.next_page_token.is_empty() && local.events.len() < requested_events {
                        coverage.pages_ended_by_response_bytes += 1;
                    }
                    local
                }
                (Err(local), Err(from_source)) => {
                    assert_eq!(
                        format!("{from_source:?}"),
                        format!("{local:?}"),
                        "{site}: {page:?} fails differently through a history source"
                    );
                    coverage.compared_history_page_errors += 1;
                    return;
                }
                (local, from_source) => {
                    panic!("{site}: {page:?} read {local:?} locally but {from_source:?} sourced")
                }
            };
            if local.next_page_token.is_empty() {
                return;
            }
            token = local.next_page_token;
        }
    }

    /// The changed runs that still have a run record.
    fn changed(&self) -> impl Iterator<Item = (RunId, &RunView, &RunRecord)> {
        self.view
            .runs
            .iter()
            .filter(|(run_id, _)| self.changed_runs.contains(*run_id))
            .filter_map(|(run_id, run)| Some((*run_id, run, run.record.as_ref()?)))
    }
}

/// Requires the fallback for runs whose metadata predates the target fields.
///
/// Strips the fields from every record row, then from the genesis row only.
fn check_older_metadata_fallback(
    checker: &ApplyChecker,
    flow_id: &str,
    now_micros: i64,
    site: &str,
    coverage: &mut Coverage,
) {
    let context = checker.context(flow_id);
    let view = checker
        .view(flow_id, RunSelection::All)
        .unwrap_or_else(|violation| panic!("{site}: {violation}"));
    for (run_id, run) in &view.runs {
        let (Some(record), Some(first_attempt)) = (&run.record, run.attempts.get(&1)) else {
            continue;
        };
        let genesis = first_attempt.start_seq;
        let older_state = strip_target_fields(checker.state(), &context, *run_id, |_| true);
        let older_genesis_state =
            strip_target_fields(checker.state(), &context, *run_id, |seq| seq == genesis);
        coverage.older_metadata_runs += 1;
        for attempt in 1..=record.current_attempt {
            for request in target_requests(run, attempt) {
                let oracle = resolve(
                    &context,
                    checker.state(),
                    *run_id,
                    now_micros,
                    &request,
                    TargetResolutionForTest::RecordOracle,
                    site,
                );
                for state in [&older_state, &older_genesis_state] {
                    let resolved = resolve(
                        &context,
                        state,
                        *run_id,
                        now_micros,
                        &request,
                        TargetResolutionForTest::Apply,
                        site,
                    );
                    assert_eq!(
                        resolved, oracle,
                        "{site}: older metadata changed the target of run {run_id} for {request:?}"
                    );
                }
                let stripped_metadata = resolve(
                    &context,
                    &older_state,
                    *run_id,
                    now_micros,
                    &request,
                    TargetResolutionForTest::Metadata,
                    site,
                );
                if stripped_metadata != oracle {
                    coverage.stripped_metadata_differences += 1;
                }
                if oracle.is_ok() {
                    // The fallback must read records, so a source that fails every read fails it.
                    let without_records = resolve_time_travel_target_for_test(
                        &context,
                        &older_genesis_state,
                        *run_id,
                        now_micros,
                        &request,
                        TargetResolutionForTest::ApplyWithoutHistoryReads,
                    );
                    assert!(
                        without_records.is_err(),
                        "{site}: run {run_id} with an older genesis row resolved {request:?} \
                         without reading records: {without_records:?}"
                    );
                    coverage.fallback_record_reads += 1;
                }
            }
        }
    }
}

fn resolve(
    context: &ApplyContext,
    state: &MemoryFlowState,
    run_id: RunId,
    now_micros: i64,
    request: &ResetFlowRequest,
    resolution: TargetResolutionForTest,
    site: &str,
) -> TargetResult {
    resolve_time_travel_target_for_test(context, state, run_id, now_micros, request, resolution)
        .unwrap_or_else(|error| {
            panic!("{site}: {resolution:?} failed on run {run_id} for {request:?}: {error}")
        })
}

/// Requests of every target type, with values drawn from the run's retained records plus misses.
fn target_requests(run: &RunView, attempt: u32) -> Vec<ResetFlowRequest> {
    let base = ResetFlowRequest {
        run_attempt: i32::try_from(attempt).unwrap_or(i32::MAX),
        reason: "compare target resolutions".to_owned(),
        ..ResetFlowRequest::default()
    };
    let of_type = |reset_type: FlowResetType| ResetFlowRequest {
        reset_type: reset_type as i32,
        ..base.clone()
    };
    let at_time = |history_event_time: String| ResetFlowRequest {
        history_event_time,
        ..of_type(FlowResetType::HistoryEventTime)
    };
    let of_step_type = |step_type: &str| ResetFlowRequest {
        step_type: step_type.to_owned(),
        ..of_type(FlowResetType::StepType)
    };
    let of_step_execution =
        |step_execution_id: &str, method: FlowResetStepMethod| ResetFlowRequest {
            step_execution_id: step_execution_id.to_owned(),
            step_method: method as i32,
            ..of_type(FlowResetType::StepExecutionId)
        };
    let semantic_records = || {
        run.history
            .iter()
            .map(|(_, record)| record)
            .filter(|record| record.is_semantic)
    };
    let timestamps: BTreeSet<i64> = semantic_records()
        .map(|record| record.timestamp_micros)
        .collect();
    let pending = || {
        run.history
            .iter()
            .flat_map(|(_, record)| &record.pending_step_methods)
    };
    let step_types: BTreeSet<&str> = pending().map(|method| method.step_type.as_str()).collect();
    let step_execution_ids: BTreeSet<&str> = pending()
        .map(|method| method.step_execution_id.as_str())
        .collect();

    let mut requests = vec![
        of_type(FlowResetType::Beginning),
        of_type(FlowResetType::Unspecified),
        at_time("not a time".to_owned()),
        at_time("0".to_owned()),
        at_time("1s".to_owned()),
        of_step_type("MissingStep"),
        of_step_execution("MissingStep-1", FlowResetStepMethod::Execute),
        of_step_execution("MissingStep-1", FlowResetStepMethod::Unspecified),
    ];
    if let Some(last) = timestamps.last() {
        requests.push(at_time(
            (i128::from(*last) * 1000 + ONE_DAY_NANOS).to_string(),
        ));
    }
    for timestamp in spread(&timestamps) {
        let nanos = i128::from(*timestamp) * 1000;
        for offset in [-1000, 0, 1] {
            requests.push(at_time((nanos + offset).to_string()));
        }
    }
    for step_type in spread(&step_types) {
        requests.push(of_step_type(step_type));
    }
    for step_execution_id in spread(&step_execution_ids) {
        for method in [FlowResetStepMethod::WaitFor, FlowResetStepMethod::Execute] {
            requests.push(of_step_execution(step_execution_id, method));
        }
    }
    requests
}

/// Up to [`MAX_VALUES_PER_KIND`] values spread evenly over the ordered set, including its ends.
fn spread<Value>(values: &BTreeSet<Value>) -> Vec<&Value> {
    let ordered: Vec<&Value> = values.iter().collect();
    if ordered.len() <= MAX_VALUES_PER_KIND {
        return ordered;
    }
    let last = ordered.len() - 1;
    (0..MAX_VALUES_PER_KIND)
        .map(|index| ordered[index * last / (MAX_VALUES_PER_KIND - 1)])
        .collect()
}

/// Returns a copy of `state` whose History record metadata rows chosen by `should_strip` lack
/// the target fields, as rows written before them do.
fn strip_target_fields(
    state: &MemoryFlowState,
    context: &ApplyContext,
    run_id: RunId,
    should_strip: impl Fn(u64) -> bool,
) -> MemoryFlowState {
    let prefix = run_table_prefix(context.flow_hash, run_id, RunTable::HistoryRowMetadata);
    let writes: Vec<StateWrite> = state
        .rows_with_prefix(ColumnFamily::Flow, &prefix)
        .filter_map(|(key, value)| {
            let suffix = &key[prefix.len()..];
            if suffix.get(8) != Some(&(RunTable::HistoryRecord as u8)) {
                return None;
            }
            let seq = u64::from_be_bytes(suffix[..8].try_into().expect("an eight-byte seq"));
            if !should_strip(seq) {
                return None;
            }
            let mut metadata = HistoryRowMetadata::decode(value).expect("history metadata decodes");
            metadata.pending_step_methods.clear();
            metadata.max_event_id = 0;
            metadata.flow_closed_message = None;
            Some(StateWrite::Put {
                column_family: ColumnFamily::Flow,
                key: key.to_vec(),
                value: metadata.encode_to_vec(),
            })
        })
        .collect();
    let mut stripped = state.clone();
    stripped.commit(&writes);
    stripped
}

/// A reader of `state` that fails every read of the History column family.
struct WithoutHistoryColumn<'state> {
    state: &'state MemoryFlowState,
}

impl WithoutHistoryColumn<'_> {
    fn reject_history(column_family: ColumnFamily) -> Result<(), StateReadError> {
        if column_family == ColumnFamily::History {
            return Err(StateReadError {
                column_family,
                detail: "the sourced read accessed local History rows".to_owned(),
            });
        }
        Ok(())
    }
}

impl FlowStateReader for WithoutHistoryColumn<'_> {
    fn get(
        &self,
        column_family: ColumnFamily,
        key: &[u8],
    ) -> Result<Option<Vec<u8>>, StateReadError> {
        Self::reject_history(column_family)?;
        self.state.get(column_family, key)
    }

    fn scan(
        &self,
        column_family: ColumnFamily,
        start: &[u8],
        end: &[u8],
        limit: usize,
    ) -> Result<Vec<StateRow>, StateReadError> {
        Self::reject_history(column_family)?;
        self.state.scan(column_family, start, end, limit)
    }

    fn seek_first_bounded(
        &self,
        column_family: ColumnFamily,
        start: &[u8],
        end: &[u8],
        max_skipped_deletions: u64,
    ) -> Result<BoundedRowSeek, StateReadError> {
        Self::reject_history(column_family)?;
        self.state
            .seek_first_bounded(column_family, start, end, max_skipped_deletions)
    }
}

/// A history source holding a run's records and checkpoints in memory, as a source built from
/// downloaded history would.
struct MemoryHistorySource {
    records: Vec<HistoryRecord>,
    checkpoints: BTreeMap<(u32, u64), CheckpointRecord>,
}

impl MemoryHistorySource {
    fn of_run(
        state: &MemoryFlowState,
        context: &ApplyContext,
        run_id: RunId,
        run: &RunView,
    ) -> Self {
        let prefix = run_table_prefix(context.flow_hash, run_id, RunTable::Checkpoint);
        let checkpoints = state
            .rows_with_prefix(ColumnFamily::History, &prefix)
            .map(|(_, value)| {
                let checkpoint = CheckpointRecord::decode(value).expect("a checkpoint decodes");
                ((checkpoint.attempt, checkpoint.seq), checkpoint)
            })
            .collect();
        Self {
            records: run
                .history
                .iter()
                .map(|(_, record)| record.clone())
                .collect(),
            checkpoints,
        }
    }
}

impl HistorySource for MemoryHistorySource {
    fn history_records(
        &self,
        seqs: RangeInclusive<u64>,
        _batch_records: usize,
    ) -> HistoryRecords<'_> {
        Box::new(
            self.records
                .iter()
                .filter(move |record| seqs.contains(&record.seq))
                .cloned()
                .map(Ok),
        )
    }

    fn checkpoint(&self, attempt: u32, seq: u64) -> Result<Option<CheckpointRecord>, ApplyError> {
        Ok(self.checkpoints.get(&(attempt, seq)).cloned())
    }
}

/// What the compared requests exercised; each count must be positive for a meaningful run.
#[derive(Debug, Default)]
struct Coverage {
    /// Resolved targets and rejections by target type.
    outcomes: BTreeMap<(&'static str, &'static str), usize>,
    genesis_targets_outside_window: usize,
    targets_with_close_message: usize,
    targets_in_ancestor_attempts: usize,
    targets_after_two_forks: usize,
    pruned_window_resolutions: usize,
    older_metadata_runs: usize,
    stripped_metadata_differences: usize,
    fallback_record_reads: usize,
    compared_history_pages: usize,
    compared_history_page_errors: usize,
    pages_ended_by_response_bytes: usize,
}

impl Coverage {
    fn record_target(
        &mut self,
        run: &RunView,
        attempt: u32,
        request: &ResetFlowRequest,
        result: &TargetResult,
    ) {
        let reset_type =
            FlowResetType::try_from(request.reset_type).unwrap_or(FlowResetType::Unspecified);
        let outcome = if result.is_ok() { "target" } else { "rejected" };
        *self
            .outcomes
            .entry((reset_type.as_str_name(), outcome))
            .or_default() += 1;
        let Some(record) = &run.record else {
            return;
        };
        let genesis = run.attempts.get(&1).map_or(0, |first| first.start_seq);
        if record.window_start_seq > genesis + 1 {
            self.pruned_window_resolutions += 1;
        }
        let Ok(target) = result else {
            return;
        };
        if reset_type == FlowResetType::Beginning && genesis < record.window_start_seq {
            self.genesis_targets_outside_window += 1;
        }
        if target.close_history_message.is_some() {
            self.targets_with_close_message += 1;
        }
        if target.run_attempt != attempt {
            self.targets_in_ancestor_attempts += 1;
        }
        if lineage_length(run, attempt) >= 3 {
            self.targets_after_two_forks += 1;
        }
    }

    fn assert_exercised(&self) {
        // The genesis point is never pruned, so a BEGINNING request always finds a target.
        let required_outcomes = [
            (FlowResetType::Beginning, &["target"][..]),
            (FlowResetType::HistoryEventTime, &["target", "rejected"][..]),
            (FlowResetType::StepType, &["target", "rejected"][..]),
            (FlowResetType::StepExecutionId, &["target", "rejected"][..]),
        ];
        for (reset_type, outcomes) in required_outcomes {
            for outcome in outcomes.iter().copied() {
                assert!(
                    self.outcomes
                        .get(&(reset_type.as_str_name(), outcome))
                        .copied()
                        .unwrap_or(0)
                        > 0,
                    "no {} request was {outcome}: {self:#?}",
                    reset_type.as_str_name()
                );
            }
        }
        assert!(
            self.outcomes
                .contains_key(&(FlowResetType::Unspecified.as_str_name(), "rejected")),
            "no request without a type was compared: {self:#?}"
        );
        for (count, what) in [
            (
                self.genesis_targets_outside_window,
                "genesis target outside the window",
            ),
            (
                self.targets_with_close_message,
                "target with a FlowClosed message",
            ),
            (
                self.targets_in_ancestor_attempts,
                "target in an ancestor attempt",
            ),
            (
                self.targets_after_two_forks,
                "target in a lineage of three attempts",
            ),
            (
                self.pruned_window_resolutions,
                "resolution after window pruning",
            ),
            (self.older_metadata_runs, "run with older metadata"),
            (
                self.stripped_metadata_differences,
                "request that stripped metadata would misresolve",
            ),
            (self.fallback_record_reads, "fallback that read records"),
            (
                self.compared_history_pages,
                "history page read through a source",
            ),
            (
                self.pages_ended_by_response_bytes,
                "history page ended by the response byte limit",
            ),
        ] {
            assert!(count > 0, "no {what} was exercised: {self:#?}");
        }
    }
}

/// Number of attempts in the lineage of `attempt`.
fn lineage_length(run: &RunView, attempt: u32) -> usize {
    let mut length = 0;
    let mut current = attempt;
    while current != 0 && length <= run.attempts.len() {
        length += 1;
        current = run
            .attempts
            .get(&current)
            .map_or(0, |record| record.parent_attempt);
    }
    length
}
