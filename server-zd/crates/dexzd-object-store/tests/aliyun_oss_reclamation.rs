// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! When `AliyunOssObjectStore` reclaims superseded versions: the minimum age by store time,
//! follow-ups and retries without another write, and stale listing counts.

#[expect(
    dead_code,
    reason = "these tests use only part of the shared create-only store"
)]
#[path = "aliyun_oss/create_only_store.rs"]
mod create_only_store;

use std::path::PathBuf;
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, AtomicI64, AtomicU64, Ordering};
use std::time::Duration;

use bytes::Bytes;
use dexzd_clock::WallClock;
use dexzd_metrics::{NodeMetrics, encode_registry};
use dexzd_object_store::{
    AliyunOssObjectStore, ByteRange, MANIFEST_OBJECT_KEY,
    MINIMUM_SUPERSEDED_AGE_BEFORE_RECLAMATION, ObjectContent, ObjectListing, ObjectMetadata,
    ObjectStore, ObjectStoreError, ObjectStoreErrorKind, ObjectStoreOperation, ObjectStoreResult,
    WriteCondition, WriteOutcome,
};

use create_only_store::{CreateOnlyStore, TEST_MINIMUM_SUPERSEDED_AGE, version_key};

const KEY: &str = MANIFEST_OBJECT_KEY;
/// Longer than the test reclamation age plus the one-second timestamp margin.
const RECLAIMABLE_GAP: Duration = Duration::from_secs(TEST_MINIMUM_SUPERSEDED_AGE.as_secs() + 2);

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

/// Writes generations 1 to 3 of `KEY` at once, so the pass after 3 keeps 1 as too young.
async fn write_three_generations(store: &dyn ObjectStore) -> String {
    let mut etag = create(store, "one").await;
    for body in ["two", "three"] {
        etag = replace(store, &etag, body).await;
    }
    etag
}

#[tokio::test(start_paused = true)]
async fn a_version_kept_as_too_young_is_reclaimed_once_it_ages_without_another_write() {
    let physical = CreateOnlyStore::new();
    let store = AliyunOssObjectStore::new(
        physical.inner(),
        MINIMUM_SUPERSEDED_AGE_BEFORE_RECLAMATION,
        NodeMetrics::unregistered(),
    );
    let first = create(&store, "one").await;
    let second = replace(&store, &first, "two").await;
    tokio::time::sleep(MINIMUM_SUPERSEDED_AGE_BEFORE_RECLAMATION - Duration::from_secs(60)).await;
    replace(&store, &second, "three").await;
    tokio::time::sleep(Duration::from_millis(100)).await;
    assert_eq!(
        physical.generations(KEY).await,
        [3, 2, 1],
        "generation 1 was superseded 59 minutes before generation 3"
    );
    // The follow-up for generation 1, an hour old after one more minute, waits out the interval.
    tokio::time::sleep(Duration::from_secs(4 * 60)).await;
    assert_eq!(physical.generations(KEY).await, [3, 2, 1]);
    tokio::time::sleep(Duration::from_secs(60)).await;
    physical.wait_for_generations(KEY, &[3, 2]).await;
}

#[tokio::test(start_paused = true)]
async fn the_last_write_within_the_pass_interval_still_has_its_versions_reclaimed() {
    let physical = CreateOnlyStore::new();
    let store = AliyunOssObjectStore::new(
        physical.inner(),
        MINIMUM_SUPERSEDED_AGE_BEFORE_RECLAMATION,
        NodeMetrics::unregistered(),
    );
    let first = create(&store, "one").await;
    let second = replace(&store, &first, "two").await;
    tokio::time::sleep(MINIMUM_SUPERSEDED_AGE_BEFORE_RECLAMATION - Duration::from_secs(60)).await;
    let third = replace(&store, &second, "three").await;
    tokio::time::sleep(Duration::from_secs(2 * 60)).await;
    // Generation 1 is reclaimable now, but the key's last pass was two minutes ago.
    replace(&store, &third, "four").await;
    tokio::time::sleep(Duration::from_millis(100)).await;
    assert_eq!(physical.generations(KEY).await, [4, 3, 2, 1]);
    // The pass for generation 4 runs when five minutes have passed since the previous one.
    tokio::time::sleep(Duration::from_secs(3 * 60)).await;
    physical.wait_for_generations(KEY, &[4, 3, 2]).await;
    // Without another write, a follow-up reclaims generation 2 once it has aged an hour.
    tokio::time::sleep(MINIMUM_SUPERSEDED_AGE_BEFORE_RECLAMATION).await;
    physical.wait_for_generations(KEY, &[4, 3]).await;
}

