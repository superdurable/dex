// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Task queue store reads, run on the blocking pool so a slow store never stalls async tasks.

use std::sync::Arc;

use dexzd_apply::FlowStateReader;
use tokio::sync::{OwnedSemaphorePermit, Semaphore};

use crate::blocking_work::BlockingWorkFailure;
use crate::effects::{EffectContext, EffectError};
use crate::flow_coordination::FlowAddress;

/// The state a task queue read sees.
pub(crate) enum QueueReadView {
    /// Committed state only.
    Committed,
    /// The Flow's speculative state.
    Speculative(FlowAddress),
}

/// Runs synchronous store reads on the blocking pool with one permit of `slots`; the reader
/// opens on this task.
///
/// Returns the read's result, or [`EffectError::Stopped`] when the node stopped first. A panic
/// in `read` resumes on this task.
pub(crate) async fn read_off_task<T: Send + 'static>(
    context: &Arc<EffectContext>,
    slots: &Arc<Semaphore>,
    view: QueueReadView,
    read: impl FnOnce(&EffectContext, &dyn FlowStateReader) -> Result<T, EffectError> + Send + 'static,
) -> Result<T, EffectError> {
    let Some(read_slot) = acquire_read_slot(context, slots).await else {
        return Err(EffectError::Stopped);
    };
    read_with_slot(context, read_slot, view, read).await
}

/// Waits for one permit of `slots`.
///
/// Returns the permit, or `None` when the node stopped first.
pub(crate) async fn acquire_read_slot(
    context: &EffectContext,
    slots: &Arc<Semaphore>,
) -> Option<OwnedSemaphorePermit> {
    // Biased for reproducible simulation; shutdown fires once.
    tokio::select! {
        biased;
        () = context.shutdown.cancelled() => None,
        permit = Arc::clone(slots).acquire_owned() => {
            Some(permit.expect("the task queue read slots are never closed"))
        }
    }
}

/// Runs reads like [`read_off_task`] with a permit the caller already holds.
///
/// Returns the read's result, or [`EffectError::Stopped`] when the node stopped first.
pub(crate) async fn read_with_slot<T: Send + 'static>(
    context: &Arc<EffectContext>,
    read_slot: OwnedSemaphorePermit,
    view: QueueReadView,
    read: impl FnOnce(&EffectContext, &dyn FlowStateReader) -> Result<T, EffectError> + Send + 'static,
) -> Result<T, EffectError> {
    let reader = match &view {
        QueueReadView::Committed => context.flow_effect_operations.committed_state_reader()?,
        QueueReadView::Speculative(flow) => context
            .flow_effect_operations
            .flow_state_reader(flow, true)?,
    };
    let read_context = Arc::clone(context);
    let handle = context
        .config
        .blocking_work
        .run_tracked(&context.tracker, move || {
            let _read_slot = read_slot;
            read(&read_context, reader.as_ref())
        });
    match handle.await {
        Ok(result) => result,
        Err(BlockingWorkFailure::Panicked(payload)) => std::panic::resume_unwind(payload),
        Err(error @ BlockingWorkFailure::Cancelled) => {
            tracing::debug!(%error, "a task queue read did not run");
            Err(EffectError::Stopped)
        }
    }
}
