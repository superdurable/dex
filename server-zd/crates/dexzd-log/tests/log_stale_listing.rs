// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Log reads and recovery over a listing that repeats an earlier listing, as on Tencent COS.

use std::path::Path;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use async_trait::async_trait;
use bytes::Bytes;
use dexzd_clock::WallClock;
use dexzd_log::{
    BatchContent, BatchRecord, LogReadAheadConfig, StreamIdentity, StreamRecovery,
    StreamRecoveryInputs, StreamRecoveryResult, StreamRecoveryStep, StreamTailReader,
    StreamTailStep, encode_batch,
};
use dexzd_object_store::{
    ByteRange, InMemoryObjectStore, ListingConsistency, ObjectContent, ObjectListing,
    ObjectMetadata, ObjectStore, ObjectStoreOperation, ObjectStoreResult, S3Credentials,
    S3ObjectStore, S3ObjectStoreConfig, WriteCondition, WriteOutcome,
};
use s3local::{Durability, EtagMode, Store as S3LocalStore, StoreConfig};

const LOG_PREFIX: &str = "log/node-a/";
const STEP_TIMEOUT: Duration = Duration::from_secs(10);
/// The server's tail probe window with tencentCOS, its limit of commit.maxInFlight.
const TENCENT_COS_TAIL_PROBE_MISSES: usize = 32;

struct FixedClock;

impl WallClock for FixedClock {
    fn now_unix_micros(&self) -> i64 {
        1_700_000_000_000_000
    }
}

fn stream() -> StreamIdentity {
    StreamIdentity {
        cluster_id: [7; 16],
        node_id: "node-a".to_owned(),
        incarnation: 3,
    }
}

fn batch(epoch: u64, seq: u64) -> Bytes {
    encode_batch(&BatchContent {
        stream: stream(),
        epoch,
        seq,
        sealed_at_unix_micros: 42,
        records: vec![BatchRecord {
            partition: 2,
            bytes: Bytes::from(format!("seq {seq}")),
        }],
        epoch_start_writer: None,
    })
    .expect("test batch encodes")
}

fn stale_listing_store() -> InMemoryObjectStore {
    InMemoryObjectStore::with_stale_listings()
}

async fn write_batches(store: &dyn ObjectStore, epoch: u64, seqs: impl IntoIterator<Item = u64>) {
    for seq in seqs {
        store
            .put(
                &stream().batch_key(seq),
                batch(epoch, seq),
                WriteCondition::CreateOnly,
            )
            .await
            .expect("test batch is stored");
    }
}

/// Lists the stream prefix once, so later listings of it repeat this one.
async fn list_stream_prefix(store: &dyn ObjectStore, max_keys: u32) {
    let first_key = stream().batch_key(1);
    let prefix = &first_key[..first_key.rfind('/').expect("a batch key has a seq") + 1];
    store
        .list(prefix, None, max_keys)
        .await
        .expect("the listing succeeds");
}

fn read_ahead(listing_consistency: ListingConsistency) -> LogReadAheadConfig {
    LogReadAheadConfig {
        max_read_ahead_batches: 4,
        listing_consistency,
        ..LogReadAheadConfig::default()
    }
}

/// A step without the batch body, so tests compare whole sequences of steps.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum Step {
    Accepted(u64),
    Voided(u64),
    WaitingAt(u64),
    Ended,
}

/// Returns the steps up to and including the first wait or end.
async fn steps_until_wait(reader: &mut StreamTailReader<'_>) -> Vec<Step> {
    let mut steps = Vec::new();
    loop {
        let step = tokio::time::timeout(STEP_TIMEOUT, reader.next_step())
            .await
            .expect("the tail step finishes")
            .expect("the tail step succeeds");
        let step = match step {
            StreamTailStep::Accepted(batch) => Step::Accepted(batch.header.seq),
            StreamTailStep::Voided(seq) => Step::Voided(seq),
            StreamTailStep::WaitingAt(seq) => Step::WaitingAt(seq),
            StreamTailStep::Ended => Step::Ended,
            step => panic!("unexpected tail step {step:?}"),
        };
        steps.push(step);
        if matches!(step, Step::WaitingAt(_) | Step::Ended) {
            return steps;
        }
    }
}

