// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use bytes::Bytes;
use dexzd_clock::WallClock;
use dexzd_keyspace::{KEY_LAYOUT_VERSION, PartitionHasher};
use dexzd_object_store::{ObjectStore, ObjectStoreErrorKind, WriteCondition, WriteOutcome};
use dexzd_protocol::dexzd::v1::{ClusterObject, PartitionHashAlgorithm};
use prost::Message;
use rand::{TryRng, rngs::SysRng};
use uuid::Uuid;

use crate::{EmbeddingApplication, METADATA_FORMAT_VERSION, MetadataError};

const CLUSTER_KEY: &str = "cluster";

/// Immutable cluster settings supplied when a node starts.
#[derive(Debug, Clone)]
pub struct ClusterConfig {
    /// Number of partitions; defaults to 1024 and never changes after bootstrap.
    pub partition_count: u32,
    /// Application that embeds server-zd and owns the cluster's state.
    /// Defaults to `None`: the cluster belongs to server-zd itself, records an empty creating
    /// application and exactly [`KEY_LAYOUT_VERSION`], checks server-zd's behavior version
    /// barrier, and records application version 0.
    pub embedding_application: Option<EmbeddingApplication>,
}

impl Default for ClusterConfig {
    fn default() -> Self {
        Self {
            partition_count: 1024,
            embedding_application: None,
        }
    }
}

impl ClusterConfig {
    /// Checks the configuration before a node reads or creates the cluster object.
    ///
    /// Returns [`MetadataError::PartitionHash`] for an invalid partition count, or the
    /// [`MetadataError::InvalidConfig`] of [`EmbeddingApplication::validate`].
    fn validate(&self) -> Result<(), MetadataError> {
        PartitionHasher::new(&[0; 16], self.partition_count)?;
        self.embedding_application
            .as_ref()
            .map_or(Ok(()), EmbeddingApplication::validate)
    }

    /// Returns the creating application name and key layout version this configuration expects.
    fn expected_creator(&self) -> (&str, u32) {
        match &self.embedding_application {
            Some(application) => (&application.name, application.key_layout_version),
            None => ("", KEY_LAYOUT_VERSION),
        }
    }
}

/// Validated cluster identity and partition hasher.
#[derive(Debug, Clone)]
pub struct ClusterBootstrap {
    /// Stored immutable cluster object.
    pub cluster: ClusterObject,
    /// Hasher configured with the cluster's persistent key.
    pub partition_hasher: PartitionHasher,
    /// Embedding application whose identity the cluster object matched; `None` for server-zd.
    pub embedding_application: Option<EmbeddingApplication>,
}

/// Creates the cluster once, then validates immutable settings on every start.
pub async fn bootstrap_or_validate(
    store: &dyn ObjectStore,
    clock: &dyn WallClock,
    config: &ClusterConfig,
) -> Result<ClusterBootstrap, MetadataError> {
    config.validate()?;
    let cluster = match store.get(CLUSTER_KEY).await? {
        Some(content) => decode_cluster(content.bytes)?,
        None => {
            let (creating_application, key_layout_version) = config.expected_creator();
            let proposed = ClusterObject {
                format_version: METADATA_FORMAT_VERSION,
                cluster_id: Uuid::now_v7().as_bytes().to_vec(),
                partition_count: config.partition_count,
                partition_hash_algorithm: PartitionHashAlgorithm::Siphash24 as i32,
                partition_hash_key: random_hash_key()?.to_vec(),
                key_layout_version,
                created_at_unix_micros: clock.now_unix_micros(),
                creating_application: creating_application.to_owned(),
            };
            loop {
                let write_result = store
                    .put(
                        CLUSTER_KEY,
                        Bytes::from(proposed.encode_to_vec()),
                        WriteCondition::CreateOnly,
                    )
                    .await;
                match write_result {
                    Ok(WriteOutcome::Written { .. }) => break proposed,
                    Ok(WriteOutcome::PreconditionFailed) => {
                        if let Some(existing) = store.get(CLUSTER_KEY).await? {
                            break decode_cluster(existing.bytes)?;
                        }
                    }
                    Err(error) if error.kind == ObjectStoreErrorKind::OutcomeUnknown => {
                        if let Some(existing) = store.get(CLUSTER_KEY).await? {
                            break decode_cluster(existing.bytes)?;
                        }
                    }
                    Err(error) => return Err(error.into()),
                }
            }
        }
    };
    validate_cluster(cluster, config)
}

