// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::future::Future;
use std::path::Path;
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, Ordering};
use std::time::Duration;

use async_trait::async_trait;
use bytes::Bytes;
use dexzd_cluster_metadata::LeaseValidityDeadline;
use dexzd_metrics::NodeMetrics;
use dexzd_object_store::{
    ByteRange, ObjectContent, ObjectListing, ObjectMetadata, ObjectStore, ObjectStoreError,
    ObjectStoreErrorKind, ObjectStoreOperation, ObjectStoreResult, WriteCondition, WriteOutcome,
};
use tokio::time::Instant;

const INITIAL_BACKOFF: Duration = Duration::from_millis(20);
const MAX_BACKOFF: Duration = Duration::from_millis(500);
const FNV_OFFSET_BASIS: u64 = 0xcbf2_9ce4_8422_2325;
const FNV_PRIME: u64 = 0x0000_0100_0000_01b3;

pub(crate) struct StartupReadRetryStore {
    inner: Arc<dyn ObjectStore>,
    budget: Duration,
    jitter_source: String,
    metrics: Arc<NodeMetrics>,
    active: AtomicBool,
    lease_validity_deadline: Option<LeaseValidityDeadline>,
}

impl StartupReadRetryStore {
    pub(crate) fn new(
        inner: Arc<dyn ObjectStore>,
        budget: Duration,
        jitter_source: String,
        metrics: Arc<NodeMetrics>,
        lease_validity_deadline: Option<LeaseValidityDeadline>,
    ) -> Self {
        Self {
            inner,
            budget,
            jitter_source,
            metrics,
            active: AtomicBool::new(true),
            lease_validity_deadline,
        }
    }

    pub(crate) fn finish_startup(&self) {
        self.active.store(false, Ordering::Release);
    }

    async fn retry_read<T, F, Fut>(
        &self,
        operation: ObjectStoreOperation,
        key: &str,
        mut read: F,
    ) -> ObjectStoreResult<T>
    where
        F: FnMut() -> Fut,
        Fut: Future<Output = ObjectStoreResult<T>>,
    {
        if !self.active.load(Ordering::Acquire) {
            return read().await;
        }
        let mut backoff = INITIAL_BACKOFF;
        let mut attempt = 0_u32;
        let mut retry_started_at = None;
        let mut last_error = None;
        loop {
            let read_result = if let Some(first_failure) = retry_started_at {
                let deadline = self.retry_deadline(first_failure);
                if deadline <= Instant::now() {
                    return Err(self.budget_exhausted(
                        operation,
                        key,
                        first_failure,
                        last_error
                            .as_ref()
                            .expect("a retry has an underlying error"),
                    ));
                }
                match tokio::time::timeout_at(deadline, read()).await {
                    Ok(result) => result,
                    Err(_) => {
                        return Err(self.budget_exhausted(
                            operation,
                            key,
                            first_failure,
                            last_error
                                .as_ref()
                                .expect("a retry has an underlying error"),
                        ));
                    }
                }
            } else {
                read().await
            };
            match read_result {
                Ok(value) => return Ok(value),
                Err(error)
                    if matches!(
                        error.kind,
                        ObjectStoreErrorKind::Transient | ObjectStoreErrorKind::OutcomeUnknown
                    ) =>
                {
                    if !self.active.load(Ordering::Acquire) {
                        return Err(error);
                    }
                    let first_failure = *retry_started_at.get_or_insert_with(Instant::now);
                    attempt = attempt.saturating_add(1);
                    let budget_remaining = self
                        .retry_deadline(first_failure)
                        .saturating_duration_since(Instant::now());
                    if budget_remaining.is_zero() {
                        return Err(self.budget_exhausted(operation, key, first_failure, &error));
                    }
                    let delay = self
                        .retry_delay(operation, key, attempt, backoff)
                        .min(budget_remaining);
                    self.metrics.startup_read_retries.inc();
                    if attempt == 1 {
                        tracing::warn!(%operation, key, attempt, delay_ms = delay.as_millis(), budget_remaining_ms = budget_remaining.as_millis(), error = %error, "retrying startup object storage read");
                    } else {
                        tracing::debug!(%operation, key, attempt, delay_ms = delay.as_millis(), budget_remaining_ms = budget_remaining.as_millis(), error = %error, "retrying startup object storage read");
                    }
                    last_error = Some(error);
                    tokio::time::sleep(delay).await;
                    backoff = backoff.saturating_mul(2).min(MAX_BACKOFF);
                }
                Err(error) => return Err(error),
            }
        }
    }

