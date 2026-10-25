// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::future::Future;
use std::path::Path;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;

use async_trait::async_trait;
use bytes::Bytes;
use dexzd_metrics::NodeMetrics;
use dexzd_object_store::{
    ByteRange, ObjectContent, ObjectListing, ObjectMetadata, ObjectStore, ObjectStoreError,
    ObjectStoreErrorKind, ObjectStoreOperation, ObjectStoreResult, WriteCondition, WriteOutcome,
};
use tokio::sync::watch;
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
    remaining_budget: Mutex<Duration>,
    retry_started: watch::Sender<bool>,
}

impl StartupReadRetryStore {
    pub(crate) fn new(
        inner: Arc<dyn ObjectStore>,
        budget: Duration,
        jitter_source: String,
        metrics: Arc<NodeMetrics>,
    ) -> Self {
        let (retry_started, _) = watch::channel(false);
        Self {
            inner,
            budget,
            jitter_source,
            metrics,
            active: AtomicBool::new(true),
            remaining_budget: Mutex::new(budget),
            retry_started,
        }
    }

    pub(crate) fn finish_startup(&self) {
        self.active.store(false, Ordering::Release);
    }

    pub(crate) async fn wait_for_retry(&self) {
        let mut retry_started = self.retry_started.subscribe();
        if !*retry_started.borrow_and_update() {
            retry_started
                .changed()
                .await
                .expect("the startup retry store keeps the sender alive");
        }
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
        let mut last_error = None;
        loop {
            let read_result = if attempt == 0 {
                read().await
            } else {
                let remaining_budget = self.budget_remaining();
                if remaining_budget.is_zero() {
                    return Err(self.budget_exhausted(
                        operation,
                        key,
                        last_error
                            .as_ref()
                            .expect("a retry has an underlying error"),
                    ));
                }
                let read_started = Instant::now();
                let result = tokio::time::timeout(remaining_budget, read()).await;
                self.consume_budget(read_started.elapsed());
                match result {
                    Ok(result) => result,
                    Err(_) => {
                        return Err(self.budget_exhausted(
                            operation,
                            key,
                            last_error
                                .as_ref()
                                .expect("a retry has an underlying error"),
                        ));
                    }
                }
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
                    attempt = attempt.saturating_add(1);
                    let Some((delay, budget_remaining)) =
                        self.reserve_delay(operation, key, attempt, backoff)
                    else {
                        return Err(self.budget_exhausted(operation, key, &error));
                    };
                    self.retry_started.send_replace(true);
                    self.metrics.startup_read_retries.inc();
                    tracing::warn!(
                        %operation,
                        key,
                        attempt,
                        delay_ms = delay.as_millis(),
                        budget_remaining_ms = budget_remaining.as_millis(),
                        error = %error,
                        "retrying startup object storage read"
                    );
                    last_error = Some(error);
                    tokio::time::sleep(delay).await;
                    backoff = backoff.saturating_mul(2).min(MAX_BACKOFF);
                }
                Err(error) => return Err(error),
            }
        }
    }

    fn budget_remaining(&self) -> Duration {
        *self
            .remaining_budget
            .lock()
            .expect("startup retry budget lock is not poisoned")
    }

    fn consume_budget(&self, elapsed: Duration) {
        let mut remaining_budget = self
            .remaining_budget
            .lock()
            .expect("startup retry budget lock is not poisoned");
        *remaining_budget = remaining_budget.saturating_sub(elapsed);
    }

    fn budget_exhausted(
        &self,
        operation: ObjectStoreOperation,
        key: &str,
        last_error: &ObjectStoreError,
    ) -> ObjectStoreError {
        ObjectStoreError::new(
            operation,
            key,
            last_error.kind,
            format!(
                "startup read retry budget {:?} exhausted; last error: {last_error}",
                self.budget
            ),
        )
    }

    fn reserve_delay(
        &self,
        operation: ObjectStoreOperation,
        key: &str,
        attempt: u32,
        backoff: Duration,
    ) -> Option<(Duration, Duration)> {
        let mut remaining_budget = self
            .remaining_budget
            .lock()
            .expect("startup retry budget lock is not poisoned");
        if remaining_budget.is_zero() {
            return None;
        }
        let delay = self
            .retry_delay(operation, key, attempt, backoff)
            .min(*remaining_budget);
        *remaining_budget -= delay;
        Some((delay, *remaining_budget))
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
