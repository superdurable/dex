// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::{BTreeMap, BTreeSet};
use std::fmt;
use std::time::{Duration, Instant};

use dexzd_protocol::dexzd::v1::{
    FlowCommand, HistoryLocalCleanupCommand, SetHistoryOffloadModeCommand, command_reply,
    flow_command::Command,
};
use prost::Message;
use xxhash_rust::xxh3::xxh3_128;

use crate::apply_checker::ApplyChecker;
use crate::invariants::{
    CloseBarrierCounts, HistoryMigrationCounts, InvariantViolation, LocalOffloadCounts,
};
use crate::model::{ClosedErasePhase, CommandGenerator, HISTORY_OFFLOAD_FLOW_IDS};
use crate::state_view::RunSelection;

/// Replays allowed while shrinking one failure.
const SHRINK_REPLAY_BUDGET: usize = 200;
/// Most HistoryLocalCleanup commands the end of a seed applies before it gives up.
const MAX_DRAIN_CLEANUPS: usize = 10_000;
const SHRINK_TIME_BUDGET: Duration = Duration::from_secs(30);

/// What one seed exercised.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct SimulationSummary {
    /// Commands proposed.
    pub commands: usize,
    /// Commands that changed state, by command kind.
    pub applied_by_kind: BTreeMap<&'static str, usize>,
    /// Commands rejected without writes, by command kind.
    pub rejected_by_kind: BTreeMap<&'static str, usize>,
    /// Invalid client inputs rejected for their value size, by invalid input class.
    pub size_rejections: BTreeMap<&'static str, usize>,
    /// Rows in the final state.
    pub final_row_count: usize,
    /// Chained xxh3-128 digest of one seed's main-stream commands; `merge` leaves it unchanged.
    pub main_command_digest: u128,
    /// Closed-run erases accepted by the separate seeded stream.
    pub closed_run_erases: usize,
    /// Time-travel rebuilds that replayed a closed-run erase.
    pub closed_run_erase_replays: usize,
    /// Applied Attribute batches carrying permission mappings in the Attribute Sync stream.
    pub permission_mapping_commands: usize,
    /// Natural closes deferred for the Attribute Sync outbox, and pending closes finalized.
    pub close_barrier: CloseBarrierCounts,
    /// History segments sealed by applied commands.
    pub history_segments_sealed: usize,
    /// TimeTravel commands applied to a run with segment state while offload was off.
    pub history_offload_off_time_travels: usize,
    /// History migrations of runs that predate their segment state.
    pub history_migrations: HistoryMigrationCounts,
    /// Local history that offload deleted, and time travels computed from uploaded history.
    pub local_offload: LocalOffloadCounts,
}

impl SimulationSummary {
    /// Applied commands of `kind`.
    pub fn applied(&self, kind: &str) -> usize {
        self.applied_by_kind.get(kind).copied().unwrap_or(0)
    }

    /// Adds another seed's counts to this summary.
    pub fn merge(&mut self, other: &Self) {
        self.commands += other.commands;
        self.final_row_count += other.final_row_count;
        self.closed_run_erases += other.closed_run_erases;
        self.closed_run_erase_replays += other.closed_run_erase_replays;
        self.permission_mapping_commands += other.permission_mapping_commands;
        self.history_segments_sealed += other.history_segments_sealed;
        self.history_offload_off_time_travels += other.history_offload_off_time_travels;
        self.history_migrations.merge(&other.history_migrations);
        self.local_offload.merge(&other.local_offload);
        for (kind, count) in &other.applied_by_kind {
            *self.applied_by_kind.entry(kind).or_default() += count;
        }
        for (kind, count) in &other.rejected_by_kind {
            *self.rejected_by_kind.entry(kind).or_default() += count;
        }
        for (class, count) in &other.size_rejections {
            *self.size_rejections.entry(class).or_default() += count;
        }
        self.close_barrier.merge(&other.close_barrier);
    }
}

/// A seed that broke an invariant, with a shrunk command sequence that still breaks it.
#[derive(Debug, Clone)]
pub struct SimulationFailure {
    /// The failing seed.
    pub seed: u64,
    /// Zero-based index of the command after which the invariant broke.
    pub command_index: usize,
    /// The broken invariant.
    pub violation: InvariantViolation,
    /// The shortest command sequence found that breaks the same invariant; its last command
    /// is the one that broke it.
    pub minimal_reproduction: Vec<FlowCommand>,
}

