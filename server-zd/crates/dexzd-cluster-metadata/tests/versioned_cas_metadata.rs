// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Cluster metadata over `AliyunOssObjectStore` on an in-memory store without ETag replacement.
//!
//! The manifest, the registry, and node leases rely on replacement of mutable object keys only.

use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use dexzd_clock::WallClock;
use dexzd_cluster_metadata::{
    AttributeIndexDeclaration, ClusterConfig, LeaseConfig, LeaseStatus, MetadataError, NodeConfig,
    acquire_lease, bootstrap_or_validate, bump_node_epoch, create_initial_manifest,
    read_attribute_index_registry, read_manifest, read_node_lease, register_attribute_indexes,
};
use dexzd_object_store::{
    AliyunOssObjectStore, FaultAction, FaultInjector, InMemoryObjectStore,
    InMemoryObjectStoreOptions, ObjectStore, ObjectStoreOperation, node_lease_object_key,
};
use dexzd_protocol::dexzd::v1::NodeLease;
use prost::Message;
use tokio::time::Instant;

/// Wall-clock time that advances with Tokio's paused clock.
struct TokioWallClock {
    origin: Instant,
}

impl WallClock for TokioWallClock {
    fn now_unix_micros(&self) -> i64 {
        1_800_000_000_000_000
            + i64::try_from(self.origin.elapsed().as_micros()).expect("test time fits in i64")
    }
}

/// Delays calls by a few pseudo-random milliseconds, and once delays the listing after a put.
struct InterleavingFaults {
    sequence: AtomicU64,
    is_delaying_every_call: bool,
    delayed_listing_after_put: Mutex<Option<(String, bool)>>,
}

impl FaultInjector for InterleavingFaults {
    fn action(&self, operation: ObjectStoreOperation, key: &str) -> FaultAction {
        {
            let mut delayed = self
                .delayed_listing_after_put
                .lock()
                .expect("fault lock is not poisoned");
            if let Some((put_key, is_armed)) = delayed.as_mut() {
                if operation == ObjectStoreOperation::Put && key == put_key {
                    *is_armed = true;
                } else if operation == ObjectStoreOperation::List && *is_armed {
                    *delayed = None;
                    return FaultAction::Delay(Duration::from_secs(10));
                }
            }
        }
        if !self.is_delaying_every_call {
            return FaultAction::Proceed;
        }
        let call = self.sequence.fetch_add(1, Ordering::SeqCst);
        FaultAction::Delay(Duration::from_millis(call.wrapping_mul(2_654_435_761) % 23))
    }
}

struct VersionedMetadataStore {
    physical: Arc<InMemoryObjectStore>,
    faults: Arc<InterleavingFaults>,
    clock: Arc<TokioWallClock>,
}

impl VersionedMetadataStore {
    fn new(is_delaying_every_call: bool) -> Self {
        let faults = Arc::new(InterleavingFaults {
            sequence: AtomicU64::new(0),
            is_delaying_every_call,
            delayed_listing_after_put: Mutex::new(None),
        });
        let clock = Arc::new(TokioWallClock {
            origin: Instant::now(),
        });
        let physical = Arc::new(InMemoryObjectStore::with_options(
            InMemoryObjectStoreOptions {
                injector: Some(Arc::clone(&faults) as Arc<dyn FaultInjector>),
                clock: Some(Arc::clone(&clock) as Arc<dyn WallClock>),
                rejects_replace_if_etag_matches: true,
            },
        ));
        Self {
            physical,
            faults,
            clock,
        }
    }

    /// Returns a new adapter over the shared physical store, as one process would hold.
    fn process_store(&self) -> Arc<dyn ObjectStore> {
        Arc::new(AliyunOssObjectStore::new(
            Arc::clone(&self.physical) as Arc<dyn ObjectStore>
        ))
    }

    fn clock(&self) -> Arc<dyn WallClock> {
        Arc::clone(&self.clock) as Arc<dyn WallClock>
    }

    async fn physical_keys(&self, prefix: &str) -> Vec<String> {
        let mut keys = Vec::new();
        let mut start_after = None;
        loop {
            let listing = self
                .physical
                .list(prefix, start_after.as_deref(), 1000)
                .await
                .expect("the physical listing succeeds");
            keys.extend(listing.objects.into_iter().map(|object| object.key));
            match listing.next_start_after {
                Some(next) => start_after = Some(next),
                None => return keys,
            }
        }
    }
}

