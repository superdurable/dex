// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Startup checks of the bucket behind an [`crate::AliyunOssObjectStore`].

use bytes::Bytes;

use crate::aliyun_oss::version_discovery::VersionDiscovery;
use crate::aliyun_oss::version_keys::version_prefix;
use crate::{
    ATTRIBUTE_INDEX_REGISTRY_OBJECT_KEY, MANIFEST_OBJECT_KEY, MUTABLE_OBJECT_KEY_PREFIXES,
    MUTABLE_OBJECT_KEYS, ObjectStore, ObjectStoreError, STARTUP_PROBE_MAXIMUM_LIFETIME,
    STARTUP_PROBE_OBJECT_KEY_PREFIX, STARTUP_SELF_CHECK_DEADLINE, WriteCondition, WriteOutcome,
};

/// Object key of the marker that the versions under a prefix are found with
/// [`VersionDiscovery::HeadProbing`].
///
/// A HEAD-probing store creates this small object before its first write of a version and
/// never deletes it. See [`check_version_discovery_marker`].
pub const HEAD_PROBING_MARKER_OBJECT_KEY: &str = "version-discovery/head";

/// Body of the marker; Tencent COS cannot create an empty object exclusively.
const HEAD_PROBING_MARKER_BODY: &[u8] = b"versionDiscovery: head\n";

/// Why a prefix does not suit the version discovery of an [`crate::AliyunOssObjectStore`].
#[derive(Debug, thiserror::Error)]
#[non_exhaustive]
pub enum VersionDiscoveryCheckError {
    /// A store with [`VersionDiscovery::Listing`] found the HEAD-probing marker.
    #[error(
        "the prefix holds {marker_key}, so its nodes use objectStore.versionDiscovery: head; \
         set head on this node too, because a prefix never returns to list"
    )]
    HeadProbingPrefix {
        /// Key of the marker relative to the store prefix.
        marker_key: String,
    },
    /// A store with [`VersionDiscovery::HeadProbing`] found versions written without the marker.
    #[error(
        "the prefix holds the version {version_key} but not {marker_key}, so it was written with \
         objectStore.versionDiscovery: list or by a release before 0.1.30; head starts only on a \
         prefix without such versions, so keep list here or start head on an empty prefix"
    )]
    VersionsWithoutHeadProbingMarker {
        /// Key of a version object relative to the store prefix.
        version_key: String,
        /// Key of the missing marker relative to the store prefix.
        marker_key: String,
    },
    /// A request of the check failed.
    #[error("checking the version discovery of the prefix: {0}")]
    ObjectStore(#[from] ObjectStoreError),
}

/// Checks that the prefix of `inner`, the store an [`crate::AliyunOssObjectStore`] wraps, suits
/// `version_discovery`, and marks it for HEAD probing.
///
/// HEAD probing relies on reclamation that deletes versions from the oldest up, which
/// [`VersionDiscovery::Listing`] does not do. So a listing store refuses a prefix that holds
/// [`HEAD_PROBING_MARKER_OBJECT_KEY`]. A HEAD-probing store accepts a prefix with the marker. On
/// a prefix without it, the store lists each mutable key prefix except startup probes, refuses
/// any version it finds, and otherwise creates the marker. Call it at startup, before the store
/// writes its first version. The check cannot see a listing node that is already running, so
/// stop every node before changing the discovery of a prefix.
///
/// Since 0.1.30.
///
/// # Errors
///
/// Returns [`VersionDiscoveryCheckError::HeadProbingPrefix`] for a listing store on a marked
/// prefix, [`VersionDiscoveryCheckError::VersionsWithoutHeadProbingMarker`] for a HEAD-probing
/// store on an unmarked prefix with versions, and the store's error when a request fails.
pub async fn check_version_discovery_marker(
    inner: &dyn ObjectStore,
    version_discovery: VersionDiscovery,
) -> Result<(), VersionDiscoveryCheckError> {
    let marker_key = HEAD_PROBING_MARKER_OBJECT_KEY;
    let is_marked = inner.head(marker_key).await?.is_some();
    match version_discovery {
        VersionDiscovery::Listing if is_marked => {
            Err(VersionDiscoveryCheckError::HeadProbingPrefix {
                marker_key: marker_key.to_owned(),
            })
        }
        VersionDiscovery::Listing => Ok(()),
        VersionDiscovery::HeadProbing if is_marked => Ok(()),
        VersionDiscovery::HeadProbing => {
            let version_prefixes = MUTABLE_OBJECT_KEYS
                .iter()
                .map(|key| version_prefix(key))
                .chain(
                    MUTABLE_OBJECT_KEY_PREFIXES
                        .iter()
                        .filter(|prefix| **prefix != STARTUP_PROBE_OBJECT_KEY_PREFIX)
                        .map(|prefix| (*prefix).to_owned()),
                );
            for prefix in version_prefixes {
                if let Some(version) = inner.list(&prefix, None, 1).await?.objects.first() {
                    return Err(
                        VersionDiscoveryCheckError::VersionsWithoutHeadProbingMarker {
                            version_key: version.key.clone(),
                            marker_key: marker_key.to_owned(),
                        },
                    );
                }
            }
            // Another node may have created the marker first.
            inner
                .put(
                    marker_key,
                    Bytes::from_static(HEAD_PROBING_MARKER_BODY),
                    WriteCondition::CreateOnly,
                )
                .await?;
            Ok(())
        }
    }
}