async fn recover(
    store: &dyn ObjectStore,
    stream: &StreamIdentity,
    new_epoch: u64,
    read_ahead: LogReadAheadConfig,
) -> (Vec<u64>, StreamRecoveryResult) {
    let mut recovery = StreamRecovery::from_seq(
        StreamRecoveryInputs {
            store,
            clock: &FixedClock,
            stream,
            new_epoch,
            writer_process_id: "recovering-writer",
            read_ahead,
        },
        1,
        0,
    )
    .expect("recovery starts");
    let mut applied = Vec::new();
    loop {
        let step = tokio::time::timeout(STEP_TIMEOUT, recovery.next_step())
            .await
            .expect("the recovery step finishes")
            .expect("the recovery step succeeds");
        match step {
            StreamRecoveryStep::Apply(batch) => applied.push(batch.header.seq),
            StreamRecoveryStep::Recovered(result) => return (applied, result),
        }
    }
}

#[tokio::test]
async fn a_closed_stream_reads_batches_that_a_stale_listing_misses() {
    let store = stale_listing_store();
    write_batches(&store, 1, 1..=3).await;
    list_stream_prefix(&store, 1000).await;
    write_batches(&store, 1, 4..=6).await;
    let stream = stream();

    let mut reader = StreamTailReader::new(
        &store,
        &stream,
        1,
        0,
        Some(6),
        read_ahead(ListingConsistency::Eventual),
    )
    .expect("the reader starts");
    assert_eq!(
        steps_until_wait(&mut reader).await,
        [1, 2, 3, 4, 5, 6]
            .map(Step::Accepted)
            .into_iter()
            .chain([Step::Ended])
            .collect::<Vec<_>>()
    );
}

#[tokio::test]
async fn a_closed_stream_reads_past_a_repeated_truncated_page() {
    let store = stale_listing_store();
    write_batches(&store, 1, 1..=5).await;
    // A one-key page that every later listing of the prefix repeats, still truncated.
    list_stream_prefix(&store, 1).await;
    let stream = stream();

    let mut reader = StreamTailReader::new(
        &store,
        &stream,
        2,
        1,
        Some(5),
        read_ahead(ListingConsistency::Eventual),
    )
    .expect("the reader starts");
    assert_eq!(
        steps_until_wait(&mut reader).await,
        [2, 3, 4, 5]
            .map(Step::Accepted)
            .into_iter()
            .chain([Step::Ended])
            .collect::<Vec<_>>()
    );
    assert!(
        store.call_count(ObjectStoreOperation::List, LOG_PREFIX) <= 6,
        "each listing after the repeated page follows a found batch"
    );
}

#[tokio::test]
async fn an_open_stream_reads_a_gap_that_a_stale_listing_shows() {
    let store = stale_listing_store();
    write_batches(&store, 1, [1, 3]).await;
    list_stream_prefix(&store, 1000).await;
    // A pipelined write below a listed batch lands after the listing.
    write_batches(&store, 1, [2]).await;
    let stream = stream();

    let mut reader = StreamTailReader::new(
        &store,
        &stream,
        1,
        0,
        None,
        read_ahead(ListingConsistency::Eventual),
    )
    .expect("the reader starts");
    assert_eq!(
        steps_until_wait(&mut reader).await,
        [
            Step::Accepted(1),
            Step::Accepted(2),
            Step::Accepted(3),
            Step::WaitingAt(4)
        ]
    );
}

