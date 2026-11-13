// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Log reads and recovery over the empty objects that Tencent COS shows during an append to a
//! missing key and keeps after an interrupted one.

use std::net::SocketAddr;
use std::sync::Arc;
use std::time::Duration;

use bytes::Bytes;
use dexzd_clock::WallClock;
use dexzd_log::{
    BatchContent, BatchRecord, LogReadAheadConfig, StreamIdentity, StreamRecovery,
    StreamRecoveryInputs, StreamRecoveryResult, StreamRecoveryStep, StreamTailReader,
    StreamTailStep, decode_batch, encode_batch,
};
use dexzd_object_store::{
    ListingConsistency, ObjectStore, ObjectStoreErrorKind, S3ApiDialect, S3Credentials,
    S3ObjectStore, S3ObjectStoreConfig, WriteCondition,
};
use s3local::{Durability, EtagMode, Store as S3LocalStore, StoreConfig};

const BUCKET: &str = "tencent-cos-log";
const KEY_PREFIX: &str = "dexzd";
const STEP_TIMEOUT: Duration = Duration::from_secs(10);
/// The server's tail probe window with tencentCOS, its limit of commit.maxInFlight.
const TENCENT_COS_TAIL_PROBE_MISSES: usize = 32;

struct FixedClock;

impl WallClock for FixedClock {
    fn now_unix_micros(&self) -> i64 {
        1_700_000_000_000_000
    }
}

/// s3local in the Tencent COS dialect and a client of that dialect.
struct TencentCosLog {
    _directory: tempfile::TempDir,
    server: s3local_server::ServerHandle,
    store: Arc<S3ObjectStore>,
}

impl TencentCosLog {
    async fn start() -> Self {
        let directory = tempfile::tempdir().expect("the s3local directory is created");
        let s3local_store = S3LocalStore::open(&StoreConfig {
            root_directory: directory.path().to_path_buf(),
            durability: Durability::Relaxed,
            etag_mode: EtagMode::Md5,
        })
        .expect("the s3local store opens");
        s3local_store
            .create_bucket(BUCKET, false)
            .expect("the s3local bucket is created");
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
            .await
            .expect("the s3local listener binds");
        let server = s3local_server::start_with_config(
            Arc::new(s3local_store),
            listener,
            s3local_server::ServerConfig {
                api_dialect: s3local_server::S3ApiDialect::TencentCos,
                ..s3local_server::ServerConfig::default()
            },
        )
        .expect("the s3local server starts");
        let mut config = S3ObjectStoreConfig::new("us-east-1", BUCKET);
        config.endpoint = Some(format!("http://{}", server.local_address()));
        config.key_prefix = KEY_PREFIX.to_owned();
        config.force_path_style = true;
        config.api_dialect = S3ApiDialect::TencentCos;
        config.credentials = S3Credentials::Static {
            access_key_id: "s3local".to_owned(),
            secret_access_key: "s3local".to_owned(),
            session_token: None,
        };
        let store = S3ObjectStore::new(config)
            .await
            .expect("the S3 client is configured");
        Self {
            _directory: directory,
            server,
            store: Arc::new(store),
        }
    }

    /// Applies `action` to every append to the batch key of `seq`.
    async fn fault_batch(&self, seq: u64, action: &str) {
        let rule = format!(
            r#"{{"operation":"AppendObject","key_prefix":"{KEY_PREFIX}/{}","action":{action}}}"#,
            stream().batch_key(seq)
        );
        set_fault_rules(self.server.local_address(), &rule).await;
    }

    async fn clear_faults(&self) {
        set_fault_rules(self.server.local_address(), "").await;
    }