fn node() -> NodeConfig {
    NodeConfig {
        node_id: "node-a".to_owned(),
        incarnation: 1,
        internal_address: "127.0.0.1:9000".to_owned(),
    }
}

#[tokio::test(start_paused = true)]
async fn bootstrap_and_racing_initial_manifests_agree() {
    let metadata = VersionedMetadataStore::new(true);
    let store = metadata.process_store();
    let clock = metadata.clock();
    let first = bootstrap_or_validate(store.as_ref(), clock.as_ref(), ClusterConfig::default())
        .await
        .expect("bootstrap");
    let second = bootstrap_or_validate(store.as_ref(), clock.as_ref(), ClusterConfig::default())
        .await
        .expect("validate");
    assert_eq!(first.cluster, second.cluster);
    let mut creators = Vec::new();
    for _ in 0..8 {
        let store = metadata.process_store();
        let clock = metadata.clock();
        let creator = tokio::spawn(async move {
            create_initial_manifest(store.as_ref(), clock.as_ref(), 4, &node()).await
        });
        creators.push(creator);
    }
    let mut manifests = Vec::new();
    for creator in creators {
        manifests.push(
            creator
                .await
                .expect("the creator finishes")
                .expect("the manifest is created or read"),
        );
    }
    let stored = read_manifest(store.as_ref())
        .await
        .expect("read")
        .expect("the manifest exists");
    assert!(manifests.iter().all(|manifest| *manifest == stored));
    assert_eq!(stored.version, 1);
    assert_eq!(
        metadata.physical_keys("").await,
        [
            "cluster".to_owned(),
            format!("manifest.v/{:020}", u64::MAX - 1)
        ]
    );
}

#[tokio::test(start_paused = true)]
async fn racing_epoch_bumps_each_apply_once() {
    let metadata = VersionedMetadataStore::new(true);
    let clock = metadata.clock();
    create_initial_manifest(
        metadata.process_store().as_ref(),
        clock.as_ref(),
        4,
        &node(),
    )
    .await
    .expect("create");
    // Each task bumps its own node: identical concurrent proposals would each count as landed.
    let mut bumpers = Vec::new();
    for bumper_index in 0..8 {
        let store = metadata.process_store();
        let clock = metadata.clock();
        let bumper = tokio::spawn(async move {
            let bumped_node = NodeConfig {
                node_id: format!("node-{bumper_index}"),
                ..node()
            };
            for _ in 0..5 {
                bump_node_epoch(store.as_ref(), clock.as_ref(), &bumped_node)
                    .await
                    .expect("bump");
            }
        });
        bumpers.push(bumper);
    }
    for bumper in bumpers {
        bumper.await.expect("the bumper finishes");
    }
    let manifest = read_manifest(metadata.process_store().as_ref())
        .await
        .expect("read")
        .expect("exists");
    for bumper_index in 0..8 {
        assert_eq!(manifest.nodes[&format!("node-{bumper_index}")].epoch, 5);
    }
    assert_eq!(manifest.version, 41);
}

#[tokio::test(start_paused = true)]
async fn a_manifest_edit_superseded_before_its_confirmation_applies_once() {
    let metadata = VersionedMetadataStore::new(false);
    let store = metadata.process_store();
    let clock = metadata.clock();
    create_initial_manifest(store.as_ref(), clock.as_ref(), 4, &node())
        .await
        .expect("create");
    *metadata
        .faults
        .delayed_listing_after_put
        .lock()
        .expect("fault lock is not poisoned") =
        Some((format!("manifest.v/{:020}", u64::MAX - 2), false));
    let delayed_store = metadata.process_store();
    let delayed_clock = metadata.clock();
    let delayed_bump = tokio::spawn(async move {
        bump_node_epoch(delayed_store.as_ref(), delayed_clock.as_ref(), &node()).await
    });
    tokio::time::sleep(Duration::from_secs(1)).await;
    let second_epoch = bump_node_epoch(store.as_ref(), clock.as_ref(), &node())
        .await
        .expect("bump");
    assert_eq!(second_epoch, 2);
    let first_epoch = delayed_bump
        .await
        .expect("the delayed bump finishes")
        .expect("the delayed bump is confirmed");
    assert_eq!(first_epoch, 1);
    let manifest = read_manifest(store.as_ref())
        .await
        .expect("read")
        .expect("exists");
    assert_eq!(manifest.nodes["node-a"].epoch, 2);
    assert_eq!(manifest.version, 3);
}

