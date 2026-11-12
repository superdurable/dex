// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Object storage interface used by server-zd, with S3 and db9 implementations and a fault-injecting in-memory implementation for tests.
//!
//! Keys passed to [`ObjectStore`] are relative to the store's configured prefix, for example
//! `log/node-a/0000000001/00000000000000000042`.
//!
//! Every server-zd commit depends on conditional writes, so implementations must honor
//! [`WriteCondition`] exactly.

mod aliyun_oss;
mod db9;
mod file_download;
mod in_memory;
mod instrumented;
mod object_store_error;
mod s3;
mod s3_error;
mod startup_self_check;

use std::path::Path;

pub use aliyun_oss::{
    AliyunOssBucketCheckError, AliyunOssObjectStore, MINIMUM_SUPERSEDED_AGE_BEFORE_RECLAMATION,
    STALE_STARTUP_PROBE_AGE, check_aliyun_oss_create_only_writes,
    check_no_data_of_another_provider,
};
pub use db9::{Db9ObjectStore, Db9ObjectStoreConfig};
pub use in_memory::{FaultAction, FaultInjector, InMemoryObjectStore, InMemoryObjectStoreOptions};
pub use instrumented::InstrumentedObjectStore;
pub use object_store_error::{ObjectStoreError, ObjectStoreErrorKind, ObjectStoreOperation};
pub use s3::{S3ApiDialect, S3Credentials, S3ObjectStore, S3ObjectStoreConfig};
pub use startup_self_check::{StartupSelfCheckError, StartupSelfCheckFailure, startup_self_check};

use async_trait::async_trait;
use bytes::Bytes;

/// Result of an object storage call.
pub type ObjectStoreResult<T> = Result<T, ObjectStoreError>;

/// Precondition of a write.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum WriteCondition {
    /// Write regardless of the current object.
    Unconditional,
    /// Create only when no object exists at the key (`If-None-Match: *`).
    CreateOnly,
    /// Replace only when the current object's ETag equals `etag` (`If-Match`).
    ReplaceIfEtagMatches {
        /// ETag returned by an earlier read or write of the object.
        etag: String,
    },
}

/// Outcome of a conditional write that reached the store.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum WriteOutcome {
    /// The object was written.
    Written {
        /// ETag of the new object.
        etag: String,
    },
    /// The precondition failed (HTTP 412, or S3 404 `NoSuchKey` for `If-Match`); nothing was written.
    PreconditionFailed,
}

/// Inclusive byte range of an object.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct ByteRange {
    /// First byte offset.
    pub start: u64,
    /// Last byte offset, inclusive. Reads past the end return the available bytes.
    pub end_inclusive: u64,
}

/// Content and metadata of an object read.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ObjectContent {
    /// The requested bytes: the whole object, or the requested range.
    pub bytes: Bytes,
    /// ETag of the object version that was read.
    pub etag: String,
    /// Size of the whole object in bytes.
    pub object_size: u64,
}

/// Metadata of an object.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ObjectMetadata {
    /// Key relative to the store prefix.
    pub key: String,
    /// Size in bytes.
    pub size: u64,
    /// ETag of the current version.
    pub etag: String,
    /// Last write time in microseconds since the Unix epoch, when available.
    pub last_modified_unix_micros: Option<i64>,
}

/// One page of a listing.
#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub struct ObjectListing {
    /// Objects in ascending key order.
    pub objects: Vec<ObjectMetadata>,
    /// Pass as `start_after` to read the next page; `None` when the listing is complete.
    pub next_start_after: Option<String>,
}

