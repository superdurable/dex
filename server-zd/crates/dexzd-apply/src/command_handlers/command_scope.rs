// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use dexzd_keyspace::{RunId, TenantId};
use dexzd_protocol::dex::FlowHistoryEvent;
use dexzd_protocol::dexzd::v1::{CommandReply, FlowCommand};

use crate::projections::history_event_links::PendingHistoryEvents;
use crate::values::ValueIdAllocator;
use crate::{ApplyEffect, ApplyOutcome, BehaviorVersion, StateWrite};

/// Read-only facts of the command being applied.
pub(crate) struct CommandScope<'command> {
    pub(crate) command: &'command FlowCommand,
    pub(crate) behavior_version: BehaviorVersion,
    pub(crate) tenant_id: TenantId,
    /// The only time apply may use.
    pub(crate) now_micros: i64,
    /// Time travel replays a recorded command to rebuild state; it records no history.
    pub(crate) is_replay: bool,
    /// What decides whether a natural close waits for the Attribute Sync outbox.
    pub(crate) close_barrier_source: CloseBarrierSource,
    /// Time travel re-marks synced Attributes after reapplied input, so a reapplied natural close
    /// waits.
    pub(crate) has_pending_time_travel_remarks: bool,
    /// A rebuild's recorded events; their IDs resolve the replayed command's event links.
    pub(crate) recorded_events: &'command [FlowHistoryEvent],
}

/// What decides whether a natural close waits for the Attribute Sync outbox.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(crate) enum CloseBarrierSource {
    /// Live apply waits while the run has outbox entries.
    Outbox,
    /// A rebuild repeats the recorded command, which closed the run if its history has FlowClosed.
    RecordedHistory { has_flow_closed: bool },
}

impl<'command> CommandScope<'command> {
    pub(crate) fn new(command: &'command FlowCommand, behavior_version: BehaviorVersion) -> Self {
        Self {
            command,
            behavior_version,
            tenant_id: TenantId(command.tenant_id),
            now_micros: command.proposed_at_micros,
            is_replay: false,
            close_barrier_source: CloseBarrierSource::Outbox,
            has_pending_time_travel_remarks: false,
            recorded_events: &[],
        }
    }

    pub(crate) fn is_history_rebuild(&self) -> bool {
        matches!(
            self.close_barrier_source,
            CloseBarrierSource::RecordedHistory { .. }
        )
    }

    pub(crate) fn flow_id(&self) -> &str {
        &self.command.flow_id
    }
}

/// What a handler produced besides state writes.
pub(crate) struct HandlerOutput {
    pub(crate) reply: CommandReply,
    pub(crate) effects: Vec<ApplyEffect>,
    /// The run the command applied to or created.
    pub(crate) run_id: Option<RunId>,
    /// Replay only: what TimeTravel folds into its own command when reapplying input.
    pub(crate) replayed: Option<ReplayedCommandOutput>,
}

/// History a replayed command did not record, and the counters the next input continues.
pub(crate) struct ReplayedCommandOutput {
    pub(crate) events: PendingHistoryEvents,
    pub(crate) counters: ReplayedCommandCounters,
}

/// Per-command counters that inputs reapplied within one TimeTravel command share.
#[derive(Clone)]
pub(crate) struct ReplayedCommandCounters {
    pub(crate) values: ValueIdAllocator,
    pub(crate) next_channel_message_index: u64,
}

impl HandlerOutput {
    pub(crate) fn into_outcome(self, writes: Vec<StateWrite>) -> ApplyOutcome {
        ApplyOutcome {
            writes,
            effects: self.effects,
            reply: self.reply,
            run_id: self.run_id,
        }
    }
}
