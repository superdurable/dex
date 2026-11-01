// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::future::Future;
use std::time::Duration;

use dexzd_clock::WallClock;
use dexzd_keyspace::validate_node_identifier;
use dexzd_object_store::{ObjectStore, ObjectStoreErrorKind, WriteCondition, WriteOutcome};
use tokio::time::{Instant, sleep, timeout_at};
use xxhash_rust::xxh3::xxh3_128;

use crate::batch_codec::{
    BatchContent, DecodedBatch, LogError, StreamIdentity, decode_batch, encode_batch,
};
use crate::batch_read_ahead::{LogReadAheadConfig, OrderedBatchReader};
use crate::stream_tail_reader::{StreamTailReader, StreamTailStep, next_seq_after};

const RECOVERY_WRITE_DEADLINE: Duration = Duration::from_secs(10);
pub(crate) const LIST_PAGE_KEYS: u32 = 1_000;

/// Every accepted batch and voided seq of a recovery, so it grows with the log.
#[derive(Debug)]
pub struct RecoveredStream {
    pub batches_to_apply: Vec<DecodedBatch>,
    pub void_sequences: Vec<u64>,
    pub epoch_start_seq: u64,
    pub next_seq: u64,
}

/// Where a recovered stream continues, once every accepted batch was returned.
#[derive(Debug)]
pub struct StreamRecoveryResult {
    /// Number of batches that an EpochStart voided.
    pub voided_batches: u64,
    /// Seq of the EpochStart that this recovery wrote.
    pub epoch_start_seq: u64,
    /// Seq after the last batch of the stream; the commit pipeline continues there.
    pub next_seq: u64,
}

/// One step of a [`StreamRecovery`].
#[derive(Debug)]
pub enum StreamRecoveryStep {
    /// The next accepted batch in seq order, to apply before the next step.
    Apply(DecodedBatch),
    /// The EpochStart is written and every batch after it is voided or filled.
    Recovered(StreamRecoveryResult),
}

/// The stream that a recovery reads and the epoch that its EpochStart starts.
#[derive(Clone, Copy)]
pub struct StreamRecoveryInputs<'a> {
    /// Object storage that holds the stream.
    pub store: &'a dyn ObjectStore,
    /// Clock for the EpochStart seal time.
    pub clock: &'a dyn WallClock,
    /// The stream to recover.
    pub stream: &'a StreamIdentity,
    /// Epoch of the EpochStart; recovery fails with [`LogError::Fenced`] unless it is the newest.
    pub new_epoch: u64,
    /// Process ID recorded in the EpochStart.
    pub writer_process_id: &'a str,
    /// Read-ahead of the batches after the start position.
    pub read_ahead: LogReadAheadConfig,
}

/// Collects every accepted batch in memory; [`StreamRecovery`] returns them as they are read.
pub async fn recover_stream(
    store: &dyn ObjectStore,
    clock: &dyn WallClock,
    stream: &StreamIdentity,
    last_applied_seq: u64,
    last_applied_epoch: u64,
    new_epoch: u64,
    writer_process_id: &str,
) -> Result<RecoveredStream, LogError> {
    let inputs = StreamRecoveryInputs {
        store,
        clock,
        stream,
        new_epoch,
        writer_process_id,
        read_ahead: LogReadAheadConfig::default(),
    };
    collect_recovered_stream(StreamRecovery::after_applied(
        inputs,
        last_applied_seq,
        last_applied_epoch,
    )?)
    .await
}

/// Collects every accepted batch in memory; [`StreamRecovery`] returns them as they are read.
pub async fn recover_stream_from(
    store: &dyn ObjectStore,
    clock: &dyn WallClock,
    stream: &StreamIdentity,
    from_seq: u64,
    effective_epoch_at_start: u64,
    new_epoch: u64,
    writer_process_id: &str,
) -> Result<RecoveredStream, LogError> {
    let inputs = StreamRecoveryInputs {
        store,
        clock,
        stream,
        new_epoch,
        writer_process_id,
        read_ahead: LogReadAheadConfig::default(),
    };
    collect_recovered_stream(StreamRecovery::from_seq(
        inputs,
        from_seq,
        effective_epoch_at_start,
    )?)
    .await
}

async fn collect_recovered_stream(
    mut recovery: StreamRecovery<'_>,
) -> Result<RecoveredStream, LogError> {
    recovery.voided.sequences = Some(Vec::new());
    let mut batches_to_apply = Vec::new();
    loop {
        match recovery.next_step().await? {
            StreamRecoveryStep::Apply(batch) => batches_to_apply.push(batch),
            StreamRecoveryStep::Recovered(result) => {
                return Ok(RecoveredStream {
                    batches_to_apply,
                    void_sequences: recovery.voided.sequences.take().unwrap_or_default(),
                    epoch_start_seq: result.epoch_start_seq,
                    next_seq: result.next_seq,
                });
            }
        }
    }
}

