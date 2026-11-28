// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::fmt;

use dexzd_apply::{FlowStateReader, StateWrite};
use dexzd_keyspace::PartitionId;

use crate::commit::extension_record::{ExtensionRecordOutcome, ExtensionStamp};

/// Applies an embedding application's extension records; see the crate README's calling contract.
pub trait ExtensionRecordApplier: Send + Sync + 'static {
    /// Applies one extension record to committed state plus the writes staged before it.
    ///
    /// Returns the record's writes and outcome. Returns [`ExtensionApplyHalt`] when the record
    /// or state breaks an invariant; the partition then stops applying.
    fn apply_record(
        &self,
        partition: PartitionId,
        stamp: &ExtensionStamp,
        payload: &[u8],
        state: &dyn FlowStateReader,
    ) -> Result<ExtensionApplied, ExtensionApplyHalt>;
}

/// What applying one extension record wrote and returned.
pub struct ExtensionApplied {
    /// Writes that commit atomically with the record's frame.
    pub writes: Vec<StateWrite>,
    /// Opaque to server-zd; the sink reads it and the submitter downcasts it.
    pub outcome: ExtensionRecordOutcome,
}

impl fmt::Debug for ExtensionApplied {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("ExtensionApplied")
            .field("writes", &self.writes)
            .finish_non_exhaustive()
    }
}

/// An extension record that must stop its partition from applying.
#[derive(Debug, Clone, PartialEq, Eq, thiserror::Error)]
#[error("{detail}")]
pub struct ExtensionApplyHalt {
    /// Which invariant failed, for the partition's halt log.
    pub detail: String,
}