impl fmt::Display for SimulationFailure {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        writeln!(
            formatter,
            "seed {} broke an invariant at command index {}: {}",
            self.seed, self.command_index, self.violation
        )?;
        writeln!(
            formatter,
            "reproduce with: DEXZD_SIMULATION_SEED={} DEXZD_SIMULATION_COMMANDS={} cargo test -p \
             dexzd-simulation",
            self.seed,
            self.command_index + 1,
        )?;
        writeln!(
            formatter,
            "reproduction ({} commands):",
            self.minimal_reproduction.len()
        )?;
        for (index, command) in self.minimal_reproduction.iter().enumerate() {
            writeln!(formatter, "[{index}] {command:?}")?;
        }
        Ok(())
    }
}

/// Runs `command_count` generated commands for `seed` and checks every invariant after each.
///
/// Reports a failure before shrinking it, then returns the failure with a shrunk reproduction.
/// The report callback receives the original sequence and may persist it before shrinking starts.
///
/// Returns what the seed exercised, or the first failure with a shrunk reproduction.
pub fn run_seed(
    seed: u64,
    command_count: usize,
    mut report_failure: impl FnMut(&SimulationFailure),
) -> Result<SimulationSummary, Box<SimulationFailure>> {
    let mut checker = ApplyChecker::new();
    let mut generator = CommandGenerator::new(seed);
    let mut side_streams = [
        CommandGenerator::attribute_sync_stream(seed),
        CommandGenerator::step_failure_stream(seed),
        CommandGenerator::history_offload_stream(seed),
    ];
    let mut closed_erase_stream = CommandGenerator::closed_erase_stream(seed);
    let mut summary = SimulationSummary::default();
    let mut history = Vec::with_capacity(command_count);
    for command_index in 0..command_count {
        let command = generator.next_command(&checker).map_err(|violation| {
            failure(
                seed,
                command_index,
                violation,
                history.clone(),
                &mut report_failure,
            )
        })?;
        summary.main_command_digest = chain_digest(summary.main_command_digest, &command);
        history.push(command.clone());
        apply_and_count(&mut checker, &command, &mut summary).map_err(|violation| {
            failure(
                seed,
                command_index,
                violation,
                history.clone(),
                &mut report_failure,
            )
        })?;
        // Side-stream commands do not count as main commands, so regression counts stay valid.
        for side_stream in &mut side_streams {
            if !side_stream.should_propose() {
                continue;
            }
            side_stream.follow_clock(&generator);
            let command = side_stream.next_command(&checker).map_err(|violation| {
                failure(
                    seed,
                    command_index,
                    violation,
                    history.clone(),
                    &mut report_failure,
                )
            })?;
            history.push(command.clone());
            apply_and_count(&mut checker, &command, &mut summary).map_err(|violation| {
                failure(
                    seed,
                    command_index,
                    violation,
                    history.clone(),
                    &mut report_failure,
                )
            })?;
        }
        closed_erase_stream.follow_clock(&generator);
        if let Some((phase, command)) = closed_erase_stream
            .next_closed_erase_command(&checker)
            .map_err(|violation| {
                failure(
                    seed,
                    command_index,
                    violation,
                    history.clone(),
                    &mut report_failure,
                )
            })?
        {
            history.push(command.clone());
            let was_applied =
                apply_and_count(&mut checker, &command, &mut summary).map_err(|violation| {
                    failure(
                        seed,
                        command_index,
                        violation,
                        history.clone(),
                        &mut report_failure,
                    )
                })?;
            if !was_applied {
                return Err(failure(
                    seed,
                    command_index,
                    InvariantViolation::new(
                        "closed-erase-stream",
                        format!("{phase:?} was rejected"),
                    ),
                    history,
                    &mut report_failure,
                ));
            }
            match phase {
                ClosedErasePhase::Erase => summary.closed_run_erases += 1,
                ClosedErasePhase::Replay => summary.closed_run_erase_replays += 1,
                _ => {}
            }
        }
    }
    let now_micros = side_streams
        .iter()
        .map(CommandGenerator::now_micros)
        .fold(generator.now_micros(), i64::max);
    drain_local_cleanup(&mut checker, now_micros, &mut history, &mut summary).map_err(
        |violation| {
            failure(
                seed,
                command_count.saturating_sub(1),
                violation,
                history.clone(),
                &mut report_failure,
            )
        },
    )?;
    summary.size_rejections = checker.size_rejections().clone();
    summary.close_barrier = checker.close_barrier_counts();
    summary.history_offload_off_time_travels = checker.history_offload_off_time_travels();
    summary.history_migrations = checker.history_migration_counts();
    summary.local_offload = checker.local_offload_counts();
    summary.final_row_count = checker.state().row_count();
    Ok(summary)
}

