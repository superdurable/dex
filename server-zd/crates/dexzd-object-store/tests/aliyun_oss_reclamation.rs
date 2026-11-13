// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The production reclamation age of `AliyunOssObjectStore` and its stale listing counts.

#[expect(
    dead_code,
    reason = "these tests use only part of the shared create-only store"
)]
#[path = "aliyun_oss/create_only_store.rs"]
mod create_only_store;

use std::path::PathBuf;
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::time::Duration;

use bytes::Bytes;
use dexzd_metrics::{MetricsLevel, NodeMetrics, encode_registry};
use dexzd_object_store::{
    AliyunOssObjectStore, ByteRange, MANIFEST_OBJECT_KEY,
    MINIMUM_SUPERSEDED_AGE_BEFORE_RECLAMATION, ObjectContent, ObjectListing, ObjectMetadata,
    ObjectStore, ObjectStoreErrorKind, ObjectStoreResult, WriteCondition, WriteOutcome,
};

use create_only_store::{CreateOnlyStore, TEST_MINIMUM_SUPERSEDED_AGE, version_key};

const KEY: &str = MANIFEST_OBJECT_KEY;
/// Longer than the test reclamation age, so the next confirmed write may reclaim older versions.
const RECLAIMABLE_GAP: Duration = Duration::from_secs(TEST_MINIMUM_SUPERSEDED_AGE.as_secs() + 1);
/// How long a delayed reclamation listing waits; far beyond every write in its test.
const DELAYED_LISTING: Duration = Duration::from_secs(200);

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

#[tokio::test(start_paused = true)]
async fn the_production_age_keeps_versions_superseded_for_less_than_ten_minutes() {
    assert_eq!(
        MINIMUM_SUPERSEDED_AGE_BEFORE_RECLAMATION,
        Duration::from_secs(10 * 60)
    );
    let physical = CreateOnlyStore::new();
    let store = AliyunOssObjectStore::new(
        physical.inner(),
        MINIMUM_SUPERSEDED_AGE_BEFORE_RECLAMATION,
        NodeMetrics::unregistered(),
    );
    let first = create(&store, "one").await;
    let second = replace(&store, &first, "two").await;
    tokio::time::sleep(Duration::from_secs(9 * 60)).await;
    let third = replace(&store, &second, "three").await;
    tokio::time::sleep(Duration::from_millis(100)).await;
    assert_eq!(
        physical.generations(KEY).await,
        [3, 2, 1],
        "generation 1 was superseded nine minutes before generation 3"
    );
    tokio::time::sleep(Duration::from_secs(2 * 60)).await;
    replace(&store, &third, "four").await;
    // Generation 1 was superseded 11 minutes before generation 4; generation 2 only 2 minutes.
    physical.wait_for_generations(KEY, &[4, 3, 2]).await;
}

#[tokio::test]
#[should_panic(expected = "the minimum superseded age must be at least one second")]
async fn an_age_below_one_second_is_rejected() {
    let physical = CreateOnlyStore::new();
    let _store = AliyunOssObjectStore::new(
        physical.inner(),
        Duration::from_millis(999),
        NodeMetrics::unregistered(),
    );
}

#[tokio::test]
#[should_panic(expected = "the minimum superseded age must fit in i64 microseconds")]
async fn an_age_beyond_i64_microseconds_is_rejected() {
    let physical = CreateOnlyStore::new();
    let _store =
        AliyunOssObjectStore::new(physical.inner(), Duration::MAX, NodeMetrics::unregistered());
}

#[tokio::test(start_paused = true)]
async fn a_listing_that_misses_the_created_version_is_counted_and_leaves_the_outcome_unknown() {
    let physical = CreateOnlyStore::new();
    let (registry, metrics) =
        NodeMetrics::registered(PathBuf::from("/nonexistent"), 1, MetricsLevel::Advanced);
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
    let (registry, metrics) =
        NodeMetrics::registered(PathBuf::from("/nonexistent"), 1, MetricsLevel::Advanced);
    // The newest-version listing still shows generation 3, so nothing can have reclaimed it.
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
    let (registry, metrics) =
        NodeMetrics::registered(PathBuf::from("/nonexistent"), 1, MetricsLevel::Advanced);
    let first_inner = Arc::new(ListingFaultStore::new(physical.inner()));
    first_inner
        .is_delaying_listings_after_a_key
        .store(true, Ordering::SeqCst);
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
    // The first writer's reclamation listing for generation 3 waits while the second writer works.
    let mut etag = create(&first_writer, "one").await;
    for body in ["two", "three"] {
        etag = replace(&first_writer, &etag, body).await;
    }
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
    let listings_before_resume = first_inner.list_calls.load(Ordering::SeqCst);
    tokio::time::sleep(DELAYED_LISTING).await;
    assert_eq!(
        first_inner.list_calls.load(Ordering::SeqCst),
        listings_before_resume + 1,
        "the resumed pass listed the newest version once"
    );
    let encoded = encode_registry(&registry).expect("the registry encodes");
    assert_eq!(
        stale_listing_count(&encoded, "reclamation"),
        0,
        "generation 5 may have reclaimed generation 3"
    );
    assert_eq!(stale_listing_count(&encoded, "confirm_created_version"), 0);
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

/// Wraps a store, counts its listings, and can delay them or leave one physical key out.
struct ListingFaultStore {
    inner: Arc<dyn ObjectStore>,
    /// The key that listings leave out, as a stale listing would.
    hidden_key: Option<String>,
    /// When set, only listings that start after a key leave the hidden key out.
    hides_only_after_start_key: bool,
    /// When set, listings that start after a key wait [`DELAYED_LISTING`] first.
    is_delaying_listings_after_a_key: AtomicBool,
    list_calls: AtomicU64,
}

impl ListingFaultStore {
    fn new(inner: Arc<dyn ObjectStore>) -> Self {
        Self {
            inner,
            hidden_key: None,
            hides_only_after_start_key: false,
            is_delaying_listings_after_a_key: AtomicBool::new(false),
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
        if start_after.is_some() && self.is_delaying_listings_after_a_key.load(Ordering::SeqCst) {
            tokio::time::sleep(DELAYED_LISTING).await;
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
