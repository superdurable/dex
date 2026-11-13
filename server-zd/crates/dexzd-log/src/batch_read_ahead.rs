// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::{BTreeMap, VecDeque};
use std::future::{Future, pending};
use std::pin::pin;

use bytes::Bytes;
use dexzd_object_store::{ListingConsistency, ObjectListing, ObjectStore};
use futures::future::BoxFuture;
use futures::stream::{FuturesUnordered, StreamExt};

use crate::batch_codec::{DecodedBatch, LogError, StreamIdentity, decode_batch};
use crate::stream_layout::{
    LIST_PAGE_KEYS, batch_key_prefix_length, parse_listed_seq, validate_batch_identity,
};

/// How far log replay reads batches ahead of the batch it applies, and how far it trusts a
/// listing of the stream.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct LogReadAheadConfig {
    /// Defaults to 64; most batches read but not yet returned, counting reads in flight.
    pub max_read_ahead_batches: usize,
    /// Defaults to 64 MiB; most batch object bytes that read-ahead reserves. A larger batch is
    /// read alone.
    pub max_buffered_bytes: usize,
    /// Defaults to [`ListingConsistency::Strong`], which treats a seq that a listing lacks as
    /// missing. With [`ListingConsistency::Eventual`], a bounded read, or a gap below a listed
    /// seq, reads the object before treating the seq as missing, and recovery reads seqs after
    /// the last listed batch; see `tail_probe_misses`.
    pub listing_consistency: ListingConsistency,
    /// Defaults to 4, the default number of concurrent batch writes of the commit pipeline.
    /// Only with an eventually consistent listing: recovery reads the seqs after the last
    /// listed batch, in concurrent rounds of this many HEADs, until this many in a row are
    /// missing. Set it to at least the most concurrent batch writes of any writer that wrote the
    /// stream, not only of this one; values below 1 count as 1.
    pub tail_probe_misses: usize,
}

impl Default for LogReadAheadConfig {
    fn default() -> Self {
        Self {
            max_read_ahead_batches: 64,
            max_buffered_bytes: 64 * 1024 * 1024,
            listing_consistency: ListingConsistency::Strong,
            tail_probe_misses: 4,
        }
    }
}

/// Reads consecutive batches of one stream concurrently and returns every result, errors
/// included, in seq order.
pub struct OrderedBatchReader<'a> {
    store: &'a dyn ObjectStore,
    stream: &'a StreamIdentity,
    config: LogReadAheadConfig,
    /// Inclusive bound of the seqs read.
    last_seq: u64,
    /// Next seq to return, or `None` after `last_seq` was returned.
    next_seq_to_return: Option<u64>,
    /// Next seq to read, or `None` after `last_seq` was read.
    next_seq_to_read: Option<u64>,
    in_flight: FuturesUnordered<BoxFuture<'a, ReadEvent>>,
    fetched: BTreeMap<u64, ObjectRead>,
    listing: ListedObjectSizes,
    /// Bytes reserved by each seq read but not yet returned, in seq order.
    reservations: VecDeque<usize>,
    /// Kept until the next call, so the batch being applied stays charged.
    returned_reservation: usize,
    reserved_bytes: usize,
}

/// One seq: the object bytes, or `None` when the seq is empty.
type ObjectRead = Result<Option<Bytes>, LogError>;

enum ReadEvent {
    Object(u64, ObjectRead),
    /// A read of a seq that an eventually consistent listing lacked.
    UnlistedObject(u64, ObjectRead),
    Listing(Result<ObjectListing, LogError>),
}

/// Object sizes from a paged listing of the stream, kept only for seqs not read yet.
struct ListedObjectSizes {
    prefix: String,
    next_start_after: String,
    /// Listed seqs not read yet with their object sizes, in seq order.
    sizes: VecDeque<(u64, usize)>,
    /// Every seq through this one that existed when listed is in `sizes` or was read.
    listed_through: u64,
    /// Highest seq that a listing showed; with an eventually consistent listing, a gap below it
    /// is read before it counts as missing.
    highest_listed_seq: u64,
    consistency: ListingConsistency,
    is_listing: bool,
    error: Option<LogError>,
}

