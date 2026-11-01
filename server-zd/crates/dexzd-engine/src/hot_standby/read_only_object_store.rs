// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::path::Path;
use std::sync::Arc;
use std::sync::atomic::{AtomicU64, Ordering};

use async_trait::async_trait;
use bytes::Bytes;
use dexzd_object_store::{
    ByteRange, ObjectContent, ObjectListing, ObjectMetadata, ObjectStore, ObjectStoreError,
    ObjectStoreErrorKind, ObjectStoreOperation, ObjectStoreResult, WriteCondition, WriteOutcome,
};

/// Object storage for a process that does not hold the node lease: reads pass, writes fail.
pub(crate) struct ReadOnlyObjectStore {
    inner: Arc<dyn ObjectStore>,
    rejected_writes: Arc<AtomicU64>,
}

impl ReadOnlyObjectStore {
    pub(crate) fn new(inner: Arc<dyn ObjectStore>, rejected_writes: Arc<AtomicU64>) -> Self {
        Self {
            inner,
            rejected_writes,
        }
    }

    /// Counts, logs, and returns the error of a write that a hot standby must never attempt.
    fn reject_write(&self, operation: ObjectStoreOperation, key: &str) -> ObjectStoreError {
        let rejected_writes = self.rejected_writes.fetch_add(1, Ordering::SeqCst) + 1;
        tracing::error!(
            %operation,
            key,
            rejected_writes,
            "a hot standby attempted an object storage write"
        );
        ObjectStoreError::new(
            operation,
            key,
            ObjectStoreErrorKind::Permanent,
            "a hot standby never writes object storage",
        )
    }
}

#[async_trait]
impl ObjectStore for ReadOnlyObjectStore {
    async fn put(
        &self,
        key: &str,
        _body: Bytes,
        _condition: WriteCondition,
    ) -> ObjectStoreResult<WriteOutcome> {
        Err(self.reject_write(ObjectStoreOperation::Put, key))
    }

    async fn put_file(
        &self,
        key: &str,
        _path: &Path,
        _condition: WriteCondition,
    ) -> ObjectStoreResult<WriteOutcome> {
        Err(self.reject_write(ObjectStoreOperation::Put, key))
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
        Err(self.reject_write(ObjectStoreOperation::Delete, key))
    }

    async fn delete_many(&self, keys: &[String]) -> ObjectStoreResult<()> {
        Err(self.reject_write(
            ObjectStoreOperation::Delete,
            keys.first().map_or("", String::as_str),
        ))
    }

    async fn list(
        &self,
        prefix: &str,
        start_after: Option<&str>,
        max_keys: u32,
    ) -> ObjectStoreResult<ObjectListing> {
        self.inner.list(prefix, start_after, max_keys).await
    }
}

#[cfg(test)]
mod tests {
    use dexzd_object_store::InMemoryObjectStore;

    use super::*;

    #[tokio::test]
    async fn writes_fail_without_reaching_the_store_and_reads_pass() {
        let inner = Arc::new(InMemoryObjectStore::new());
        inner
            .put(
                "kept",
                Bytes::from_static(b"kept"),
                WriteCondition::CreateOnly,
            )
            .await
            .expect("the test object is stored");
        let rejected_writes = Arc::new(AtomicU64::new(0));
        let read_only = ReadOnlyObjectStore::new(inner.clone(), rejected_writes.clone());
        let directory = tempfile::tempdir().expect("temporary directory");
        let file = directory.path().join("body");
        std::fs::write(&file, b"body").expect("the test file is written");

        let rejected = [
            read_only
                .put(
                    "new",
                    Bytes::from_static(b"new"),
                    WriteCondition::Unconditional,
                )
                .await
                .map(|_| ()),
            read_only
                .put_file("new", &file, WriteCondition::Unconditional)
                .await
                .map(|_| ()),
            read_only.delete("kept").await,
            read_only.delete_many(&["kept".to_owned()]).await,
        ];
        for result in rejected {
            let error = result.expect_err("a read-only store rejects every write");
            assert_eq!(error.kind, ObjectStoreErrorKind::Permanent);
        }
        for operation in [ObjectStoreOperation::Put, ObjectStoreOperation::Delete] {
            assert_eq!(
                inner.call_count(operation, ""),
                u64::from(operation == ObjectStoreOperation::Put)
            );
        }
        assert_eq!(rejected_writes.load(Ordering::SeqCst), 4);
        let kept = read_only
            .get("kept")
            .await
            .expect("the read passes")
            .expect("the object remains");
        assert_eq!(kept.bytes, Bytes::from_static(b"kept"));
        assert!(
            read_only
                .head("new")
                .await
                .expect("the read passes")
                .is_none()
        );
        assert_eq!(
            read_only
                .list("", None, 10)
                .await
                .expect("the listing passes")
                .objects
                .len(),
            1
        );
    }
}
