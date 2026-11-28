// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Cluster identity and version barrier of a test-only application that embeds server-zd.

use std::ops::RangeInclusive;

use bytes::Bytes;
use dexzd_clock::SystemWallClock;
use dexzd_cluster_metadata::{
    ClusterConfig, EmbeddingApplication, ManifestChange, MetadataError, NodeConfig,
    bootstrap_or_validate, create_initial_manifest, read_cluster, update_manifest,
};
use dexzd_object_store::{InMemoryObjectStore, ObjectStore, WriteCondition};
use dexzd_protocol::dexzd::v1::ClusterObject;
use prost::Message;

const TEST_APPLICATION_NAME: &str = "test-embedding-application";

/// The cluster object as server-zd encoded it before it recorded a creating application.
#[derive(Clone, PartialEq, prost::Message)]
struct ClusterObjectBeforeCreatingApplication {
    #[prost(uint32, tag = "1")]
    format_version: u32,
    #[prost(bytes = "vec", tag = "2")]
    cluster_id: Vec<u8>,
    #[prost(uint32, tag = "3")]
    partition_count: u32,
    #[prost(int32, tag = "4")]
    partition_hash_algorithm: i32,
    #[prost(bytes = "vec", tag = "5")]
    partition_hash_key: Vec<u8>,
    #[prost(uint32, tag = "6")]
    key_layout_version: u32,
    #[prost(int64, tag = "7")]
    created_at_unix_micros: i64,
}

fn application(
    name: &str,
    key_layout_version: u32,
    supported_versions: RangeInclusive<u32>,
    current_version: u32,
) -> EmbeddingApplication {
    EmbeddingApplication {
        name: name.to_owned(),
        key_layout_version,
        supported_versions,
        current_version,
    }
}

fn test_application() -> EmbeddingApplication {
    application(TEST_APPLICATION_NAME, 0x8000_0001, 1..=1, 1)
}

fn application_config(application: EmbeddingApplication) -> ClusterConfig {
    ClusterConfig {
        partition_count: 4,
        embedding_application: Some(application),
    }
}

fn server_zd_config() -> ClusterConfig {
    ClusterConfig {
        partition_count: 4,
        embedding_application: None,
    }
}

fn node() -> NodeConfig {
    NodeConfig {
        node_id: "node-a".to_owned(),
        incarnation: 1,
        internal_address: "127.0.0.1:9000".to_owned(),
    }
}

/// Creates a cluster with `creator`, then returns the error of opening it with `opener`.
async fn open_error(creator: &ClusterConfig, opener: &ClusterConfig) -> MetadataError {
    let store = InMemoryObjectStore::new();
    bootstrap_or_validate(&store, &SystemWallClock, creator)
        .await
        .expect("the creator creates the cluster");
    bootstrap_or_validate(&store, &SystemWallClock, creator)
        .await
        .expect("the creator reopens its cluster");
    read_cluster(&store, opener)
        .await
        .expect_err("the opener rejects the cluster")
}

fn assert_mismatch(error: &MetadataError, expected_field: &str) {
    assert!(
        matches!(error, MetadataError::ClusterMismatch { field, .. } if *field == expected_field),
        "{error}"
    );
}

#[tokio::test]
async fn server_zd_and_an_embedding_application_reject_each_others_clusters() {
    let application_config = application_config(test_application());
    let error = open_error(&application_config, &server_zd_config()).await;
    assert_mismatch(&error, "creating application");
    assert_eq!(
        error.to_string(),
        "cluster creating application mismatch: configured server-zd with key layout version 1, \
         stored embedding application \"test-embedding-application\" with key layout version \
         2147483649"
    );
    let error = open_error(&server_zd_config(), &application_config).await;
    assert_mismatch(&error, "creating application");
}

#[tokio::test]
async fn the_creator_is_compared_before_the_partition_count() {
    let application_config = ClusterConfig {
        partition_count: 8,
        ..application_config(test_application())
    };
    let error = open_error(&application_config, &server_zd_config()).await;
    assert_mismatch(&error, "creating application");
}

#[tokio::test]
async fn two_embedding_applications_reject_each_others_clusters() {
    let first = application_config(test_application());
    let other_name =
        application_config(application("other-test-application", 0x8000_0001, 1..=1, 1));
    assert_mismatch(
        &open_error(&first, &other_name).await,
        "creating application",
    );
    let other_layout =
        application_config(application(TEST_APPLICATION_NAME, 0x8000_0002, 1..=1, 1));
    let error = open_error(&first, &other_layout).await;
    assert_mismatch(&error, "key layout version");
    assert!(
        error
            .to_string()
            .contains("\"test-embedding-application\" with key layout version 2147483649"),
        "{error}"
    );
}

