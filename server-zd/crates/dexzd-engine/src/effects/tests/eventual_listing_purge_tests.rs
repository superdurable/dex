// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Run purges over listings that repeat an earlier listing of their prefix, as on Tencent COS.

use std::sync::Arc;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::Duration;

use bytes::Bytes;
use dexzd_apply::ApplyEffect;
use dexzd_history_segment::history_run_object_key_prefix;
use dexzd_keyspace::TenantId;
use dexzd_object_store::{
    FaultAction, FaultInjector, InMemoryObjectStore, InMemoryObjectStoreOptions,
    ListingConsistency, ObjectStore, ObjectStoreOperation, WriteCondition,
};
use dexzd_protocol::dexzd::v1::{RunSettings, flow_command};

use super::hard_delete_tests::set_hard_delete_attribute;
use super::history_upload_tests::{
    close_run, enable_history_offload, run_purges, short_retention_settings, wait_until,
    wait_until_uploaded,
};
use super::outbox_and_purge_tests::{mark_purging, put_object};
use super::{DurableState, EffectHarness, ok_override, ok_reply, settings, start_request};
use crate::effects::{EffectsConfig, HardDeletePrefixListing};
use crate::flow_coordination::FlowCoordinationEffectSink;

/// How long a listing repeats the first listing of its prefix.
const LISTING_CACHE_LIFETIME: Duration = Duration::from_secs(3);

/// Listings of one prefix that a purge may make before it counts as a busy loop.
const MAX_PREFIX_LISTINGS: usize = 16;

/// Counts listings of one prefix and fails those past a cap, so a busy loop ends.
struct PrefixListingCounter {
    prefix: std::sync::Mutex<Option<String>>,
    listings: AtomicUsize,
}

impl PrefixListingCounter {
    fn new() -> Self {
        Self {
            prefix: std::sync::Mutex::new(None),
            listings: AtomicUsize::new(0),
        }
    }

    fn count_prefix(&self, prefix: String) {
        *self.prefix.lock().expect("counter lock") = Some(prefix);
    }

    fn listings(&self) -> usize {
        self.listings.load(Ordering::SeqCst)
    }
}

impl FaultInjector for PrefixListingCounter {
    fn action(&self, operation: ObjectStoreOperation, key: &str) -> FaultAction {
        let is_counted = operation == ObjectStoreOperation::List
            && self
                .prefix
                .lock()
                .expect("counter lock")
                .as_deref()
                .is_some_and(|prefix| key == prefix);
        if is_counted && self.listings.fetch_add(1, Ordering::SeqCst) >= 10 * MAX_PREFIX_LISTINGS {
            // Failures back off, so paused time advances past a busy loop.
            return FaultAction::FailBeforeEffect;
        }
        FaultAction::Proceed
    }
}

fn stale_listing_store(counter: &Arc<PrefixListingCounter>) -> Arc<InMemoryObjectStore> {
    Arc::new(InMemoryObjectStore::with_options_and_stale_listings(
        InMemoryObjectStoreOptions {
            injector: Some(Arc::clone(counter) as _),
            ..InMemoryObjectStoreOptions::default()
        },
        LISTING_CACHE_LIFETIME,
    ))
}

/// A stale empty listing of the run's prefix must not let RunPurge commit over its objects.
#[tokio::test(start_paused = true)]
async fn an_eventual_listing_purge_deletes_the_objects_that_a_stale_empty_listing_hides() {
    let counter = Arc::new(PrefixListingCounter::new());
    let objects = stale_listing_store(&counter);
    let harness = EffectHarness::with_config(
        DurableState {
            objects: Arc::clone(&objects),
            hard_delete_prefix_listing: HardDeletePrefixListing {
                listing_consistency: ListingConsistency::Eventual,
                ..HardDeletePrefixListing::default()
            },
            ..DurableState::new()
        },
        EffectsConfig::default(),
    );
    harness.rebuild_all_partitions().await;
    harness
        .flow_effect_operations
        .add_override(ok_override(|command| {
            matches!(command, flow_command::Command::RunPurge(_))
        }));
    let run_id = harness.start_flow(start_request("order-1"), settings());
    let owner = harness
        .hard_delete_objects
        .owner_of(&EffectHarness::flow("order-1"), run_id);
    let prefix = owner.object_key_prefix();
    counter.count_prefix(prefix.clone());
    // The listing cache holds an empty listing of the prefix from before the uploads.
    let cached = objects.list(&prefix, None, 1000).await.expect("list");
    assert!(cached.objects.is_empty());
    let referenced = set_hard_delete_attribute(&harness, "order-1", run_id, "secret", "v1").await;
    let orphan = owner.object_key("orphan-of-lost-disk");
    put_object(&harness, &orphan).await;
    mark_purging(&harness, "order-1", run_id);

    harness.runner.handle(
        &EffectHarness::flow("order-1"),
        &[ApplyEffect::RunPurgeRequested { run_id }],
        true,
    );
    harness.settle().await;
    assert!(
        run_purges(&harness).is_empty(),
        "one empty listing does not end the deletion"
    );
    wait_until(&harness, "the purge commits", || {
        !run_purges(&harness).is_empty()
    })
    .await;

    for key in [owner.object_key(&referenced), orphan] {
        assert!(
            objects.get(&key).await.expect("read").is_none(),
            "{key} is deleted before RunPurge"
        );
    }
    let purges = run_purges(&harness);
    assert_eq!(purges.len(), 1);
    assert_eq!(purges[0].reply, ok_reply());
    // The test's own priming listing is one of them.
    assert!(
        counter.listings() <= MAX_PREFIX_LISTINGS,
        "{} listings of the run's prefix",
        counter.listings()
    );
    harness.shutdown().await;
}

/// A listing that keeps repeating deleted history objects is listed again with backoff.
#[tokio::test(start_paused = true)]
async fn a_purge_backs_off_while_a_listing_repeats_deleted_history_objects() {
    let counter = Arc::new(PrefixListingCounter::new());
    let history_objects = stale_listing_store(&counter);
    let harness = EffectHarness::with_config(
        DurableState {
            history_objects: Some(Arc::clone(&history_objects) as _),
            ..DurableState::new()
        },
        EffectsConfig::default(),
    );
    harness.rebuild_all_partitions().await;
    enable_history_offload(&harness);
    let run_id = harness.start_flow(
        start_request("order-1"),
        RunSettings {
            history_segment_target_bytes: 1,
            ..short_retention_settings()
        },
    );
    close_run(&harness, "order-1", run_id);
    wait_until_uploaded(&harness, "order-1", run_id).await;
    let run = harness.run_location("order-1", run_id);
    let prefix = history_run_object_key_prefix(TenantId::DEFAULT, run.flow_hash, run_id);
    counter.count_prefix(prefix.clone());
    // An object outside the run's catalog, which only the prefix sweep finds.
    let stray = format!("{prefix}stray");
    history_objects
        .put(&stray, Bytes::from_static(b"x"), WriteCondition::CreateOnly)
        .await
        .expect("put succeeds");

    tokio::time::sleep(Duration::from_secs(3600)).await;
    wait_until(&harness, "the purge commits", || {
        !run_purges(&harness).is_empty()
    })
    .await;

    history_objects.expire_stale_listings();
    let remaining = history_objects
        .list(&prefix, None, 1000)
        .await
        .expect("list");
    assert!(remaining.objects.is_empty(), "{remaining:?}");
    assert_eq!(run_purges(&harness).len(), 1);
    assert!(
        counter.listings() <= MAX_PREFIX_LISTINGS,
        "{} listings of the run's history prefix",
        counter.listings()
    );
    harness.shutdown().await;
}
