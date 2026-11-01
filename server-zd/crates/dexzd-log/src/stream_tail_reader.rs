// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::future::Future;

use dexzd_keyspace::validate_node_identifier;
use dexzd_object_store::ObjectStore;

use crate::batch_codec::{DecodedBatch, LogError, StreamIdentity};
use crate::batch_read_ahead::{LogReadAheadConfig, OrderedBatchReader, decode_stream_batch};
use crate::stream_recovery::{batch_key_prefix_length, parse_listed_seq};

/// Batches found by single GETs in a row before reading ahead again; above the pipeline's
/// in-flight writes.
const PROBE_HITS_BEFORE_READ_AHEAD: u32 = 8;

/// Reads one log stream in seq order, applies the EpochStart void rule, and stops at the first
/// missing seq.
pub struct StreamTailReader<'a> {
    store: &'a dyn ObjectStore,
    stream: &'a StreamIdentity,
    read_ahead: LogReadAheadConfig,
    /// Next seq to examine.
    next_seq: u64,
    /// Inclusive last seq of a closed stream; `None` while the stream may grow.
    last_seq: Option<u64>,
    effective_epoch: u64,
    phase: TailPhase<'a>,
}

/// One step of a [`StreamTailReader`].
#[derive(Debug)]
pub enum StreamTailStep {
    /// The next batch that the effective epoch accepts; apply it before the next step.
    Accepted(DecodedBatch),
    /// The batch at this seq has an epoch older than an earlier EpochStart; it is not applied.
    Voided(u64),
    /// No batch exists at this seq yet; every earlier seq was returned.
    WaitingAt(u64),
    /// Every seq through the last seq of a closed stream was returned.
    Ended,
}

enum TailPhase<'a> {
    /// Reads consecutive batches concurrently from `next_seq`.
    ReadingAhead(Box<OrderedBatchReader<'a>>),
    /// Reads only `next_seq` with one GET, after a read missed or failed.
    Probing { consecutive_hits: u32 },
    /// Every seq through `last_seq` was returned.
    Ended,
}

impl<'a> StreamTailReader<'a> {
    /// Creates a reader from `from_seq`, with `effective_epoch_at_start` in effect before it.
    ///
    /// `last_seq` bounds a closed stream. Reads start at the first [`Self::next_step`].
    ///
    /// Returns [`LogError::Invalid`] when `from_seq` is zero or the stream names an invalid node.
    pub fn new(
        store: &'a dyn ObjectStore,
        stream: &'a StreamIdentity,
        from_seq: u64,
        effective_epoch_at_start: u64,
        last_seq: Option<u64>,
        read_ahead: LogReadAheadConfig,
    ) -> Result<Self, LogError> {
        if from_seq == 0 {
            return Err(LogError::Invalid("a log stream starts at seq 1".to_owned()));
        }
        validate_node_identifier(&stream.node_id)
            .map_err(|error| LogError::Invalid(error.to_string()))?;
        let mut reader = Self {
            store,
            stream,
            read_ahead,
            next_seq: from_seq,
            last_seq,
            effective_epoch: effective_epoch_at_start,
            phase: TailPhase::Ended,
        };
        reader.phase = reader.reading_ahead_or_ended();
        Ok(reader)
    }

    /// Examines the next seq. After a missing seq it reads single seqs until several are found.
    ///
    /// Returns the accepted or voided batch at the next seq, [`StreamTailStep::WaitingAt`] when
    /// that seq has no batch yet, or [`StreamTailStep::Ended`] after the last seq of a closed
    /// stream. Returns a listing, read, decode, or stream identity error of the next seq; the next
    /// call reads that seq again.
    pub async fn next_step(&mut self) -> Result<StreamTailStep, LogError> {
        let seq = self.next_seq;
        let read = match &mut self.phase {
            TailPhase::Ended => return Ok(StreamTailStep::Ended),
            TailPhase::ReadingAhead(reader) => reader.next_batch().await,
            TailPhase::Probing { .. } => read_one_batch(self.store, self.stream, seq)
                .await
                .map(|batch| Some((seq, batch))),
        };
        let batch = match read {
            Err(error) => {
                // Dropping a reader discards reads past the failed seq, so the next call reads it.
                self.phase = TailPhase::Probing {
                    consecutive_hits: 0,
                };
                return Err(error);
            }
            Ok(None) => {
                self.phase = TailPhase::Ended;
                return Ok(StreamTailStep::Ended);
            }
            Ok(Some((read_seq, batch))) => {
                debug_assert_eq!(read_seq, seq, "batches are read in seq order");
                batch
            }
        };
        let Some(batch) = batch else {
            // Dropping a reader discards reads past the missing seq unseen.
            self.phase = TailPhase::Probing {
                consecutive_hits: 0,
            };
            return Ok(StreamTailStep::WaitingAt(seq));
        };
        let step = self.examine(seq, batch)?;
        let should_read_ahead = match &mut self.phase {
            TailPhase::Probing { consecutive_hits } => {
                *consecutive_hits += 1;
                *consecutive_hits >= PROBE_HITS_BEFORE_READ_AHEAD
            }
            TailPhase::ReadingAhead(_) | TailPhase::Ended => false,
        };
        if should_read_ahead {
            self.phase = self.reading_ahead_or_ended();
        }
        Ok(step)
    }

    /// Runs `work`, such as applying the last returned batch, while read-ahead continues.
    ///
    /// Returns the output of `work`.
    pub async fn read_ahead_while<F: Future>(&mut self, work: F) -> F::Output {
        match &mut self.phase {
            TailPhase::ReadingAhead(reader) => reader.read_ahead_while(work).await,
            TailPhase::Probing { .. } | TailPhase::Ended => work.await,
        }
    }

