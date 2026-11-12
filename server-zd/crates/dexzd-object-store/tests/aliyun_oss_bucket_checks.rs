// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Timeouts and cleanup failures of the Aliyun OSS create-only bucket check.

use std::sync::Arc;
use std::sync::atomic::{AtomicU64, Ordering};
use std::time::Duration;

use dexzd_object_store::{
    AliyunOssBucketCheckError, FaultAction, FaultInjector, InMemoryObjectStore, ObjectStore,
    ObjectStoreOperation, STARTUP_SELF_CHECK_DEADLINE, check_aliyun_oss_create_only_writes,
};

/// Delays or fails the second probe write and, optionally, every delete.
struct ProbeFaults {
    puts: AtomicU64,
    second_put: FaultAction,
    delete: FaultAction,
}

impl FaultInjector for ProbeFaults {
    fn action(&self, operation: ObjectStoreOperation, _key: &str) -> FaultAction {
        match operation {
            ObjectStoreOperation::Put if self.puts.fetch_add(1, Ordering::SeqCst) == 1 => {
                self.second_put
            }
            ObjectStoreOperation::Delete => self.delete,
            _ => FaultAction::Proceed,
        }
    }
}

fn store_with(second_put: FaultAction, delete: FaultAction) -> InMemoryObjectStore {
    InMemoryObjectStore::with_injector(Arc::new(ProbeFaults {
        puts: AtomicU64::new(0),
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
async fn a_check_that_times_out_still_deletes_its_probe_object() {
    let store = store_with(
        FaultAction::Delay(STARTUP_SELF_CHECK_DEADLINE + Duration::from_secs(60)),
        FaultAction::Proceed,
    );
    let error = check_aliyun_oss_create_only_writes(&store)
        .await
        .expect_err("the second write outlasts the deadline");
    assert!(
        matches!(error, AliyunOssBucketCheckError::TimedOut { .. }),
        "{error:?}"
    );
    assert_eq!(object_count(&store).await, 0);
}

#[tokio::test(start_paused = true)]
async fn a_failed_check_reports_a_failed_cleanup_too() {
    let store = store_with(FaultAction::FailBeforeEffect, FaultAction::FailBeforeEffect);
    let error = check_aliyun_oss_create_only_writes(&store)
        .await
        .expect_err("the second write fails");
    let AliyunOssBucketCheckError::CheckAndCleanupFailed { check, cleanup } = &error else {
        panic!("both failures are reported: {error:?}");
    };
    assert!(
        matches!(**check, AliyunOssBucketCheckError::ObjectStore(_)),
        "{check:?}"
    );
    assert!(!cleanup.is_empty());
    assert!(
        error.to_string().contains("deleting the probe object"),
        "{error}"
    );
}

#[tokio::test(start_paused = true)]
async fn a_passed_check_reports_a_failed_cleanup() {
    let store = store_with(FaultAction::Proceed, FaultAction::FailBeforeEffect);
    let error = check_aliyun_oss_create_only_writes(&store)
        .await
        .expect_err("the probe delete fails");
    assert!(
        matches!(error, AliyunOssBucketCheckError::CleanupFailed { .. }),
        "{error:?}"
    );
}