/// Recovers a log stream one step at a time, returning accepted batches in seq order as they are
/// read.
pub struct StreamRecovery<'a> {
    writer: EpochStartWriter<'a>,
    read_ahead: LogReadAheadConfig,
    from_seq: u64,
    /// Reads from `from_seq` and holds the next seq to examine and the effective epoch.
    tail: StreamTailReader<'a>,
    voided: VoidedBatches,
    phase: RecoveryPhase,
}

/// Batches that an EpochStart voided; their seqs are kept only for the collecting functions.
#[derive(Default)]
struct VoidedBatches {
    count: u64,
    sequences: Option<Vec<u64>>,
}

enum RecoveryPhase {
    /// Reading from `from_seq` up to the first missing seq.
    Reading,
    /// Writing the EpochStart at the first missing seq; a batch found there is examined instead.
    WritingEpochStart { deadline: Instant },
    /// No step remains.
    Finished,
}

impl<'a> StreamRecovery<'a> {
    /// Starts a recovery after the applied position `(last_applied_seq, last_applied_epoch)`,
    /// or from seq 1 when both are zero.
    ///
    /// Returns [`LogError::Invalid`] for an inconsistent position and [`LogError::Fenced`] when
    /// the applied epoch is not older than the new epoch.
    pub fn after_applied(
        inputs: StreamRecoveryInputs<'a>,
        last_applied_seq: u64,
        last_applied_epoch: u64,
    ) -> Result<Self, LogError> {
        if (last_applied_seq == 0) != (last_applied_epoch == 0) {
            return Err(LogError::Invalid(
                "invalid applied position or epoch".into(),
            ));
        }
        let from_seq = last_applied_seq
            .checked_add(1)
            .ok_or_else(|| LogError::Invalid("stream sequence exhausted".into()))?;
        Self::from_seq(inputs, from_seq, last_applied_epoch)
    }

    /// Starts a recovery that reads from `from_seq`, with `effective_epoch_at_start` in effect
    /// before it.
    ///
    /// Returns [`LogError::Invalid`] for invalid inputs and [`LogError::Fenced`] when
    /// `effective_epoch_at_start` is not older than the new epoch.
    pub fn from_seq(
        inputs: StreamRecoveryInputs<'a>,
        from_seq: u64,
        effective_epoch_at_start: u64,
    ) -> Result<Self, LogError> {
        let StreamRecoveryInputs {
            store,
            clock,
            stream,
            new_epoch,
            writer_process_id,
            read_ahead,
        } = inputs;
        if from_seq == 0 || new_epoch == 0 || writer_process_id.is_empty() {
            return Err(LogError::Invalid(
                "invalid stream position, epoch, or writer process ID".into(),
            ));
        }
        validate_node_identifier(&stream.node_id)
            .map_err(|error| LogError::Invalid(error.to_string()))?;
        if effective_epoch_at_start >= new_epoch {
            return Err(LogError::Fenced);
        }
        Ok(Self {
            writer: EpochStartWriter {
                store,
                clock,
                stream,
                new_epoch,
                writer_process_id,
            },
            read_ahead,
            from_seq,
            tail: StreamTailReader::new(
                store,
                stream,
                from_seq,
                effective_epoch_at_start,
                None,
                read_ahead,
            )?,
            voided: VoidedBatches::default(),
            phase: RecoveryPhase::Reading,
        })
    }

    /// Returns the next accepted batch, or the result once the EpochStart is written.
    ///
    /// Returns [`LogError::Fenced`] as soon as the stream shows an epoch that is not older than
    /// the new epoch, [`LogError::MissingBatches`] when the batches from the start position were
    /// deleted, and [`LogError::Invalid`] after the recovery finished or failed.
    pub async fn next_step(&mut self) -> Result<StreamRecoveryStep, LogError> {
        loop {
            let accepted = match self.phase {
                RecoveryPhase::Reading => match self.tail.next_step().await? {
                    StreamTailStep::Accepted(batch) => Some(batch),
                    StreamTailStep::Voided(seq) => {
                        self.voided.record(seq);
                        None
                    }
                    StreamTailStep::WaitingAt(_) => {
                        self.finish_reading().await?;
                        continue;
                    }
                    StreamTailStep::Ended => {
                        return Err(LogError::Invalid(
                            "an open log stream has no last seq".into(),
                        ));
                    }
                },
                RecoveryPhase::WritingEpochStart { deadline } => {
                    match self
                        .writer
                        .create_epoch_start(self.tail.next_seq(), deadline)
                        .await?
                    {
                        RecoveryWrite::Written => {
                            return self
                                .finish_recovery()
                                .await
                                .map(StreamRecoveryStep::Recovered);
                        }
                        RecoveryWrite::Found(batch) => {
                            if batch.header.epoch >= self.writer.new_epoch {
                                return Err(LogError::Fenced);
                            }
                            let seq = self.tail.next_seq();
                            let accepted = self.tail.examine_found_batch(batch)?;
                            if accepted.is_none() {
                                self.voided.record(seq);
                            }
                            accepted
                        }
                    }
                }
                RecoveryPhase::Finished => {
                    return Err(LogError::Invalid(
                        "the stream recovery has no step left".into(),
                    ));
                }
            };
            if let Some(batch) = accepted {
                self.ensure_older_than_new_epoch()?;
                return Ok(StreamRecoveryStep::Apply(batch));
            }
        }
    }