    /// Bounds the stream at `last_seq` once it is closed, discarding reads started past it.
    ///
    /// Returns [`LogError::Invalid`] when a seq after `last_seq` was already returned or the
    /// stream already ends at another seq.
    pub fn end_at(&mut self, last_seq: u64) -> Result<(), LogError> {
        if let Some(current) = self.last_seq {
            if current == last_seq {
                return Ok(());
            }
            return Err(LogError::Invalid(format!(
                "log stream of incarnation {} ends at seq {current}, not {last_seq}",
                self.stream.incarnation
            )));
        }
        if last_seq < self.next_seq - 1 {
            return Err(LogError::Invalid(format!(
                "log stream of incarnation {} cannot end at seq {last_seq} after seq {} was read",
                self.stream.incarnation,
                self.next_seq - 1
            )));
        }
        self.last_seq = Some(last_seq);
        if matches!(self.phase, TailPhase::ReadingAhead(_)) || self.next_seq > last_seq {
            self.phase = self.reading_ahead_or_ended();
        }
        Ok(())
    }

    /// Checks whether log cleanup deleted the seq this reader waits at.
    ///
    /// Returns [`LogError::MissingBatches`] when the batch before the next seq is gone and a later
    /// batch exists. Returns `Ok` when the next seq was not written yet or is the first seq.
    pub fn ensure_waiting_seq_was_not_deleted(
        &self,
    ) -> impl Future<Output = Result<(), LogError>> + Send + use<'a> {
        // Borrows only the store and stream; the reader itself is not Sync.
        ensure_batches_after_start_were_not_deleted(self.store, self.stream, self.next_seq)
    }

    /// Returns the next seq to examine: the seq waited at, or the seq after the last one returned.
    pub fn next_seq(&self) -> u64 {
        self.next_seq
    }

    /// Returns the epoch in effect after the last returned seq; older batches are void.
    pub fn effective_epoch(&self) -> u64 {
        self.effective_epoch
    }

    /// Examines `batch`, which another read found at the next seq.
    ///
    /// Returns the batch when the effective epoch accepts it, or `None` when it is void.
    pub(crate) fn examine_found_batch(
        &mut self,
        batch: DecodedBatch,
    ) -> Result<Option<DecodedBatch>, LogError> {
        match self.examine(self.next_seq, batch)? {
            StreamTailStep::Accepted(batch) => Ok(Some(batch)),
            StreamTailStep::Voided(_) | StreamTailStep::WaitingAt(_) | StreamTailStep::Ended => {
                Ok(None)
            }
        }
    }

    /// Moves past `seq` and applies the void rule to its batch.
    fn examine(&mut self, seq: u64, batch: DecodedBatch) -> Result<StreamTailStep, LogError> {
        self.next_seq = next_seq_after(seq)?;
        if self.last_seq.is_some_and(|last_seq| seq >= last_seq) {
            self.phase = TailPhase::Ended;
        }
        Ok(self.apply_void_rule(seq, batch))
    }

    /// Voids `batch` when an earlier EpochStart superseded its epoch, or returns it to apply.
    fn apply_void_rule(&mut self, seq: u64, batch: DecodedBatch) -> StreamTailStep {
        if batch.header.epoch < self.effective_epoch {
            return StreamTailStep::Voided(seq);
        }
        if let Some(epoch_start) = &batch.header.epoch_start {
            self.effective_epoch = epoch_start.epoch;
        }
        StreamTailStep::Accepted(batch)
    }

    /// Returns a read-ahead phase from the next seq, or the end once a closed stream is past it.
    fn reading_ahead_or_ended(&self) -> TailPhase<'a> {
        if self
            .last_seq
            .is_some_and(|last_seq| self.next_seq > last_seq)
        {
            return TailPhase::Ended;
        }
        TailPhase::ReadingAhead(Box::new(OrderedBatchReader::new(
            self.store,
            self.stream,
            self.next_seq,
            self.last_seq,
            self.read_ahead,
        )))
    }
}

/// Reads the batch at `seq` with one GET.
///
/// Returns `None` when the store has no batch there.
async fn read_one_batch(
    store: &dyn ObjectStore,
    stream: &StreamIdentity,
    seq: u64,
) -> Result<Option<DecodedBatch>, LogError> {
    store
        .get(&stream.batch_key(seq))
        .await?
        .map(|object| decode_stream_batch(&object.bytes, stream, seq))
        .transpose()
}

/// Fails with [`LogError::MissingBatches`] when `missing_seq` was deleted and later batches remain.
async fn ensure_batches_after_start_were_not_deleted(
    store: &dyn ObjectStore,
    stream: &StreamIdentity,
    missing_seq: u64,
) -> Result<(), LogError> {
    // Cleanup deletes only batches below a published snapshot's applied position, so a replayable
    // applied position keeps its batch.
    if missing_seq == 1
        || store
            .head(&stream.batch_key(missing_seq - 1))
            .await?
            .is_some()
    {
        return Ok(());
    }
    let missing_key = stream.batch_key(missing_seq);
    let prefix_length = batch_key_prefix_length(&missing_key);
    let listing = store
        .list(&missing_key[..prefix_length], Some(&missing_key), 1)
        .await?;
    let Some(next) = listing.objects.first() else {
        return Ok(());
    };
    let next_existing_seq = parse_listed_seq(&next.key, prefix_length)?;
    Err(LogError::MissingBatches {
        missing_seq,
        next_existing_seq,
    })
}

pub(crate) fn next_seq_after(seq: u64) -> Result<u64, LogError> {
    seq.checked_add(1)
        .ok_or_else(|| LogError::Invalid("stream sequence exhausted".into()))
}
