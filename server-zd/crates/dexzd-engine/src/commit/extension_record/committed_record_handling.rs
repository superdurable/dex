// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::Arc;

use dexzd_keyspace::PartitionId;
use dexzd_protocol::dexzd::v1::LogPosition;

use crate::command_applier::CommandApplier;
use crate::commit::extension_record::{
    CommittedRecordKind, ExtensionFrameApplier, ExtensionRecordApplier, ExtensionRecordSink,
    PublishedExtensionRecord,
};
use crate::recovery::CommittedCommandSink;

/// What a node applies committed log records with; the choice is fixed when it starts.
#[derive(Clone)]
#[non_exhaustive]
pub enum CommittedRecordHandling {
    /// server-zd's own Flow commands.
    FlowCommand(Arc<dyn CommandApplier>),
    /// An embedding application's extension records; its `ClusterConfig` names that application.
    ExtensionRecord(Arc<dyn ExtensionRecordApplier>),
}

/// Where a serving node publishes applied records; its kind matches the node's [`CommittedRecordHandling`].
#[derive(Clone)]
#[non_exhaustive]
pub enum CommittedRecordSink {
    /// Receives every applied Flow command.
    FlowCommand(Arc<dyn CommittedCommandSink>),
    /// Receives every written frame of extension records.
    ExtensionRecord(Arc<dyn ExtensionRecordSink>),
}

/// How a frame applier applies and publishes one node's records.
pub(crate) enum FrameRecordHandling {
    FlowCommand {
        applier: Arc<dyn CommandApplier>,
        committed_commands: Arc<dyn CommittedCommandSink>,
    },
    ExtensionRecord(ExtensionFrameApplier),
}

impl CommittedRecordHandling {
    pub(crate) fn record_kind(&self) -> CommittedRecordKind {
        match self {
            Self::FlowCommand(_) => CommittedRecordKind::FlowCommand,
            Self::ExtensionRecord(_) => CommittedRecordKind::ExtensionRecord,
        }
    }

    /// Pairs the applier with a serving node's sink.
    ///
    /// Returns `None` when `sink` publishes another kind of record than this applier applies.
    pub(crate) fn serving_frame_records(
        self,
        sink: CommittedRecordSink,
        serving_epoch: u64,
    ) -> Option<FrameRecordHandling> {
        match (self, sink) {
            (Self::FlowCommand(applier), CommittedRecordSink::FlowCommand(committed_commands)) => {
                Some(FrameRecordHandling::FlowCommand {
                    applier,
                    committed_commands,
                })
            }
            (Self::ExtensionRecord(applier), CommittedRecordSink::ExtensionRecord(sink)) => {
                Some(FrameRecordHandling::ExtensionRecord(
                    ExtensionFrameApplier::new(applier, sink, serving_epoch),
                ))
            }
            (Self::FlowCommand(_), CommittedRecordSink::ExtensionRecord(_))
            | (Self::ExtensionRecord(_), CommittedRecordSink::FlowCommand(_)) => None,
        }
    }

    /// Returns a hot standby's record handling; it publishes Flow commands to `committed_commands` only.
    pub(crate) fn standby_frame_records(
        &self,
        committed_commands: Arc<dyn CommittedCommandSink>,
    ) -> FrameRecordHandling {
        match self {
            Self::FlowCommand(applier) => FrameRecordHandling::FlowCommand {
                applier: applier.clone(),
                committed_commands,
            },
            // A standby runs no effects; the serving start rebuilds them.
            Self::ExtensionRecord(applier) => {
                FrameRecordHandling::ExtensionRecord(ExtensionFrameApplier::new(
                    applier.clone(),
                    Arc::new(IgnoredExtensionRecords),
                    u64::MAX,
                ))
            }
        }
    }
}

/// Drops a hot standby's published extension records and their outcomes.
struct IgnoredExtensionRecords;

impl ExtensionRecordSink for IgnoredExtensionRecords {
    fn publish_frame(
        &self,
        _partition: PartitionId,
        _position: &LogPosition,
        _is_replayed: bool,
        _records: &[PublishedExtensionRecord],
    ) {
    }
}
