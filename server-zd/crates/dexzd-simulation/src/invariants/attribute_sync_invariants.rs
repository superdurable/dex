// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Attribute Sync outbox invariants AS1 to AS3 and close barrier invariants AS4 and AS5.

use std::collections::{BTreeMap, BTreeSet};

use dexzd_apply::diagnostics::TimeTravelTargetClose;
use dexzd_apply::{StateWrite, TransactionalRpcHold, transactional_rpc_hold};
use dexzd_keyspace::{ColumnFamily, RunId, RunTable, parse_run_key};
use dexzd_protocol::dex::FlowStatus;
use dexzd_protocol::dexzd::v1::{
    AttributeSyncAckCommand, AttributeSyncOutboxRecord, FlowCommand, RunRecord, StepExecutionPhase,
    TimerId, flow_command::Command, timer_id,
};
use prost::Message;

use crate::apply_simulation::command_kind;
use crate::invariants::{InvariantViolation, RunContext};
use crate::memory_state::MemoryFlowState;
use crate::state_view::{FlowView, RunView};

/// Highest applied ack seq per run, store, and key; `None` stands for an ack of every key.
pub(crate) type AttributeSyncAcks = BTreeMap<(RunId, String, Option<String>), u64>;

/// Remembers an applied ack, which may legitimately have deleted entries up to its seq.
pub(crate) fn remember_ack(
    acks: &mut AttributeSyncAcks,
    run_id: RunId,
    ack: &AttributeSyncAckCommand,
) {
    let keys: Vec<Option<String>> = if ack.attribute_keys.is_empty() {
        vec![None]
    } else {
        ack.attribute_keys.iter().cloned().map(Some).collect()
    };
    for key in keys {
        let acked = acks
            .entry((run_id, ack.store_name.clone(), key))
            .or_default();
        *acked = (*acked).max(ack.acked_seq);
    }
}

/// AS2: markings never lower an entry's seq, and only a covering ack deletes an entry.
pub(crate) fn check_outbox_writes(
    command: &FlowCommand,
    writes: &[StateWrite],
    state: &MemoryFlowState,
) -> Result<(), InvariantViolation> {
    for write in writes {
        let (key, new_value) = match write {
            StateWrite::Put {
                column_family: ColumnFamily::Flow,
                key,
                value,
            } => (key, Some(value)),
            StateWrite::Delete {
                column_family: ColumnFamily::Flow,
                key,
            } => (key, None),
            _ => continue,
        };
        let Ok(parsed) = parse_run_key(key) else {
            continue;
        };
        if parsed.table != RunTable::AttributeSyncOutbox {
            continue;
        }
        let Some(previous) = state
            .row(ColumnFamily::Flow, key)
            .map(|bytes| decode(bytes, "AS2-attribute-sync-seq"))
            .transpose()?
        else {
            continue;
        };
        match new_value {
            Some(bytes) => {
                let next = decode(bytes, "AS2-attribute-sync-seq")?;
                if next.written_seq < previous.written_seq {
                    return Err(InvariantViolation::new(
                        "AS2-attribute-sync-seq",
                        format!(
                            "entry {}/{} went from seq {} to {}",
                            previous.store_name,
                            previous.attribute_key,
                            previous.written_seq,
                            next.written_seq
                        ),
                    ));
                }
            }
            None => {
                let is_covered = matches!(
                    &command.command,
                    Some(Command::AttributeSyncAck(ack))
                        if ack.store_name == previous.store_name
                            && previous.written_seq <= ack.acked_seq
                            && (ack.attribute_keys.is_empty()
                                || ack.attribute_keys.contains(&previous.attribute_key))
                );
                if !is_covered {
                    return Err(InvariantViolation::new(
                        "AS2-attribute-sync-ack",
                        format!(
                            "entry {}/{} at seq {} was deleted by {:?}",
                            previous.store_name,
                            previous.attribute_key,
                            previous.written_seq,
                            command.command
                        ),
                    ));
                }
            }
        }
    }
    Ok(())
}

