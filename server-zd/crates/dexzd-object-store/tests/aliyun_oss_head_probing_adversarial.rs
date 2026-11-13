// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! HEAD-probing version discovery stays linearizable whatever the clocks and listings do: frozen
//! VMs, process clocks far behind the store's, and listings staler than the reclamation age.

#[expect(
    dead_code,
    reason = "these tests use only part of the shared create-only store"
)]
#[path = "aliyun_oss/create_only_store.rs"]
mod create_only_store;
#[expect(
    dead_code,
    reason = "these tests choose the version discovery of every workload"
)]
#[path = "aliyun_oss/replacement_history.rs"]
mod replacement_history;

use std::sync::Arc;
use std::time::Duration;

use bytes::Bytes;
use dexzd_metrics::NodeMetrics;
use dexzd_object_store::{
    AliyunOssObjectStore, FaultAction, MANIFEST_OBJECT_KEY, ObjectStore, ObjectStoreErrorKind,
    ObjectStoreOperation, VersionDiscovery, WriteCondition, WriteOutcome,
};

use create_only_store::{
    CreateOnlyStore, FastStoreClock, FreezableStore, ManualStoreClock, RandomDelays,
    TEST_MINIMUM_SUPERSEDED_AGE, version_key,
};
use replacement_history::{ReplacementWorkload, run_replacement_workload_with_discovery};

const KEY: &str = MANIFEST_OBJECT_KEY;
/// Longer than the reclamation age, so the next confirmed write may reclaim older versions.
const RECLAIMABLE_GAP: Duration = Duration::from_secs(TEST_MINIMUM_SUPERSEDED_AGE.as_secs() + 1);
/// How long a stale listing repeats itself; Tencent COS was measured at 1 to 3 seconds.
const STALE_LISTING_LIFETIME: Duration = Duration::from_secs(3);

fn head_probing_store(inner: Arc<dyn ObjectStore>) -> AliyunOssObjectStore {
    AliyunOssObjectStore::with_version_discovery(
        inner,
        TEST_MINIMUM_SUPERSEDED_AGE,
        NodeMetrics::unregistered(),
        VersionDiscovery::HeadProbing,
    )
}

async fn create(store: &dyn ObjectStore, body: &'static str) -> String {
    match store
        .put(
            KEY,
            Bytes::from_static(body.as_bytes()),
            WriteCondition::CreateOnly,
        )
        .await
        .expect("create")
    {
        WriteOutcome::Written { etag } => etag,
        WriteOutcome::PreconditionFailed => panic!("the create must succeed"),
    }
}

async fn replace(store: &dyn ObjectStore, etag: &str, body: &'static str) -> String {
    match store
        .put(
            KEY,
            Bytes::from_static(body.as_bytes()),
            WriteCondition::ReplaceIfEtagMatches {
                etag: etag.to_owned(),
            },
        )
        .await
        .expect("replace")
    {
        WriteOutcome::Written { etag } => etag,
        WriteOutcome::PreconditionFailed => panic!("the replacement must succeed"),
    }
}

