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

use create_only_store::{CreateOnlyStore, RandomDelays, TEST_MINIMUM_SUPERSEDED_AGE, version_key};
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
async fn listing_and_head_probing_processes_share_a_key_linearizably() {
    for seed in 200..212 {
        check_stalled_workload(
            &CreateOnlyStore::new(),
            seed,
            &[VersionDiscovery::Listing, VersionDiscovery::HeadProbing],
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
    physical
        .store
        .delete(&version_key(KEY, 3))
        .await
        .expect("delete");
    let stale = store
        .put(
            KEY,
            Bytes::from_static(b"stale"),
            WriteCondition::ReplaceIfEtagMatches { etag: second },
        )
        .await
        .expect("the stale replacement completes");
    assert_eq!(stale, WriteOutcome::PreconditionFailed);
    assert_eq!(existing_generations(&physical, KEY, 5).await, [4, 2, 1]);
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

#[tokio::test(start_paused = true)]
async fn a_writer_superseded_before_its_confirmation_still_reports_written() {
    let physical = CreateOnlyStore::new();
    let store = Arc::new(head_probing_store(physical.inner()));
    let mut etag = create(store.as_ref(), "one").await;
    for body in ["two", "three", "four"] {
        etag = replace(store.as_ref(), &etag, body).await;
    }
    // The confirmation reads generation 6, which another writer creates meanwhile.
    physical.faults.inject_after_put(
        &version_key(KEY, 5),
        ObjectStoreOperation::Get,
        &version_key(KEY, 6),
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
    physical
        .store
        .delete(&version_key(KEY, 5))
        .await
        .expect("delete");
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

#[tokio::test(start_paused = true)]
async fn a_slow_writer_whose_successor_is_missing_reports_an_unknown_outcome() {
    let physical = CreateOnlyStore::new();
    let store = Arc::new(head_probing_store(physical.inner()));
    let first = create(store.as_ref(), "one").await;
    // Half the minimum age passes before the confirmation reads generation 3.
    physical.faults.inject_after_put(
        &version_key(KEY, 2),
        ObjectStoreOperation::Get,
        &version_key(KEY, 3),
        FaultAction::Delay(TEST_MINIMUM_SUPERSEDED_AGE),
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
    let second = store.get(KEY).await.expect("get").expect("exists").etag;
    let third = replace(store.as_ref(), &second, "three").await;
    replace(store.as_ref(), &third, "four").await;
    for generation in [2, 3] {
        physical
            .store
            .delete(&version_key(KEY, generation))
            .await
            .expect("delete");
    }
    let error = writer
        .await
        .expect("the writer finishes")
        .expect_err("the outcome cannot be decided");
    assert_eq!(error.kind, ObjectStoreErrorKind::OutcomeUnknown);
    assert_eq!(error.key, KEY);
}

#[tokio::test(start_paused = true)]
async fn a_slowly_confirmed_create_leaves_no_lasting_hint() {
    let physical = CreateOnlyStore::new();
    let store = Arc::new(head_probing_store(physical.inner()));
    let first = create(store.as_ref(), "one").await;
    physical.faults.inject_after_put(
        &version_key(KEY, 2),
        ObjectStoreOperation::Get,
        &version_key(KEY, 3),
        FaultAction::Delay(TEST_MINIMUM_SUPERSEDED_AGE),
    );
    let writer_store = Arc::new(head_probing_store(physical.inner()));
    let spawned_store = Arc::clone(&writer_store);
    let writer = tokio::spawn(async move {
        spawned_store
            .put(
                KEY,
                Bytes::from_static(b"two"),
                WriteCondition::ReplaceIfEtagMatches { etag: first },
            )
            .await
    });
    wait_for_physical_version(&physical, 2).await;
    let second = store.get(KEY).await.expect("get").expect("exists").etag;
    replace(store.as_ref(), &second, "three").await;
    let outcome = writer
        .await
        .expect("the writer finishes")
        .expect("the successor names the writer's version");
    assert!(matches!(outcome, WriteOutcome::Written { .. }));
    let listings_before = physical
        .store
        .call_count(ObjectStoreOperation::List, "manifest.v/");
    assert_eq!(
        current_body(writer_store.as_ref()).await,
        Bytes::from_static(b"three")
    );
    assert_eq!(
        physical
            .store
            .call_count(ObjectStoreOperation::List, "manifest.v/"),
        listings_before + 1,
        "a hint older than half the minimum age is not trusted"
    );
}

#[tokio::test(start_paused = true)]
async fn reclamation_keeps_two_versions_and_tombstones_over_stale_listings() {
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
    for body in ["eight", "nine", "ten", "eleven"] {
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
    }
    assert!(etag.starts_with("\"g11-"), "{etag}");
    wait_for_existing_generations(&physical, 12, &[11, 10, 7]).await;
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
