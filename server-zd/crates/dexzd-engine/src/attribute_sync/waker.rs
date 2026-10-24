// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::BTreeSet;
use std::sync::{Arc, Mutex};

use dexzd_keyspace::PartitionId;
use tokio::sync::Notify;

/// Coalesces wake-ups of partitions with new Attribute Sync entries. Cheap to clone.
///
/// Wake-ups before the projector starts are kept; the projector scans every partition at start.
#[derive(Clone, Default)]
pub struct AttributeSyncWaker {
    state: Arc<WakerState>,
}

#[derive(Default)]
struct WakerState {
    woken_partitions: Mutex<BTreeSet<PartitionId>>,
    notify: Notify,
}

impl AttributeSyncWaker {
    /// Creates a waker with no woken partitions.
    pub fn new() -> Self {
        Self::default()
    }

    /// Marks `partition` for the projector's next cycle.
    pub(crate) fn wake_partition(&self, partition: PartitionId) {
        self.state
            .woken_partitions
            .lock()
            .expect("the woken partition lock is poisoned only if a holder panicked")
            .insert(partition);
        self.state.notify.notify_one();
    }

    /// Returns and clears the woken partitions, in partition order.
    pub(super) fn take_woken_partitions(&self) -> BTreeSet<PartitionId> {
        std::mem::take(
            &mut *self
                .state
                .woken_partitions
                .lock()
                .expect("the woken partition lock is poisoned only if a holder panicked"),
        )
    }

    /// Waits for a wake-up; one that arrived since the last wait returns at once.
    pub(super) async fn woken(&self) {
        self.state.notify.notified().await;
    }
}