    /// Runs `work`, such as applying the last returned batch, while read-ahead continues.
    ///
    /// Returns the output of `work`.
    pub async fn read_ahead_while<F: Future>(&mut self, work: F) -> F::Output {
        self.tail.read_ahead_while(work).await
    }

    /// Ends reading at the missing next seq and prepares the EpochStart write there.
    async fn finish_reading(&mut self) -> Result<(), LogError> {
        self.phase = RecoveryPhase::Finished;
        if self.tail.next_seq() == self.from_seq {
            self.tail.ensure_waiting_seq_was_not_deleted().await?;
        }
        self.phase = RecoveryPhase::WritingEpochStart {
            deadline: Instant::now() + RECOVERY_WRITE_DEADLINE,
        };
        Ok(())
    }

    async fn finish_recovery(&mut self) -> Result<StreamRecoveryResult, LogError> {
        self.phase = RecoveryPhase::Finished;
        self.writer
            .finish_epoch_start(self.tail.next_seq(), &mut self.voided, self.read_ahead)
            .await
    }

    /// Returns [`LogError::Fenced`] once the effective epoch reaches the new epoch.
    fn ensure_older_than_new_epoch(&self) -> Result<(), LogError> {
        if self.tail.effective_epoch() >= self.writer.new_epoch {
            return Err(LogError::Fenced);
        }
        Ok(())
    }
}

impl VoidedBatches {
    fn record(&mut self, seq: u64) {
        self.count += 1;
        if let Some(sequences) = &mut self.sequences {
            sequences.push(seq);
        }
    }
}

/// What a create-only recovery write found at its seq.
enum RecoveryWrite {
    /// The seq holds the batch this recovery wrote.
    Written,
    /// The seq already held another batch.
    Found(DecodedBatch),
}

/// Writes the EpochStart batches of one recovery.
struct EpochStartWriter<'a> {
    store: &'a dyn ObjectStore,
    clock: &'a dyn WallClock,
    stream: &'a StreamIdentity,
    new_epoch: u64,
    writer_process_id: &'a str,
}

