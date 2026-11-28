// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::fmt;

use dexzd_keyspace::PartitionId;
use dexzd_protocol::dexzd::v1::LogPosition;

use crate::commit::extension_record::{ExtensionRecordOutcome, ExtensionStamp};

/// Receives each written frame of extension records before their submitters do; see the crate README.
pub trait ExtensionRecordSink: Send + Sync + 'static {
    /// Publishes one written frame in log order; must not block, submit, or wait for a commit.
    ///
    /// `is_replayed` is true for frames of an earlier epoch, which recovery replays before the
    /// partition's rebuild; their records have no waiter.
    fn publish_frame(
        &self,
        partition: PartitionId,
        position: &LogPosition,
        is_replayed: bool,
        records: &[PublishedExtensionRecord],
    );
}

/// One applied extension record of a published frame.
pub struct PublishedExtensionRecord {
    /// The values server-zd stamped on the record.
    pub stamp: ExtensionStamp,
    /// The applier's outcome; server-zd moves it to the waiting ticket after publishing.
    pub outcome: ExtensionRecordOutcome,
    /// Whether a submitter of this process waits for the record.
    pub has_waiter: bool,
}

impl fmt::Debug for PublishedExtensionRecord {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("PublishedExtensionRecord")
            .field("stamp", &self.stamp)
            .field("has_waiter", &self.has_waiter)
            .finish_non_exhaustive()
    }
}