/// Reads and validates the cluster object without creating it.
///
/// Returns `None` until a node created the cluster object.
pub async fn read_cluster(
    store: &dyn ObjectStore,
    config: &ClusterConfig,
) -> Result<Option<ClusterBootstrap>, MetadataError> {
    config.validate()?;
    let Some(content) = store.get(CLUSTER_KEY).await? else {
        return Ok(None);
    };
    validate_cluster(decode_cluster(content.bytes)?, config).map(Some)
}

/// Checks the stored cluster object against `config` and builds its partition hasher.
fn validate_cluster(
    cluster: ClusterObject,
    config: &ClusterConfig,
) -> Result<ClusterBootstrap, MetadataError> {
    // The creator goes first: other settings of another application's cluster mean nothing here.
    check_creator(&cluster, config)?;
    if config.partition_count != cluster.partition_count {
        return Err(MetadataError::PartitionCountMismatch {
            configured: config.partition_count,
            stored: cluster.partition_count,
        });
    }
    check_match(
        "partition hash algorithm",
        PartitionHashAlgorithm::Siphash24 as i32,
        cluster.partition_hash_algorithm,
    )?;
    check_match(
        "metadata format version",
        METADATA_FORMAT_VERSION,
        cluster.format_version,
    )?;
    let cluster_id =
        Uuid::from_slice(&cluster.cluster_id).map_err(|error| MetadataError::InvalidMetadata {
            key: CLUSTER_KEY,
            detail: format!("cluster ID is not a UUID: {error}"),
        })?;
    if cluster_id.get_version_num() != 7 {
        return Err(MetadataError::InvalidMetadata {
            key: CLUSTER_KEY,
            detail: "cluster ID is not UUIDv7".to_owned(),
        });
    }
    let partition_hasher =
        PartitionHasher::new(&cluster.partition_hash_key, cluster.partition_count)?;
    Ok(ClusterBootstrap {
        cluster,
        partition_hasher,
        embedding_application: config.embedding_application.clone(),
    })
}

/// Compares the creating application, then the key layout version, with `config`.
///
/// Returns [`MetadataError::ClusterMismatch`] naming both creators and key layout versions.
fn check_creator(cluster: &ClusterObject, config: &ClusterConfig) -> Result<(), MetadataError> {
    let (application_name, key_layout_version) = config.expected_creator();
    let field = if cluster.creating_application != application_name {
        "creating application"
    } else if cluster.key_layout_version != key_layout_version {
        "key layout version"
    } else {
        return Ok(());
    };
    Err(MetadataError::ClusterMismatch {
        field,
        configured: describe_creator(application_name, key_layout_version),
        stored: describe_creator(&cluster.creating_application, cluster.key_layout_version),
    })
}

fn describe_creator(application_name: &str, key_layout_version: u32) -> String {
    if application_name.is_empty() {
        format!("server-zd with key layout version {key_layout_version}")
    } else {
        format!(
            "embedding application {application_name:?} with key layout version \
             {key_layout_version}"
        )
    }
}

fn decode_cluster(bytes: Bytes) -> Result<ClusterObject, MetadataError> {
    ClusterObject::decode(bytes).map_err(|source| MetadataError::Decode {
        key: CLUSTER_KEY,
        source,
    })
}

fn check_match<T: std::fmt::Display + PartialEq>(
    field: &'static str,
    configured: T,
    stored: T,
) -> Result<(), MetadataError> {
    if configured == stored {
        return Ok(());
    }
    Err(MetadataError::ClusterMismatch {
        field,
        configured: configured.to_string(),
        stored: stored.to_string(),
    })
}

fn random_hash_key() -> Result<[u8; 16], MetadataError> {
    let mut key = [0; 16];
    SysRng
        .try_fill_bytes(&mut key)
        .map_err(|error| MetadataError::RandomSource {
            detail: error.to_string(),
        })?;
    Ok(key)
}