/// Object storage operations used by server-zd.
///
/// Implementations are shared across tasks (`Arc<dyn ObjectStore>`).
///
/// Large bodies may use multipart internally. The [`WriteCondition`] then applies to
/// `CompleteMultipartUpload`, so the result matches a single PUT.
///
/// HTTP 409 `ConditionalRequestConflict` means a concurrent operation, not a false condition.
/// Implementations retry it (restarting a multipart upload) and report
/// [`ObjectStoreErrorKind::Transient`] if it persists.
#[async_trait]
pub trait ObjectStore: Send + Sync + 'static {
    /// Writes `body` at `key` if `condition` holds.
    ///
    /// Returns [`WriteOutcome::PreconditionFailed`] when the store rejected the condition. An
    /// error of kind [`ObjectStoreErrorKind::OutcomeUnknown`] means the write may or may not
    /// have happened; callers must read the key to find out.
    /// A missing key fails [`WriteCondition::ReplaceIfEtagMatches`] with `PreconditionFailed`.
    async fn put(
        &self,
        key: &str,
        body: Bytes,
        condition: WriteCondition,
    ) -> ObjectStoreResult<WriteOutcome>;

    /// Streams the file at `path` to `key` if `condition` holds; same outcomes as [`Self::put`].
    async fn put_file(
        &self,
        key: &str,
        path: &Path,
        condition: WriteCondition,
    ) -> ObjectStoreResult<WriteOutcome>;

    /// Reads the whole object, or returns `None` when it does not exist.
    async fn get(&self, key: &str) -> ObjectStoreResult<Option<ObjectContent>>;

    /// Reads a byte range of the object, or returns `None` when it does not exist.
    async fn get_range(
        &self,
        key: &str,
        range: ByteRange,
    ) -> ObjectStoreResult<Option<ObjectContent>>;

    /// Streams the object into a new file at `path` and returns its size, or `None` when the
    /// object does not exist (no file is created then).
    ///
    /// Implementations write to a temporary file and rename it on success. On error no file
    /// remains at `path`.
    async fn get_to_file(&self, key: &str, path: &Path) -> ObjectStoreResult<Option<u64>>;

    /// Returns the object's metadata, or `None` when it does not exist.
    async fn head(&self, key: &str) -> ObjectStoreResult<Option<ObjectMetadata>>;

    /// Deletes the object. Deleting a missing object succeeds.
    async fn delete(&self, key: &str) -> ObjectStoreResult<()>;

    /// Deletes up to 1000 objects. Missing objects count as deleted.
    async fn delete_many(&self, keys: &[String]) -> ObjectStoreResult<()>;

    /// Lists objects whose keys start with `prefix` and sort after `start_after`, at most
    /// `max_keys` per page (1-1000).
    async fn list(
        &self,
        prefix: &str,
        start_after: Option<&str>,
        max_keys: u32,
    ) -> ObjectStoreResult<ObjectListing>;

    /// Returns whether [`WriteCondition::ReplaceIfEtagMatches`] works on every key.
    ///
    /// The default is `true`. A store that returns `false` accepts the condition only on mutable
    /// object keys, see [`is_mutable_object_key`], and rejects it on any other key with
    /// [`ObjectStoreErrorKind::Permanent`]. A caller that must replace another key then deletes
    /// it and writes it again with [`WriteCondition::CreateOnly`], which is not atomic.
    ///
    /// A store that wraps another store must return the answer of the wrapped store.
    fn supports_replace_if_etag_matches_on_every_key(&self) -> bool {
        true
    }
}

/// Object key of the cluster manifest.
pub const MANIFEST_OBJECT_KEY: &str = "manifest";

/// Object key of the Attribute index registry.
pub const ATTRIBUTE_INDEX_REGISTRY_OBJECT_KEY: &str = "registry/attribute-indexes";

/// Prefix of node lease object keys; see [`node_lease_object_key`].
pub const NODE_LEASE_OBJECT_KEY_PREFIX: &str = "lease/";

/// Prefix of the probe object keys that [`startup_self_check`] writes and deletes.
pub const STARTUP_PROBE_OBJECT_KEY_PREFIX: &str = "probe/";

/// Exact mutable object keys; see [`is_mutable_object_key`].
pub const MUTABLE_OBJECT_KEYS: [&str; 2] =
    [MANIFEST_OBJECT_KEY, ATTRIBUTE_INDEX_REGISTRY_OBJECT_KEY];

/// Prefixes of mutable object keys; see [`is_mutable_object_key`].
pub const MUTABLE_OBJECT_KEY_PREFIXES: [&str; 2] = [
    NODE_LEASE_OBJECT_KEY_PREFIX,
    STARTUP_PROBE_OBJECT_KEY_PREFIX,
];

/// Returns the object key of the lease of the node `node_id`.
///
/// Node IDs contain only `[a-z0-9-]`, so a lease key never contains `.`.
pub fn node_lease_object_key(node_id: &str) -> String {
    format!("{NODE_LEASE_OBJECT_KEY_PREFIX}{node_id}")
}

/// Returns whether `key` is a mutable object key.
///
/// Mutable object keys are the keys that server-zd replaces with
/// [`WriteCondition::ReplaceIfEtagMatches`]: the keys in [`MUTABLE_OBJECT_KEYS`] and the keys
/// that start with a prefix in [`MUTABLE_OBJECT_KEY_PREFIXES`]. A store without atomic
/// replacement, such as [`AliyunOssObjectStore`], keeps each of them as a series of create-only
/// versions and rejects replacement of every other key; see
/// [`ObjectStore::supports_replace_if_etag_matches_on_every_key`]. Register a key here before
/// replacing it. Mutable object keys must not contain `.`.
pub fn is_mutable_object_key(key: &str) -> bool {
    MUTABLE_OBJECT_KEYS.contains(&key)
        || MUTABLE_OBJECT_KEY_PREFIXES
            .iter()
            .any(|prefix| key.starts_with(prefix))
}