impl<'a> OrderedBatchReader<'a> {
    /// Creates a reader from `first_seq` through `last_seq`, unbounded when `None`; reads start
    /// at the first [`Self::next_batch`].
    pub fn new(
        store: &'a dyn ObjectStore,
        stream: &'a StreamIdentity,
        first_seq: u64,
        last_seq: Option<u64>,
        config: LogReadAheadConfig,
    ) -> Self {
        let last_seq = last_seq.unwrap_or(u64::MAX);
        let listed_through = first_seq.saturating_sub(1);
        let first_seq = (first_seq <= last_seq).then_some(first_seq);
        let first_key = stream.batch_key(listed_through);
        Self {
            store,
            stream,
            config,
            last_seq,
            next_seq_to_return: first_seq,
            next_seq_to_read: first_seq,
            in_flight: FuturesUnordered::new(),
            fetched: BTreeMap::new(),
            listing: ListedObjectSizes {
                prefix: first_key[..batch_key_prefix_length(&first_key)].to_owned(),
                next_start_after: first_key,
                sizes: VecDeque::new(),
                listed_through,
                highest_listed_seq: listed_through,
                consistency: config.listing_consistency,
                is_listing: false,
                error: None,
            },
            reservations: VecDeque::new(),
            returned_reservation: 0,
            reserved_bytes: 0,
        }
    }

    /// Releases the batch returned by the previous call and reads the next one.
    ///
    /// Returns the next seq with its batch, or with `None` when the store has no batch there.
    /// Returns `Ok(None)` once every seq through the last one was returned, and the first
    /// listing, read, decode, or stream identity error in seq order.
    pub async fn next_batch(&mut self) -> Result<Option<(u64, Option<DecodedBatch>)>, LogError> {
        self.reserved_bytes -= std::mem::take(&mut self.returned_reservation);
        let Some(seq) = self.next_seq_to_return else {
            return Ok(None);
        };
        loop {
            self.start_reads();
            if let Some(read) = self.fetched.remove(&seq) {
                self.returned_reservation = self
                    .reservations
                    .pop_front()
                    .expect("a fetched seq holds the oldest reservation");
                self.next_seq_to_return = seq.checked_add(1).filter(|next| *next <= self.last_seq);
                self.start_reads();
                let batch = read.and_then(|body| {
                    body.map(|body| decode_stream_batch(&body, self.stream, seq))
                        .transpose()
                });
                if batch.is_err() {
                    // A failed batch is never applied, so later reads may use its bytes now.
                    self.reserved_bytes -= std::mem::take(&mut self.returned_reservation);
                    self.start_reads();
                }
                return batch.map(|batch| Some((seq, batch)));
            }
            if self.next_seq_to_read == Some(seq)
                && let Some(error) = self.listing.error.take()
            {
                return Err(error);
            }
            let event = self
                .in_flight
                .next()
                .await
                .expect("the next seq to return is read or listed until it is fetched");
            self.keep(event);
        }
    }

    /// Runs `work` to completion while reads in flight keep finishing and new reads start.
    ///
    /// Returns the output of `work`.
    pub async fn read_ahead_while<F: Future>(&mut self, work: F) -> F::Output {
        let mut work = pin!(work);
        loop {
            tokio::select! {
                biased;
                output = &mut work => return output,
                () = self.finish_one_read() => {}
            }
        }
    }

    /// Keeps the next finished read or listing and starts more reads. Cancel safe; pends while
    /// nothing is in flight.
    async fn finish_one_read(&mut self) {
        let Some(event) = self.in_flight.next().await else {
            return pending().await;
        };
        self.keep(event);
        self.start_reads();
    }

