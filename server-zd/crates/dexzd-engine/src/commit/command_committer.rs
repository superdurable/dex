// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::{Arc, Mutex};

use bytes::Bytes;
use dexzd_apply::{ApplyContext, ApplyOutcome, FlowStateReader};
use dexzd_clock::WallClock;
use dexzd_keyspace::{FlowHash, PartitionHasher, PartitionId};
use dexzd_protocol::dexzd::v1::{ExtensionRecord, FlowCommand, LogRecord, log_record};
use prost::Message;
use tokio::sync::{OwnedSemaphorePermit, Semaphore, mpsc};
use uuid::Uuid;

use crate::commit::EngineLocalStore;
use crate::commit::commit_state::{
    CommitState, NodeStopReason, PendingCommand, PendingCommit, partition_stopped_error,
};
use crate::commit::committed_state_view::CommittedStateView;
use crate::commit::extension_record::{
    CommittedRecordKind, ExtensionCommitTicket, ExtensionStamp, ExtensionSubmitError,
    largest_stamped_extension_record_bytes,
};
use crate::commit::replay_checks::{ReplayMismatch, compare_speculative_outcome};
use crate::engine_id_source::EngineIdSource;
use crate::flow_coordination::{
    CommitError, CommitTicket, CommitUrgency, CommittedCommand, FlowCommandCommitter,
};

pub(crate) struct LogProposal {
    pub(crate) partition: PartitionId,
    pub(crate) record: Bytes,
    pub(crate) should_wait_for_commit: bool,
}

/// Stamps commands and submits them to the commit pipeline; shared by every Flow runtime.
#[derive(Clone)]
pub struct CommandCommitter {
    inner: Arc<CommitterInner>,
}

struct CommitterInner {
    state: Arc<CommitState>,
    store: Arc<EngineLocalStore>,
    clock: Arc<dyn WallClock>,
    id_source: Arc<dyn EngineIdSource>,
    epoch: u64,
    behavior_version: u32,
    admission: Arc<Semaphore>,
    /// Each partition's last stamped proposed_at; its lock orders that partition's stamps and
    /// enqueues.
    stamp_watermarks: Vec<Mutex<i64>>,
    proposals: mpsc::UnboundedSender<LogProposal>,
    is_checking_replay: bool,
    hasher: PartitionHasher,
    record_kind: CommittedRecordKind,
    /// A larger record never commits: the log rejects it or it never fits the unapplied budget.
    extension_record_limit_bytes: usize,
}

pub(crate) struct CommandCommitterParts {
    pub(crate) state: Arc<CommitState>,
    pub(crate) store: Arc<EngineLocalStore>,
    pub(crate) clock: Arc<dyn WallClock>,
    pub(crate) id_source: Arc<dyn EngineIdSource>,
    pub(crate) epoch: u64,
    pub(crate) behavior_version: u32,
    pub(crate) max_unapplied_bytes: u32,
    pub(crate) is_checking_replay: bool,
    pub(crate) proposals: mpsc::UnboundedSender<LogProposal>,
    /// Derives an extension record's partition from its routing hash.
    pub(crate) hasher: PartitionHasher,
    /// The only kind of record the committer accepts.
    pub(crate) record_kind: CommittedRecordKind,
}

/// The values stamped on one record under its partition's stamp lock.
struct RecordStamp {
    epoch: u64,
    command_id: Uuid,
    proposed_at_micros: i64,
}

/// A record encoded with its final stamp, ready to enqueue.
struct EncodedRecord<Stamped> {
    stamped: Stamped,
    record: Bytes,
    /// The watermark the partition advances to once the record is enqueued.
    proposed_at_micros: i64,
}

impl CommandCommitter {
    pub(crate) fn new(parts: CommandCommitterParts) -> Self {
        let partition_count = parts.state.partition_count() as usize;
        let max_unapplied_bytes = parts.max_unapplied_bytes as usize;
        Self {
            inner: Arc::new(CommitterInner {
                state: parts.state,
                store: parts.store,
                clock: parts.clock,
                id_source: parts.id_source,
                epoch: parts.epoch,
                behavior_version: parts.behavior_version,
                admission: Arc::new(Semaphore::new(max_unapplied_bytes)),
                stamp_watermarks: (0..partition_count).map(|_| Mutex::new(0)).collect(),
                proposals: parts.proposals,
                is_checking_replay: parts.is_checking_replay,
                hasher: parts.hasher,
                record_kind: parts.record_kind,
                extension_record_limit_bytes: dexzd_log::MAX_RECORD_LENGTH.min(max_unapplied_bytes),
            }),
        }
    }

    /// Raises each partition's stamp watermark to its applied command timestamp.
    pub(crate) fn seed_stamp_watermarks(&self, applied_micros: &[(PartitionId, i64)]) {
        for (partition, micros) in applied_micros {
            let mut watermark = self.lock_stamp_watermark(*partition);
            *watermark = (*watermark).max(*micros);
        }
    }