    /// Leaves an empty object at `seq`, as a writer that crashed mid-append does.
    async fn leave_residue(&self, epoch: u64, seq: u64) {
        self.fault_batch(seq, r#"{"type":"interrupted_append"}"#)
            .await;
        let error = self
            .store
            .put(
                &stream().batch_key(seq),
                batch(epoch, seq),
                WriteCondition::CreateOnly,
            )
            .await
            .expect_err("the interrupted append fails");
        assert_eq!(error.kind, ObjectStoreErrorKind::OutcomeUnknown, "{error}");
        self.clear_faults().await;
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

/// The read-ahead of the server with tencentCOS.
fn tencent_cos_read_ahead() -> LogReadAheadConfig {
    LogReadAheadConfig {
        max_read_ahead_batches: 4,
        listing_consistency: ListingConsistency::Eventual,
        tail_probe_misses: TENCENT_COS_TAIL_PROBE_MISSES,
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

async fn recover(store: &dyn ObjectStore, new_epoch: u64) -> (Vec<u64>, StreamRecoveryResult) {
    let stream = stream();
    let mut recovery = StreamRecovery::from_seq(
        StreamRecoveryInputs {
            store,
            clock: &FixedClock,
            stream: &stream,
            new_epoch,
            writer_process_id: "recovering-writer",
            read_ahead: tencent_cos_read_ahead(),
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

/// Returns the epoch of the batch at `seq`, which must exist.
async fn epoch_at(store: &dyn ObjectStore, seq: u64) -> u64 {
    let object = store
        .get(&stream().batch_key(seq))
        .await
        .expect("the batch reads")
        .expect("the batch exists");
    decode_batch(&object.bytes)
        .expect("the batch decodes")
        .header
        .epoch
}

/// A hot standby following the log waits at a batch whose append is in flight.
#[tokio::test]
async fn a_follower_waits_at_a_batch_whose_append_is_in_flight() {
    let log = TencentCosLog::start().await;
    write_batches(log.store.as_ref(), 1, 1..=3).await;
    log.fault_batch(4, r#"{"type":"append_placeholder","milliseconds":1500}"#)
        .await;
    let pending_write = tokio::spawn({
        let store = Arc::clone(&log.store);
        async move {
            store
                .put(
                    &stream().batch_key(4),
                    batch(1, 4),
                    WriteCondition::CreateOnly,
                )
                .await
        }
    });
    tokio::time::sleep(Duration::from_millis(500)).await;
    let stream = stream();
    let mut reader = StreamTailReader::new(
        log.store.as_ref(),
        &stream,
        1,
        0,
        None,
        tencent_cos_read_ahead(),
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
    pending_write
        .await
        .expect("the write task completes")
        .expect("the append commits");
    let mut reader = StreamTailReader::new(
        log.store.as_ref(),
        &stream,
        4,
        1,
        None,
        tencent_cos_read_ahead(),
    )
    .expect("the reader starts");
    assert_eq!(
        steps_until_wait(&mut reader).await,
        [Step::Accepted(4), Step::WaitingAt(5)]
    );
    log.server.shutdown().await;
}

/// The writer crashed mid-append at seq 4 while seqs 5 and 6 committed.
#[tokio::test]
async fn recovery_writes_the_epoch_start_over_an_interrupted_append_below_later_batches() {
    let log = TencentCosLog::start().await;
    write_batches(log.store.as_ref(), 1, 1..=3).await;
    log.leave_residue(1, 4).await;
    write_batches(log.store.as_ref(), 1, 5..=6).await;

    let (applied, result) = recover(log.store.as_ref(), 2).await;
    assert_eq!(applied, [1, 2, 3]);
    assert_eq!(result.epoch_start_seq, 4);
    assert_eq!(result.voided_batches, 2, "seqs 5 and 6 are voided");
    assert_eq!(result.next_seq, 7);
    assert_eq!(epoch_at(log.store.as_ref(), 4).await, 2);
    log.server.shutdown().await;
}

/// The writer crashed mid-append at its last seq.
#[tokio::test]
async fn recovery_writes_the_epoch_start_over_an_interrupted_append_at_the_tail() {
    let log = TencentCosLog::start().await;
    write_batches(log.store.as_ref(), 1, 1..=3).await;
    log.leave_residue(1, 4).await;

    let (applied, result) = recover(log.store.as_ref(), 2).await;
    assert_eq!(applied, [1, 2, 3]);
    assert_eq!(result.epoch_start_seq, 4);
    assert_eq!(result.voided_batches, 0);
    assert_eq!(result.next_seq, 5);
    assert_eq!(epoch_at(log.store.as_ref(), 4).await, 2);
    log.server.shutdown().await;
}

async fn set_fault_rules(address: SocketAddr, rules: &str) {
    use tokio::io::{AsyncReadExt, AsyncWriteExt};
    let body = format!(r#"{{"rules":[{rules}]}}"#);
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
