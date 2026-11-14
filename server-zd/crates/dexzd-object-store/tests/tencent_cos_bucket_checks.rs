// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Rejected appends, timeouts, and cleanup failures of the Tencent COS create-only bucket check.

use std::sync::Arc;
use std::sync::atomic::{AtomicU64, Ordering};
use std::time::Duration;

use dexzd_object_store::{
    FaultAction, FaultInjector, InMemoryObjectStore, ObjectStore, ObjectStoreOperation,
    STARTUP_SELF_CHECK_DEADLINE, TencentCosBucketCheckError, check_tencent_cos_create_only_writes,
};

/// Applies one action to the first probe write, another to the second, and one to every delete.
struct ProbeFaults {
    puts: AtomicU64,
    first_put: FaultAction,
    second_put: FaultAction,
    delete: FaultAction,
}

impl FaultInjector for ProbeFaults {
    fn action(&self, operation: ObjectStoreOperation, _key: &str) -> FaultAction {
        match operation {
            ObjectStoreOperation::Put => match self.puts.fetch_add(1, Ordering::SeqCst) {
                0 => self.first_put,
                1 => self.second_put,
                _ => FaultAction::Proceed,
            },
            ObjectStoreOperation::Delete => self.delete,
            _ => FaultAction::Proceed,
        }
    }
}

fn store_with(
    first_put: FaultAction,
    second_put: FaultAction,
    delete: FaultAction,
) -> InMemoryObjectStore {
    InMemoryObjectStore::with_injector(Arc::new(ProbeFaults {
        puts: AtomicU64::new(0),
        first_put,
        second_put,
        delete,
    }))
}

async fn object_count(store: &InMemoryObjectStore) -> usize {
    store
        .list("", None, 1000)
        .await
        .expect("the store lists")
        .objects
        .len()
}

#[tokio::test(start_paused = true)]
async fn a_bucket_that_rejects_append_names_the_unsupported_bucket_kinds() {
    let store = store_with(
        FaultAction::FailPermanently,
        FaultAction::Proceed,
        FaultAction::Proceed,
    );
    let error = check_tencent_cos_create_only_writes(&store)
        .await
        .expect_err("the first append fails permanently");
    assert!(
        matches!(error, TencentCosBucketCheckError::AppendRejected { .. }),
        "{error:?}"
    );
    let message = error.to_string();
    assert!(
        message.contains("multi-AZ") && message.contains("INTELLIGENT_TIERING"),
        "{message}"
    );
}

#[tokio::test(start_paused = true)]
async fn a_transient_append_failure_is_a_store_error() {
    let store = store_with(
        FaultAction::FailBeforeEffect,
        FaultAction::Proceed,
        FaultAction::Proceed,
    );
    let error = check_tencent_cos_create_only_writes(&store)
        .await
        .expect_err("the first append fails");
    assert!(
        matches!(error, TencentCosBucketCheckError::ObjectStore(_)),
        "{error:?}"
    );
}

#[tokio::test(start_paused = true)]
async fn a_check_that_times_out_still_deletes_its_probe_object() {
    let store = store_with(
        FaultAction::Proceed,
        FaultAction::Delay(STARTUP_SELF_CHECK_DEADLINE + Duration::from_secs(60)),
        FaultAction::Proceed,
    );
    let error = check_tencent_cos_create_only_writes(&store)
        .await
        .expect_err("the second write outlasts the deadline");
    assert!(
        matches!(error, TencentCosBucketCheckError::TimedOut { .. }),
        "{error:?}"
    );
    assert_eq!(object_count(&store).await, 0);
}

#[tokio::test(start_paused = true)]
async fn a_failed_check_reports_a_failed_cleanup_too() {
    let store = store_with(
        FaultAction::Proceed,
        FaultAction::FailBeforeEffect,
        FaultAction::FailBeforeEffect,
    );
    let error = check_tencent_cos_create_only_writes(&store)
        .await
        .expect_err("the second write fails");
    let TencentCosBucketCheckError::CheckAndCleanupFailed { check, cleanup } = &error else {
        panic!("both failures are reported: {error:?}");
    };
    assert!(
        matches!(**check, TencentCosBucketCheckError::ObjectStore(_)),
        "{check:?}"
    );
    assert!(!cleanup.is_empty());
}

#[tokio::test(start_paused = true)]
async fn a_passed_check_reports_a_failed_cleanup() {
    let store = store_with(
        FaultAction::Proceed,
        FaultAction::Proceed,
        FaultAction::FailBeforeEffect,
    );
    let error = check_tencent_cos_create_only_writes(&store)
        .await
        .expect_err("the probe delete fails");
    assert!(
        matches!(error, TencentCosBucketCheckError::CleanupFailed { .. }),
        "{error:?}"
    );
}