/// AS1: unacknowledged synced changes have an entry at their seq or later. AS3: the index
/// matches the entries.
pub(crate) fn check_run_entries(
    context: &RunContext<'_>,
    run: &RunView,
    view: &FlowView,
    acks: &AttributeSyncAcks,
) -> Result<(), InvariantViolation> {
    let store_names: Vec<String> = run
        .record
        .as_ref()
        .and_then(|record| record.flow_config.as_ref())
        .and_then(|config| config.attribute_store_names.as_ref())
        .map(|names| names.names.clone())
        .unwrap_or_default();
    for (attribute_key, attribute) in &run.attributes {
        if !attribute.sync_config.is_some_and(|sync| sync.enabled) {
            continue;
        }
        for store_name in &store_names {
            let acked = [None, Some(attribute_key.clone())]
                .into_iter()
                .filter_map(|key| acks.get(&(*context.run_id, store_name.clone(), key)))
                .copied()
                .max()
                .unwrap_or(0);
            if attribute.written_seq <= acked {
                continue;
            }
            let entry = run
                .attribute_sync_entries
                .get(&(store_name.clone(), attribute_key.clone()));
            if entry.is_none_or(|entry| entry.written_seq < attribute.written_seq) {
                return Err(InvariantViolation::new(
                    "AS1-attribute-sync-entry",
                    format!(
                        "{context}: Attribute {attribute_key} written at seq {} has entry {entry:?} \
                         for store {store_name} (acked up to {acked})",
                        attribute.written_seq
                    ),
                ));
            }
        }
    }
    let entry_stores: BTreeSet<&str> = run
        .attribute_sync_entries
        .keys()
        .map(|(store_name, _)| store_name.as_str())
        .collect();
    let indexed_stores: BTreeSet<&str> = view
        .attribute_sync_pending_index
        .iter()
        .filter(|entry| entry.run_id == *context.run_id)
        .map(|entry| entry.store_name.as_str())
        .collect();
    if entry_stores != indexed_stores {
        return Err(InvariantViolation::new(
            "AS3-attribute-sync-index",
            format!(
                "{context}: entries name stores {entry_stores:?}, the index {indexed_stores:?}"
            ),
        ));
    }
    Ok(())
}

/// AS4: a pending close keeps its run RUNNING without active Steps or a Flow timeout. It has
/// outbox entries, or an admitted transactional RPC holds it.
pub(crate) fn check_pending_close(
    context: &RunContext<'_>,
    run: &RunView,
    is_held_by_rpcs: bool,
) -> Result<(), InvariantViolation> {
    let Some(record) = &run.record else {
        return Ok(());
    };
    let Some(pending_close) = &record.pending_close else {
        return Ok(());
    };
    let violation = |detail: String| {
        Err(InvariantViolation::new(
            "AS4-pending-close",
            format!("{context}: {detail} (pending close {pending_close:?})"),
        ))
    };
    if record.status != FlowStatus::Running as i32 || record.closed_at_micros != 0 {
        return violation(format!(
            "status {} with close time {}",
            record.status, record.closed_at_micros
        ));
    }
    if record.is_graceful_completion_requested {
        return violation("graceful completion is still requested".to_owned());
    }
    if run.attribute_sync_entries.is_empty() && !is_held_by_rpcs {
        return violation(
            "no outbox entry or transactional RPC is left to finalize the close".to_owned(),
        );
    }
    let active_step = run.step_executions.values().find(|step| {
        matches!(
            StepExecutionPhase::try_from(step.phase),
            Ok(StepExecutionPhase::WaitFor
                | StepExecutionPhase::Waiting
                | StepExecutionPhase::Execute)
        )
    });
    if let Some(step) = active_step {
        return violation(format!(
            "Step execution {} is still in phase {}",
            step.step_execution_id, step.phase
        ));
    }
    let has_flow_timeout = run.timers.iter().any(|(_, timer)| {
        matches!(
            timer.timer_id,
            Some(TimerId {
                kind: Some(timer_id::Kind::FlowTimeout(_)),
                ..
            })
        )
    });
    if has_flow_timeout {
        return violation("the Flow timeout is still armed".to_owned());
    }
    Ok(())
}