impl EpochStartWriter<'_> {
    /// Voids older batches after the EpochStart and fills every hole below the last listed batch.
    ///
    /// Returns the result with the voided seqs recorded in `voided` and `next_seq` after the last
    /// batch, or [`LogError::Fenced`] when that range holds a batch of this or a newer epoch.
    async fn finish_epoch_start(
        &self,
        epoch_start_seq: u64,
        voided: &mut VoidedBatches,
        read_ahead: LogReadAheadConfig,
    ) -> Result<StreamRecoveryResult, LogError> {
        let first_seq = next_seq_after(epoch_start_seq)?;
        let Some(last_seq) = self.last_listed_seq_after(epoch_start_seq).await? else {
            return Ok(StreamRecoveryResult {
                voided_batches: voided.count,
                epoch_start_seq,
                next_seq: first_seq,
            });
        };
        let deadline = Instant::now() + RECOVERY_WRITE_DEADLINE;
        let mut reader = OrderedBatchReader::new(
            self.store,
            self.stream,
            first_seq,
            Some(last_seq),
            read_ahead,
        );
        while let Some((seq, batch)) = reader.next_batch().await? {
            let found = match batch {
                Some(batch) => Some(batch),
                // No empty ordinary batch decodes, so an EpochStart of this epoch fills the hole.
                None => match self.create_epoch_start(seq, deadline).await? {
                    RecoveryWrite::Written => None,
                    RecoveryWrite::Found(batch) => Some(batch),
                },
            };
            if let Some(batch) = found {
                if batch.header.epoch >= self.new_epoch {
                    return Err(LogError::Fenced);
                }
                voided.record(seq);
            }
        }
        Ok(StreamRecoveryResult {
            voided_batches: voided.count,
            epoch_start_seq,
            next_seq: next_seq_after(last_seq)?,
        })
    }

    /// Creates an EpochStart at `seq` unless a batch is there; retries reuse its bytes until `deadline`.
    ///
    /// Returns [`RecoveryWrite::Written`] when `seq` holds these bytes, including after an unknown
    /// write outcome, and [`RecoveryWrite::Found`] with the batch that another writer stored there.
    async fn create_epoch_start(
        &self,
        seq: u64,
        deadline: Instant,
    ) -> Result<RecoveryWrite, LogError> {
        let key = self.stream.batch_key(seq);
        let body = encode_batch(&BatchContent {
            stream: self.stream.clone(),
            epoch: self.new_epoch,
            seq,
            sealed_at_unix_micros: self.clock.now_unix_micros(),
            records: Vec::new(),
            epoch_start_writer: Some(self.writer_process_id.to_owned()),
        })?;
        let ours = xxh3_128(&body[..body.len() - 16]);
        let mut backoff = Duration::from_millis(10);
        loop {
            if Instant::now() >= deadline {
                return Err(LogError::Deadline);
            }
            let should_read_back = match timeout_at(
                deadline,
                self.store
                    .put(&key, body.clone(), WriteCondition::CreateOnly),
            )
            .await
            {
                Err(_) => return Err(LogError::Deadline),
                Ok(write_result) => match write_result {
                    Ok(WriteOutcome::Written { .. }) => return Ok(RecoveryWrite::Written),
                    Ok(WriteOutcome::PreconditionFailed) => true,
                    Err(error) if error.kind == ObjectStoreErrorKind::OutcomeUnknown => true,
                    Err(error) if error.kind == ObjectStoreErrorKind::Transient => false,
                    Err(error) => return Err(error.into()),
                },
            };
            if should_read_back {
                match timeout_at(deadline, self.store.get(&key)).await {
                    Err(_) => return Err(LogError::Deadline),
                    Ok(read_result) => match read_result {
                        Ok(Some(object)) => {
                            let batch = decode_batch(&object.bytes)?;
                            validate_batch_identity(&batch, self.stream, seq)?;
                            if batch.trailer == ours {
                                return Ok(RecoveryWrite::Written);
                            }
                            return Ok(RecoveryWrite::Found(batch));
                        }
                        Ok(None) => {}
                        Err(error) if error.kind == ObjectStoreErrorKind::Transient => {}
                        Err(error) => return Err(error.into()),
                    },
                }
            }
            sleep(backoff.min(deadline.saturating_duration_since(Instant::now()))).await;
            backoff = (backoff * 2).min(Duration::from_millis(500));
        }
    }

    /// Returns the highest seq that a listing of the stream shows after `seq`, if any.
    async fn last_listed_seq_after(&self, seq: u64) -> Result<Option<u64>, LogError> {
        let start_key = self.stream.batch_key(seq);
        let prefix_length = batch_key_prefix_length(&start_key);
        let mut start_after = start_key.clone();
        let mut last_key = None;
        loop {
            let page = self
                .store
                .list(
                    &start_key[..prefix_length],
                    Some(&start_after),
                    LIST_PAGE_KEYS,
                )
                .await?;
            if let Some(object) = page.objects.last() {
                last_key = Some(object.key.clone());
            }
            match page.next_start_after {
                Some(next_start_after) => start_after = next_start_after,
                None => break,
            }
        }
        last_key
            .map(|key| parse_listed_seq(&key, prefix_length))
            .transpose()
    }
}

/// Returns the length of the stream prefix of `batch_key`, up to its last `/`.
pub(crate) fn batch_key_prefix_length(batch_key: &str) -> usize {
    batch_key.rfind('/').expect("a batch key ends with /<seq>") + 1
}

/// Returns the seq that a listed key of the stream names, or [`LogError::Corrupt`] for another name.
pub(crate) fn parse_listed_seq(key: &str, prefix_length: usize) -> Result<u64, LogError> {
    key[prefix_length..]
        .parse::<u64>()
        .map_err(|_| LogError::Corrupt(format!("log stream holds an object named {key}")))
}

pub(crate) fn validate_batch_identity(
    batch: &DecodedBatch,
    stream: &StreamIdentity,
    seq: u64,
) -> Result<(), LogError> {
    if batch.header.cluster_id != stream.cluster_id
        || batch.header.node_id != stream.node_id
        || batch.header.incarnation != stream.incarnation
        || batch.header.seq != seq
    {
        return Err(LogError::Corrupt(format!(
            "batch at seq {seq} has a different stream identity"
        )));
    }
    Ok(())
}