    /// Stamps the command (epoch, behavior version from the version barrier, command ID,
    /// proposed_at_micros = max(wall clock, partition watermark + 1)) and enqueues it in the
    /// commit pipeline in one step, so log order equals stamp order within a partition.
    /// Returns the stamped command (apply it to the speculative overlay) and a ticket that
    /// resolves once the command is committed AND applied to the local store.
    ///
    /// Fails without enqueueing with [`CommitError::Unavailable`] while the node recovers,
    /// when the unapplied budget is full, or when the partition stopped, and with
    /// [`CommitError::NodeStopping`] after the node stopped.
    pub fn submit(
        &self,
        context: ApplyContext,
        command: FlowCommand,
        urgency: CommitUrgency,
    ) -> Result<(FlowCommand, CommitTicket), CommitError> {
        self.submit_with_final_stamp(context, command, urgency, &mut |_| {})
    }

    /// Like [`Self::submit`], but calls `finish_stamp` with the stamped command under the
    /// partition's stamp lock, before the command is encoded and enqueued. Other partitions keep
    /// stamping meanwhile.
    ///
    /// Returns what [`Self::submit`] returns.
    pub fn submit_with_final_stamp(
        &self,
        context: ApplyContext,
        mut command: FlowCommand,
        urgency: CommitUrgency,
        finish_stamp: &mut dyn FnMut(&mut FlowCommand),
    ) -> Result<(FlowCommand, CommitTicket), CommitError> {
        let inner = &*self.inner;
        // Enqueueing it would write a record that halts the partition at apply.
        if inner.record_kind != CommittedRecordKind::FlowCommand {
            return Err(CommitError::Unavailable {
                detail: "the node commits extension records, so it accepts no Flow command"
                    .to_owned(),
            });
        }
        inner.state.check_serving()?;
        let partition = context.partition;
        assert!(
            u32::from(partition.0) < inner.state.partition_count(),
            "the runtime computes partitions from the cluster's partition count"
        );
        self.stamp_and_enqueue(
            partition,
            urgency,
            |stamp| {
                command.epoch = stamp.epoch;
                command.behavior_version = inner.behavior_version;
                command.command_id = stamp.command_id.as_bytes().to_vec();
                command.proposed_at_micros = stamp.proposed_at_micros;
                finish_stamp(&mut command);
                let record = Bytes::from(
                    LogRecord {
                        partition: u32::from(partition.0),
                        body: Some(log_record::Body::Flow(command.clone())),
                    }
                    .encode_to_vec(),
                );
                let proposed_at_micros = command.proposed_at_micros;
                EncodedRecord {
                    stamped: command,
                    record,
                    proposed_at_micros,
                }
            },
            |admission| {
                let (responder, ticket) = CommitTicket::new_pair();
                let pending = PendingCommit::FlowCommand(PendingCommand {
                    responder,
                    _admission: admission,
                });
                (pending, ticket)
            },
        )
    }

    /// Stamps and enqueues by `routing_hash`, returning its stamp and ticket;
    /// see [submission constraints](https://github.com/superdurable/dex/blob/main/server-zd/crates/dexzd-engine/README.md#extension-record-submission).
    pub fn submit_extension_record(
        &self,
        routing_hash: FlowHash,
        payload: Bytes,
        urgency: CommitUrgency,
    ) -> Result<(ExtensionStamp, ExtensionCommitTicket), ExtensionSubmitError> {
        let inner = &*self.inner;
        if inner.record_kind != CommittedRecordKind::ExtensionRecord {
            return Err(ExtensionSubmitError::WrongRecordKind);
        }
        let record_bytes = largest_stamped_extension_record_bytes(&payload);
        if record_bytes > inner.extension_record_limit_bytes {
            return Err(ExtensionSubmitError::RecordTooLarge {
                record_bytes,
                limit_bytes: inner.extension_record_limit_bytes,
            });
        }
        inner.state.check_serving()?;
        let partition = inner.hasher.partition_of(routing_hash);
        let stamped = self.stamp_and_enqueue(
            partition,
            urgency,
            |stamp| {
                let record = Bytes::from(
                    LogRecord {
                        partition: u32::from(partition.0),
                        body: Some(log_record::Body::ExtensionRecord(ExtensionRecord {
                            epoch: stamp.epoch,
                            command_id: stamp.command_id.as_bytes().to_vec(),
                            proposed_at_micros: stamp.proposed_at_micros,
                            routing_hash: routing_hash.0,
                            payload,
                        })),
                    }
                    .encode_to_vec(),
                );
                EncodedRecord {
                    stamped: ExtensionStamp {
                        epoch: stamp.epoch,
                        command_id: stamp.command_id,
                        proposed_at_micros: stamp.proposed_at_micros,
                        routing_hash,
                    },
                    record,
                    proposed_at_micros: stamp.proposed_at_micros,
                }
            },
            |admission| {
                let (responder, ticket) = ExtensionCommitTicket::new_pair();
                let pending = PendingCommit::ExtensionRecord(PendingCommand {
                    responder,
                    _admission: admission,
                });
                (pending, ticket)
            },
        )?;
        Ok(stamped)
    }

