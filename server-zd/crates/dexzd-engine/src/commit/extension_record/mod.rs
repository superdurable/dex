// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Extension records: records of an application that embeds server-zd. server-zd stamps, routes,
//! orders, and acknowledges them, and never decodes their payload.

use std::any::Any;
use std::fmt;

use bytes::Bytes;
use dexzd_keyspace::FlowHash;
use dexzd_protocol::dexzd::v1::{ExtensionRecord, LogPosition, LogRecord, log_record};
use prost::Message;
use tokio::sync::oneshot;
use uuid::Uuid;

use crate::flow_coordination::CommitError;

#[cfg(test)]
mod extension_record_submit_tests;

/// The result that the embedding application's apply returns for one extension record.
///
/// server-zd never inspects it. It hands the value to the record's submitter through
/// [`ExtensionCommitted::outcome`], and the submitter downcasts it to the application's own type.
pub type ExtensionRecordOutcome = Box<dyn Any + Send>;

/// The values that server-zd stamps on one extension record before it enqueues the record.
///
/// The same values are in the record's log envelope. server-zd reads them back at apply time to
/// order the partition's commands and to find the record's waiting submitter.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct ExtensionStamp {
    /// Epoch of the node that stamped the record.
    pub epoch: u64,
    /// UUIDv7 that server-zd minted for the record.
    pub command_id: Uuid,
    /// Stamping wall clock in microseconds; strictly increasing within a partition.
    pub proposed_at_micros: i64,
    /// [`dexzd_keyspace::PartitionHasher`] hash of the record's routing key; it selects the
    /// partition.
    pub routing_hash: FlowHash,
}

/// An extension record that is committed and applied to the local store.
pub struct ExtensionCommitted {
    /// Log batch that holds the record.
    pub position: LogPosition,
    /// The values server-zd stamped on the record.
    pub stamp: ExtensionStamp,
    /// What the embedding application's apply returned for the record.
    pub outcome: ExtensionRecordOutcome,
}

impl fmt::Debug for ExtensionCommitted {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("ExtensionCommitted")
            .field("position", &self.position)
            .field("stamp", &self.stamp)
            .finish_non_exhaustive()
    }
}

/// Resolves once a submitted extension record is committed and applied to the local store.
#[derive(Debug)]
pub struct ExtensionCommitTicket {
    receiver: oneshot::Receiver<Result<ExtensionCommitted, CommitError>>,
}

/// Producer side of an [`ExtensionCommitTicket`], held by the commit pipeline.
#[derive(Debug)]
pub(crate) struct ExtensionCommitTicketSender {
    sender: oneshot::Sender<Result<ExtensionCommitted, CommitError>>,
}

impl ExtensionCommitTicket {
    pub(crate) fn new_pair() -> (ExtensionCommitTicketSender, Self) {
        let (sender, receiver) = oneshot::channel();
        (ExtensionCommitTicketSender { sender }, Self { receiver })
    }

    /// Waits for the commit result.
    ///
    /// Returns the committed record with its apply outcome, the pipeline's error, or
    /// [`CommitError::NodeStopping`] when the pipeline dropped the ticket without resolving it.
    pub async fn wait(self) -> Result<ExtensionCommitted, CommitError> {
        match self.receiver.await {
            Ok(result) => result,
            Err(_) => Err(CommitError::NodeStopping {
                detail: "commit pipeline dropped the extension commit ticket".to_owned(),
            }),
        }
    }
}

impl ExtensionCommitTicketSender {
    /// Resolves the ticket. A ticket whose waiter is gone is resolved silently.
    pub(crate) fn resolve(self, result: Result<ExtensionCommitted, CommitError>) {
        // Nobody waits; dropping the result also drops its outcome.
        if self.sender.send(result).is_err() {
            tracing::trace!("extension commit ticket waiter is gone");
        }
    }
}

/// Why [`crate::CommandCommitter::submit_extension_record`] enqueued nothing.
#[derive(Debug, Clone, PartialEq, Eq, thiserror::Error)]
#[non_exhaustive]
pub enum ExtensionSubmitError {
    /// The stamped record would exceed the largest record the node can commit. Never retry it.
    #[error(
        "extension record of up to {record_bytes} encoded bytes exceeds the limit of {limit_bytes} bytes"
    )]
    RecordTooLarge {
        /// Largest encoded size the record can have once stamped.
        record_bytes: usize,
        /// The smaller of the log's record limit and the node's unapplied byte budget.
        limit_bytes: usize,
    },
    /// The node commits Flow commands, so it accepts no extension record.
    #[error("the node commits Flow commands, so it accepts no extension record")]
    WrongRecordKind,
    /// The same failures as a Flow command submission.
    #[error(transparent)]
    Commit(#[from] CommitError),
}

/// Which kind of log record a node commits. Fixed when the node starts.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(crate) enum CommittedRecordKind {
    /// Flow commands of server-zd itself.
    FlowCommand,
    /// Extension records of an embedding application.
    ExtensionRecord,
}

/// Returns the encoded `LogRecord` length of an extension record holding `payload` once stamped
/// with the largest-encoding stamp values, so no real stamp makes the record longer.
pub(crate) fn largest_stamped_extension_record_bytes(payload: &Bytes) -> usize {
    LogRecord {
        partition: u32::MAX,
        body: Some(log_record::Body::ExtensionRecord(ExtensionRecord {
            epoch: u64::MAX,
            command_id: vec![u8::MAX; 16],
            // Negative varints take the full ten bytes.
            proposed_at_micros: -1,
            routing_hash: u64::MAX,
            payload: payload.clone(),
        })),
    }
    .encoded_len()
}
