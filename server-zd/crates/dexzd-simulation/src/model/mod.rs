// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! A seeded model of clients, the owner, and pull Workers that proposes commands.
//!
//! It also proposes stale, duplicate, and conflicting commands that apply must reject.

mod client_model;
mod history_offload_model;
mod value_generation;
mod worker_model;

pub(crate) use value_generation::MAX_VALUE_BYTES;
pub(crate) use value_generation::{
    ATTRIBUTE_KEYS, CHANNEL_NAMES, LOCAL_KEYS, STEP_TYPES, int_value, null_value, pick,
    random_value, wire_value,
};

use std::collections::BTreeSet;

use dexzd_apply::{TransactionalRpcHold, transactional_rpc_hold};
use dexzd_keyspace::RunId;
use dexzd_protocol::dex::{
    AttributeWrite, FlowConfig, FlowStatus, SetAttributesRequest, StopFlowRequest, StopType,
    UpdateFlowConfigRequest, Value, value,
};
use dexzd_protocol::dexzd::v1::{
    AttributesSetCommand, FlowCommand, FlowConfigUpdateCommand, FlowStopCommand,
    HardDeleteObjectReference, HistoryLocalCleanupCommand, HistorySegmentObjectsDeletedCommand,
    HistorySegmentUploadedCommand, PendingCloseCheckCommand, SetHistoryOffloadModeCommand,
    StepInvocationPhase, TimerId, flow_command::Command, timer_id,
};
use rand::{RngExt, SeedableRng};
use rand_chacha::ChaCha8Rng;

use crate::apply_checker::ApplyChecker;
use crate::invariants::InvariantViolation;
use crate::model::history_offload_model::{
    expired_object_proposals, history_segment_acknowledgments, local_cleanup_proposals,
};
use crate::model::worker_model::Call;
use crate::state_view::{FlowView, RunSelection, RunView};

/// Flow IDs the simulated clients use.
pub const FLOW_IDS: &[&str] = &["order-1", "order-2"];
/// The only Flow of the Attribute Sync stream; it targets Attribute Stores and writes synced keys.
pub const ATTRIBUTE_SYNC_FLOW_ID: &str = "account-sync";
/// Mixed into the seed so the Attribute Sync stream never shares draws with the main stream.
const ATTRIBUTE_SYNC_STREAM_SEED: u64 = 0xa77e_5c00_0000_0000;
/// Chance that the Attribute Sync stream proposes a command after each main command.
const ATTRIBUTE_SYNC_COMMAND_PROBABILITY: f64 = 0.3;
/// Flows of the Step failure stream; their Steps carry failure policies and short total durations.
pub const STEP_FAILURE_FLOW_IDS: &[&str] = &["order-recovery-1", "order-recovery-2"];
/// Mixed into the seed so the Step failure stream never shares draws with the main stream.
const STEP_FAILURE_STREAM_SEED: u64 = 0x5fa1_7e00_0000_0000;
const CLOSED_ERASE_STREAM_SEED: u64 = 0xc105_edee_0000_0000;
const CLOSED_ERASE_FLOW_ID: &str = "closed-erase-simulation";
/// Flows of the history offload stream; their partitions offload history and no other stream's
/// Flow shares them.
pub const HISTORY_OFFLOAD_FLOW_IDS: &[&str] = &["history-offload-1", "history-offload-3"];
/// Mixed into the seed so the history offload stream never shares draws with the main stream.
const HISTORY_OFFLOAD_STREAM_SEED: u64 = 0x415e_9e00_0000_0000;
/// Chance that the history offload stream proposes a command after each main command.
const HISTORY_OFFLOAD_COMMAND_PROBABILITY: f64 = 0.5;
/// Small enough that a few large writes seal a segment.
const HISTORY_OFFLOAD_SEGMENT_TARGET_BYTES: u64 = 3_000;
/// Weight of turning offload on again; low enough that runs apply several commands while off.
const HISTORY_OFFLOAD_REENABLE_WEIGHT: u32 = 4;
/// Grace period of superseded values in one history offload Flow; the other keeps 10 minutes.
const HISTORY_OFFLOAD_SHORT_GRACE_MICROS: i64 = 30_000_000;
/// Chance that the Step failure stream proposes a command after each main command.
const STEP_FAILURE_COMMAND_PROBABILITY: f64 = 0.8;
/// Proposal time of the first command: 2027-01-15 in microseconds.
const START_MICROS: i64 = 1_800_000_000_000_000;
/// Result commands remembered for redelivery.
const REMEMBERED_RESULTS: usize = 16;