#[tokio::test]
async fn recovery_finds_batches_after_the_epoch_start_that_a_stale_listing_misses() {
    let store = stale_listing_store();
    write_batches(&store, 1, 1..=3).await;
    list_stream_prefix(&store, 1000).await;
    // The old writer kept writing; seq 7 was still in flight when it stopped.
    write_batches(&store, 1, [4, 5, 6, 8]).await;
    let stream = stream();

    let (applied, result) =
        recover(&store, &stream, 2, read_ahead(ListingConsistency::Eventual)).await;
    assert_eq!(applied, [1, 2, 3, 4, 5, 6]);
    assert_eq!(result.epoch_start_seq, 7);
    assert_eq!(result.voided_batches, 1, "the batch at seq 8 is voided");
    assert_eq!(
        result.next_seq, 9,
        "the next writer continues after the old writer's last batch"
    );
}

#[tokio::test]
async fn recovery_probes_past_holes_shorter_than_the_tail_probe_window() {
    let store = stale_listing_store();
    write_batches(&store, 1, [1]).await;
    list_stream_prefix(&store, 1000).await;
    write_batches(&store, 1, [3, 6]).await;
    let stream = stream();

    let (applied, result) = recover(
        &store,
        &stream,
        2,
        LogReadAheadConfig {
            tail_probe_misses: 3,
            ..read_ahead(ListingConsistency::Eventual)
        },
    )
    .await;
    assert_eq!(applied, [1]);
    assert_eq!(result.epoch_start_seq, 2);
    assert_eq!(result.voided_batches, 2, "seqs 3 and 6 are voided");
    assert_eq!(result.next_seq, 7);
    for seq in [4, 5] {
        assert!(
            store
                .head(&stream.batch_key(seq))
                .await
                .expect("the head succeeds")
                .is_some(),
            "recovery fills the hole at seq {seq} with an EpochStart"
        );
    }
}

#[tokio::test]
async fn recovery_finds_the_last_batch_of_an_old_writer_with_a_wider_window() {
    let store = stale_listing_store();
    write_batches(&store, 1, [1]).await;
    list_stream_prefix(&store, 1000).await;
    // The old writer had 32 writes in flight; only its last one landed, 31 seqs past seq 2.
    let late_seq = 2 + TENCENT_COS_TAIL_PROBE_MISSES as u64;
    write_batches(&store, 1, [late_seq]).await;
    let stream = stream();

    let (applied, result) = recover(
        &store,
        &stream,
        2,
        LogReadAheadConfig {
            tail_probe_misses: TENCENT_COS_TAIL_PROBE_MISSES,
            ..read_ahead(ListingConsistency::Eventual)
        },
    )
    .await;
    assert_eq!(applied, [1]);
    assert_eq!(result.epoch_start_seq, 2);
    assert_eq!(
        result.voided_batches, 1,
        "the old writer's last batch is voided"
    );
    assert_eq!(
        result.next_seq,
        late_seq + 1,
        "a new writer with fewer writes in flight still continues after the old writer"
    );
}

/// Writes a batch of the old writer when recovery first reads the head of a trigger key.
struct LateWriteStore {
    inner: InMemoryObjectStore,
    late_write: Mutex<Option<(String, u64)>>,
}

#[async_trait]
impl ObjectStore for LateWriteStore {
    async fn put(
        &self,
        key: &str,
        body: Bytes,
        condition: WriteCondition,
    ) -> ObjectStoreResult<WriteOutcome> {
        self.inner.put(key, body, condition).await
    }

    async fn put_file(
        &self,
        key: &str,
        path: &Path,
        condition: WriteCondition,
    ) -> ObjectStoreResult<WriteOutcome> {
        self.inner.put_file(key, path, condition).await
    }

    async fn get(&self, key: &str) -> ObjectStoreResult<Option<ObjectContent>> {
        self.inner.get(key).await
    }

    async fn get_range(
        &self,
        key: &str,
        range: ByteRange,
    ) -> ObjectStoreResult<Option<ObjectContent>> {
        self.inner.get_range(key, range).await
    }

    async fn get_to_file(&self, key: &str, path: &Path) -> ObjectStoreResult<Option<u64>> {
        self.inner.get_to_file(key, path).await
    }

