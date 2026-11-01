// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The process's engine ID source, which remembers the holder ID of its latest lease attempt.

use std::sync::Mutex;

use dexzd_engine::{EngineIdSource, SystemEngineIdSource};
use uuid::Uuid;

/// [`SystemEngineIdSource`] that also remembers the holder process ID it gave out last.
#[derive(Debug, Default)]
pub(in crate::server_node) struct LeaseHolderRecordingIdSource {
    system: SystemEngineIdSource,
    last_lease_holder_process_id: Mutex<String>,
}

impl LeaseHolderRecordingIdSource {
    /// Returns the holder process ID of the latest lease attempt, empty before the first.
    pub(in crate::server_node) fn last_lease_holder_process_id(&self) -> String {
        self.last_lease_holder_process_id
            .lock()
            .expect("the holder ID lock is poisoned only if a holder panicked")
            .clone()
    }
}

impl EngineIdSource for LeaseHolderRecordingIdSource {
    fn command_id(&self) -> Uuid {
        self.system.command_id()
    }

    fn writer_process_id(&self) -> String {
        self.system.writer_process_id()
    }

    fn lineage_id(&self) -> Uuid {
        self.system.lineage_id()
    }

    fn run_id(&self) -> Uuid {
        self.system.run_id()
    }

    fn channel_message_id(&self) -> Uuid {
        self.system.channel_message_id()
    }

    fn hard_delete_object_id(&self) -> Uuid {
        self.system.hard_delete_object_id()
    }

    fn dispatch_task_id(&self) -> Uuid {
        self.system.dispatch_task_id()
    }

    fn store_lineage_id(&self) -> Uuid {
        self.system.store_lineage_id()
    }

    fn lease_holder_process_id(&self) -> String {
        let holder_process_id = self.system.lease_holder_process_id();
        self.last_lease_holder_process_id
            .lock()
            .expect("the holder ID lock is poisoned only if a holder panicked")
            .clone_from(&holder_process_id);
        holder_process_id
    }
}