    fn keep(&mut self, event: ReadEvent) {
        match event {
            ReadEvent::Object(seq, read) => {
                self.fetched.insert(seq, read);
            }
            ReadEvent::UnlistedObject(seq, read) => {
                if matches!(read, Ok(Some(_)))
                    && self.next_seq_to_read == seq.checked_add(1)
                    && self.listing.sizes.is_empty()
                    && !self.listing.is_listing
                {
                    // The listing missed this batch, so it was stale; a new listing may be fresh.
                    self.listing.list_again_after(seq, self.stream);
                }
                self.fetched.insert(seq, read);
            }
            ReadEvent::Listing(page) => self.listing.keep_page(page, self.last_seq),
        }
    }

    /// Starts reads in seq order while the window and byte budget allow, then lists ahead.
    fn start_reads(&mut self) {
        while let Some(seq) = self.next_seq_to_read
            && self.reservations.len() < self.config.max_read_ahead_batches
            && let Some(listed_bytes) = self.listing.size_of(seq)
        {
            // A seq missing from the listing needs no GET or byte reservation.
            let reservation = listed_bytes.unwrap_or(0);
            let should_read_unlisted =
                listed_bytes.is_none() && self.listing.should_read_unlisted(seq, self.last_seq);
            // An unlisted read has no known size, so it reserves the whole budget and runs alone.
            let reservation = if should_read_unlisted {
                self.config.max_buffered_bytes
            } else {
                reservation
            };
            let fits =
                self.reserved_bytes.saturating_add(reservation) <= self.config.max_buffered_bytes;
            if self.reserved_bytes > 0 && !fits {
                break;
            }
            self.listing.forget_through(seq);
            self.reservations.push_back(reservation);
            self.reserved_bytes += reservation;
            if let Some(listed_bytes) = listed_bytes {
                self.in_flight.push(Box::pin(read_object(
                    self.store,
                    self.stream,
                    seq,
                    listed_bytes,
                )));
            } else if should_read_unlisted {
                self.in_flight
                    .push(Box::pin(read_unlisted_object(self.store, self.stream, seq)));
            } else {
                self.in_flight
                    .push(Box::pin(async move { ReadEvent::Object(seq, Ok(None)) }));
            }
            self.next_seq_to_read = seq.checked_add(1).filter(|next| *next <= self.last_seq);
        }
        self.start_listing();
    }

    /// Lists the next page once fewer than a page of listed sizes remain, hiding LIST latency.
    fn start_listing(&mut self) {
        let listing = &mut self.listing;
        let listed_ahead = self
            .config
            .max_read_ahead_batches
            .max(LIST_PAGE_KEYS as usize);
        if self.next_seq_to_read.is_none()
            || listing.is_listing
            || listing.error.is_some()
            || listing.listed_through >= self.last_seq
            || listing.sizes.len() >= listed_ahead
        {
            return;
        }
        listing.is_listing = true;
        let store = self.store;
        let prefix = listing.prefix.clone();
        let start_after = listing.next_start_after.clone();
        self.in_flight.push(Box::pin(async move {
            let page = store
                .list(&prefix, Some(&start_after), LIST_PAGE_KEYS)
                .await;
            ReadEvent::Listing(page.map_err(LogError::from))
        }));
    }
}

impl ListedObjectSizes {
    /// Returns `None` until a listing covers `seq`, then its listed size, or `Some(None)` when
    /// the listing did not show it.
    fn size_of(&self, seq: u64) -> Option<Option<usize>> {
        (seq <= self.listed_through).then(|| {
            self.sizes
                .front()
                .filter(|(listed_seq, _)| *listed_seq == seq)
                .map(|(_, bytes)| *bytes)
        })
    }

    /// Returns whether a seq that the listing lacks is read before it counts as missing: with an
    /// eventually consistent listing, in a bounded read or below a listed seq. Unbounded reads
    /// past the listing return the seq as missing, and their callers read it.
    fn should_read_unlisted(&self, seq: u64, last_seq: u64) -> bool {
        !self.consistency.is_strong() && (last_seq != u64::MAX || seq < self.highest_listed_seq)
    }