    async fn head(&self, key: &str) -> ObjectStoreResult<Option<ObjectMetadata>> {
        let late_write = {
            let mut late_write = self.late_write.lock().expect("late write lock");
            match late_write.as_ref() {
                Some((trigger_key, _)) if trigger_key == key => late_write.take(),
                _ => None,
            }
        };
        if let Some((_, seq)) = late_write {
            write_batches(&self.inner, 1, [seq]).await;
        }
        self.inner.head(key).await
    }

    async fn delete(&self, key: &str) -> ObjectStoreResult<()> {
        self.inner.delete(key).await
    }

    async fn delete_many(&self, keys: &[String]) -> ObjectStoreResult<()> {
        self.inner.delete_many(keys).await
    }

    async fn list(
        &self,
        prefix: &str,
        start_after: Option<&str>,
        max_keys: u32,
    ) -> ObjectStoreResult<ObjectListing> {
        self.inner.list(prefix, start_after, max_keys).await
    }
}

#[tokio::test]
async fn recovery_finds_a_batch_that_lands_while_it_probes_the_tail() {
    let stream = stream();
    let store = LateWriteStore {
        inner: stale_listing_store(),
        late_write: Mutex::new(Some((stream.batch_key(3), 6))),
    };
    write_batches(&store, 1, [1]).await;
    list_stream_prefix(&store, 1000).await;
    write_batches(&store, 1, [3]).await;

    let (applied, result) = recover(
        &store,
        &stream,
        2,
        LogReadAheadConfig {
            tail_probe_misses: 4,
            ..read_ahead(ListingConsistency::Eventual)
        },
    )
    .await;
    assert_eq!(applied, [1]);
    assert_eq!(result.epoch_start_seq, 2);
    assert_eq!(result.voided_batches, 2, "seqs 3 and 6 are voided");
    assert_eq!(
        result.next_seq, 7,
        "the write that landed during the probe is found"
    );
    for seq in [4, 5] {
        assert!(
            store
                .head(&stream.batch_key(seq))
                .await
                .expect("the head succeeds")
                .is_some(),
            "recovery fills the hole at seq {seq} with an EpochStart"
        );
    }
}

/// With a strongly consistent listing, these reads keep their request pattern exactly.
#[tokio::test]
async fn a_strong_listing_keeps_the_request_pattern() {
    let stream = stream();
    let count = |store: &InMemoryObjectStore| {
        [
            ObjectStoreOperation::List,
            ObjectStoreOperation::Get,
            ObjectStoreOperation::Head,
            ObjectStoreOperation::Put,
        ]
        .map(|operation| store.call_count(operation, LOG_PREFIX))
    };

    let closed = InMemoryObjectStore::new();
    write_batches(&closed, 1, (1..=9).filter(|seq| *seq != 5)).await;
    let mut reader = StreamTailReader::new(
        &closed,
        &stream,
        1,
        0,
        Some(9),
        read_ahead(ListingConsistency::Strong),
    )
    .expect("the reader starts");
    assert_eq!(
        steps_until_wait(&mut reader).await.last(),
        Some(&Step::WaitingAt(5))
    );
    drop(reader);
    assert_eq!(count(&closed), STRONG_CLOSED_STREAM_REQUESTS);

    let open = InMemoryObjectStore::new();
    write_batches(&open, 1, (1..=6).chain([8, 9])).await;
    let mut reader = StreamTailReader::new(
        &open,
        &stream,
        1,
        0,
        None,
        read_ahead(ListingConsistency::Strong),
    )
    .expect("the reader starts");
    for _ in 0..3 {
        steps_until_wait(&mut reader).await;
    }
    drop(reader);
    assert_eq!(count(&open), STRONG_OPEN_STREAM_REQUESTS);

    let recovered = InMemoryObjectStore::new();
    write_batches(&recovered, 1, (1..=6).chain([8, 10])).await;
    let (applied, result) = recover(
        &recovered,
        &stream,
        2,
        read_ahead(ListingConsistency::Strong),
    )
    .await;
    assert_eq!(applied, [1, 2, 3, 4, 5, 6]);
    assert_eq!((result.epoch_start_seq, result.next_seq), (7, 11));
    assert_eq!(count(&recovered), STRONG_RECOVERY_REQUESTS);
}