/// Proposes the next command from the committed state and a seeded random source.
pub struct CommandGenerator {
    rng: ChaCha8Rng,
    now_micros: i64,
    sent_results: Vec<FlowCommand>,
    last_command_was_redelivered_result: bool,
    flow_ids: &'static [&'static str],
    stream: CommandStream,
    closed_erase_phase: ClosedErasePhase,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(crate) enum ClosedErasePhase {
    Start,
    Store,
    Close,
    Erase,
    Replay,
    Done,
}

/// The command stream a generator proposes for; each draws from its own seeded source.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum CommandStream {
    /// The stream over [`FLOW_IDS`] whose per-seed digests the regression seeds pin.
    Main,
    /// Synced Attribute writes and their acks on [`ATTRIBUTE_SYNC_FLOW_ID`].
    AttributeSync,
    /// Step failure policies and retry total durations on [`STEP_FAILURE_FLOW_IDS`].
    StepFailure,
    ClosedErase,
    /// Sealed history segments and their upload acknowledgments on
    /// [`HISTORY_OFFLOAD_FLOW_IDS`], whose partitions offload history.
    HistoryOffload,
}

/// One possible next command and its weight.
enum Proposal<'view> {
    StartFlow,
    ClientWrite {
        run_id: RunId,
        run: &'view RunView,
    },
    StartAttempt(Call<'view>),
    WorkerOutcome(Call<'view>),
    StaleAttempt(Call<'view>),
    FireTimer {
        run_id: RunId,
        timer: TimerId,
        fire_at_micros: i64,
    },
    RedeliverResult,
    LaterHandler {
        run_id: RunId,
        run: &'view RunView,
    },
    TravelToWindowPoint {
        run_id: RunId,
        timestamp_micros: i64,
        run_attempt: i32,
    },
    AttributeSyncAck {
        run_id: RunId,
        run: &'view RunView,
    },
    PendingCloseCheck {
        run_id: RunId,
    },
    SkipFlowTimeout {
        run_id: RunId,
    },
    /// Turns history offload on or off for the Flow's partition.
    SetHistoryOffloadMode {
        enabled: bool,
        partition: u32,
    },
    /// The uploader acknowledges a sealed history segment; a stale one changes nothing.
    AcknowledgeHistorySegment {
        run_id: RunId,
        acknowledgment: HistorySegmentUploadedCommand,
    },
    /// The owner records deleted objects of expired segments; a stale record changes nothing.
    RecordDeletedHistoryObjects {
        run_id: RunId,
        deleted: HistorySegmentObjectsDeletedCommand,
    },
    /// A client turns the run's delete switch on or off with UpdateFlowConfig.
    SetExpiredObjectDeletion {
        run_id: RunId,
        is_enabled: bool,
    },
    /// The owner proposes the run's local history cleanup, usually once it is due; an early
    /// one changes nothing.
    CleanUpLocalHistory {
        run_id: RunId,
        due_micros: i64,
    },
}

impl CommandGenerator {
    /// Creates a generator whose every choice derives from `seed`.
    pub fn new(seed: u64) -> Self {
        Self {
            rng: ChaCha8Rng::seed_from_u64(seed),
            now_micros: START_MICROS,
            sent_results: Vec::new(),
            last_command_was_redelivered_result: false,
            flow_ids: FLOW_IDS,
            stream: CommandStream::Main,
            closed_erase_phase: ClosedErasePhase::Start,
        }
    }

    /// Creates `seed`'s Attribute Sync stream, whose own draws drive one synced Flow and leave
    /// the main stream unchanged.
    pub fn attribute_sync_stream(seed: u64) -> Self {
        Self {
            rng: ChaCha8Rng::seed_from_u64(seed ^ ATTRIBUTE_SYNC_STREAM_SEED),
            flow_ids: &[ATTRIBUTE_SYNC_FLOW_ID],
            stream: CommandStream::AttributeSync,
            ..Self::new(seed)
        }
    }

    /// Creates `seed`'s Step failure stream; its own draws leave the main stream unchanged.
    pub fn step_failure_stream(seed: u64) -> Self {
        Self {
            rng: ChaCha8Rng::seed_from_u64(seed ^ STEP_FAILURE_STREAM_SEED),
            flow_ids: STEP_FAILURE_FLOW_IDS,
            stream: CommandStream::StepFailure,
            ..Self::new(seed)
        }
    }

    /// Creates `seed`'s history offload stream; its own draws leave the main stream unchanged.
    pub fn history_offload_stream(seed: u64) -> Self {
        Self {
            rng: ChaCha8Rng::seed_from_u64(seed ^ HISTORY_OFFLOAD_STREAM_SEED),
            flow_ids: HISTORY_OFFLOAD_FLOW_IDS,
            stream: CommandStream::HistoryOffload,
            ..Self::new(seed)
        }
    }

    pub(crate) fn closed_erase_stream(seed: u64) -> Self {
        Self {
            rng: ChaCha8Rng::seed_from_u64(seed ^ CLOSED_ERASE_STREAM_SEED),
            flow_ids: &[CLOSED_ERASE_FLOW_ID],
            stream: CommandStream::ClosedErase,
            ..Self::new(seed)
        }
    }

    pub(crate) fn next_closed_erase_command(
        &mut self,
        checker: &ApplyChecker,
    ) -> Result<Option<(ClosedErasePhase, FlowCommand)>, InvariantViolation> {
        let phase = self.closed_erase_phase;
        if phase == ClosedErasePhase::Done {
            return Ok(None);
        }
        let view = checker.view(CLOSED_ERASE_FLOW_ID, RunSelection::All)?;
        let run = view.latest_run();
        let (run_id, body, next_phase) = match phase {
            ClosedErasePhase::Start => (
                Vec::new(),
                client_model::start_flow(&mut self.rng, CLOSED_ERASE_FLOW_ID, false, false),
                ClosedErasePhase::Store,
            ),
            ClosedErasePhase::Store => (
                run.expect("the closed-erase Flow started")
                    .0
                    .as_bytes()
                    .to_vec(),
                Command::AttributesSet(AttributesSetCommand {
                    request: Some(SetAttributesRequest {
                        attributes: vec![AttributeWrite {
                            key: "secret".to_owned(),
                            is_hard_delete_on_null: true,
                            ..AttributeWrite::default()
                        }],
                        ..SetAttributesRequest::default()
                    }),
                    hard_delete_objects: vec![HardDeleteObjectReference {
                        write_index: 0,
                        object_id: format!("secret-{}", self.rng.random::<u64>()),
                        size_bytes: 12,
                        is_string_value: true,
                    }],
                    ..AttributesSetCommand::default()
                }),
                ClosedErasePhase::Close,
            ),
            ClosedErasePhase::Close => (
                run.expect("the closed-erase Flow has a run")
                    .0
                    .as_bytes()
                    .to_vec(),
                Command::FlowStop(FlowStopCommand {
                    request: Some(StopFlowRequest {
                        stop_type: StopType::Terminate as i32,
                        ..StopFlowRequest::default()
                    }),
                }),
                ClosedErasePhase::Erase,
            ),
            ClosedErasePhase::Erase => (
                run.expect("the closed-erase Flow has a run")
                    .0
                    .as_bytes()
                    .to_vec(),
                Command::AttributesSet(AttributesSetCommand {
                    request: Some(SetAttributesRequest {
                        attributes: vec![AttributeWrite {
                            key: "secret".to_owned(),
                            value: Some(Value {
                                kind: Some(value::Kind::NullValue(0)),
                            }),
                            is_hard_delete_on_null: true,
                            ..AttributeWrite::default()
                        }],
                        ..SetAttributesRequest::default()
                    }),
                    ..AttributesSetCommand::default()
                }),
                ClosedErasePhase::Replay,
            ),
            ClosedErasePhase::Replay => {
                let (run_id, run) = run.expect("the closed-erase Flow has a run");
                let (erase_seq, erase_record) = run.history.last().expect("the erase has history");
                assert!(
                    run.attempts
                        .get(&1)
                        .expect("the initial attempt exists")
                        .checkpoint_seqs
                        .iter()
                        .all(|seq| seq < erase_seq)
                );
                let erase_time = erase_record.timestamp_micros;
                (
                    run_id.as_bytes().to_vec(),
                    client_model::time_travel_to_window_point(CLOSED_ERASE_FLOW_ID, erase_time, 0),
                    ClosedErasePhase::Done,
                )
            }
            ClosedErasePhase::Done => unreachable!("the completed phase returned above"),
        };
        self.closed_erase_phase = next_phase;
        let mut command = self.envelope(CLOSED_ERASE_FLOW_ID, run_id, body);
        command.expected_run_attempt = 0;
        Ok(Some((phase, command)))
    }

    /// Draws whether this stream proposes a command now.
    pub fn should_propose(&mut self) -> bool {
        let probability = match self.stream {
            CommandStream::StepFailure => STEP_FAILURE_COMMAND_PROBABILITY,
            CommandStream::HistoryOffload => HISTORY_OFFLOAD_COMMAND_PROBABILITY,
            CommandStream::Main | CommandStream::AttributeSync | CommandStream::ClosedErase => {
                ATTRIBUTE_SYNC_COMMAND_PROBABILITY
            }
        };
        self.rng.random_bool(probability)
    }

    /// Moves this stream's clock up to `other`'s without changing `other`.
    pub fn follow_clock(&mut self, other: &Self) {
        self.now_micros = self.now_micros.max(other.now_micros);
    }

    /// Returns the command time of this stream's latest command.
    pub fn now_micros(&self) -> i64 {
        self.now_micros
    }

    /// Proposes the next command for the checker's committed state.
    ///
    /// Returns a violation when the committed state cannot be decoded.
    pub fn next_command(
        &mut self,
        checker: &ApplyChecker,
    ) -> Result<FlowCommand, InvariantViolation> {
        let flow_id = self.flow_ids[self.rng.random_range(0..self.flow_ids.len())];
        let directory_view = checker.view(flow_id, RunSelection::Only(&BTreeSet::new()))?;
        let run_ids: Vec<RunId> = directory_view
            .directory
            .iter()
            .flat_map(|directory| &directory.runs)
            .filter_map(|run| RunId::from_slice(&run.run_id).ok())
            .collect();
        let mut selected = BTreeSet::new();
        if let Some(latest) = run_ids.last() {
            selected.insert(*latest);
        }
        if run_ids.len() > 1 && self.rng.random_bool(0.3) {
            selected.insert(run_ids[self.rng.random_range(0..run_ids.len() - 1)]);
        }
        let view = checker.view(flow_id, RunSelection::Only(&selected))?;
        let proposals = if self.stream == CommandStream::HistoryOffload {
            self.history_offload_proposals(&view, checker)?
        } else {
            self.proposals(&view, checker)?
        };
        let total: u32 = proposals.iter().map(|(weight, _)| weight).sum();
        let mut ticket = self.rng.random_range(0..total);
        let mut chosen = None;
        for (weight, proposal) in proposals {
            if ticket < weight {
                chosen = Some(proposal);
                break;
            }
            ticket -= weight;
        }
        let proposal = chosen.expect("the ticket is below the total weight");
        let mut command = self.realize(flow_id, proposal);
        // Only this stream models owner stamps, so the main stream's draws stay unchanged.
        if self.stream == CommandStream::AttributeSync
            && transactional_rpc_hold(&command) == TransactionalRpcHold::MayClose
        {
            command.is_transactional_rpc_in_flight = self.rng.random_bool(0.3);
        }
        if self.stream == CommandStream::HistoryOffload
            && let Some(Command::FlowStart(start)) = command.command.as_mut()
            && let Some(settings) = start.settings.as_mut()
        {
            settings.history_segment_target_bytes = HISTORY_OFFLOAD_SEGMENT_TARGET_BYTES;
            // One Flow starts its runs deleting expired objects, the other keeping them.
            settings.delete_expired_history_objects = flow_id == HISTORY_OFFLOAD_FLOW_IDS[0];
            // The other Flow's runs keep superseded values a short grace period.
            if flow_id != HISTORY_OFFLOAD_FLOW_IDS[0] {
                settings.superseded_value_grace_micros = HISTORY_OFFLOAD_SHORT_GRACE_MICROS;
            }
        }
        Ok(command)
    }

    /// Turns offload on before the Flow's first run, then sometimes off and on again, so runs
    /// also apply commands while offload is off, with SEALED segments still waiting.
    fn history_offload_proposals<'view>(
        &mut self,
        view: &'view FlowView,
        checker: &ApplyChecker,
    ) -> Result<Vec<(u32, Proposal<'view>)>, InvariantViolation> {
        let (partition, _) = view.location.expect("a decoded view records its partition");
        let mode = |enabled| Proposal::SetHistoryOffloadMode {
            enabled,
            partition: u32::from(partition.0),
        };
        if view.directory.is_none() && !view.is_history_offload_enabled {
            return Ok(vec![(1, mode(true))]);
        }
        let mut proposals = self.proposals(view, checker)?;
        if view.is_history_offload_enabled {
            proposals.push((1, mode(false)));
        } else {
            proposals.push((HISTORY_OFFLOAD_REENABLE_WEIGHT, mode(true)));
        }
        Ok(proposals)
    }

    fn proposals<'view>(
        &mut self,
        view: &'view FlowView,
        checker: &ApplyChecker,
    ) -> Result<Vec<(u32, Proposal<'view>)>, InvariantViolation> {
        let mut proposals = vec![(1, Proposal::StartFlow)];
        if !self.sent_results.is_empty() {
            proposals.push((2, Proposal::RedeliverResult));
        }
        let Some((latest_run_id, latest_run)) = view.latest_run() else {
            proposals[0].0 = 20;
            return Ok(proposals);
        };
        if self.stream == CommandStream::HistoryOffload {
            proposals.extend(history_segment_acknowledgments(view, checker)?);
            proposals.extend(expired_object_proposals(view));
            proposals.extend(local_cleanup_proposals(view));
        }
        let is_running = latest_run
            .record
            .as_ref()
            .is_some_and(|record| record.status == FlowStatus::Running as i32);
        if !is_running {
            proposals[0].0 = 12;
        }
        for (run_id, run) in view.runs.iter().filter(|(_, run)| run.is_complete) {
            let is_latest = *run_id == latest_run_id;
            let client_weight = if is_latest { 8 } else { 1 };
            proposals.push((
                client_weight,
                Proposal::ClientWrite {
                    run_id: *run_id,
                    run,
                },
            ));
            if is_latest
                && let Some(timestamp_micros) = checker.earlier_window_value_point(*run_id, run)
            {
                proposals.push((
                    3,
                    Proposal::TravelToWindowPoint {
                        run_id: *run_id,
                        timestamp_micros,
                        run_attempt: 0,
                    },
                ));
            }
            if self.stream == CommandStream::AttributeSync
                && is_latest
                && let Some((timestamp_micros, run_attempt)) =
                    checker.closed_window_point(*run_id, run)
            {
                proposals.push((
                    3,
                    Proposal::TravelToWindowPoint {
                        run_id: *run_id,
                        timestamp_micros,
                        run_attempt,
                    },
                ));
            }
            if self.stream == CommandStream::AttributeSync && !run.attribute_sync_entries.is_empty()
            {
                proposals.push((
                    3,
                    Proposal::AttributeSyncAck {
                        run_id: *run_id,
                        run,
                    },
                ));
            }
            let has_pending_close = run
                .record
                .as_ref()
                .is_some_and(|record| record.pending_close.is_some());
            if self.stream == CommandStream::AttributeSync && has_pending_close {
                proposals.push((3, Proposal::PendingCloseCheck { run_id: *run_id }));
            }
            let has_flow_timeout = run.timers.iter().any(|(_, timer)| {
                matches!(
                    timer
                        .timer_id
                        .as_ref()
                        .and_then(|timer_id| timer_id.kind.as_ref()),
                    Some(timer_id::Kind::FlowTimeout(_))
                )
            });
            if self.stream == CommandStream::AttributeSync && is_latest && has_flow_timeout {
                proposals.push((6, Proposal::SkipFlowTimeout { run_id: *run_id }));
            }
            if is_latest {
                proposals.push((
                    1,
                    Proposal::LaterHandler {
                        run_id: *run_id,
                        run,
                    },
                ));
            }
            for (_, timer) in &run.timers {
                let Some(timer_id) = timer.timer_id.clone() else {
                    continue;
                };
                proposals.push((
                    3,
                    Proposal::FireTimer {
                        run_id: *run_id,
                        timer: timer_id,
                        fire_at_micros: timer.fire_at_micros,
                    },
                ));
            }
            for step in run.step_executions.values() {
                let Some(invocation) = &step.invocation else {
                    continue;
                };
                let call = || Call {
                    run_id: *run_id,
                    run,
                    step,
                    invocation,
                };
                let phase = StepInvocationPhase::try_from(invocation.phase);
                match phase {
                    Ok(StepInvocationPhase::Pending) => {
                        let weight = if call().can_take_locks() { 10 } else { 1 };
                        proposals.push((weight, Proposal::StartAttempt(call())));
                    }
                    Ok(StepInvocationPhase::Running) => {
                        proposals.push((3, Proposal::WorkerOutcome(call())));
                        proposals.push((1, Proposal::StaleAttempt(call())));
                    }
                    _ => proposals.push((1, Proposal::StaleAttempt(call()))),
                }
            }
        }
        Ok(proposals)
    }

    fn realize(&mut self, flow_id: &str, proposal: Proposal<'_>) -> FlowCommand {
        self.last_command_was_redelivered_result = false;
        let is_synced = self.stream == CommandStream::AttributeSync;
        let is_step_failure_stream = self.stream == CommandStream::StepFailure;
        let rng = &mut self.rng;
        match proposal {
            Proposal::StartFlow => {
                let body =
                    client_model::start_flow(rng, flow_id, is_synced, is_step_failure_stream);
                self.envelope(flow_id, Vec::new(), body)
            }
            Proposal::ClientWrite { run_id, run } => {
                let body = match rng.random_range(0..40) {
                    0..=13 => client_model::set_attributes(rng, is_synced),
                    14..=27 => client_model::publish(rng),
                    28..=33 => client_model::delete_message(rng, run),
                    34..=35 => client_model::stop(rng),
                    36 => client_model::config_update(),
                    _ => client_model::start_flow(rng, flow_id, is_synced, is_step_failure_stream),
                };
                let run_id = match body {
                    Command::FlowStart(_) => Vec::new(),
                    _ => run_id.as_bytes().to_vec(),
                };
                self.envelope(flow_id, run_id, body)
            }
            Proposal::LaterHandler { run_id, run } => {
                let body = client_model::later_handler_command(rng, flow_id, run_id, run);
                self.envelope(flow_id, run_id.as_bytes().to_vec(), body)
            }
            Proposal::TravelToWindowPoint {
                run_id,
                timestamp_micros,
                run_attempt,
            } => {
                let body = client_model::time_travel_to_window_point(
                    flow_id,
                    timestamp_micros,
                    run_attempt,
                );
                self.envelope(flow_id, run_id.as_bytes().to_vec(), body)
            }
            Proposal::AttributeSyncAck { run_id, run } => {
                let body = client_model::attribute_sync_ack(rng, run);
                self.envelope(flow_id, run_id.as_bytes().to_vec(), body)
            }
            Proposal::PendingCloseCheck { run_id } => {
                let body = Command::PendingCloseCheck(PendingCloseCheckCommand {});
                self.envelope(flow_id, run_id.as_bytes().to_vec(), body)
            }
            Proposal::SkipFlowTimeout { run_id } => {
                let body = client_model::skip_flow_timeout(flow_id);
                self.envelope(flow_id, run_id.as_bytes().to_vec(), body)
            }
            Proposal::SetHistoryOffloadMode { enabled, partition } => {
                let body = Command::SetHistoryOffloadMode(SetHistoryOffloadModeCommand {
                    enabled,
                    partition,
                });
                let mut command = self.envelope(flow_id, Vec::new(), body);
                command.expected_run_attempt = 0;
                command
            }
            Proposal::AcknowledgeHistorySegment {
                run_id,
                acknowledgment,
            } => {
                let body = Command::HistorySegmentUploaded(acknowledgment);
                let mut command = self.envelope(flow_id, run_id.as_bytes().to_vec(), body);
                // The uploader never binds an acknowledgment to an attempt.
                command.expected_run_attempt = 0;
                command
            }
            Proposal::RecordDeletedHistoryObjects { run_id, deleted } => {
                let body = Command::HistorySegmentObjectsDeleted(deleted);
                let mut command = self.envelope(flow_id, run_id.as_bytes().to_vec(), body);
                // The owner never binds a deletion record to an attempt.
                command.expected_run_attempt = 0;
                command
            }
            Proposal::CleanUpLocalHistory { run_id, due_micros } => {
                if rng.random_bool(0.8) {
                    self.now_micros = self.now_micros.max(due_micros);
                }
                let body = Command::HistoryLocalCleanup(HistoryLocalCleanupCommand {});
                let mut command = self.envelope(flow_id, run_id.as_bytes().to_vec(), body);
                // The owner never binds a cleanup to an attempt.
                command.expected_run_attempt = 0;
                command
            }
            Proposal::SetExpiredObjectDeletion { run_id, is_enabled } => {
                let body = Command::FlowConfigUpdate(FlowConfigUpdateCommand {
                    request: Some(UpdateFlowConfigRequest {
                        flow_id: flow_id.to_owned(),
                        flow_config: Some(FlowConfig {
                            delete_expired_history_objects: Some(is_enabled),
                            ..FlowConfig::default()
                        }),
                        ..UpdateFlowConfigRequest::default()
                    }),
                });
                self.envelope(flow_id, run_id.as_bytes().to_vec(), body)
            }
            Proposal::StartAttempt(call) => {
                // Only the Step failure stream queues, so main-stream trajectories stay pinned.
                let body = if is_step_failure_stream && rng.random_bool(0.3) {
                    worker_model::queue_attempt(rng, &call)
                } else {
                    worker_model::start_attempt(rng, &call)
                };
                self.invocation_envelope(flow_id, &call, body)
            }
            Proposal::WorkerOutcome(call) => {
                let body = if is_step_failure_stream && rng.random_bool(0.1) {
                    worker_model::return_attempt(rng, &call)
                } else {
                    match rng.random_range(0..10) {
                        0..=5 => {
                            worker_model::complete(rng, &call, is_synced, is_step_failure_stream)
                        }
                        6..=7 => worker_model::fail(rng, &call, is_step_failure_stream),
                        _ => worker_model::heartbeat(rng, &call),
                    }
                };
                let command = self.invocation_envelope(flow_id, &call, body);
                self.remember_result(&command);
                command
            }
            Proposal::StaleAttempt(call) => {
                let body = worker_model::stale_attempt_command(rng, &call, is_step_failure_stream);
                self.invocation_envelope(flow_id, &call, body)
            }
            Proposal::FireTimer {
                run_id,
                mut timer,
                fire_at_micros,
            } => {
                self.now_micros = self.now_micros.max(fire_at_micros);
                if rng.random_bool(0.05) {
                    timer.run_attempt += 1;
                }
                let body = worker_model::fire_timers(vec![timer]);
                self.envelope(flow_id, run_id.as_bytes().to_vec(), body)
            }
            Proposal::RedeliverResult => {
                self.last_command_was_redelivered_result = true;
                let index = rng.random_range(0..self.sent_results.len());
                let previous = self.sent_results[index].clone();
                let body = previous
                    .command
                    .expect("remembered results are generated with a body");
                let mut command = self.envelope(&previous.flow_id, previous.run_id, body);
                command.expected_run_attempt = previous.expected_run_attempt;
                command
            }
        }
    }

    fn invocation_envelope(
        &mut self,
        flow_id: &str,
        call: &Call<'_>,
        body: Command,
    ) -> FlowCommand {
        let mut command = self.envelope(flow_id, call.run_id.as_bytes().to_vec(), body);
        command.expected_run_attempt = call
            .run
            .record
            .as_ref()
            .expect("a dispatched invocation belongs to an existing run")
            .current_attempt;
        command
    }

    fn envelope(&mut self, flow_id: &str, run_id: Vec<u8>, body: Command) -> FlowCommand {
        self.now_micros += self.rng.random_range(1_000..3_000_000);
        let expected_run_attempt = if self.rng.random_bool(0.05) {
            self.rng.random_range(1..3)
        } else {
            0
        };
        FlowCommand {
            flow_id: flow_id.to_owned(),
            run_id,
            expected_run_attempt,
            epoch: 1,
            behavior_version: dexzd_apply::BehaviorVersion::CURRENT.number(),
            proposed_at_micros: self.now_micros,
            command_id: value_generation::random_bytes(&mut self.rng).to_vec(),
            tenant_id: 0,
            is_transactional_rpc_in_flight: false,
            command: Some(body),
        }
    }

    fn remember_result(&mut self, command: &FlowCommand) {
        if self.sent_results.len() == REMEMBERED_RESULTS {
            self.sent_results.remove(0);
        }
        self.sent_results.push(command.clone());
    }

    /// Reports whether the last successful [`Self::next_command`] selected a remembered Worker result for redelivery; false before generation.
    pub fn last_command_was_redelivered_result(&self) -> bool {
        self.last_command_was_redelivered_result
    }
}