    fn retry_deadline(&self, first_failure: Instant) -> Instant {
        let budget_deadline = first_failure + self.budget;
        self.armed_lease_deadline()
            .map_or(budget_deadline, |lease_deadline| {
                budget_deadline.min(lease_deadline)
            })
    }

    /// Returns the armed local lease validity deadline, or `None` when no confirmed lease bounds
    /// retries.
    fn armed_lease_deadline(&self) -> Option<Instant> {
        self.lease_validity_deadline
            .as_ref()
            .and_then(LeaseValidityDeadline::armed_deadline)
            .map(Instant::from_std)
    }

    fn budget_exhausted(
        &self,
        operation: ObjectStoreOperation,
        key: &str,
        first_failure: Instant,
        last_error: &ObjectStoreError,
    ) -> ObjectStoreError {
        let is_lease_bounded = self
            .armed_lease_deadline()
            .is_some_and(|lease_deadline| lease_deadline < first_failure + self.budget);
        let detail = if is_lease_bounded {
            format!(
                "startup read retries reached the local lease validity deadline before the {:?} retry budget; last error: {last_error}",
                self.budget
            )
        } else {
            format!(
                "startup read retry budget of {:?} exhausted; last error: {last_error}",
                self.budget
            )
        };
        ObjectStoreError::new(operation, key, last_error.kind, detail)
    }

    fn retry_delay(
        &self,
        operation: ObjectStoreOperation,
        key: &str,
        attempt: u32,
        backoff: Duration,
    ) -> Duration {
        let hash = self
            .jitter_source
            .bytes()
            .chain(operation.to_string().bytes())
            .chain(key.bytes())
            .chain(attempt.to_le_bytes())
            .fold(FNV_OFFSET_BASIS, |hash, byte| {
                (hash ^ u64::from(byte)).wrapping_mul(FNV_PRIME)
            });
        let half = backoff / 2;
        half + Duration::from_nanos(hash % u64::try_from(half.as_nanos()).unwrap_or(u64::MAX))
    }
}

#[async_trait]
impl ObjectStore for StartupReadRetryStore {
    async fn put(
        &self,
        key: &str,
        body: Bytes,
        condition: WriteCondition,
    ) -> ObjectStoreResult<WriteOutcome> {
        self.inner.put(key, body, condition).await
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
        self.retry_read(ObjectStoreOperation::Get, key, || self.inner.get(key))
            .await
    }

    async fn get_range(
        &self,
        key: &str,
        range: ByteRange,
    ) -> ObjectStoreResult<Option<ObjectContent>> {
        self.retry_read(ObjectStoreOperation::Get, key, || {
            self.inner.get_range(key, range)
        })
        .await
    }

    async fn get_to_file(&self, key: &str, path: &Path) -> ObjectStoreResult<Option<u64>> {
        self.retry_read(ObjectStoreOperation::Get, key, || {
            self.inner.get_to_file(key, path)
        })
        .await
    }

    async fn head(&self, key: &str) -> ObjectStoreResult<Option<ObjectMetadata>> {
        self.retry_read(ObjectStoreOperation::Head, key, || self.inner.head(key))
            .await
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
        self.retry_read(ObjectStoreOperation::List, prefix, || {
            self.inner.list(prefix, start_after, max_keys)
        })
        .await
    }
}