/// LIST, GET, HEAD, and PUT calls above, test writes included, measured before eventual listings.
const STRONG_CLOSED_STREAM_REQUESTS: [u64; 4] = [1, 4, 0, 8];
const STRONG_OPEN_STREAM_REQUESTS: [u64; 4] = [1, 8, 0, 8];
const STRONG_RECOVERY_REQUESTS: [u64; 4] = [3, 8, 0, 10];

/// The s3local `stale_listing` fault reproduces the closed-stream failure through an S3 client.
#[tokio::test]
async fn a_closed_stream_reads_batches_that_an_s3local_stale_listing_misses() {
    let directory = tempfile::tempdir().expect("the s3local directory is created");
    let s3local_store = S3LocalStore::open(&StoreConfig {
        root_directory: directory.path().to_path_buf(),
        durability: Durability::Relaxed,
        etag_mode: EtagMode::Opaque,
    })
    .expect("the s3local store opens");
    s3local_store
        .create_bucket("stale-listing", false)
        .expect("the s3local bucket is created");
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
        .await
        .expect("the s3local listener binds");
    let server = s3local_server::start(Arc::new(s3local_store), listener)
        .expect("the s3local server starts");
    let mut config = S3ObjectStoreConfig::new("us-east-1", "stale-listing");
    config.endpoint = Some(format!("http://{}", server.local_address()));
    config.key_prefix = "dexzd".to_owned();
    config.force_path_style = true;
    config.credentials = S3Credentials::Static {
        access_key_id: "s3local".to_owned(),
        secret_access_key: "s3local".to_owned(),
        session_token: None,
    };
    let store = S3ObjectStore::new(config)
        .await
        .expect("the S3 client is configured");
    set_stale_listing_fault(server.local_address()).await;
    write_batches(&store, 1, 1..=2).await;
    list_stream_prefix(&store, 1000).await;
    write_batches(&store, 1, 3..=4).await;
    let stream = stream();

    for (listing_consistency, expected) in [
        (
            ListingConsistency::Strong,
            vec![Step::Accepted(1), Step::Accepted(2), Step::WaitingAt(3)],
        ),
        (
            ListingConsistency::Eventual,
            [1, 2, 3, 4]
                .map(Step::Accepted)
                .into_iter()
                .chain([Step::Ended])
                .collect(),
        ),
    ] {
        let mut reader = StreamTailReader::new(
            &store,
            &stream,
            1,
            0,
            Some(4),
            read_ahead(listing_consistency),
        )
        .expect("the reader starts");
        assert_eq!(
            steps_until_wait(&mut reader).await,
            expected,
            "{listing_consistency:?}"
        );
    }
    server.shutdown().await;
}

async fn set_stale_listing_fault(address: std::net::SocketAddr) {
    use tokio::io::{AsyncReadExt, AsyncWriteExt};
    let body = r#"{"rules":[{"operation":"ListObjectsV2","action":{"type":"stale_listing","ttl_milliseconds":600000}}]}"#;
    let mut connection = tokio::net::TcpStream::connect(address)
        .await
        .expect("the s3local admin endpoint is reachable");
    let request = format!(
        "PUT /_s3local/faults HTTP/1.1\r\nHost: {address}\r\nConnection: close\r\nContent-Type: application/json\r\nContent-Length: {}\r\n\r\n{body}",
        body.len()
    );
    connection
        .write_all(request.as_bytes())
        .await
        .expect("the fault request is sent");
    let mut response = Vec::new();
    connection
        .read_to_end(&mut response)
        .await
        .expect("the fault response is read");
    assert!(
        response.starts_with(b"HTTP/1.1 200"),
        "{}",
        String::from_utf8_lossy(&response)
    );
}
