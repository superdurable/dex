// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Startup checks of the bucket behind an [`crate::AliyunOssObjectStore`].

use bytes::Bytes;

use crate::{
    ATTRIBUTE_INDEX_REGISTRY_OBJECT_KEY, MANIFEST_OBJECT_KEY, ObjectStore, ObjectStoreError,
    STARTUP_PROBE_MAXIMUM_LIFETIME, STARTUP_PROBE_OBJECT_KEY_PREFIX, STARTUP_SELF_CHECK_DEADLINE,
    WriteCondition, WriteOutcome,
};

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
