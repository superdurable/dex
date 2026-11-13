// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Linearizability of `AliyunOssObjectStore` with HEAD-probing version discovery, over
//! consistent and over stale listings, on Tokio's paused clock.

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

use std::path::PathBuf;
use std::sync::Arc;
use std::time::Duration;

use bytes::Bytes;
use dexzd_metrics::{MetricsLevel, NodeMetrics, encode_registry};
use dexzd_object_store::{
    AliyunOssObjectStore, FaultAction, InMemoryObjectStore, MANIFEST_OBJECT_KEY, ObjectStore,
    ObjectStoreErrorKind, ObjectStoreOperation, StartupSelfCheckFailure, VersionDiscovery,
    WriteCondition, WriteOutcome, startup_self_check,
};

use create_only_store::{
    CreateOnlyStore, FreezableStore, ManualStoreClock, RandomDelays, TEST_MINIMUM_SUPERSEDED_AGE,
    anchor_key, version_key,
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

async fn current_body(store: &dyn ObjectStore) -> Bytes {
    store
        .get(KEY)
        .await
        .expect("get")
        .expect("the key exists")
        .bytes
}

/// Returns the generations of `logical_key` that HEAD finds, newest first, up to `highest`.
async fn existing_generations(
    physical: &CreateOnlyStore,
    logical_key: &str,
    highest: u64,
) -> Vec<u64> {
    let mut generations = Vec::new();
    for generation in (1..=highest).rev() {
        if physical
            .store
            .head(&version_key(logical_key, generation))
            .await
            .expect("head")
            .is_some()
        {
            generations.push(generation);
        }
    }
    generations
}

/// Deletes generations 1 through `highest` of `KEY` from the oldest up, as reclamation does.
async fn reclaim_oldest_generations_by_hand(physical: &CreateOnlyStore, highest: u64) {
    physical
        .store
        .put(
            &anchor_key(KEY),
            Bytes::from_static(b"anchor"),
            WriteCondition::CreateOnly,
        )
        .await
        .expect("the anchor is written");
    for generation in 1..=highest {
        physical
            .store
            .delete(&version_key(KEY, generation))
            .await
            .expect("delete");
    }
}

/// Waits on the paused clock until a spawned write has created `generation`.
async fn wait_for_physical_version(physical: &CreateOnlyStore, generation: u64) {
    for _ in 0..1000 {
        if !existing_generations(physical, KEY, generation)
            .await
            .first()
            .is_none_or(|newest| *newest < generation)
        {
            return;
        }
        tokio::time::sleep(Duration::from_millis(1)).await;
    }
    panic!("generation {generation} was never created");
}

/// Waits until HEAD finds exactly `expected`, newest first, among generations up to `highest`.
async fn wait_for_existing_generations(physical: &CreateOnlyStore, highest: u64, expected: &[u64]) {
    for _ in 0..1000 {
        if existing_generations(physical, KEY, highest).await == expected {
            return;
        }
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    assert_eq!(existing_generations(physical, KEY, highest).await, expected);
}

#[tokio::test(start_paused = true)]
async fn racing_replacements_have_one_winner_per_generation_without_faults() {
    for seed in 0..8 {
        let physical = CreateOnlyStore::new();
        physical
            .faults
            .delay_randomly(seed, RandomDelays::uniform(Duration::from_millis(50)));
        let workload = ReplacementWorkload {
            writers: 8,
            attempts: 20,
            maximum_pause: Duration::ZERO,
        };
        let history = run_replacement_workload_with_discovery(
            &physical,
            KEY,
            workload,
            &[VersionDiscovery::HeadProbing],
        )
        .await;
        history.assert_linearizable();
        assert!(history.written_count() >= 20, "seed {seed} made progress");
    }
}

/// Stalls far beyond the reclamation age let reclamation run under stalled creates and confirms.
fn stalled_delays() -> RandomDelays {
    RandomDelays {
        maximum_delay: Duration::from_millis(50),
        stall_probability: 0.05,
        maximum_stall: Duration::from_secs(600),
    }
}

async fn check_stalled_workload(
    physical: &CreateOnlyStore,
    seed: u64,
    discoveries: &[VersionDiscovery],
) {
    physical.faults.delay_randomly(seed, stalled_delays());
    let workload = ReplacementWorkload {
        writers: 6,
        attempts: 30,
        maximum_pause: Duration::from_secs(20),
    };
    let history =
        run_replacement_workload_with_discovery(physical, KEY, workload, discoveries).await;
    history.assert_linearizable();
    assert!(history.written_count() >= 30, "seed {seed} made progress");
}

#[tokio::test(start_paused = true)]
async fn racing_replacements_stay_linearizable_while_reclamation_races_stalled_writers() {
    for seed in 100..124 {
        check_stalled_workload(
            &CreateOnlyStore::new(),
            seed,
            &[VersionDiscovery::HeadProbing],
        )
        .await;
    }
}

#[tokio::test(start_paused = true)]
async fn racing_replacements_stay_linearizable_over_stale_listings() {
    for seed in 300..324 {
        check_stalled_workload(
            &CreateOnlyStore::with_stale_listings(STALE_LISTING_LIFETIME),
            seed,
            &[VersionDiscovery::HeadProbing],
        )
        .await;
    }
}

#[tokio::test(start_paused = true)]
async fn the_startup_self_check_needs_head_probing_over_stale_listings() {
    let stale = Arc::new(InMemoryObjectStore::with_stale_listings());
    let listing = AliyunOssObjectStore::new(
        Arc::clone(&stale) as Arc<dyn ObjectStore>,
        TEST_MINIMUM_SUPERSEDED_AGE,
        NodeMetrics::unregistered(),
    );
    let error = startup_self_check(&listing)
        .await
        .expect_err("listing discovery cannot confirm a create over a stale listing");
    assert_eq!(error.check, StartupSelfCheckFailure::InitialCreate);
    let source = error.source.expect("the create failed in the store");
    assert_eq!(source.kind, ObjectStoreErrorKind::OutcomeUnknown);
    assert!(
        source.detail.contains("shows no well-formed version"),
        "{source}"
    );

    let stale = Arc::new(InMemoryObjectStore::with_stale_listings());
    let head_probing = head_probing_store(Arc::clone(&stale) as Arc<dyn ObjectStore>);
    startup_self_check(&head_probing)
        .await
        .expect("head probing passes the startup self check over a stale listing");
    assert_eq!(
        head_probing
            .remove_stale_startup_probes()
            .await
            .expect("the probe sweep reads store time"),
        0
    );
    stale.expire_stale_listings();
    assert!(
        stale
            .list("probe/", None, 10)
            .await
            .expect("list")
            .objects
            .is_empty(),
        "probe versions are deleted by key"
    );
}

#[tokio::test(start_paused = true)]
async fn a_stale_replacement_of_a_reclaimed_generation_is_rejected() {
    let physical = CreateOnlyStore::new();
    let store = head_probing_store(physical.inner());
    let first = create(&store, "one").await;
    let second = replace(&store, &first, "two").await;
    let third = replace(&store, &second, "three").await;
    replace(&store, &third, "four").await;
    reclaim_oldest_generations_by_hand(&physical, 2).await;
    let stale = store
        .put(
            KEY,
            Bytes::from_static(b"stale"),
            WriteCondition::ReplaceIfEtagMatches { etag: second },
        )
        .await
        .expect("the stale replacement completes");
    assert_eq!(stale, WriteOutcome::PreconditionFailed);
    assert_eq!(existing_generations(&physical, KEY, 5).await, [4, 3]);
    assert_eq!(current_body(&store).await, Bytes::from_static(b"four"));
}

#[tokio::test(start_paused = true)]
async fn a_stale_first_create_after_reclamation_is_rejected_and_removed() {
    let physical = CreateOnlyStore::new();
    let store = Arc::new(head_probing_store(physical.inner()));
    // The stale creator finds no version, then its create arrives after everything below.
    physical.faults.inject(
        ObjectStoreOperation::Put,
        &version_key(KEY, 1),
        0,
        1,
        FaultAction::Delay(Duration::from_secs(600)),
    );
    let stale_creator_store = Arc::new(head_probing_store(physical.inner()));
    let stale_creator = tokio::spawn(async move {
        stale_creator_store
            .put(
                KEY,
                Bytes::from_static(b"stale"),
                WriteCondition::CreateOnly,
            )
            .await
    });
    tokio::time::sleep(Duration::from_secs(1)).await;
    let first = create(store.as_ref(), "one").await;
    let second = replace(store.as_ref(), &first, "two").await;
    tokio::time::sleep(RECLAIMABLE_GAP).await;
    replace(store.as_ref(), &second, "three").await;
    wait_for_existing_generations(&physical, 4, &[3, 2]).await;
    let outcome = stale_creator
        .await
        .expect("the stale creator finishes")
        .expect("the stale create completes");
    assert_eq!(outcome, WriteOutcome::PreconditionFailed);
    assert_eq!(existing_generations(&physical, KEY, 4).await, [3, 2]);
    assert_eq!(
        current_body(store.as_ref()).await,
        Bytes::from_static(b"three")
    );
}

/// The writer of generation 5 confirms it only after another writer created generation 6 and
/// generations 1 to 4 were reclaimed, so the successor's predecessor ID confirms it.
#[tokio::test(start_paused = true)]
async fn a_writer_superseded_before_its_confirmation_still_reports_written() {
    let physical = CreateOnlyStore::new();
    let store = Arc::new(head_probing_store(physical.inner()));
    let mut etag = create(store.as_ref(), "one").await;
    for body in ["two", "three", "four"] {
        etag = replace(store.as_ref(), &etag, body).await;
    }
    // The confirmation first reads generation 4, which is reclaimed meanwhile.
    physical.faults.inject_after_put(
        &version_key(KEY, 5),
        ObjectStoreOperation::Get,
        &version_key(KEY, 4),
        FaultAction::Delay(Duration::from_secs(20)),
    );
    let writer_b_store = Arc::new(head_probing_store(physical.inner()));
    let writer_b = tokio::spawn(async move {
        writer_b_store
            .put(
                KEY,
                Bytes::from_static(b"five"),
                WriteCondition::ReplaceIfEtagMatches { etag },
            )
            .await
    });
    wait_for_physical_version(&physical, 5).await;
    let fifth = store.get(KEY).await.expect("get").expect("exists");
    assert_eq!(fifth.bytes, Bytes::from_static(b"five"));
    replace(store.as_ref(), &fifth.etag, "six").await;
    reclaim_oldest_generations_by_hand(&physical, 4).await;
    let outcome = writer_b
        .await
        .expect("writer B finishes")
        .expect("writer B's put completes");
    assert_eq!(
        outcome,
        WriteOutcome::Written {
            etag: fifth.etag.clone()
        }
    );
    assert_eq!(
        current_body(store.as_ref()).await,
        Bytes::from_static(b"six")
    );
}

/// The writer of generation 2 confirms it only after generations 1 to 3 were reclaimed, so
/// neither neighbor nor the version itself is left to tell whether it was written.
#[tokio::test(start_paused = true)]
async fn a_writer_whose_version_and_neighbors_were_reclaimed_reports_an_unknown_outcome() {
    let physical = CreateOnlyStore::new();
    let store = Arc::new(head_probing_store(physical.inner()));
    let first = create(store.as_ref(), "one").await;
    physical.faults.inject_after_put(
        &version_key(KEY, 2),
        ObjectStoreOperation::Get,
        &version_key(KEY, 1),
        FaultAction::Delay(Duration::from_secs(600)),
    );
    let writer_store = Arc::new(head_probing_store(physical.inner()));
    let writer = tokio::spawn(async move {
        writer_store
            .put(
                KEY,
                Bytes::from_static(b"two"),
                WriteCondition::ReplaceIfEtagMatches { etag: first },
            )
            .await
    });
    wait_for_physical_version(&physical, 2).await;
    let mut etag = store.get(KEY).await.expect("get").expect("exists").etag;
    for body in ["three", "four", "five"] {
        tokio::time::sleep(RECLAIMABLE_GAP).await;
        etag = replace(store.as_ref(), &etag, body).await;
    }
    wait_for_existing_generations(&physical, 5, &[5, 4]).await;
    let error = writer
        .await
        .expect("the writer finishes")
        .expect_err("the outcome cannot be decided");
    assert_eq!(error.kind, ObjectStoreErrorKind::OutcomeUnknown);
    assert_eq!(error.key, KEY);
}

/// Reclamation deletes superseded versions and tombstones from the oldest up, over stale listings.
#[tokio::test(start_paused = true)]
async fn reclamation_deletes_superseded_versions_and_tombstones_from_the_oldest_up() {
    let physical = CreateOnlyStore::with_stale_listings(STALE_LISTING_LIFETIME);
    let store = head_probing_store(physical.inner());
    let mut etag = create(&store, "one").await;
    for body in ["two", "three", "four"] {
        etag = replace(&store, &etag, body).await;
    }
    tokio::time::sleep(Duration::from_millis(100)).await;
    assert_eq!(
        existing_generations(&physical, KEY, 4).await,
        [4, 3, 2, 1],
        "versions younger than the minimum age stay"
    );
    for body in ["five", "six"] {
        tokio::time::sleep(RECLAIMABLE_GAP).await;
        etag = replace(&store, &etag, body).await;
    }
    wait_for_existing_generations(&physical, 6, &[6, 5]).await;
    tokio::time::sleep(RECLAIMABLE_GAP).await;
    store.delete(KEY).await.expect("delete");
    // The tombstone, generation 7, stays while it is one of the newest two.
    wait_for_existing_generations(&physical, 12, &[7, 6]).await;
    for (body, newest) in [("eight", 8), ("nine", 9), ("ten", 10), ("eleven", 11)] {
        tokio::time::sleep(RECLAIMABLE_GAP).await;
        etag = match store
            .put(
                KEY,
                Bytes::from_static(body.as_bytes()),
                if store.get(KEY).await.expect("get").is_none() {
                    WriteCondition::CreateOnly
                } else {
                    WriteCondition::ReplaceIfEtagMatches { etag: etag.clone() }
                },
            )
            .await
            .expect("write")
        {
            WriteOutcome::Written { etag } => etag,
            WriteOutcome::PreconditionFailed => panic!("the write must succeed"),
        };
        wait_for_existing_generations(&physical, 12, &[newest, newest - 1]).await;
    }
    assert!(etag.starts_with("\"g11-"), "{etag}");
    assert!(
        physical
            .store
            .head(&anchor_key(KEY))
            .await
            .expect("head")
            .is_some(),
        "reclaiming generation 1 anchors the key"
    );
}

/// A failed deletion ends a reclamation pass, so the remaining generations stay consecutive, and
/// the next pass continues from the oldest remaining one.
#[tokio::test(start_paused = true)]
async fn a_failed_deletion_ends_reclamation_without_leaving_a_gap() {
    let physical = CreateOnlyStore::new();
    let store = head_probing_store(physical.inner());
    let mut etag = create(&store, "one").await;
    for body in ["two", "three", "four", "five", "six"] {
        etag = replace(&store, &etag, body).await;
    }
    tokio::time::sleep(RECLAIMABLE_GAP).await;
    physical.faults.inject(
        ObjectStoreOperation::Delete,
        &version_key(KEY, 3),
        0,
        1,
        FaultAction::FailBeforeEffect,
    );
    etag = replace(&store, &etag, "seven").await;
    wait_for_existing_generations(&physical, 8, &[7, 6, 5, 4, 3]).await;
    replace(&store, &etag, "eight").await;
    // Generation 6 stays: its successor is younger than the minimum age before generation 8.
    wait_for_existing_generations(&physical, 8, &[8, 7, 6]).await;
    assert_eq!(current_body(&store).await, Bytes::from_static(b"eight"));
}

/// Generation 1 goes only after the key's anchor exists, so a failed anchor keeps every version.
#[tokio::test(start_paused = true)]
async fn a_failed_anchor_keeps_generation_one_and_every_newer_version() {
    let physical = CreateOnlyStore::new();
    let store = head_probing_store(physical.inner());
    let mut etag = create(&store, "one").await;
    for body in ["two", "three"] {
        etag = replace(&store, &etag, body).await;
    }
    tokio::time::sleep(RECLAIMABLE_GAP).await;
    physical.faults.inject(
        ObjectStoreOperation::Put,
        &anchor_key(KEY),
        0,
        1,
        FaultAction::FailBeforeEffect,
    );
    etag = replace(&store, &etag, "four").await;
    tokio::time::sleep(Duration::from_millis(100)).await;
    assert_eq!(existing_generations(&physical, KEY, 5).await, [4, 3, 2, 1]);
    assert!(
        physical
            .store
            .head(&anchor_key(KEY))
            .await
            .expect("head")
            .is_none()
    );
    replace(&store, &etag, "five").await;
    wait_for_existing_generations(&physical, 5, &[5, 4, 3]).await;
    assert!(
        physical
            .store
            .head(&anchor_key(KEY))
            .await
            .expect("head")
            .is_some(),
        "the anchor exists before generation 1 goes"
    );
    let fresh_reader = head_probing_store(physical.inner());
    assert_eq!(
        current_body(&fresh_reader).await,
        Bytes::from_static(b"five")
    );
}

/// A reader's listing shows generation 2, and before its HEAD requests run, three more writes
/// reclaim generations 1 to 3. The reader must not read generation 2, which is gone.
#[tokio::test(start_paused = true)]
async fn a_listed_version_reclaimed_before_the_probe_is_never_read() {
    let store_clock = ManualStoreClock::new();
    let physical = CreateOnlyStore::with_store_clock(store_clock.clone());
    let writer = head_probing_store(physical.inner());
    let mut etag = create(&writer, "one").await;
    store_clock.advance(RECLAIMABLE_GAP);
    etag = replace(&writer, &etag, "two").await;
    let reader_view = FreezableStore::new(physical.inner());
    let reader = Arc::new(head_probing_store(
        Arc::clone(&reader_view) as Arc<dyn ObjectStore>
    ));
    let frozen = reader_view.freeze_next(ObjectStoreOperation::Head, &version_key(KEY, 3));
    let spawned_reader = Arc::clone(&reader);
    let read = tokio::spawn(async move { spawned_reader.get(KEY).await });
    frozen.wait_until_reached().await;
    for body in ["three", "four", "five"] {
        store_clock.advance(RECLAIMABLE_GAP);
        etag = replace(&writer, &etag, body).await;
    }
    wait_for_existing_generations(&physical, 5, &[5, 4]).await;
    frozen.release();
    let content = read
        .await
        .expect("the reader finishes")
        .expect("get")
        .expect("exists");
    assert_eq!(content.bytes, Bytes::from_static(b"five"));
}

#[tokio::test(start_paused = true)]
async fn deleting_a_startup_probe_deletes_its_versions_by_key() {
    let physical = CreateOnlyStore::with_stale_listings(Duration::MAX);
    let store = head_probing_store(physical.inner());
    let probe_key = "probe/1-1";
    let WriteOutcome::Written { etag } = store
        .put(
            probe_key,
            Bytes::from_static(b"probe"),
            WriteCondition::CreateOnly,
        )
        .await
        .expect("create")
    else {
        panic!("the probe create must succeed");
    };
    store
        .put(
            probe_key,
            Bytes::from_static(b"replacement"),
            WriteCondition::ReplaceIfEtagMatches { etag },
        )
        .await
        .expect("replace");
    let listings_before = physical
        .store
        .call_count(ObjectStoreOperation::List, "probe/");
    store.delete(probe_key).await.expect("delete");
    assert_eq!(
        physical
            .store
            .call_count(ObjectStoreOperation::List, "probe/"),
        listings_before,
        "a probe delete lists nothing"
    );
    assert!(
        existing_generations(&physical, probe_key, 3)
            .await
            .is_empty()
    );
}

#[tokio::test(start_paused = true)]
async fn a_listed_lower_bound_that_is_gone_is_never_read_as_absent() {
    let physical = CreateOnlyStore::with_stale_listings(Duration::MAX);
    let writer = head_probing_store(physical.inner());
    let probe_key = "probe/2-2";
    writer
        .put(
            probe_key,
            Bytes::from_static(b"probe"),
            WriteCondition::CreateOnly,
        )
        .await
        .expect("create");
    // Another process reads it, caching a listing that shows generation 1.
    physical.store.expire_stale_listings();
    let reader = head_probing_store(physical.inner());
    assert!(reader.get(probe_key).await.expect("get").is_some());
    writer.delete(probe_key).await.expect("delete");
    let fresh_reader = head_probing_store(physical.inner());
    let error = fresh_reader
        .get(probe_key)
        .await
        .expect_err("the listed version is gone");
    assert_eq!(error.kind, ObjectStoreErrorKind::Transient);
}

#[tokio::test(start_paused = true)]
async fn a_listing_behind_the_probed_newest_version_is_counted() {
    let physical = CreateOnlyStore::with_stale_listings(Duration::MAX);
    let writer = head_probing_store(physical.inner());
    let first = create(&writer, "one").await;
    // The reader's listing then shows generation 1 and stays cached.
    physical.store.expire_stale_listings();
    let (registry, metrics) =
        NodeMetrics::registered(PathBuf::from("/nonexistent"), 1, MetricsLevel::Advanced);
    let reader = AliyunOssObjectStore::with_version_discovery(
        physical.inner(),
        TEST_MINIMUM_SUPERSEDED_AGE,
        metrics,
        VersionDiscovery::HeadProbing,
    );
    assert_eq!(current_body(&reader).await, Bytes::from_static(b"one"));
    replace(&writer, &first, "two").await;
    let (fresh_registry, fresh_metrics) =
        NodeMetrics::registered(PathBuf::from("/nonexistent"), 1, MetricsLevel::Advanced);
    let fresh_reader = AliyunOssObjectStore::with_version_discovery(
        physical.inner(),
        TEST_MINIMUM_SUPERSEDED_AGE,
        fresh_metrics,
        VersionDiscovery::HeadProbing,
    );
    assert_eq!(
        current_body(&fresh_reader).await,
        Bytes::from_static(b"two")
    );
    let count = |registry| {
        let encoded = encode_registry(registry).expect("the registry encodes");
        let series =
            "dexzd_object_store_versioned_listing_stale_total{site=\"newest_version_lookup\"} ";
        encoded
            .lines()
            .find_map(|line| line.strip_prefix(series))
            .expect("every site has a series")
            .parse::<u64>()
            .expect("a counter value is an integer")
    };
    assert_eq!(
        count(&registry),
        0,
        "the first read listed the newest version"
    );
    assert_eq!(
        count(&fresh_registry),
        1,
        "the cached listing showed generation 1"
    );
}
