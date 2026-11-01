// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::collections::BTreeSet;
use std::sync::{Arc, Mutex};

use bytes::Bytes;
use dexzd_log::{
    BatchContent, BatchRecord, LogError, LogReadAheadConfig, StreamIdentity, StreamTailReader,
    StreamTailStep, encode_batch,
};
use dexzd_object_store::{
    FaultAction, FaultInjector, InMemoryObjectStore, ObjectStore, ObjectStoreOperation,
    WriteCondition,
};

const LOG_PREFIX: &str = "log/node-a/";

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

fn epoch_start(epoch: u64, seq: u64) -> Bytes {
    encode_batch(&BatchContent {
        stream: stream(),
        epoch,
        seq,
        sealed_at_unix_micros: 42,
        records: Vec::new(),
        epoch_start_writer: Some("writer".to_owned()),
    })
    .expect("test EpochStart encodes")
}

async fn write(store: &InMemoryObjectStore, seq: u64, body: Bytes) {
    store
        .put(&stream().batch_key(seq), body, WriteCondition::CreateOnly)
        .await
        .expect("test batch is stored");
}

async fn write_batches(store: &InMemoryObjectStore, seqs: impl IntoIterator<Item = u64>) {
    for seq in seqs {
        write(store, seq, batch(1, seq)).await;
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

async fn next(reader: &mut StreamTailReader<'_>) -> Step {
    match reader.next_step().await.expect("the tail step succeeds") {
        StreamTailStep::Accepted(batch) => Step::Accepted(batch.header.seq),
        StreamTailStep::Voided(seq) => Step::Voided(seq),
        StreamTailStep::WaitingAt(seq) => Step::WaitingAt(seq),
        StreamTailStep::Ended => Step::Ended,
    }
}

/// Returns the steps up to and including the first wait or end.
async fn steps_until_wait(reader: &mut StreamTailReader<'_>) -> Vec<Step> {
    let mut steps = Vec::new();
    loop {
        let step = next(reader).await;
        steps.push(step);
        if matches!(step, Step::WaitingAt(_) | Step::Ended) {
            return steps;
        }
    }
}

fn read_ahead() -> LogReadAheadConfig {
    LogReadAheadConfig {
        max_read_ahead_batches: 4,
        ..LogReadAheadConfig::default()
    }
}

fn list_count(store: &InMemoryObjectStore) -> u64 {
    store.call_count(ObjectStoreOperation::List, LOG_PREFIX)
}

fn get_count(store: &InMemoryObjectStore) -> u64 {
    store.call_count(ObjectStoreOperation::Get, LOG_PREFIX)
}

/// Fails the first GET of each armed key once, before it takes effect.
#[derive(Default)]
struct FailFirstGets {
    armed_keys: Mutex<BTreeSet<String>>,
}

impl FailFirstGets {
    fn arm(&self, key: String) {
        self.armed_keys
            .lock()
            .expect("armed key lock is not poisoned")
            .insert(key);
    }
}

impl FaultInjector for FailFirstGets {
    fn action(&self, operation: ObjectStoreOperation, key: &str) -> FaultAction {
        let mut armed_keys = self
            .armed_keys
            .lock()
            .expect("armed key lock is not poisoned");
        if operation == ObjectStoreOperation::Get && armed_keys.remove(key) {
            return FaultAction::FailBeforeEffect;
        }
        FaultAction::Proceed
    }
}

#[tokio::test]
async fn the_void_rule_matches_recovery_from_the_start_and_from_an_applied_position() {
    let store = InMemoryObjectStore::new();
    write(&store, 1, batch(1, 1)).await;
    write(&store, 2, epoch_start(3, 2)).await;
    write(&store, 3, batch(1, 3)).await;
    // An older EpochStart after a newer one is void and cannot lower the effective epoch.
    write(&store, 4, epoch_start(2, 4)).await;
    write(&store, 5, batch(2, 5)).await;
    write(&store, 6, batch(3, 6)).await;
    let stream = stream();

    let mut reader = StreamTailReader::new(&store, &stream, 1, 0, None, read_ahead())
        .expect("the reader starts");
    assert_eq!(
        steps_until_wait(&mut reader).await,
        vec![
            Step::Accepted(1),
            Step::Accepted(2),
            Step::Voided(3),
            Step::Voided(4),
            Step::Voided(5),
            Step::Accepted(6),
            Step::WaitingAt(7),
        ]
    );
    assert_eq!(reader.effective_epoch(), 3);
    assert_eq!(reader.next_seq(), 7);

    // Resuming after seq 2 with its epoch in effect voids exactly the same batches.
    let mut resumed = StreamTailReader::new(&store, &stream, 3, 3, None, read_ahead())
        .expect("the reader starts");
    assert_eq!(
        steps_until_wait(&mut resumed).await,
        vec![
            Step::Voided(3),
            Step::Voided(4),
            Step::Voided(5),
            Step::Accepted(6),
            Step::WaitingAt(7),
        ]
    );
}

#[tokio::test]
async fn the_reader_waits_at_a_gap_while_a_later_seq_exists() {
    let store = InMemoryObjectStore::new();
    write_batches(&store, [1, 2, 4, 5]).await;
    let stream = stream();
    let mut reader = StreamTailReader::new(&store, &stream, 1, 0, None, read_ahead())
        .expect("the reader starts");
    assert_eq!(
        steps_until_wait(&mut reader).await,
        vec![Step::Accepted(1), Step::Accepted(2), Step::WaitingAt(3)]
    );
    for _ in 0..3 {
        assert_eq!(next(&mut reader).await, Step::WaitingAt(3));
    }
    assert_eq!(reader.next_seq(), 3);

    write_batches(&store, [3]).await;
    assert_eq!(
        steps_until_wait(&mut reader).await,
        vec![
            Step::Accepted(3),
            Step::Accepted(4),
            Step::Accepted(5),
            Step::WaitingAt(6),
        ]
    );
}

#[tokio::test]
async fn a_waiting_reader_probes_only_the_waiting_seq_without_listing() {
    let store = InMemoryObjectStore::new();
    write_batches(&store, 1..=3).await;
    let stream = stream();
    let mut reader = StreamTailReader::new(&store, &stream, 1, 0, None, read_ahead())
        .expect("the reader starts");
    assert_eq!(
        steps_until_wait(&mut reader).await.last(),
        Some(&Step::WaitingAt(4))
    );
    let lists = list_count(&store);
    let gets = get_count(&store);
    let waiting_key = stream.batch_key(4);
    let waiting_key_gets = store.call_count(ObjectStoreOperation::Get, &waiting_key);

    for poll in 1..=5 {
        assert_eq!(next(&mut reader).await, Step::WaitingAt(4));
        assert_eq!(get_count(&store), gets + poll, "one GET per poll");
        assert_eq!(
            store.call_count(ObjectStoreOperation::Get, &waiting_key),
            waiting_key_gets + poll,
            "every GET reads the waiting seq"
        );
    }
    write_batches(&store, [4]).await;
    assert_eq!(next(&mut reader).await, Step::Accepted(4));
    assert_eq!(next(&mut reader).await, Step::WaitingAt(5));
    assert_eq!(list_count(&store), lists, "probing never lists the stream");
}

#[tokio::test]
async fn consecutive_probe_hits_switch_back_to_read_ahead() {
    let store = InMemoryObjectStore::new();
    write_batches(&store, 1..=3).await;
    let stream = stream();
    let mut reader = StreamTailReader::new(&store, &stream, 1, 0, None, read_ahead())
        .expect("the reader starts");
    assert_eq!(
        steps_until_wait(&mut reader).await.last(),
        Some(&Step::WaitingAt(4))
    );
    let lists = list_count(&store);

    // Five hits, then a miss, which restarts the count of consecutive hits.
    write_batches(&store, 4..=8).await;
    assert_eq!(
        steps_until_wait(&mut reader).await,
        vec![
            Step::Accepted(4),
            Step::Accepted(5),
            Step::Accepted(6),
            Step::Accepted(7),
            Step::Accepted(8),
            Step::WaitingAt(9),
        ]
    );
    assert_eq!(list_count(&store), lists);

    write_batches(&store, 9..=30).await;
    let gets = get_count(&store);
    for seq in 9..=16 {
        assert_eq!(next(&mut reader).await, Step::Accepted(seq));
        assert_eq!(
            list_count(&store),
            lists,
            "seq {seq} is read by a single GET"
        );
    }
    assert_eq!(get_count(&store), gets + 8);
    // The eighth consecutive hit starts reading ahead, which lists the stream first.
    assert_eq!(next(&mut reader).await, Step::Accepted(17));
    assert_eq!(list_count(&store), lists + 1);
    let mut steps = steps_until_wait(&mut reader).await;
    assert_eq!(steps.pop(), Some(Step::WaitingAt(31)));
    assert_eq!(steps, (18..=30).map(Step::Accepted).collect::<Vec<_>>());
}

#[tokio::test]
async fn end_at_bounds_a_stream_closed_while_it_is_read() {
    let store = InMemoryObjectStore::new();
    write_batches(&store, 1..=5).await;
    // A fenced writer's late batch past the recorded end is never read.
    write_batches(&store, [6]).await;
    let stream = stream();

    let mut reading_ahead = StreamTailReader::new(&store, &stream, 1, 0, None, read_ahead())
        .expect("the reader starts");
    assert_eq!(next(&mut reading_ahead).await, Step::Accepted(1));
    assert_eq!(next(&mut reading_ahead).await, Step::Accepted(2));
    reading_ahead
        .end_at(4)
        .expect("the stream ends after the read seqs");
    assert_eq!(
        steps_until_wait(&mut reading_ahead).await,
        vec![Step::Accepted(3), Step::Accepted(4), Step::Ended]
    );
    assert_eq!(next(&mut reading_ahead).await, Step::Ended);
    reading_ahead
        .end_at(4)
        .expect("the same end can be recorded again");
    assert!(matches!(reading_ahead.end_at(5), Err(LogError::Invalid(_))));

    // A reader waiting past the recorded end has ended.
    let mut waiting = StreamTailReader::new(&store, &stream, 7, 1, None, read_ahead())
        .expect("the reader starts");
    assert_eq!(next(&mut waiting).await, Step::WaitingAt(7));
    waiting
        .end_at(6)
        .expect("the stream ends before the waiting seq");
    assert_eq!(next(&mut waiting).await, Step::Ended);

    // A reader waiting inside the recorded end keeps waiting for the missing seq.
    let mut probing = StreamTailReader::new(&store, &stream, 8, 1, None, read_ahead())
        .expect("the reader starts");
    assert_eq!(next(&mut probing).await, Step::WaitingAt(8));
    probing
        .end_at(9)
        .expect("the stream ends after the waiting seq");
    assert_eq!(next(&mut probing).await, Step::WaitingAt(8));
    write_batches(&store, [8, 9, 10]).await;
    assert_eq!(
        steps_until_wait(&mut probing).await,
        vec![Step::Accepted(8), Step::Accepted(9), Step::Ended]
    );

    let mut late = StreamTailReader::new(&store, &stream, 1, 0, None, read_ahead())
        .expect("the reader starts");
    assert_eq!(next(&mut late).await, Step::Accepted(1));
    assert_eq!(next(&mut late).await, Step::Accepted(2));
    assert!(
        matches!(late.end_at(1), Err(LogError::Invalid(_))),
        "a stream cannot end before a returned seq"
    );

    let mut closed = StreamTailReader::new(&store, &stream, 2, 1, Some(3), read_ahead())
        .expect("the reader starts");
    assert_eq!(
        steps_until_wait(&mut closed).await,
        vec![Step::Accepted(2), Step::Accepted(3), Step::Ended]
    );
}

#[tokio::test]
async fn the_deletion_check_tells_a_deleted_seq_from_an_unwritten_one() {
    let stream = stream();

    let tail = InMemoryObjectStore::new();
    write_batches(&tail, 1..=5).await;
    let mut caught_up =
        StreamTailReader::new(&tail, &stream, 1, 0, None, read_ahead()).expect("the reader starts");
    assert_eq!(
        steps_until_wait(&mut caught_up).await.last(),
        Some(&Step::WaitingAt(6))
    );
    caught_up
        .ensure_waiting_seq_was_not_deleted()
        .await
        .expect("an unwritten seq was not deleted");
    // A pipelined later batch above a hole that was never written is not a deletion.
    write_batches(&tail, [7]).await;
    caught_up
        .ensure_waiting_seq_was_not_deleted()
        .await
        .expect("a hole above a kept batch was not deleted");

    let empty = InMemoryObjectStore::new();
    write_batches(&empty, [2]).await;
    let first = StreamTailReader::new(&empty, &stream, 1, 0, None, read_ahead())
        .expect("the reader starts");
    first
        .ensure_waiting_seq_was_not_deleted()
        .await
        .expect("the first seq is never a deletion");

    let cleaned = InMemoryObjectStore::new();
    write_batches(&cleaned, 1..=3).await;
    let mut behind = StreamTailReader::new(&cleaned, &stream, 1, 0, None, read_ahead())
        .expect("the reader starts");
    assert_eq!(
        steps_until_wait(&mut behind).await.last(),
        Some(&Step::WaitingAt(4))
    );
    // While the reader waits, the stream grows and cleanup deletes batches it still needs.
    write_batches(&cleaned, 4..=10).await;
    let deleted: Vec<String> = (1..=6).map(|seq| stream.batch_key(seq)).collect();
    cleaned
        .delete_many(&deleted)
        .await
        .expect("cleanup deletes covered batches");
    assert_eq!(next(&mut behind).await, Step::WaitingAt(4));
    let result = behind.ensure_waiting_seq_was_not_deleted().await;
    assert!(
        matches!(
            result,
            Err(LogError::MissingBatches {
                missing_seq: 4,
                next_existing_seq: 7
            })
        ),
        "{result:?}"
    );
    assert_eq!(
        cleaned.call_count(ObjectStoreOperation::Put, LOG_PREFIX),
        10,
        "the check never writes"
    );
}

#[tokio::test]
async fn a_failed_read_is_read_again_by_the_next_step() {
    let faults = Arc::new(FailFirstGets::default());
    let store = InMemoryObjectStore::with_injector(faults.clone());
    write_batches(&store, 1..=6).await;
    let stream = stream();
    faults.arm(stream.batch_key(3));
    let mut reader = StreamTailReader::new(&store, &stream, 1, 0, None, read_ahead())
        .expect("the reader starts");
    assert_eq!(next(&mut reader).await, Step::Accepted(1));
    assert_eq!(next(&mut reader).await, Step::Accepted(2));
    let failed = reader.next_step().await;
    assert!(matches!(failed, Err(LogError::Store(_))), "{failed:?}");
    assert_eq!(reader.next_seq(), 3);

    // The retry probes seq 3 alone; a failed probe is read again too.
    faults.arm(stream.batch_key(4));
    assert_eq!(next(&mut reader).await, Step::Accepted(3));
    assert!(matches!(reader.next_step().await, Err(LogError::Store(_))));
    assert_eq!(
        steps_until_wait(&mut reader).await,
        vec![
            Step::Accepted(4),
            Step::Accepted(5),
            Step::Accepted(6),
            Step::WaitingAt(7),
        ]
    );
}

#[tokio::test]
async fn the_reader_rejects_seq_zero_and_invalid_node_ids() {
    let store = InMemoryObjectStore::new();
    let stream = stream();
    assert!(matches!(
        StreamTailReader::new(&store, &stream, 0, 0, None, read_ahead()),
        Err(LogError::Invalid(_))
    ));
    let invalid = StreamIdentity {
        node_id: "node/a".to_owned(),
        ..stream
    };
    assert!(matches!(
        StreamTailReader::new(&store, &invalid, 1, 0, None, read_ahead()),
        Err(LogError::Invalid(_))
    ));
}

#[tokio::test]
async fn a_reader_follows_a_stream_inside_a_spawned_task() {
    let store = Arc::new(InMemoryObjectStore::new());
    write_batches(&store, 1..=3).await;
    let follower = tokio::spawn({
        let store = Arc::clone(&store);
        async move {
            let stream = stream();
            let mut reader =
                StreamTailReader::new(store.as_ref(), &stream, 1, 0, None, read_ahead())
                    .expect("the reader starts");
            let steps = steps_until_wait(&mut reader).await;
            reader
                .ensure_waiting_seq_was_not_deleted()
                .await
                .expect("the waiting seq was not deleted");
            steps
        }
    });
    assert_eq!(
        follower.await.expect("the follower task finishes"),
        vec![
            Step::Accepted(1),
            Step::Accepted(2),
            Step::Accepted(3),
            Step::WaitingAt(4),
        ]
    );
}
