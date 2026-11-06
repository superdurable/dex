// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::time::Duration;

use dexzd_metrics::StepStreamWriteDropReason;
use dexzd_protocol::dex::{
    PollWorkerTaskFrame, StepStreamWrite, Value, invoke_execute_method_output,
    invoke_wait_for_method_output, poll_worker_task_frame,
};
use dexzd_stream_store::StreamStoreError;
use tokio::time::Instant;

/// A checkpoint change carried by a Worker frame: `Some(value)` sets it, `None` clears it.
pub(super) type CheckpointChange = Option<Value>;

/// Returns the checkpoint change of an explicit heartbeat; stream writes keep the checkpoint.
pub(super) fn heartbeat_change(frame: &PollWorkerTaskFrame) -> Option<CheckpointChange> {
    match frame.output.as_ref()? {
        poll_worker_task_frame::Output::WaitFor(output) => match output.output.as_ref()? {
            invoke_wait_for_method_output::Output::Heartbeat(heartbeat) => {
                Some(heartbeat.value.clone())
            }
            _ => None,
        },
        poll_worker_task_frame::Output::Execute(output) => match output.output.as_ref()? {
            invoke_execute_method_output::Output::Heartbeat(heartbeat) => {
                Some(heartbeat.value.clone())
            }
            _ => None,
        },
        _ => None,
    }
}

/// Returns the Step Stream write that a Worker frame carries.
pub(super) fn into_step_stream_write(frame: PollWorkerTaskFrame) -> Option<StepStreamWrite> {
    match frame.output? {
        poll_worker_task_frame::Output::WaitFor(output) => match output.output? {
            invoke_wait_for_method_output::Output::StreamWrite(write) => Some(write),
            _ => None,
        },
        poll_worker_task_frame::Output::Execute(output) => match output.output? {
            invoke_execute_method_output::Output::StreamWrite(write) => Some(write),
            _ => None,
        },
        _ => None,
    }
}

/// Returns the metric label of the store error that dropped a Step Stream write.
pub(super) fn step_stream_write_drop_reason(error: &StreamStoreError) -> StepStreamWriteDropReason {
    match error {
        StreamStoreError::Disabled => StepStreamWriteDropReason::Disabled,
        StreamStoreError::InvalidArgument(_)
        | StreamStoreError::InvalidResumeToken
        | StreamStoreError::InvalidConfig(_) => StepStreamWriteDropReason::InvalidArgument,
        StreamStoreError::MessageTooLarge => StepStreamWriteDropReason::MessageTooLarge,
        StreamStoreError::CapacityExceeded => StepStreamWriteDropReason::StreamCapacityExceeded,
        StreamStoreError::TotalCapacityExceeded => StepStreamWriteDropReason::TotalCapacityExceeded,
        StreamStoreError::Closed | StreamStoreError::TrimWorkerFailed => {
            StepStreamWriteDropReason::Closed
        }
    }
}

/// Coalesces checkpoint changes within the batch window and proposes only real changes.
pub(super) struct CheckpointTracker {
    pub(super) last_proposed: CheckpointChange,
    pub(super) pending: Option<CheckpointChange>,
    pub(super) flush_at: Option<Instant>,
}

impl CheckpointTracker {
    pub(super) fn observe(&mut self, change: CheckpointChange, batch_window: Duration) {
        if change == self.last_proposed {
            self.pending = None;
            self.flush_at = None;
            return;
        }
        self.pending = Some(change);
        self.flush_at
            .get_or_insert_with(|| Instant::now() + batch_window);
    }

    pub(super) fn take_pending(&mut self) -> Option<CheckpointChange> {
        self.flush_at = None;
        let change = self.pending.take()?;
        self.last_proposed = change.clone();
        Some(change)
    }
}