/// Turns offload on again, applies each due HistoryLocalCleanup at its due time, then checks H17.
///
/// Returns the first violated invariant, or a violation when a due cleanup changes nothing or
/// cleanups never finish.
fn drain_local_cleanup(
    checker: &mut ApplyChecker,
    mut now_micros: i64,
    history: &mut Vec<FlowCommand>,
    summary: &mut SimulationSummary,
) -> Result<(), InvariantViolation> {
    for flow_id in HISTORY_OFFLOAD_FLOW_IDS {
        let view = checker.view(flow_id, RunSelection::Only(&BTreeSet::new()))?;
        let Some((partition, _)) = view.location.filter(|_| !view.is_history_offload_enabled)
        else {
            continue;
        };
        now_micros = now_micros.saturating_add(1_000);
        let body = Command::SetHistoryOffloadMode(SetHistoryOffloadModeCommand {
            enabled: true,
            partition: u32::from(partition.0),
        });
        let command = owner_command(flow_id.to_string(), Vec::new(), now_micros, body);
        history.push(command.clone());
        apply_and_count(checker, &command, summary)?;
    }
    for _ in 0..MAX_DRAIN_CLEANUPS {
        let Some((flow_id, run_id, due_micros)) = checker.first_due_local_cleanup()? else {
            return checker.check_local_cleanup_drained();
        };
        now_micros = now_micros.saturating_add(1_000).max(due_micros);
        let body = Command::HistoryLocalCleanup(HistoryLocalCleanupCommand {});
        let command = owner_command(flow_id, run_id.as_bytes().to_vec(), now_micros, body);
        history.push(command.clone());
        if !apply_and_count(checker, &command, summary)? {
            return Err(InvariantViolation::new(
                "H17-local-cleanup-drained",
                format!("the due cleanup of run {run_id} at {now_micros} changed nothing"),
            ));
        }
    }
    Err(InvariantViolation::new(
        "H17-local-cleanup-drained",
        format!("local cleanup is still due after {MAX_DRAIN_CLEANUPS} cleanups"),
    ))
}

/// Returns an owner command with `body`, proposed at `now_micros` and bound to no attempt.
fn owner_command(flow_id: String, run_id: Vec<u8>, now_micros: i64, body: Command) -> FlowCommand {
    FlowCommand {
        flow_id,
        run_id,
        epoch: 1,
        behavior_version: dexzd_apply::BehaviorVersion::CURRENT.number(),
        proposed_at_micros: now_micros,
        command_id: u128::from(now_micros.unsigned_abs()).to_be_bytes().to_vec(),
        command: Some(body),
        ..FlowCommand::default()
    }
}

fn apply_and_count(
    checker: &mut ApplyChecker,
    command: &FlowCommand,
    summary: &mut SimulationSummary,
) -> Result<bool, InvariantViolation> {
    let outcome = checker.apply_and_check(command)?;
    let kind = command_kind(command);
    let is_error_reply = matches!(outcome.reply.reply, Some(command_reply::Reply::Error(_)));
    let counts = if outcome.writes.is_empty() || is_error_reply {
        &mut summary.rejected_by_kind
    } else {
        &mut summary.applied_by_kind
    };
    *counts.entry(kind).or_default() += 1;
    summary.commands += 1;
    let is_applied = !outcome.writes.is_empty() && !is_error_reply;
    summary.history_segments_sealed += outcome
        .effects
        .iter()
        .filter(|effect| {
            matches!(
                effect,
                dexzd_apply::ApplyEffect::HistorySegmentSealed { .. }
            )
        })
        .count();
    if is_applied
        && let Some(Command::AttributesSet(command)) = &command.command
        && command.request.as_ref().is_some_and(|request| {
            request
                .action_permission_mappings
                .as_ref()
                .is_some_and(|mappings| !mappings.mappings.is_empty())
        })
    {
        summary.permission_mapping_commands += 1;
    }
    Ok(is_applied)
}

/// Chains `command`'s encoding onto `previous`, so the digest pins a whole command sequence.
pub(crate) fn chain_digest(previous: u128, command: &FlowCommand) -> u128 {
    let mut bytes = previous.to_be_bytes().to_vec();
    bytes.extend_from_slice(&command.encode_to_vec());
    xxh3_128(&bytes)
}

/// Applies `commands` in order to an empty state with every check.
///
/// Returns the index and violation of the first failing command, or `None`.
pub fn replay_commands(commands: &[FlowCommand]) -> Option<(usize, InvariantViolation)> {
    let mut checker = ApplyChecker::new();
    for (index, command) in commands.iter().enumerate() {
        if let Err(violation) = checker.apply_and_check(command) {
            return Some((index, violation));
        }
    }
    None
}