/// Natural closes that started waiting, and pending closes that finalized.
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq)]
pub struct CloseBarrierCounts {
    /// Natural closes that became pending closes.
    pub deferred: usize,
    /// Pending closes that a later command finalized.
    pub finalized: usize,
    /// Pending closes without outbox entries that an admitted transactional RPC held.
    pub held_by_rpcs: usize,
    /// Held pending closes that a later command finalized.
    pub finalized_after_rpcs: usize,
    /// Of the held pending closes, those that a skipped Flow timeout recorded.
    pub held_by_timer_skips: usize,
}

impl CloseBarrierCounts {
    /// Adds another count to this one.
    pub fn merge(&mut self, other: &Self) {
        self.deferred += other.deferred;
        self.finalized += other.finalized;
        self.held_by_rpcs += other.held_by_rpcs;
        self.finalized_after_rpcs += other.finalized_after_rpcs;
        self.held_by_timer_skips += other.held_by_timer_skips;
    }
}

/// AS5: a run closes naturally only once it has no outbox entry and its command carries no
/// in-flight transactional RPC. StopFlow and terminations close at once; time travel follows its
/// restored close. Every close records whether it closed at once. AS6: an unstamped command that
/// consults the barrier records a pending close that nothing holds.
///
/// Returns the natural closes this command deferred and finalized, updating which runs an
/// admitted transactional RPC holds.
pub(crate) fn check_close_barrier(
    command: &FlowCommand,
    is_applied: bool,
    time_travel_target: Option<TimeTravelTargetClose>,
    previous_records: &BTreeMap<RunId, RunRecord>,
    view: &FlowView,
    rpc_held_runs: &mut BTreeSet<RunId>,
) -> Result<CloseBarrierCounts, InvariantViolation> {
    let may_close_at_once = transactional_rpc_hold(command) != TransactionalRpcHold::MayClose;
    let consults_barrier = match command.command {
        Some(Command::PendingCloseCheck(_)) => true,
        Some(Command::AttributeSyncAck(_) | Command::RpcCommitted(_) | Command::TimeTravel(_)) => {
            is_applied
        }
        _ => false,
    };
    let mut counts = CloseBarrierCounts::default();
    for (run_id, previous) in previous_records {
        let Some(run) = view.runs.get(run_id) else {
            rpc_held_runs.remove(run_id);
            continue;
        };
        let Some(record) = &run.record else {
            continue;
        };
        if previous.pending_close.is_none() && record.pending_close.is_some() {
            counts.deferred += 1;
        }
        let is_unblocked_pending_close =
            record.pending_close.is_some() && run.attribute_sync_entries.is_empty();
        if is_unblocked_pending_close && command.is_transactional_rpc_in_flight {
            if rpc_held_runs.insert(*run_id) {
                counts.held_by_rpcs += 1;
                if matches!(command.command, Some(Command::TimerSkip(_))) {
                    counts.held_by_timer_skips += 1;
                }
            }
        } else if is_unblocked_pending_close && consults_barrier {
            return Err(InvariantViolation::new(
                "AS6-unheld-pending-close",
                format!(
                    "Flow {} run {run_id} kept a pending close with no outbox entry after {:?}",
                    command.flow_id, command.command
                ),
            ));
        }
        if record.pending_close.is_none() {
            let was_held = rpc_held_runs.remove(run_id);
            if was_held && record.status != FlowStatus::Running as i32 && !may_close_at_once {
                counts.finalized_after_rpcs += 1;
            }
        }
        let is_newly_closed = record.status != FlowStatus::Running as i32
            && (previous.status == FlowStatus::Running as i32
                || previous.current_attempt != record.current_attempt);
        if !is_newly_closed {
            continue;
        }
        let closes_without_barrier = expected_close_origin(command, time_travel_target, *run_id)?;
        let recorded_origin = record
            .close
            .as_ref()
            .and_then(|close| close.is_closed_at_once);
        if recorded_origin != Some(closes_without_barrier) {
            return Err(InvariantViolation::new(
                "AS5-close-origin",
                format!(
                    "Flow {} run {run_id} recorded close origin {recorded_origin:?} for {}; \
                     expected {closes_without_barrier}",
                    command.flow_id,
                    command_kind(command)
                ),
            ));
        }
        if closes_without_barrier {
            continue;
        }
        if command.is_transactional_rpc_in_flight {
            return Err(InvariantViolation::new(
                "AS5-natural-close-barrier",
                format!(
                    "Flow {} run {run_id} closed by {:?} while a transactional RPC was in flight",
                    command.flow_id, command.command
                ),
            ));
        }
        if !run.attribute_sync_entries.is_empty() {
            return Err(InvariantViolation::new(
                "AS5-natural-close-barrier",
                format!(
                    "Flow {} run {run_id} closed with status {} by {:?} while outbox entries {:?} \
                     were pending",
                    command.flow_id,
                    record.status,
                    command.command,
                    run.attribute_sync_entries.keys().collect::<Vec<_>>()
                ),
            ));
        }
        if previous.pending_close.is_some() {
            counts.finalized += 1;
        }
    }
    Ok(counts)
}

