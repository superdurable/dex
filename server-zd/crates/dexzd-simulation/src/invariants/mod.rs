// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! State invariants checked after every applied command (plan sections 8 and 10).

mod attribute_sync_invariants;
mod directory_invariants;
mod dispatch_queue_invariants;
mod history_migration_invariants;
mod history_segment_invariants;
mod local_cleanup_invariants;
mod lock_invariants;
mod search_order_invariants;
mod value_invariants;

use std::collections::{BTreeMap, BTreeSet};
use std::fmt;

use dexzd_keyspace::{FlowHash, RunId, ValueId};

use crate::memory_state::MemoryFlowState;
use crate::state_view::FlowView;

pub use attribute_sync_invariants::CloseBarrierCounts;
pub(crate) use attribute_sync_invariants::{
    AttributeSyncAcks, check_close_barrier, check_outbox_writes, remember_ack,
};
pub use history_migration_invariants::HistoryMigrationCounts;
pub(crate) use history_migration_invariants::check_history_migration;
pub use history_segment_invariants::LocalOffloadCounts;
pub(crate) use history_segment_invariants::{
    OffloadGapsByRun, SealedSegmentEncodings, check_deletions_were_uploaded,
    check_open_segment_transitions, check_sealed_segment_encodings, check_value_offloads,
    collect_value_offloads,
};
pub use lock_invariants::check_fresh_snapshot;
pub(crate) use value_invariants::{offloaded_value_row, remember_window_points};

/// Requires that no run of the Flow keeps doable local cleanup once none is due (H17).
pub(crate) fn check_local_cleanup_drained(
    flow_id: &str,
    view: &FlowView,
) -> Result<(), InvariantViolation> {
    for (run_id, run) in view.runs.iter().filter(|(_, run)| run.is_complete) {
        let context = RunContext { flow_id, run_id };
        local_cleanup_invariants::check_cleanup_drained(&context, run, view)?;
    }
    Ok(())
}

pub(crate) fn check_window_value_content(
    flow_id: &str,
    view: &FlowView,
    memory: &CheckerMemory,
    state: &MemoryFlowState,
    flow_hash: FlowHash,
) -> Result<(), InvariantViolation> {
    for (run_id, run) in view.runs.iter().filter(|(_, run)| run.is_complete) {
        let context = RunContext { flow_id, run_id };
        value_invariants::check_window_value_content(
            &context,
            run,
            memory.window_points.get(run_id),
            state,
            flow_hash,
            &memory.sealed_segment_encodings,
        )?;
    }
    Ok(())
}

/// A broken invariant: a stable name for grouping and a description of the evidence.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct InvariantViolation {
    /// Stable name of the invariant, such as `L3-lock-holder`.
    pub invariant: &'static str,
    /// What was observed.
    pub detail: String,
}

impl InvariantViolation {
    /// Creates a violation of `invariant`.
    pub fn new(invariant: &'static str, detail: impl Into<String>) -> Self {
        Self {
            invariant,
            detail: detail.into(),
        }
    }
}

impl fmt::Display for InvariantViolation {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(formatter, "{}: {}", self.invariant, self.detail)
    }
}

/// Facts the checker learned from earlier commands that state alone does not show.
#[derive(Debug, Clone, Default)]
pub struct CheckerMemory {
    /// Values each run pinned with the genesis holder when it started (plan D12).
    pub genesis_values: BTreeMap<RunId, BTreeSet<ValueId>>,
    /// Value references observed when each semantic point was current, retained through its window.
    pub(crate) window_points: BTreeMap<RunId, BTreeMap<u64, WindowPointState>>,
    /// Applied Attribute Sync acks, which may delete entries up to their seqs.
    pub(crate) attribute_sync_acks: attribute_sync_invariants::AttributeSyncAcks,
    /// Runs whose pending close without outbox entries an admitted transactional RPC holds.
    pub(crate) rpc_held_runs: BTreeSet<RunId>,
    /// Encoding of each history segment when the checker first saw it SEALED, kept after its
    /// upload as its object.
    pub(crate) sealed_segment_encodings: history_segment_invariants::SealedSegmentEncodings,
    /// The seqs and events that commands applied while offload was off took out of each run's
    /// segment chain.
    pub(crate) history_offload_gaps: history_segment_invariants::OffloadGapsByRun,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub(crate) struct WindowPointState {
    pub(crate) references: BTreeSet<WindowValueReference>,
    pub(crate) attribute_values: BTreeSet<ValueId>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord)]
pub(crate) struct WindowValueReference {
    pub(crate) value_id: ValueId,
    pub(crate) size_bytes: u64,
    pub(crate) content_hash: u64,
}

/// Attribute keys that only lock-holding attempts write, for the lost-update check (L2).
pub const GUARDED_ATTRIBUTE_KEYS: &[&str] = &["counter"];

/// Checks the directory against every run record, and every invariant of completely decoded runs.
///
/// Returns the first violation found.
pub fn check_flow(
    flow_id: &str,
    view: &FlowView,
    memory: &CheckerMemory,
) -> Result<(), InvariantViolation> {
    directory_invariants::check_directory(flow_id, view)?;
    attribute_sync_invariants::check_index_runs_exist(flow_id, view)?;
    search_order_invariants::check_search_order(flow_id, view)?;
    dispatch_queue_invariants::check_dispatch_queue_runs_exist(flow_id, view)?;
    local_cleanup_invariants::check_cleanup_index_runs_exist(flow_id, view)?;
    for (run_id, run) in view.runs.iter().filter(|(_, run)| run.is_complete) {
        let context = RunContext { flow_id, run_id };
        directory_invariants::check_run_record(&context, run)?;
        search_order_invariants::check_active_step_types(&context, run)?;
        directory_invariants::check_time_travel_staging(&context, run)?;
        directory_invariants::check_history(&context, run)?;
        directory_invariants::check_timers(&context, run, view)?;
        lock_invariants::check_invocations(&context, run)?;
        dispatch_queue_invariants::check_dispatch_queue_rows(&context, run, view)?;
        lock_invariants::check_locks(&context, run)?;
        lock_invariants::check_guarded_attributes(&context, run)?;
        let genesis = memory.genesis_values.get(run_id);
        value_invariants::check_values(&context, run, genesis, memory.window_points.get(run_id))?;
        attribute_sync_invariants::check_run_entries(
            &context,
            run,
            view,
            &memory.attribute_sync_acks,
        )?;
        attribute_sync_invariants::check_pending_close(
            &context,
            run,
            memory.rpc_held_runs.contains(run_id),
        )?;
        history_segment_invariants::check_segment_chain(
            &context,
            run,
            view,
            memory.history_offload_gaps.get(run_id),
        )?;
        history_segment_invariants::check_window_frontier(&context, run)?;
        history_segment_invariants::check_segment_expiry(&context, run, view)?;
        local_cleanup_invariants::check_cleanup_index(&context, run, view)?;
    }
    Ok(())
}

/// Identifies the run being checked in violation messages.
pub(crate) struct RunContext<'check> {
    pub(crate) flow_id: &'check str,
    pub(crate) run_id: &'check RunId,
}

impl fmt::Display for RunContext<'_> {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(formatter, "Flow {} run {}", self.flow_id, self.run_id)
    }
}
