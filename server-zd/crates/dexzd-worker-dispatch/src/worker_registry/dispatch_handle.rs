// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The receiving end of one dispatch: its ordered events, cancellation, and delivery report.

use std::sync::Arc;
use std::sync::atomic::{AtomicBool, Ordering};

use tokio::sync::{mpsc, watch};
use tokio::task::JoinHandle;

use super::DispatchEvent;

/// Receives ordered events and can request cancellation of an active Worker task.
pub struct DispatchHandle {
    pub(super) events: mpsc::Receiver<DispatchEvent>,
    pub(super) cancel: watch::Sender<bool>,
    pub(super) task: Option<JoinHandle<()>>,
    /// Set once a task frame went to a Worker, so delivery is known even without `Assigned`.
    pub(super) delivered: Arc<AtomicBool>,
}

impl DispatchHandle {
    /// Returns the next Worker output, assignment change, or the single final outcome, then
    /// returns `None`.
    pub async fn next(&mut self) -> Option<DispatchEvent> {
        self.events.recv().await
    }

    /// Sends cancel_task for an assigned task, then allows one second for its final frame.
    pub fn cancel(&self) {
        self.cancel.send_replace(true);
    }

    /// Cancels the dispatch and waits until it can no longer send the task to any Worker.
    ///
    /// Returns true when any Worker received the task, including one whose `Assigned` event never
    /// reached this handle. Waiting can last the one-second cancellation grace period.
    pub async fn finish(&mut self) -> bool {
        self.cancel();
        // Draining keeps the dispatch task from blocking on a full event channel.
        while self.events.recv().await.is_some() {}
        if let Some(task) = self.task.take()
            && let Err(error) = task.await
        {
            tracing::warn!(%error, "dispatch task ended abnormally");
        }
        self.delivered.load(Ordering::SeqCst)
    }
}

impl Drop for DispatchHandle {
    fn drop(&mut self) {
        self.cancel();
        // The dispatch task owns its one-second cancellation grace period.
        drop(self.task.take());
    }
}