/// Waits a few milliseconds of Tokio time until generation `generation` of `KEY` is gone.
async fn wait_until_reclaimed(physical: &CreateOnlyStore, generation: u64) {
    for _ in 0..100 {
        let head = physical
            .store
            .head(&version_key(KEY, generation))
            .await
            .expect("head");
        if head.is_none() {
            return;
        }
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    panic!("generation {generation} was never reclaimed");
}

/// Node A deletes the key and remembers its tombstone. Node B then writes three versions while
/// the store clock passes the reclamation age between them, but Tokio's clock, which is node A's,
/// stands still. Node A must still read B's newest version, not its own superseded tombstone.
#[tokio::test(start_paused = true)]
async fn a_node_whose_clock_stood_still_reads_the_newest_version_not_its_old_tombstone() {
    let store_clock = ManualStoreClock::new();
    let physical = CreateOnlyStore::with_store_clock(store_clock.clone());
    let node_a = head_probing_store(physical.inner());
    let node_b = head_probing_store(physical.inner());
    create(&node_a, "one").await;
    node_a.delete(KEY).await.expect("delete");

    store_clock.advance(RECLAIMABLE_GAP);
    let third = create(&node_b, "three").await;
    store_clock.advance(RECLAIMABLE_GAP);
    let fourth = replace(&node_b, &third, "four").await;
    store_clock.advance(RECLAIMABLE_GAP);
    replace(&node_b, &fourth, "five").await;
    wait_until_reclaimed(&physical, 3).await;

    let content = node_a
        .get(KEY)
        .await
        .expect("get")
        .expect("node B's write of five completed before this read");
    assert_eq!(content.bytes, Bytes::from_static(b"five"));
}

/// Node A reads generation 1 and freezes before its create of generation 2, without its clock
/// moving. Node B meanwhile writes generations 2 to 5 while the store clock passes the
/// reclamation age, and reclamation removes generations 1 to 3. Node A's late create recreates
/// generation 2, which must never be confirmed as written.
#[tokio::test(start_paused = true)]
async fn a_frozen_writer_never_confirms_a_recreated_reclaimed_generation() {
    let store_clock = ManualStoreClock::new();
    let physical = CreateOnlyStore::with_store_clock(store_clock.clone());
    let node_a_view = FreezableStore::new(physical.inner());
    let node_a = Arc::new(head_probing_store(
        Arc::clone(&node_a_view) as Arc<dyn ObjectStore>
    ));
    let node_b = head_probing_store(physical.inner());
    let first = create(&node_b, "one").await;

    let frozen = node_a_view.freeze_next(ObjectStoreOperation::Put, &version_key(KEY, 2));
    let stale_node_a = Arc::clone(&node_a);
    let stale_etag = first.clone();
    let stale_writer = tokio::spawn(async move {
        stale_node_a
            .put(
                KEY,
                Bytes::from_static(b"stale two"),
                WriteCondition::ReplaceIfEtagMatches { etag: stale_etag },
            )
            .await
    });
    frozen.wait_until_reached().await;

    let mut etag = first;
    for body in ["two", "three", "four", "five"] {
        store_clock.advance(RECLAIMABLE_GAP);
        etag = replace(&node_b, &etag, body).await;
    }
    wait_until_reclaimed(&physical, 3).await;
    frozen.release();

    let outcome = stale_writer
        .await
        .expect("node A finishes")
        .expect("node A's put completes");
    assert_eq!(
        outcome,
        WriteOutcome::PreconditionFailed,
        "node A read generation 1 before node B replaced it"
    );
    assert_eq!(
        node_b
            .get(KEY)
            .await
            .expect("get")
            .expect("the key exists")
            .bytes,
        Bytes::from_static(b"five")
    );
}

/// A stale writer's recreated generation 2 lands above the generation 1 that a reader remembers,
/// with a gap above it, and stays while the stale writer's confirmation stalls. The reader's
/// clock stands still, so its memory of generation 1 looks fresh, but it must read generation 5.
#[tokio::test(start_paused = true)]
async fn a_recreated_generation_above_a_remembered_one_is_never_read_as_newest() {
    let store_clock = ManualStoreClock::new();
    let physical = CreateOnlyStore::with_store_clock(store_clock.clone());
    let reader = head_probing_store(physical.inner());
    let writer = head_probing_store(physical.inner());
    let first = create(&writer, "one").await;
    assert_eq!(
        reader.get(KEY).await.expect("get").expect("exists").bytes,
        Bytes::from_static(b"one")
    );
    let stale_view = FreezableStore::new(physical.inner());
    let stale_writer_store = Arc::new(head_probing_store(
        Arc::clone(&stale_view) as Arc<dyn ObjectStore>
    ));
    let frozen = stale_view.freeze_next(ObjectStoreOperation::Put, &version_key(KEY, 2));
    let stale_etag = first.clone();
    let stale_writer = tokio::spawn(async move {
        stale_writer_store
            .put(
                KEY,
                Bytes::from_static(b"stale two"),
                WriteCondition::ReplaceIfEtagMatches { etag: stale_etag },
            )
            .await
    });
    frozen.wait_until_reached().await;
    let mut etag = first;
    for body in ["two", "three", "four", "five"] {
        store_clock.advance(RECLAIMABLE_GAP);
        etag = replace(&writer, &etag, body).await;
    }
    wait_until_reclaimed(&physical, 3).await;
    // The stale writer's confirmation stalls on its first read, keeping its recreated version.
    for neighbor in [1, 3] {
        physical.faults.inject_after_put(
            &version_key(KEY, 2),
            ObjectStoreOperation::Get,
            &version_key(KEY, neighbor),
            FaultAction::Delay(Duration::from_secs(600)),
        );
    }
    frozen.release();
    for _ in 0..100 {
        if physical
            .store
            .head(&version_key(KEY, 2))
            .await
            .expect("head")
            .is_some()
        {
            break;
        }
        tokio::task::yield_now().await;
    }
    assert_eq!(
        reader.get(KEY).await.expect("get").expect("exists").bytes,
        Bytes::from_static(b"five"),
        "the writer's version five completed before this read"
    );
    let outcome = stale_writer
        .await
        .expect("the stale writer finishes")
        .expect("the stale put completes");
    assert_eq!(outcome, WriteOutcome::PreconditionFailed);
}

/// A key whose generation 1 was reclaimed is never read as absent, even when every listing of
/// its versions repeats the empty listing made before its first write.
#[tokio::test(start_paused = true)]
async fn a_key_whose_oldest_generations_were_reclaimed_is_never_read_as_absent() {
    let physical = CreateOnlyStore::with_stale_listings(Duration::MAX);
    let writer = head_probing_store(physical.inner());
    // The create lists the versions first, so every later listing shows none.
    let mut etag = create(&writer, "one").await;
    for body in ["two", "three"] {
        tokio::time::sleep(RECLAIMABLE_GAP).await;
        etag = replace(&writer, &etag, body).await;
    }
    wait_until_reclaimed(&physical, 1).await;
    let fresh_reader = head_probing_store(physical.inner());
    let error = fresh_reader
        .get(KEY)
        .await
        .expect_err("no listing shows a version to start from");
    assert_eq!(error.kind, ObjectStoreErrorKind::Transient, "{error}");
    let created = fresh_reader
        .put(
            KEY,
            Bytes::from_static(b"recreated"),
            WriteCondition::CreateOnly,
        )
        .await;
    assert!(
        created.is_err(),
        "a create must not treat the key as absent: {created:?}"
    );
    physical.store.expire_stale_listings();
    assert_eq!(
        fresh_reader
            .get(KEY)
            .await
            .expect("get")
            .expect("exists")
            .bytes,
        Bytes::from_static(b"three")
    );
}

/// Racing writers and readers over stale listings, while the store clock runs a thousand times
/// faster than every process's clock, so reclamation by store time races every operation.
#[tokio::test(start_paused = true)]
async fn racing_replacements_stay_linearizable_while_the_store_clock_runs_far_ahead() {
    for seed in 400..432 {
        let physical = CreateOnlyStore::with_store_clock_and_stale_listings(
            FastStoreClock::new(1000),
            STALE_LISTING_LIFETIME,
        );
        physical.faults.delay_randomly(
            seed,
            RandomDelays {
                maximum_delay: Duration::from_millis(50),
                stall_probability: 0.05,
                maximum_stall: Duration::from_secs(5),
            },
        );
        let workload = ReplacementWorkload {
            writers: 3,
            attempts: 30,
            maximum_pause: Duration::from_millis(500),
        };
        let history = run_replacement_workload_with_discovery(
            &physical,
            KEY,
            workload,
            &[VersionDiscovery::HeadProbing],
        )
        .await;
        history.assert_linearizable();
        assert!(history.written_count() >= 10, "seed {seed} made progress");
    }
}