    /// Lists again after `seq`, which an earlier listing lacked but a read found.
    fn list_again_after(&mut self, seq: u64, stream: &StreamIdentity) {
        self.listed_through = seq;
        self.next_start_after = stream.batch_key(seq);
    }

    fn forget_through(&mut self, seq: u64) {
        while self
            .sizes
            .front()
            .is_some_and(|(listed_seq, _)| *listed_seq <= seq)
        {
            self.sizes.pop_front();
        }
    }

    fn keep_page(&mut self, page: Result<ObjectListing, LogError>, last_seq: u64) {
        self.is_listing = false;
        if let Err(error) = self.try_keep_page(page, last_seq) {
            self.error = Some(error);
        }
    }

    fn try_keep_page(
        &mut self,
        page: Result<ObjectListing, LogError>,
        last_seq: u64,
    ) -> Result<(), LogError> {
        let page = page?;
        let prefix_length = self.prefix.len();
        let mut sizes = Vec::with_capacity(page.objects.len());
        for object in &page.objects {
            let seq = parse_listed_seq(&object.key, prefix_length)?;
            if seq > self.listed_through && seq <= last_seq {
                sizes.push((seq, usize::try_from(object.size).unwrap_or(usize::MAX)));
            }
        }
        let listed_through = match (&page.next_start_after, page.objects.last()) {
            (None, _) => u64::MAX,
            (Some(_), Some(last)) => parse_listed_seq(&last.key, prefix_length)?,
            (Some(_), None) => self.listed_through,
        };
        if !self.consistency.is_strong() {
            self.keep_eventual_page(sizes, listed_through, page.next_start_after);
            return Ok(());
        }
        self.sizes.extend(sizes);
        self.listed_through = listed_through;
        if let Some(next_start_after) = page.next_start_after {
            self.next_start_after = next_start_after;
        }
        Ok(())
    }

    /// Keeps a page that may repeat an earlier one; a page without a new seq ends listing.
    fn keep_eventual_page(
        &mut self,
        sizes: Vec<(u64, usize)>,
        listed_through: u64,
        next_start_after: Option<String>,
    ) {
        if let Some((seq, _)) = sizes.last() {
            self.highest_listed_seq = self.highest_listed_seq.max(*seq);
        }
        self.sizes.extend(sizes);
        if listed_through <= self.listed_through {
            self.listed_through = u64::MAX;
            return;
        }
        self.listed_through = listed_through;
        if let Some(next_start_after) = next_start_after {
            self.next_start_after = next_start_after;
        }
    }
}

/// Reads one object that an eventually consistent listing lacked.
async fn read_unlisted_object(
    store: &dyn ObjectStore,
    stream: &StreamIdentity,
    seq: u64,
) -> ReadEvent {
    let read = store
        .get(&stream.batch_key(seq))
        .await
        .map(|object| object.map(|object| object.bytes))
        .map_err(LogError::from);
    ReadEvent::UnlistedObject(seq, read)
}

/// Reads one object, failing when a listed size differs from the bytes read.
async fn read_object(
    store: &dyn ObjectStore,
    stream: &StreamIdentity,
    seq: u64,
    listed_bytes: usize,
) -> ReadEvent {
    let read = async {
        let Some(object) = store.get(&stream.batch_key(seq)).await? else {
            return Ok(None);
        };
        if object.bytes.len() != listed_bytes {
            return Err(LogError::Corrupt(format!(
                "batch at seq {seq} has {} bytes, but its listing showed {listed_bytes}",
                object.bytes.len()
            )));
        }
        Ok(Some(object.bytes))
    };
    ReadEvent::Object(seq, read.await)
}

pub(crate) fn decode_stream_batch(
    body: &[u8],
    stream: &StreamIdentity,
    seq: u64,
) -> Result<DecodedBatch, LogError> {
    let batch = decode_batch(body)?;
    validate_batch_identity(&batch, stream, seq)?;
    Ok(batch)
}

#[cfg(test)]
mod tests;