    /// Encodes and enqueues under the partition stamp lock; advances the watermark only after enqueueing.
    fn stamp_and_enqueue<Stamped, Ticket>(
        &self,
        partition: PartitionId,
        urgency: CommitUrgency,
        encode: impl FnOnce(RecordStamp) -> EncodedRecord<Stamped>,
        new_pending: impl FnOnce(OwnedSemaphorePermit) -> (PendingCommit, Ticket),
    ) -> Result<(Stamped, Ticket), CommitError> {
        let inner = &*self.inner;
        if inner.state.is_partition_halted(partition) {
            return Err(partition_stopped_error(partition));
        }
        let command_id = inner.id_source.command_id();
        let mut watermark = self.lock_stamp_watermark(partition);
        let EncodedRecord {
            stamped,
            record,
            proposed_at_micros,
        } = encode(RecordStamp {
            epoch: inner.epoch,
            command_id,
            proposed_at_micros: inner
                .clock
                .now_unix_micros()
                .max(watermark.saturating_add(1)),
        });
        let admission = u32::try_from(record.len().max(1))
            .ok()
            .and_then(|bytes| inner.admission.clone().try_acquire_many_owned(bytes).ok())
            .ok_or_else(|| CommitError::Unavailable {
                detail: "the commit backlog is full".to_owned(),
            })?;
        let (pending, ticket) = new_pending(admission);
        let command_id = command_id.as_bytes().to_vec();
        inner.state.register(command_id.clone(), pending)?;
        let proposal = LogProposal {
            partition,
            record,
            should_wait_for_commit: urgency == CommitUrgency::Prompt,
        };
        if inner.proposals.send(proposal).is_err() {
            let reason = inner
                .state
                .stop_reason()
                .unwrap_or(NodeStopReason::Shutdown);
            let error = CommitError::NodeStopping {
                detail: reason.to_string(),
            };
            if let Some(pending) = inner.state.take(&command_id) {
                pending.fail(error.clone());
            }
            return Err(error);
        }
        *watermark = proposed_at_micros;
        drop(watermark);
        Ok((stamped, ticket))
    }

    /// Returns a consistent view of committed state. Reads block, so use it on a
    /// blocking-pool thread.
    ///
    /// Fails with [`CommitError::Unavailable`] when the local store cannot open a view.
    pub fn committed_view(&self) -> Result<CommittedStateView, CommitError> {
        CommittedStateView::open(self.inner.store.clone()).map_err(|error| {
            CommitError::Unavailable {
                detail: format!("local store cannot open a consistent view: {error}"),
            }
        })
    }

    /// Compares speculative and committed outcomes when debug replay checks are on.
    ///
    /// Returns the mismatch after stopping the partition.
    pub fn check_speculative_outcome(
        &self,
        partition: PartitionId,
        command: &FlowCommand,
        speculative: &ApplyOutcome,
        committed: &CommittedCommand,
    ) -> Result<(), ReplayMismatch> {
        if !self.inner.is_checking_replay {
            return Ok(());
        }
        compare_speculative_outcome(speculative, command, partition, committed).inspect_err(|mismatch| {
            tracing::error!(partition = partition.0, mismatch = %mismatch, "speculative outcome differs from committed outcome");
            self.inner.state.halt_partition(partition);
        })
    }

    /// Returns why the node stopped, or `None` while it recovers or serves.
    pub fn stop_reason(&self) -> Option<NodeStopReason> {
        self.inner.state.stop_reason()
    }

    /// Returns whether `partition` stopped applying commands, so it rejects every submission.
    pub fn is_partition_halted(&self, partition: PartitionId) -> bool {
        self.inner.state.is_partition_halted(partition)
    }

    fn lock_stamp_watermark(&self, partition: PartitionId) -> std::sync::MutexGuard<'_, i64> {
        self.inner.stamp_watermarks[usize::from(partition.0)]
            .lock()
            .expect("stamp lock is not poisoned")
    }
}

impl FlowCommandCommitter for CommandCommitter {
    fn submit_with_final_stamp(
        &self,
        context: ApplyContext,
        command: FlowCommand,
        urgency: CommitUrgency,
        finish_stamp: &mut dyn FnMut(&mut FlowCommand),
    ) -> Result<(FlowCommand, CommitTicket), CommitError> {
        CommandCommitter::submit_with_final_stamp(self, context, command, urgency, finish_stamp)
    }

    fn committed_view(&self) -> Result<Box<dyn FlowStateReader + Send>, CommitError> {
        Ok(Box::new(CommandCommitter::committed_view(self)?))
    }

    fn check_speculative_outcome(
        &self,
        partition: PartitionId,
        command: &FlowCommand,
        speculative: &ApplyOutcome,
        committed: &CommittedCommand,
    ) -> Result<(), crate::flow_coordination::ReplayMismatch> {
        CommandCommitter::check_speculative_outcome(
            self,
            partition,
            command,
            speculative,
            committed,
        )
        .map_err(|mismatch| crate::flow_coordination::ReplayMismatch {
            detail: mismatch.to_string(),
        })
    }
}