#[tokio::test]
async fn server_zd_opens_only_its_own_key_layout_version() {
    let store = InMemoryObjectStore::new();
    bootstrap_or_validate(&store, &SystemWallClock, &server_zd_config())
        .await
        .expect("bootstrap");
    let mut stored = ClusterObject::decode(
        store
            .get("cluster")
            .await
            .expect("read cluster object")
            .expect("cluster object exists")
            .bytes,
    )
    .expect("decode cluster object");
    for other_layout_version in [2, 0x8000_0001] {
        stored.key_layout_version = other_layout_version;
        store
            .put(
                "cluster",
                Bytes::from(stored.encode_to_vec()),
                WriteCondition::Unconditional,
            )
            .await
            .expect("rewrite cluster object");
        let error = read_cluster(&store, &server_zd_config())
            .await
            .expect_err("server-zd rejects another key layout version");
        assert_mismatch(&error, "key layout version");
    }
}

#[tokio::test]
async fn a_server_zd_cluster_keeps_its_earlier_encoding_and_application_version_0() {
    let store = InMemoryObjectStore::new();
    let bootstrap = bootstrap_or_validate(&store, &SystemWallClock, &server_zd_config())
        .await
        .expect("bootstrap");
    assert_eq!(bootstrap.cluster.creating_application, "");
    assert_eq!(bootstrap.embedding_application, None);
    let stored = store
        .get("cluster")
        .await
        .expect("read cluster object")
        .expect("cluster object exists")
        .bytes;
    let earlier = ClusterObjectBeforeCreatingApplication::decode(stored.clone())
        .expect("the earlier format decodes the cluster object");
    assert_eq!(
        earlier.encode_to_vec(),
        stored.to_vec(),
        "no field 8 is written"
    );
    let manifest = create_initial_manifest(&store, &SystemWallClock, 4, &node(), None)
        .await
        .expect("manifest");
    let barrier = manifest.version_barrier.expect("version barrier");
    assert_eq!(barrier.application_version, 0);
}

#[tokio::test]
async fn the_initial_manifest_records_the_current_application_version() {
    let store = InMemoryObjectStore::new();
    let application = application(TEST_APPLICATION_NAME, 0x8000_0001, 1..=3, 2);
    let manifest =
        create_initial_manifest(&store, &SystemWallClock, 4, &node(), Some(&application))
            .await
            .expect("manifest");
    assert_eq!(
        application
            .check_recorded_version(&manifest)
            .expect("supported"),
        2
    );
    let advanced = update_manifest(&store, &SystemWallClock, |manifest| {
        manifest
            .version_barrier
            .get_or_insert_default()
            .application_version = 4;
        Ok(ManifestChange::Changed)
    })
    .await
    .expect("advance the barrier");
    let error = application
        .check_recorded_version(&advanced)
        .expect_err("version 4 is unsupported");
    assert!(
        matches!(
            error,
            MetadataError::UnsupportedApplicationVersion {
                recorded: 4,
                oldest_supported: 1,
                newest_supported: 3,
                ..
            }
        ),
        "{error}"
    );
}

#[tokio::test]
#[expect(
    clippy::reversed_empty_ranges,
    reason = "an empty range of supported versions is one of the rejected inputs"
)]
async fn startup_rejects_an_embedding_application_with_server_zd_values() {
    for invalid in [
        application("", 0x8000_0001, 1..=1, 1),
        application(TEST_APPLICATION_NAME, 1, 1..=1, 1),
        application(TEST_APPLICATION_NAME, 0x7fff_ffff, 1..=1, 1),
        application(TEST_APPLICATION_NAME, 0x8000_0001, 0..=1, 1),
        application(TEST_APPLICATION_NAME, 0x8000_0001, 2..=1, 2),
        application(TEST_APPLICATION_NAME, 0x8000_0001, 1..=2, 3),
    ] {
        let store = InMemoryObjectStore::new();
        let config = application_config(invalid);
        let created = bootstrap_or_validate(&store, &SystemWallClock, &config).await;
        assert!(
            matches!(created, Err(MetadataError::InvalidConfig { .. })),
            "{config:?}: {created:?}"
        );
        assert!(
            store.get("cluster").await.expect("read").is_none(),
            "{config:?} created a cluster object"
        );
        let read = read_cluster(&store, &config).await;
        assert!(
            matches!(read, Err(MetadataError::InvalidConfig { .. })),
            "{config:?}: {read:?}"
        );
    }
}