/// Whether AS5 expects the command's close to skip the barrier, independently of apply's
/// classification of a restored close.
///
/// Returns a violation when a TimeTravel close has no target that explains it.
fn expected_close_origin(
    command: &FlowCommand,
    time_travel_target: Option<TimeTravelTargetClose>,
    run_id: RunId,
) -> Result<bool, InvariantViolation> {
    if !matches!(command.command, Some(Command::TimeTravel(_))) {
        return Ok(transactional_rpc_hold(command) == TransactionalRpcHold::ClosesAtOnce);
    }
    let unexplained = |detail: &str| {
        InvariantViolation::new(
            "AS5-close-origin",
            format!("Flow {} run {run_id} {detail}", command.flow_id),
        )
    };
    match time_travel_target {
        // Reapplied input and a restored pending close only close naturally.
        Some(TimeTravelTargetClose::Running) => Ok(false),
        Some(TimeTravelTargetClose::Closed {
            is_closed_at_once: Some(is_closed_at_once),
        }) => Ok(is_closed_at_once),
        Some(TimeTravelTargetClose::Closed {
            is_closed_at_once: None,
        }) => Err(unexplained("restored a close that has no close origin")),
        None => Err(unexplained("closed by a TimeTravel without a valid target")),
    }
}

/// AS3: every pending index key of the Flow names a run that still has rows.
pub(crate) fn check_index_runs_exist(
    flow_id: &str,
    view: &FlowView,
) -> Result<(), InvariantViolation> {
    for entry in &view.attribute_sync_pending_index {
        if !view.runs.contains_key(&entry.run_id) {
            return Err(InvariantViolation::new(
                "AS3-attribute-sync-index",
                format!(
                    "Flow {flow_id}: store {} indexes run {}, which has no rows",
                    entry.store_name, entry.run_id
                ),
            ));
        }
    }
    Ok(())
}

fn decode(
    bytes: &[u8],
    invariant: &'static str,
) -> Result<AttributeSyncOutboxRecord, InvariantViolation> {
    AttributeSyncOutboxRecord::decode(bytes)
        .map_err(|error| InvariantViolation::new(invariant, error.to_string()))
}
