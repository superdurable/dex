// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use tokio::sync::{mpsc, watch};
use tokio::task::JoinHandle;

use super::DispatchEvent;

/// Receives ordered events and can request cancellation of an active Worker task.
pub struct DispatchHandle {
    pub(super) events: mpsc::Receiver<DispatchEvent>,
    pub(super) cancel: watch::Sender<bool>,
    pub(super) task: Option<JoinHandle<()>>,
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
}

impl Drop for DispatchHandle {
    fn drop(&mut self) {
        self.cancel();
        // The dispatch task owns its one-second cancellation grace period.
        drop(self.task.take());
    }
}