#[tokio::test(start_paused = true)]
async fn leases_acquire_renew_release_and_are_taken_over() {
    let metadata = VersionedMetadataStore::new(false);
    let clock = metadata.clock();
    let store = metadata.process_store();
    create_initial_manifest(store.as_ref(), clock.as_ref(), 2, &node())
        .await
        .expect("create");
    let config = LeaseConfig {
        fail_fast: true,
        ..LeaseConfig::default()
    };
    let first = acquire_lease(metadata.process_store(), clock.clone(), &node(), &config)
        .await
        .expect("acquire");
    assert_eq!(first.epoch(), 1);
    assert!(matches!(
        acquire_lease(metadata.process_store(), clock.clone(), &node(), &config).await,
        Err(MetadataError::LeaseHeld { .. })
    ));
    let mut status = first.subscribe();
    let first_deadline = match *status.borrow() {
        LeaseStatus::ValidUntil(deadline) => deadline,
        LeaseStatus::Lost(_) => panic!("the lease is held"),
    };
    // Three minutes of renewals let reclamation run past its minimum age.
    for _ in 0..90 {
        tokio::time::advance(Duration::from_secs(2)).await;
        tokio::task::yield_now().await;
    }
    status.changed().await.expect("the status changes");
    let renewed_deadline = match *status.borrow() {
        LeaseStatus::ValidUntil(deadline) => deadline,
        LeaseStatus::Lost(ref detail) => panic!("the lease was lost: {detail}"),
    };
    assert!(renewed_deadline > first_deadline + Duration::from_secs(150));
    let lease_versions = metadata
        .physical_keys(&format!("{}.v/", node_lease_object_key("node-a")))
        .await
        .len();
    assert!(
        (3..=60).contains(&lease_versions),
        "reclamation bounds the lease versions: {lease_versions}"
    );
    first.shutdown().await.expect("release");
    let released = read_node_lease(store.as_ref(), "node-a")
        .await
        .expect("read")
        .expect("the released lease is stored");
    assert_eq!(released.expires_at_unix_micros, i64::MIN);
    let second = acquire_lease(metadata.process_store(), clock.clone(), &node(), &config)
        .await
        .expect("reacquire");
    assert_eq!(second.epoch(), 2);
    // Dropping the handle leaves the lease held until expiry, like a crash.
    drop(second);
    tokio::time::advance(Duration::from_secs(12)).await;
    let successor = acquire_lease(metadata.process_store(), clock, &node(), &config)
        .await
        .expect("take over");
    assert_eq!(successor.epoch(), 3);
    let stored = store
        .get(&node_lease_object_key("node-a"))
        .await
        .expect("get")
        .expect("exists");
    let stored_lease = NodeLease::decode(stored.bytes).expect("decode");
    assert!(stored_lease.expires_at_unix_micros > i64::MIN);
    successor.shutdown().await.expect("release");
}

#[tokio::test(start_paused = true)]
async fn racing_registrations_each_apply_once() {
    let metadata = VersionedMetadataStore::new(true);
    let mut registrars = Vec::new();
    for index in 0..6 {
        let store = metadata.process_store();
        let clock = metadata.clock();
        let registrar = tokio::spawn(async move {
            let declarations = [AttributeIndexDeclaration {
                index_key: format!("key-{index}"),
                index_type: 1,
            }];
            register_attribute_indexes(store.as_ref(), clock.as_ref(), 7, &declarations, None).await
        });
        registrars.push(registrar);
    }
    for registrar in registrars {
        registrar
            .await
            .expect("the registrar finishes")
            .expect("the registration succeeds");
    }
    let registry = read_attribute_index_registry(metadata.process_store().as_ref())
        .await
        .expect("read");
    assert_eq!(registry.registrations.len(), 6);
    assert_eq!(registry.version, 6);
}
