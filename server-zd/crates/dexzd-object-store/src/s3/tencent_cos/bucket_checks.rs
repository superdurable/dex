// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Startup checks of a Tencent COS bucket before server-zd stores data in it.

use bytes::Bytes;

use crate::s3::S3ObjectStore;
use crate::s3_error::map_sdk_error;
use crate::{
    ATTRIBUTE_INDEX_REGISTRY_OBJECT_KEY, MANIFEST_OBJECT_KEY, ObjectStore, ObjectStoreError,
    ObjectStoreErrorKind, ObjectStoreOperation, STARTUP_PROBE_MAXIMUM_LIFETIME,
    STARTUP_PROBE_OBJECT_KEY_PREFIX, STARTUP_SELF_CHECK_DEADLINE, WriteCondition, WriteOutcome,
};

/// Why a Tencent COS bucket cannot hold server-zd data.
#[derive(Debug, thiserror::Error)]
#[non_exhaustive]
pub enum TencentCosBucketCheckError {
    /// The bucket has versioning enabled or suspended.
    #[error(
        "bucket versioning is {status}; tencentCOS supports only buckets whose versioning was \
         never enabled, so use a bucket that never had versioning enabled"
    )]
    VersioningConfigured {
        /// The versioning status that GetBucketVersioning returned.
        status: String,
    },
    /// The first AppendObject of a new probe object failed permanently.
    #[error(
        "the bucket rejected AppendObject of the probe object {probe_key}, which tencentCOS \
         needs for create-only writes; multi-AZ buckets and the INTELLIGENT_TIERING storage \
         class do not support appendable objects: {source}"
    )]
    AppendRejected {
        /// Key of the probe object relative to the store prefix.
        probe_key: String,
        /// The append error.
        source: ObjectStoreError,
    },
    /// A second create-only write of the probe object succeeded.
    #[error(
        "the bucket accepted a second AppendObject at position 0 of the probe object \
         {probe_key}, so its create-only writes are not exclusive"
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
        check: Box<TencentCosBucketCheckError>,
        /// The delete error, or that the delete did not finish in time.
        cleanup: String,
    },
    /// A mutable object key holds an object that another provider wrote in place.
    #[error(
        "the object {key} was written by another objectStore.provider, such as s3; the \
         provider of existing data cannot be switched, so start tencentCOS on an empty prefix"
    )]
    WrittenByAnotherProvider {
        /// Key of the object relative to the store prefix.
        key: String,
    },
    /// A request of the check failed.
    #[error("checking the bucket: {0}")]
    ObjectStore(#[from] ObjectStoreError),
}

/// Fails when the bucket of `store` has versioning enabled or suspended.
///
/// The check sends GetBucketVersioning, so the credentials need that permission. A bucket
/// whose versioning was never enabled reports no status.
///
/// # Errors
///
/// Returns [`TencentCosBucketCheckError::VersioningConfigured`] for a versioned bucket and
/// [`TencentCosBucketCheckError::ObjectStore`] when the request fails.
pub async fn check_tencent_cos_bucket_versioning(
    store: &S3ObjectStore,
) -> Result<(), TencentCosBucketCheckError> {
    let response = store
        .read_client
        .get_bucket_versioning()
        .bucket(&store.bucket)
        .send()
        .await
        .map_err(|error| {
            let mapped = map_sdk_error(ObjectStoreOperation::Head, "", false, error);
            ObjectStoreError::new(
                mapped.operation,
                "",
                mapped.kind,
                format!(
                    "GetBucketVersioning of bucket {}: {}",
                    store.bucket, mapped.detail
                ),
            )
        })?;
    match response.status() {
        None => Ok(()),
        // COS reports only Enabled or Suspended, and both rule the bucket out.
        Some(status) => Err(TencentCosBucketCheckError::VersioningConfigured {
            status: status.as_str().to_owned(),
        }),
    }
}

/// Checks that `inner`, the Tencent COS store the versioned layer wraps, rejects a second
/// create-only write of one key, then deletes the probe object.
///
/// The probe key is a plain object under the startup probe prefix, so
/// [`crate::AliyunOssObjectStore::remove_stale_startup_probes`] removes it after a crash.
///
/// # Errors
///
/// Returns [`TencentCosBucketCheckError::AppendRejected`] when the first append fails
/// permanently, for example on a multi-AZ bucket,
/// [`TencentCosBucketCheckError::CreateOnlyIgnored`] when the second write succeeds,
/// [`TencentCosBucketCheckError::InitialCreateRejected`] when the first one fails its
/// condition, [`TencentCosBucketCheckError::TimedOut`] after [`STARTUP_SELF_CHECK_DEADLINE`],
/// and the store's error when a request fails. A failed probe delete returns
/// [`TencentCosBucketCheckError::CleanupFailed`], or
/// [`TencentCosBucketCheckError::CheckAndCleanupFailed`] with the check's own error.
pub async fn check_tencent_cos_create_only_writes(
    inner: &dyn ObjectStore,
) -> Result<(), TencentCosBucketCheckError> {
    let probe_key = format!(
        "{STARTUP_PROBE_OBJECT_KEY_PREFIX}append-create-{}-{}",
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
                Err(TencentCosBucketCheckError::TimedOut {
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
            Err(TencentCosBucketCheckError::CleanupFailed { probe_key, detail })
        }
        (Err(check), None) => Err(check),
        (Err(check), Some(cleanup)) => Err(TencentCosBucketCheckError::CheckAndCleanupFailed {
            check: Box::new(check),
            cleanup,
        }),
    }
}

async fn create_twice(
    inner: &dyn ObjectStore,
    probe_key: &str,
) -> Result<(), TencentCosBucketCheckError> {
    let first = inner
        .put(
            probe_key,
            Bytes::from_static(b"first"),
            WriteCondition::CreateOnly,
        )
        .await
        .map_err(|source| match source.kind {
            ObjectStoreErrorKind::Permanent => TencentCosBucketCheckError::AppendRejected {
                probe_key: probe_key.to_owned(),
                source,
            },
            _ => TencentCosBucketCheckError::ObjectStore(source),
        })?;
    if first == WriteOutcome::PreconditionFailed {
        return Err(TencentCosBucketCheckError::InitialCreateRejected {
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
        WriteOutcome::Written { .. } => Err(TencentCosBucketCheckError::CreateOnlyIgnored {
            probe_key: probe_key.to_owned(),
        }),
    }
}

/// Fails when `inner`, the store the versioned layer wraps, holds an object at the cluster
/// manifest or Attribute index registry key itself.
///
/// The versioned layer keeps these keys only as versions, so such an object came from
/// another provider.
///
/// # Errors
///
/// Returns [`TencentCosBucketCheckError::WrittenByAnotherProvider`] for the first such key,
/// and the store's error when a request fails.
pub async fn check_tencent_cos_holds_no_data_of_another_provider(
    inner: &dyn ObjectStore,
) -> Result<(), TencentCosBucketCheckError> {
    for key in [MANIFEST_OBJECT_KEY, ATTRIBUTE_INDEX_REGISTRY_OBJECT_KEY] {
        if inner.head(key).await?.is_some() {
            return Err(TencentCosBucketCheckError::WrittenByAnotherProvider {
                key: key.to_owned(),
            });
        }
    }
    Ok(())
}
