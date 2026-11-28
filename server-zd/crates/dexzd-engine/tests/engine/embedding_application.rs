// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Node starts on clusters of a test-only application that embeds server-zd.

use std::ops::RangeInclusive;
use std::path::Path;
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, Ordering};

use async_trait::async_trait;
use bytes::Bytes;
use dexzd_clock::SystemWallClock;
use dexzd_cluster_metadata::{
    EmbeddingApplication, ManifestChange, MetadataError, update_manifest,
};
use dexzd_engine::{EngineNodeConfig, EngineStartError, HotStandbyState, LocalStoreConfig};
use dexzd_object_store::{
    ByteRange, InMemoryObjectStore, ObjectContent, ObjectListing, ObjectMetadata, ObjectStore,
    ObjectStoreResult, WriteCondition, WriteOutcome, node_lease_object_key,
};

use crate::fake_embedding_application::{
    start_fake_application, start_fake_application_on, start_fake_application_standby,
};
use crate::harness::TestCluster;
use crate::hot_standby::{fast_standby_config, wait_for_status};

fn embedding_application_config(
    cluster: &TestCluster,
    supported_versions: RangeInclusive<u32>,
    current_version: u32,
) -> EngineNodeConfig {
    let mut config = cluster.config(LocalStoreConfig::InMemory);
    config.cluster.embedding_application = Some(EmbeddingApplication {
        name: "test-embedding-application".to_owned(),
        key_layout_version: 0x8000_0001,
        supported_versions,
        current_version,
    });
    config
}

async fn set_application_version(object_store: &dyn ObjectStore, application_version: u32) {
    update_manifest(object_store, &SystemWallClock, |manifest| {
        manifest
            .version_barrier
            .get_or_insert_default()
            .application_version = application_version;
        Ok(ManifestChange::Changed)
    })
    .await
    .expect("the barrier changes");
}

/// Returns the node lease object and node-a's epoch in the manifest.
async fn lease_and_epoch(cluster: &TestCluster) -> (Option<Bytes>, u64) {
    let lease = cluster
        .object_store
        .get(&node_lease_object_key("node-a"))
        .await
        .expect("read node lease")
        .map(|content| content.bytes);
    let epoch = cluster.cluster_manifest().await.nodes["node-a"].epoch;
    (lease, epoch)
}

#[tokio::test(start_paused = true)]
async fn an_unsupported_application_version_fails_startup_before_the_lease() {
    let cluster = TestCluster::new();
    let first = start_fake_application(&cluster, &embedding_application_config(&cluster, 1..=1, 1))
        .await
        .expect("the embedding application creates its cluster");
    first.node.shutdown().await.expect("lease released");
    let barrier = cluster
        .cluster_manifest()
        .await
        .version_barrier
        .expect("version barrier");
    assert_eq!(barrier.application_version, 1);
    let before = lease_and_epoch(&cluster).await;
    let result =
        start_fake_application(&cluster, &embedding_application_config(&cluster, 2..=3, 2)).await;
    assert!(
        matches!(
            &result,
            Err(EngineStartError::Metadata(
                MetadataError::UnsupportedApplicationVersion {
                    recorded: 1,
                    oldest_supported: 2,
                    newest_supported: 3,
                    ..
                }
            ))
        ),
        "{:?}",
        result.err()
    );
    assert_eq!(
        lease_and_epoch(&cluster).await,
        before,
        "the rejected start neither took the lease nor bumped the epoch"
    );
}

/// Changes the application version barrier right after the first node lease write lands.
struct BarrierChangingStore {
    inner: Arc<InMemoryObjectStore>,
    application_version: u32,
    has_changed: AtomicBool,
}