fn failure(
    seed: u64,
    command_index: usize,
    violation: InvariantViolation,
    history: Vec<FlowCommand>,
    report_failure: &mut impl FnMut(&SimulationFailure),
) -> Box<SimulationFailure> {
    let mut failure = SimulationFailure {
        seed,
        command_index,
        violation,
        minimal_reproduction: history,
    };
    report_failure(&failure);
    failure.minimal_reproduction = shrink(
        std::mem::take(&mut failure.minimal_reproduction),
        failure.violation.invariant,
    );
    Box::new(failure)
}

/// Removes chunks of commands, halving the chunk size, while the same invariant still breaks.
fn shrink(mut commands: Vec<FlowCommand>, invariant: &str) -> Vec<FlowCommand> {
    let started_at = Instant::now();
    let mut replays_left = SHRINK_REPLAY_BUDGET;
    let mut chunk_size = commands.len().div_ceil(2);
    while chunk_size > 0 && replays_left > 0 && started_at.elapsed() < SHRINK_TIME_BUDGET {
        let mut start = 0;
        while start < commands.len()
            && replays_left > 0
            && started_at.elapsed() < SHRINK_TIME_BUDGET
        {
            let end = (start + chunk_size).min(commands.len());
            let mut candidate = commands[..start].to_vec();
            candidate.extend_from_slice(&commands[end..]);
            replays_left -= 1;
            match replay_commands(&candidate) {
                Some((index, violation)) if violation.invariant == invariant => {
                    candidate.truncate(index + 1);
                    commands = candidate;
                }
                _ => start = end,
            }
        }
        chunk_size /= 2;
    }
    commands
}

/// Short name of the command kind for summaries.
pub fn command_kind(command: &FlowCommand) -> &'static str {
    match &command.command {
        None => "none",
        Some(Command::FlowStart(_)) => "FlowStart",
        Some(Command::FlowStop(_)) => "FlowStop",
        Some(Command::FlowConfigUpdate(_)) => "FlowConfigUpdate",
        Some(Command::AttributesSet(_)) => "AttributesSet",
        Some(Command::ChannelPublish(_)) => "ChannelPublish",
        Some(Command::ChannelMessageDelete(_)) => "ChannelMessageDelete",
        Some(Command::StepAttemptStarted(_)) => "StepAttemptStarted",
        Some(Command::StepAttemptHeartbeat(_)) => "StepAttemptHeartbeat",
        Some(Command::StepAttemptFailed(_)) => "StepAttemptFailed",
        Some(Command::StepMethodCompleted(_)) => "StepMethodCompleted",
        Some(Command::StepAttemptReturned(_)) => "StepAttemptReturned",
        Some(Command::StepAttemptQueued(_)) => "StepAttemptQueued",
        Some(Command::RpcCommitted(_)) => "RpcCommitted",
        Some(Command::TimersFired(_)) => "TimersFired",
        Some(Command::TimerSkip(_)) => "TimerSkip",
        Some(Command::TimeTravel(_)) => "TimeTravel",
        Some(Command::TimeTravelStage(_)) => "TimeTravelStage",
        Some(Command::InboxMessage(_)) => "InboxMessage",
        Some(Command::OutboxAck(_)) => "OutboxAck",
        Some(Command::AttributeSyncAck(_)) => "AttributeSyncAck",
        Some(Command::PendingCloseCheck(_)) => "PendingCloseCheck",
        Some(Command::SearchOutboxAck(_)) => "SearchOutboxAck",
        Some(Command::SetSearchProjectionMode(_)) => "SetSearchProjectionMode",
        Some(Command::SetHistoryOffloadMode(_)) => "SetHistoryOffloadMode",
        Some(Command::HardDeleteObjectsDeleted(_)) => "HardDeleteObjectsDeleted",
        Some(Command::RunPurge(_)) => "RunPurge",
        Some(Command::HistorySegmentUploaded(_)) => "HistorySegmentUploaded",
        Some(Command::ValueRevive(_)) => "ValueRevive",
        Some(Command::HistorySegmentObjectsDeleted(_)) => "HistorySegmentObjectsDeleted",
        Some(Command::HistoryLocalCleanup(_)) => "HistoryLocalCleanup",
    }
}

#[cfg(test)]
mod tests {
    use super::{FlowCommand, InvariantViolation, failure};

    #[test]
    fn reports_seed_failure_before_shrinking() {
        let mut initial_report = None;
        let failure = failure(
            42,
            7,
            InvariantViolation::new("test-invariant", "broken"),
            vec![FlowCommand::default()],
            &mut |failure| initial_report = Some(failure.to_string()),
        );
        let initial_report = initial_report.expect("failure callback must run");
        assert!(initial_report.contains("seed 42 broke an invariant at command index 7"));
        assert!(initial_report.contains("reproduce with: DEXZD_SIMULATION_SEED=42"));
        assert!(initial_report.contains("reproduction (1 commands)"));
        assert_eq!(failure.seed, 42);
    }
}