#[tokio::test(start_paused = true)]
async fn a_successor_time_counts_one_more_second_before_its_age_is_compared() {
    let physical = CreateOnlyStore::new();
    let store = AliyunOssObjectStore::new(
        physical.inner(),
        TEST_MINIMUM_SUPERSEDED_AGE,
        NodeMetrics::unregistered(),
    );
    let first = create(&store, "one").await;
    let second = replace(&store, &first, "two").await;
    tokio::time::sleep(TEST_MINIMUM_SUPERSEDED_AGE + Duration::from_millis(500)).await;
    replace(&store, &second, "three").await;
    tokio::time::sleep(Duration::from_millis(100)).await;
    assert_eq!(
        physical.generations(KEY).await,
        [3, 2, 1],
        "generation 2 is 60.5 seconds older than generation 3, less than the age plus a second"
    );
    tokio::time::sleep(Duration::from_secs(10)).await;
    physical.wait_for_generations(KEY, &[3, 2]).await;
}

#[tokio::test(start_paused = true)]
async fn a_follow_up_deletes_nothing_until_the_store_clock_shows_the_age() {
    let store_clock = ControlledStoreClock::starting_at(1_800_000_000_000_000);
    let physical = CreateOnlyStore::with_store_clock(store_clock.clone());
    let store = AliyunOssObjectStore::new(
        physical.inner(),
        TEST_MINIMUM_SUPERSEDED_AGE,
        NodeMetrics::unregistered(),
    );
    write_three_generations(&store).await;
    // Local time passes an hour while the store clock stands still.
    tokio::time::sleep(Duration::from_secs(60 * 60)).await;
    assert_eq!(
        physical.generations(KEY).await,
        [3, 2, 1],
        "local time alone never makes a version reclaimable"
    );
    store_clock.advance(RECLAIMABLE_GAP);
    tokio::time::sleep(RECLAIMABLE_GAP).await;
    physical.wait_for_generations(KEY, &[3, 2]).await;
}

#[tokio::test(start_paused = true)]
async fn a_follow_up_whose_listings_fail_is_retried_until_it_reclaims() {
    let physical = CreateOnlyStore::new();
    let inner = Arc::new(ListingFaultStore::new(physical.inner()));
    let store = AliyunOssObjectStore::new(
        Arc::clone(&inner) as Arc<dyn ObjectStore>,
        TEST_MINIMUM_SUPERSEDED_AGE,
        NodeMetrics::unregistered(),
    );
    write_three_generations(&store).await;
    tokio::time::sleep(Duration::from_millis(100)).await;
    inner.is_failing_lists.store(true, Ordering::SeqCst);
    let listings_before_failures = inner.list_calls.load(Ordering::SeqCst);
    tokio::time::sleep(2 * TEST_MINIMUM_SUPERSEDED_AGE).await;
    assert_eq!(physical.generations(KEY).await, [3, 2, 1]);
    let failed_listings = inner.list_calls.load(Ordering::SeqCst) - listings_before_failures;
    assert!(
        failed_listings >= 4,
        "the follow-up was retried: {failed_listings} listings"
    );
    inner.is_failing_lists.store(false, Ordering::SeqCst);
    tokio::time::sleep(TEST_MINIMUM_SUPERSEDED_AGE + Duration::from_secs(5)).await;
    physical.wait_for_generations(KEY, &[3, 2]).await;
}

#[tokio::test(start_paused = true)]
async fn a_follow_up_whose_deletes_fail_is_retried_until_it_reclaims() {
    let physical = CreateOnlyStore::new();
    let store = AliyunOssObjectStore::new(
        physical.inner(),
        TEST_MINIMUM_SUPERSEDED_AGE,
        NodeMetrics::unregistered(),
    );
    write_three_generations(&store).await;
    tokio::time::sleep(Duration::from_millis(100)).await;
    physical.faults.fail_every_delete(true);
    tokio::time::sleep(2 * TEST_MINIMUM_SUPERSEDED_AGE).await;
    assert_eq!(physical.generations(KEY).await, [3, 2, 1]);
    physical.faults.fail_every_delete(false);
    tokio::time::sleep(TEST_MINIMUM_SUPERSEDED_AGE + Duration::from_secs(5)).await;
    physical.wait_for_generations(KEY, &[3, 2]).await;
}