#[async_trait]
impl ObjectStore for BarrierChangingStore {
    async fn put(
        &self,
        key: &str,
        body: Bytes,
        condition: WriteCondition,
    ) -> ObjectStoreResult<WriteOutcome> {
        let outcome = self.inner.put(key, body, condition).await?;
        if key == node_lease_object_key("node-a")
            && matches!(outcome, WriteOutcome::Written { .. })
            && !self.has_changed.swap(true, Ordering::SeqCst)
        {
            set_application_version(self.inner.as_ref(), self.application_version).await;
        }
        Ok(outcome)
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

    fn supports_replace_if_etag_matches_on_every_key(&self) -> bool {
        self.inner.supports_replace_if_etag_matches_on_every_key()
    }
}

#[tokio::test(start_paused = true)]
async fn a_barrier_changed_while_the_lease_is_taken_fails_the_check_under_the_lease() {
    let cluster = TestCluster::new();
    let config = embedding_application_config(&cluster, 1..=1, 1);
    let first = start_fake_application(&cluster, &config)
        .await
        .expect("the embedding application creates its cluster");
    first.node.shutdown().await.expect("lease released");
    let changing_store = Arc::new(BarrierChangingStore {
        inner: cluster.object_store.clone(),
        application_version: 2,
        has_changed: AtomicBool::new(false),
    });
    let result = start_fake_application_on(&cluster, &config, changing_store.clone()).await;
    assert!(
        changing_store.has_changed.load(Ordering::SeqCst),
        "the start took the lease"
    );
    assert!(
        matches!(
            &result,
            Err(EngineStartError::Metadata(
                MetadataError::UnsupportedApplicationVersion { recorded: 2, .. }
            ))
        ),
        "{:?}",
        result.err()
    );
}

#[tokio::test(start_paused = true)]
async fn server_zd_and_an_embedding_application_reject_each_others_clusters() {
    let mut application_cluster = TestCluster::new();
    let first = start_fake_application(
        &application_cluster,
        &embedding_application_config(&application_cluster, 1..=1, 1),
    )
    .await
    .expect("the embedding application creates its cluster");
    first.node.shutdown().await.expect("lease released");
    let before = lease_and_epoch(&application_cluster).await;
    let server_zd_config = application_cluster.config(LocalStoreConfig::InMemory);
    let result = application_cluster.start_with(&server_zd_config).await;
    let Err(EngineStartError::Metadata(
        error @ MetadataError::ClusterMismatch {
            field: "creating application",
            ..
        },
    )) = &result
    else {
        panic!(
            "server-zd opened the application's cluster: {:?}",
            result.err()
        );
    };
    let message = error.to_string();
    assert!(
        message.contains(
            "embedding application \"test-embedding-application\" with key layout version \
             2147483649"
        ) && message.contains("server-zd with key layout version 1"),
        "{message}"
    );
    assert_eq!(lease_and_epoch(&application_cluster).await, before);

    let mut server_zd_cluster = TestCluster::new();
    let first = server_zd_cluster.start_in_memory().await;
    first.node.shutdown().await.expect("lease released");
    let result = start_fake_application(
        &server_zd_cluster,
        &embedding_application_config(&server_zd_cluster, 1..=1, 1),
    )
    .await;
    assert!(
        matches!(
            &result,
            Err(EngineStartError::Metadata(MetadataError::ClusterMismatch {
                field: "creating application",
                ..
            }))
        ),
        "{:?}",
        result.err()
    );
}

#[tokio::test(start_paused = true)]
async fn an_embedding_application_cluster_does_not_check_the_behavior_version_barrier() {
    let cluster = TestCluster::new();
    let config = embedding_application_config(&cluster, 1..=1, 1);
    let first = start_fake_application(&cluster, &config)
        .await
        .expect("the embedding application creates its cluster");
    first.node.shutdown().await.expect("lease released");
    // Below the oldest behavior version server-zd supports, which fails a server-zd cluster.
    update_manifest(
        cluster.object_store.as_ref(),
        cluster.clock.as_ref(),
        |manifest| {
            manifest
                .version_barrier
                .get_or_insert_default()
                .behavior_version = 0;
            Ok(ManifestChange::Changed)
        },
    )
    .await
    .expect("barrier lowered");
    let second = start_fake_application(&cluster, &config)
        .await
        .expect("the embedding application ignores the behavior version barrier");
    second.node.shutdown().await.expect("lease released");
}

#[tokio::test(start_paused = true)]
async fn a_hot_standby_retries_an_unsupported_application_version_until_the_barrier_advances() {
    let cluster = TestCluster::new();
    let serving =
        start_fake_application(&cluster, &embedding_application_config(&cluster, 1..=1, 1))
            .await
            .expect("the embedding application creates its cluster");
    serving.node.shutdown().await.expect("lease released");
    let standby = start_fake_application_standby(
        &cluster,
        embedding_application_config(&cluster, 2..=2, 2),
        fast_standby_config(),
    )
    .expect("the hot standby starts");
    let retrying = wait_for_status(&standby, "the standby retries", |status| {
        status
            .last_error
            .as_deref()
            .is_some_and(|error| error.contains("records version 1"))
    })
    .await;
    assert_ne!(retrying.state, HotStandbyState::Failed, "{retrying:?}");
    assert!(retrying.failure.is_none(), "{retrying:?}");
    set_application_version(cluster.object_store.as_ref(), 2).await;
    wait_for_status(&standby, "the standby follows", |status| {
        status.state == HotStandbyState::Following
    })
    .await;
    standby.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn a_following_hot_standby_restarts_when_the_barrier_moves_past_its_versions() {
    let cluster = TestCluster::new();
    let config = embedding_application_config(&cluster, 1..=1, 1);
    let serving = start_fake_application(&cluster, &config)
        .await
        .expect("the embedding application creates its cluster");
    serving.node.shutdown().await.expect("lease released");
    let standby = start_fake_application_standby(&cluster, config, fast_standby_config())
        .expect("the hot standby starts");
    wait_for_status(&standby, "the standby follows", |status| {
        status.state == HotStandbyState::Following
    })
    .await;
    set_application_version(cluster.object_store.as_ref(), 2).await;
    let restarting = wait_for_status(&standby, "the standby restarts", |status| {
        status
            .last_error
            .as_deref()
            .is_some_and(|error| error.contains("records version 2"))
    })
    .await;
    assert_ne!(restarting.state, HotStandbyState::Failed, "{restarting:?}");
    assert_ne!(
        restarting.state,
        HotStandbyState::Following,
        "{restarting:?}"
    );
    assert!(restarting.failure.is_none(), "{restarting:?}");
    standby.shutdown().await;
}
