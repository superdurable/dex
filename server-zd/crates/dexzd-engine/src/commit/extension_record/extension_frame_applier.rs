// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::panic::{AssertUnwindSafe, catch_unwind};
use std::sync::Arc;

use bytes::Bytes;
use dexzd_keyspace::{FlowHash, PartitionHasher, PartitionId};
use dexzd_protocol::dexzd::v1::{LogRecord, log_record};
use prost::Message;
use uuid::Uuid;

use crate::commit::commit_state::{
    CommitState, LEASE_VALIDITY_EXPIRED_DETAIL, NodeStopReason, PendingCommand,
    lease_validity_expired_error,
};
use crate::commit::extension_record::{
    ExtensionApplied, ExtensionCommitTicketSender, ExtensionCommitted, ExtensionRecordApplier,
    ExtensionRecordOutcome, ExtensionRecordSink, ExtensionStamp, PublishedExtensionRecord,
};
use crate::commit::partition_applier::PartitionFrame;
use crate::commit::staged_state_reader::StagedStateReader;

/// Applies and publishes the extension records of a node's partition frames.
pub(crate) struct ExtensionFrameApplier {
    applier: Arc<dyn ExtensionRecordApplier>,
    sink: Arc<dyn ExtensionRecordSink>,
    /// Frames of older epochs are replayed; a hot standby passes `u64::MAX`.
    serving_epoch: u64,
}

/// An applied extension record whose writes are staged in its frame's batch.
pub(crate) struct AppliedExtensionRecord {
    pub(crate) stamp: ExtensionStamp,
    outcome: ExtensionRecordOutcome,
    pending: Option<PendingCommand<ExtensionCommitTicketSender>>,
}

impl ExtensionFrameApplier {
    pub(crate) fn new(
        applier: Arc<dyn ExtensionRecordApplier>,
        sink: Arc<dyn ExtensionRecordSink>,
        serving_epoch: u64,
    ) -> Self {
        Self {
            applier,
            sink,
            serving_epoch,
        }
    }

    /// Applies the frame's records in order, staging each record's writes in `reader`.
    ///
    /// Returns the applied records, or the detail that halts the partition: an undecodable or
    /// misrouted record, a Flow command, an apply that halted or panicked, or a repeated apply
    /// whose writes differ while `is_checking_replay`.
    pub(crate) fn apply_records(
        &self,
        hasher: &PartitionHasher,
        frame: &PartitionFrame,
        reader: &mut StagedStateReader<'_>,
        is_checking_replay: bool,
    ) -> Result<Vec<AppliedExtensionRecord>, String> {
        let mut applied_records = Vec::with_capacity(frame.records.len());
        for record in &frame.records {
            let (stamp, payload) = decode_extension_record(hasher, frame.partition, record)?;
            let applied = self.apply_catching_panics(frame.partition, &stamp, &payload, reader)?;
            if is_checking_replay {
                let repeated =
                    self.apply_catching_panics(frame.partition, &stamp, &payload, reader)?;
                if repeated.writes != applied.writes {
                    return Err(format!(
                        "repeated apply of extension record {} at log seq {} returned different writes",
                        stamp.command_id, frame.position.seq
                    ));
                }
            }
            reader.stage(&applied.writes);
            applied_records.push(AppliedExtensionRecord {
                stamp,
                outcome: applied.outcome,
                pending: None,
            });
        }
        Ok(applied_records)
    }

    /// Takes the waiting tickets of `records`; call it under the frame's write admission.
    pub(crate) fn take_waiters(state: &CommitState, records: &mut [AppliedExtensionRecord]) {
        for record in records {
            record.pending = state.take_extension_record(record.stamp.command_id.as_bytes());
        }
    }

    /// Publishes a written frame to the sink, then resolves its waiting tickets with their outcomes.
    pub(crate) fn publish(
        &self,
        state: &CommitState,
        frame: &PartitionFrame,
        records: Vec<AppliedExtensionRecord>,
    ) {
        let mut published = Vec::with_capacity(records.len());
        let mut waiters = Vec::with_capacity(records.len());
        for AppliedExtensionRecord {
            stamp,
            outcome,
            pending,
        } in records
        {
            published.push(PublishedExtensionRecord {
                stamp,
                outcome,
                has_waiter: pending.is_some(),
            });
            waiters.push(pending);
        }
        // The sink sees the whole frame before any submitter, so reads after a commit observe it.
        self.sink.publish_frame(
            frame.partition,
            &frame.position,
            frame.epoch < self.serving_epoch,
            &published,
        );
        if waiters.iter().all(Option::is_none) {
            return;
        }
        // Durable either way, but an acknowledgement must not outlive the lease.
        if !state.is_lease_valid_now() {
            for pending in waiters.into_iter().flatten() {
                pending
                    .responder
                    .resolve(Err(lease_validity_expired_error()));
            }
            state.stop(NodeStopReason::LeaseLost {
                detail: LEASE_VALIDITY_EXPIRED_DETAIL.to_owned(),
            });
            return;
        }
        for (record, pending) in published.into_iter().zip(waiters) {
            if let Some(pending) = pending {
                pending.responder.resolve(Ok(ExtensionCommitted {
                    position: frame.position.clone(),
                    stamp: record.stamp,
                    outcome: record.outcome,
                }));
            }
        }
    }

    fn apply_catching_panics(
        &self,
        partition: PartitionId,
        stamp: &ExtensionStamp,
        payload: &[u8],
        reader: &StagedStateReader<'_>,
    ) -> Result<ExtensionApplied, String> {
        match catch_unwind(AssertUnwindSafe(|| {
            self.applier.apply_record(partition, stamp, payload, reader)
        })) {
            Ok(Ok(applied)) => Ok(applied),
            Ok(Err(halt)) => Err(format!(
                "apply of extension record {} failed: {halt}",
                stamp.command_id
            )),
            Err(_) => Err(format!(
                "apply of extension record {} panicked",
                stamp.command_id
            )),
        }
    }
}

/// Returns the record's stamp and payload, or the detail that halts the partition.
fn decode_extension_record(
    hasher: &PartitionHasher,
    partition: PartitionId,
    record: &Bytes,
) -> Result<(ExtensionStamp, Bytes), String> {
    let record = LogRecord::decode(record.clone())
        .map_err(|error| format!("undecodable log record: {error}"))?;
    let extension_record = match record.body {
        Some(log_record::Body::ExtensionRecord(extension_record)) => extension_record,
        Some(log_record::Body::Flow(_)) => {
            return Err("a node that applies extension records read a Flow command".to_owned());
        }
        None => return Err("log record has no command".to_owned()),
    };
    let routing_hash = FlowHash(extension_record.routing_hash);
    let record_partition = hasher.partition_of(routing_hash);
    if record.partition != u32::from(partition.0) || record_partition != partition {
        return Err(format!(
            "extension record of partition {} is in the frame of partition {}",
            record_partition.0, partition.0
        ));
    }
    let command_id = Uuid::from_slice(&extension_record.command_id)
        .map_err(|error| format!("extension record has an invalid command ID: {error}"))?;
    let stamp = ExtensionStamp {
        epoch: extension_record.epoch,
        command_id,
        proposed_at_micros: extension_record.proposed_at_micros,
        routing_hash,
    };
    Ok((stamp, extension_record.payload))
}