/// Why a bucket cannot hold server-zd data through an [`crate::AliyunOssObjectStore`].
#[derive(Debug, thiserror::Error)]
#[non_exhaustive]
pub enum AliyunOssBucketCheckError {
    /// A second create-only write of the probe object replaced the first one.
    #[error(
        "the bucket overwrote the probe object {probe_key} although each write sent \
         x-oss-forbid-overwrite, so it ignores that header; most likely bucket versioning was \
         enabled at some time (Enabled or Suspended). Buckets that ever had versioning enabled \
         are not supported; use a bucket that never had versioning enabled"
    )]
    CreateOnlyIgnored {
        /// Key of the probe object relative to the store prefix.
        probe_key: String,
    },
    /// The first create-only write of a new probe object did not report a write.
    #[error("the first create-only write of the new probe object {probe_key} was rejected")]
    InitialCreateRejected {
        /// Key of the probe object relative to the store prefix.
        probe_key: String,
    },
    /// The check did not finish within [`STARTUP_SELF_CHECK_DEADLINE`].
    #[error("the create-only check of the probe object {probe_key} did not finish in time")]
    TimedOut {
        /// Key of the probe object relative to the store prefix.
        probe_key: String,
    },
    /// The probe object could not be deleted after the check passed.
    #[error("the check passed, but deleting the probe object {probe_key} failed: {detail}")]
    CleanupFailed {
        /// Key of the probe object relative to the store prefix.
        probe_key: String,
        /// The delete error, or that the delete did not finish in time.
        detail: String,
    },
    /// The check failed, and deleting its probe object failed too.
    #[error("{check}; deleting the probe object afterwards also failed: {cleanup}")]
    CheckAndCleanupFailed {
        /// Why the check failed.
        check: Box<AliyunOssBucketCheckError>,
        /// The delete error, or that the delete did not finish in time.
        cleanup: String,
    },
    /// A mutable object key holds an object that another provider wrote in place.
    #[error(
        "the object {key} was written by another objectStore.provider, such as s3; the \
         provider of existing data cannot be switched, so start aliyunOSS on an empty prefix"
    )]
    WrittenByAnotherProvider {
        /// Key of the object relative to the store prefix.
        key: String,
    },
    /// A request of the check failed.
    #[error("checking the bucket: {0}")]
    ObjectStore(#[from] ObjectStoreError),
}