#[tokio::test(start_paused = true)]
async fn a_listing_that_misses_the_created_version_is_counted_and_leaves_the_outcome_unknown() {
    let physical = CreateOnlyStore::new();
    let (registry, metrics) = NodeMetrics::registered(PathBuf::from("/nonexistent"), 1);
    let inner = Arc::new(ListingFaultStore::hiding(
        physical.inner(),
        version_key(KEY, 2),
        false,
    ));
    let store = AliyunOssObjectStore::new(inner, TEST_MINIMUM_SUPERSEDED_AGE, metrics);
    let first = create(&store, "one").await;
    let error = store
        .put(
            KEY,
            Bytes::from_static(b"two"),
            WriteCondition::ReplaceIfEtagMatches { etag: first },
        )
        .await
        .expect_err("the listing after the create misses generation 2");
    assert_eq!(error.kind, ObjectStoreErrorKind::OutcomeUnknown);
    let encoded = encode_registry(&registry).expect("the registry encodes");
    assert_eq!(stale_listing_count(&encoded, "confirm_created_version"), 1);
    assert_eq!(stale_listing_count(&encoded, "reclamation"), 0);
    assert_eq!(physical.generations(KEY).await, [2, 1]);
}

#[tokio::test(start_paused = true)]
async fn a_reclamation_listing_that_misses_a_version_that_must_exist_is_counted() {
    let physical = CreateOnlyStore::new();
    let (registry, metrics) = NodeMetrics::registered(PathBuf::from("/nonexistent"), 1);
    // The newest-version listing still shows generation 3, so it cannot have been reclaimed.
    let inner = Arc::new(ListingFaultStore::hiding(
        physical.inner(),
        version_key(KEY, 3),
        true,
    ));
    let store = AliyunOssObjectStore::new(inner, TEST_MINIMUM_SUPERSEDED_AGE, metrics);
    let first = create(&store, "one").await;
    let second = replace(&store, &first, "two").await;
    tokio::time::sleep(RECLAIMABLE_GAP).await;
    replace(&store, &second, "three").await;
    let mut reclamation_count = 0;
    for _ in 0..100 {
        let encoded = encode_registry(&registry).expect("the registry encodes");
        reclamation_count = stale_listing_count(&encoded, "reclamation");
        if reclamation_count > 0 {
            break;
        }
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    assert_eq!(reclamation_count, 1);
    let encoded = encode_registry(&registry).expect("the registry encodes");
    assert_eq!(stale_listing_count(&encoded, "confirm_created_version"), 0);
    assert_eq!(
        physical.generations(KEY).await,
        [3, 2, 1],
        "generation 1 is reclaimable, but the pass stops at the stale listing"
    );
}

#[tokio::test(start_paused = true)]
async fn a_version_that_another_writer_reclaimed_is_not_counted_as_a_stale_listing() {
    let physical = CreateOnlyStore::new();
    let (registry, metrics) = NodeMetrics::registered(PathBuf::from("/nonexistent"), 1);
    let first_inner = Arc::new(ListingFaultStore::new(physical.inner()));
    let first_writer = AliyunOssObjectStore::new(
        Arc::clone(&first_inner) as Arc<dyn ObjectStore>,
        TEST_MINIMUM_SUPERSEDED_AGE,
        metrics,
    );
    let second_writer = AliyunOssObjectStore::new(
        physical.inner(),
        TEST_MINIMUM_SUPERSEDED_AGE,
        NodeMetrics::unregistered(),
    );
    write_three_generations(&first_writer).await;
    tokio::time::sleep(Duration::from_millis(100)).await;
    // The first writer's follow-up for generation 1 keeps failing while the second writer works.
    first_inner.is_failing_lists.store(true, Ordering::SeqCst);
    tokio::time::sleep(Duration::from_secs(1)).await;
    let third = second_writer
        .get(KEY)
        .await
        .expect("get")
        .expect("the key exists")
        .etag;
    let fourth = replace(&second_writer, &third, "four").await;
    tokio::time::sleep(RECLAIMABLE_GAP + Duration::from_secs(5)).await;
    replace(&second_writer, &fourth, "five").await;
    physical.wait_for_generations(KEY, &[5, 4]).await;
    let listings_before_recovery = first_inner.list_calls.load(Ordering::SeqCst);
    first_inner.is_failing_lists.store(false, Ordering::SeqCst);
    tokio::time::sleep(2 * TEST_MINIMUM_SUPERSEDED_AGE).await;
    let listings_after_recovery = first_inner.list_calls.load(Ordering::SeqCst);
    assert!(
        listings_after_recovery > listings_before_recovery,
        "the first writer retried its pass"
    );
    let encoded = encode_registry(&registry).expect("the registry encodes");
    assert_eq!(
        stale_listing_count(&encoded, "reclamation"),
        0,
        "generation 5 may have reclaimed generation 3"
    );
    assert_eq!(stale_listing_count(&encoded, "confirm_created_version"), 0);
    tokio::time::sleep(10 * TEST_MINIMUM_SUPERSEDED_AGE).await;
    assert_eq!(
        first_inner.list_calls.load(Ordering::SeqCst),
        listings_after_recovery,
        "the first writer left reclamation to the newer writer"
    );
    assert_eq!(physical.generations(KEY).await, [5, 4]);
}

/// Returns the stale listing count of `site` in an encoded registry.
fn stale_listing_count(encoded: &str, site: &str) -> u64 {
    let series = format!("dexzd_object_store_versioned_listing_stale_total{{site=\"{site}\"}} ");
    encoded
        .lines()
        .find_map(|line| line.strip_prefix(&series))
        .expect("every site has a series")
        .parse()
        .expect("a counter value is an integer")
}

/// A store clock that moves only when a test advances it.
#[derive(Clone)]
struct ControlledStoreClock {
    now_unix_micros: Arc<AtomicI64>,
}

impl ControlledStoreClock {
    fn starting_at(unix_micros: i64) -> Self {
        Self {
            now_unix_micros: Arc::new(AtomicI64::new(unix_micros)),
        }
    }

    fn advance(&self, duration: Duration) {
        let micros = i64::try_from(duration.as_micros()).expect("the step fits in i64");
        self.now_unix_micros.fetch_add(micros, Ordering::SeqCst);
    }
}

impl WallClock for ControlledStoreClock {
    fn now_unix_micros(&self) -> i64 {
        self.now_unix_micros.load(Ordering::SeqCst)
    }
}

/// Wraps a store, counts its listings, and can fail them or leave one physical key out.
struct ListingFaultStore {
    inner: Arc<dyn ObjectStore>,
    /// The key that listings leave out, as a stale listing would.
    hidden_key: Option<String>,
    /// When set, only listings that start after a key leave the hidden key out.
    hides_only_after_start_key: bool,
    is_failing_lists: AtomicBool,
    list_calls: AtomicU64,
}

impl ListingFaultStore {
    fn new(inner: Arc<dyn ObjectStore>) -> Self {
        Self {
            inner,
            hidden_key: None,
            hides_only_after_start_key: false,
            is_failing_lists: AtomicBool::new(false),
            list_calls: AtomicU64::new(0),
        }
    }

    fn hiding(
        inner: Arc<dyn ObjectStore>,
        hidden_key: String,
        hides_only_after_start_key: bool,
    ) -> Self {
        Self {
            hidden_key: Some(hidden_key),
            hides_only_after_start_key,
            ..Self::new(inner)
        }
    }
}

#[async_trait::async_trait]
impl ObjectStore for ListingFaultStore {
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
        path: &std::path::Path,
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

    async fn get_to_file(
        &self,
        key: &str,
        path: &std::path::Path,
    ) -> ObjectStoreResult<Option<u64>> {
        self.inner.get_to_file(key, path).await
    }

    async fn head(&self, key: &str) -> ObjectStoreResult<Option<ObjectMetadata>> {
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
        self.list_calls.fetch_add(1, Ordering::SeqCst);
        if self.is_failing_lists.load(Ordering::SeqCst) {
            return Err(ObjectStoreError::new(
                ObjectStoreOperation::List,
                prefix,
                ObjectStoreErrorKind::Transient,
                "injected listing failure",
            ));
        }
        let Some(hidden_key) = &self.hidden_key else {
            return self.inner.list(prefix, start_after, max_keys).await;
        };
        if self.hides_only_after_start_key && start_after.is_none() {
            return self.inner.list(prefix, start_after, max_keys).await;
        }
        let mut listing = self
            .inner
            .list(prefix, start_after, max_keys.saturating_add(1).min(1000))
            .await?;
        listing.objects.retain(|object| object.key != *hidden_key);
        listing
            .objects
            .truncate(usize::try_from(max_keys).expect("max_keys fits in usize"));
        Ok(listing)
    }

    fn supports_replace_if_etag_matches_on_every_key(&self) -> bool {
        false
    }
}
