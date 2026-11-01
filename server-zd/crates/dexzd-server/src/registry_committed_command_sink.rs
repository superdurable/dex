// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::Arc;

use dexzd_engine::{CommittedCommandSink, CommittedFlowCommand, FlowCoordinatorRegistry};

use crate::server_node::SearchOutboxWatermarks;

/// Forwards every committed command from the commit pipeline to the Flow coordinator registry.
pub(crate) struct RegistryCommittedCommandSink {
    registry: FlowCoordinatorRegistry,
    /// Sees SearchOutbox appends first, so read-your-writes waits cover a woken waiter's write.
    search_outbox_watermarks: Option<Arc<SearchOutboxWatermarks>>,
}

impl RegistryCommittedCommandSink {
    pub(crate) fn new(
        registry: FlowCoordinatorRegistry,
        search_outbox_watermarks: Option<Arc<SearchOutboxWatermarks>>,
    ) -> Self {
        Self {
            registry,
            search_outbox_watermarks,
        }
    }
}

impl CommittedCommandSink for RegistryCommittedCommandSink {
    fn apply_committed(&self, committed: CommittedFlowCommand) {
        if let Some(watermarks) = &self.search_outbox_watermarks {
            watermarks.record_committed(&committed);
        }
        self.registry.apply_committed(committed);
    }
}