/// Checks that `inner`, the store an [`crate::AliyunOssObjectStore`] wraps, rejects a second
/// create-only write of one key, then deletes the probe object.
///
/// Aliyun OSS ignores `x-oss-forbid-overwrite` once bucket versioning was enabled or
/// suspended. The probe key is a plain object under the startup probe prefix, so
/// [`crate::AliyunOssObjectStore::remove_stale_startup_probes`] removes it after a crash.
///
/// # Errors
///
/// Returns [`AliyunOssBucketCheckError::CreateOnlyIgnored`] when the second write succeeds,
/// [`AliyunOssBucketCheckError::InitialCreateRejected`] when the first one fails its condition,
/// [`AliyunOssBucketCheckError::TimedOut`] after [`STARTUP_SELF_CHECK_DEADLINE`], and the store's
/// error when a request fails. A failed probe delete returns
/// [`AliyunOssBucketCheckError::CleanupFailed`], or
/// [`AliyunOssBucketCheckError::CheckAndCleanupFailed`] with the check's own error.
pub async fn check_aliyun_oss_create_only_writes(
    inner: &dyn ObjectStore,
) -> Result<(), AliyunOssBucketCheckError> {
    let probe_key = format!(
        "{STARTUP_PROBE_OBJECT_KEY_PREFIX}forbid-overwrite-{}-{}",
        std::process::id(),
        std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .expect("system clock is after the Unix epoch")
            .as_nanos()
    );
    // Bounded steps keep the probe younger than the age at which other processes remove it.
    let checked =
        tokio::time::timeout(STARTUP_SELF_CHECK_DEADLINE, create_twice(inner, &probe_key))
            .await
            .unwrap_or_else(|_| {
                Err(AliyunOssBucketCheckError::TimedOut {
                    probe_key: probe_key.clone(),
                })
            });
    let cleanup_limit = STARTUP_PROBE_MAXIMUM_LIFETIME.saturating_sub(STARTUP_SELF_CHECK_DEADLINE);
    let cleanup = match tokio::time::timeout(cleanup_limit, inner.delete(&probe_key)).await {
        Ok(Ok(())) => None,
        Ok(Err(error)) => Some(error.to_string()),
        Err(_) => Some(format!(
            "the delete did not finish within {cleanup_limit:?}"
        )),
    };
    match (checked, cleanup) {
        (Ok(()), None) => Ok(()),
        (Ok(()), Some(detail)) => {
            Err(AliyunOssBucketCheckError::CleanupFailed { probe_key, detail })
        }
        (Err(check), None) => Err(check),
        (Err(check), Some(cleanup)) => Err(AliyunOssBucketCheckError::CheckAndCleanupFailed {
            check: Box::new(check),
            cleanup,
        }),
    }
}

async fn create_twice(
    inner: &dyn ObjectStore,
    probe_key: &str,
) -> Result<(), AliyunOssBucketCheckError> {
    let first = inner
        .put(
            probe_key,
            Bytes::from_static(b"first"),
            WriteCondition::CreateOnly,
        )
        .await?;
    if first == WriteOutcome::PreconditionFailed {
        return Err(AliyunOssBucketCheckError::InitialCreateRejected {
            probe_key: probe_key.to_owned(),
        });
    }
    let second = inner
        .put(
            probe_key,
            Bytes::from_static(b"second"),
            WriteCondition::CreateOnly,
        )
        .await?;
    match second {
        WriteOutcome::PreconditionFailed => Ok(()),
        WriteOutcome::Written { .. } => Err(AliyunOssBucketCheckError::CreateOnlyIgnored {
            probe_key: probe_key.to_owned(),
        }),
    }
}

/// Fails when `inner`, the store an [`crate::AliyunOssObjectStore`] wraps, holds an object at
/// the cluster manifest or Attribute index registry key itself.
///
/// The adapter keeps these keys only as versions, so such an object came from another provider.
///
/// # Errors
///
/// Returns [`AliyunOssBucketCheckError::WrittenByAnotherProvider`] for the first such key, and
/// the store's error when a request fails.
pub async fn check_no_data_of_another_provider(
    inner: &dyn ObjectStore,
) -> Result<(), AliyunOssBucketCheckError> {
    for key in [MANIFEST_OBJECT_KEY, ATTRIBUTE_INDEX_REGISTRY_OBJECT_KEY] {
        if inner.head(key).await?.is_some() {
            return Err(AliyunOssBucketCheckError::WrittenByAnotherProvider {
                key: key.to_owned(),
            });
        }
    }
    Ok(())
}
