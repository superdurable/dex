// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The inner requests of `AliyunOssObjectStore` with listing version discovery stay as they were.

#[expect(
    dead_code,
    reason = "these tests use only part of the shared create-only store"
)]
#[path = "aliyun_oss/create_only_store.rs"]
mod create_only_store;

use std::time::Duration;

use bytes::Bytes;
use dexzd_metrics::NodeMetrics;
use dexzd_object_store::{
    AliyunOssObjectStore, MANIFEST_OBJECT_KEY, ObjectStore, ObjectStoreOperation, WriteCondition,
    WriteOutcome, startup_self_check,
};

use create_only_store::{CreateOnlyStore, TEST_MINIMUM_SUPERSEDED_AGE};

const OPERATIONS: [ObjectStoreOperation; 5] = [
    ObjectStoreOperation::List,
    ObjectStoreOperation::Get,
    ObjectStoreOperation::Head,
    ObjectStoreOperation::Put,
    ObjectStoreOperation::Delete,
];

/// LIST, GET, HEAD, PUT, and DELETE requests of the workload below, measured on the code before
/// version discovery was configurable.
const LISTING_DISCOVERY_REQUESTS: [u64; 5] = [36, 18, 0, 9, 5];

fn written_etag(outcome: WriteOutcome) -> String {
    match outcome {
        WriteOutcome::Written { etag } => etag,
        WriteOutcome::PreconditionFailed => panic!("the write must succeed"),
    }
}

#[tokio::test(start_paused = true)]
async fn listing_discovery_sends_the_same_inner_requests() {
    let physical = CreateOnlyStore::new();
    let store = AliyunOssObjectStore::new(
        physical.inner(),
        TEST_MINIMUM_SUPERSEDED_AGE,
        NodeMetrics::unregistered(),
    );
    assert!(store.get(MANIFEST_OBJECT_KEY).await.expect("get").is_none());
    let mut etag = written_etag(
        store
            .put(
                MANIFEST_OBJECT_KEY,
                Bytes::from_static(b"first"),
                WriteCondition::CreateOnly,
            )
            .await
            .expect("create"),
    );
    for round in 0..4 {
        // Spacing replacements past the age lets reclamation delete superseded versions.
        tokio::time::sleep(TEST_MINIMUM_SUPERSEDED_AGE + Duration::from_secs(1)).await;
        assert!(
            store
                .head(MANIFEST_OBJECT_KEY)
                .await
                .expect("head")
                .is_some()
        );
        etag = written_etag(
            store
                .put(
                    MANIFEST_OBJECT_KEY,
                    Bytes::from(format!("round {round}")),
                    WriteCondition::ReplaceIfEtagMatches { etag },
                )
                .await
                .expect("replace"),
        );
        tokio::time::sleep(Duration::from_secs(1)).await;
    }
    assert_eq!(
        store
            .put(
                MANIFEST_OBJECT_KEY,
                Bytes::from_static(b"duplicate"),
                WriteCondition::CreateOnly,
            )
            .await
            .expect("duplicate create"),
        WriteOutcome::PreconditionFailed
    );
    store
        .get(MANIFEST_OBJECT_KEY)
        .await
        .expect("get")
        .expect("the key exists");
    store.list("manifest", None, 10).await.expect("list");
    store.delete(MANIFEST_OBJECT_KEY).await.expect("delete");
    startup_self_check(&store)
        .await
        .expect("the startup self check passes");
    store
        .remove_stale_startup_probes()
        .await
        .expect("the probe sweep runs");
    tokio::time::sleep(Duration::from_secs(1)).await;
    let requests = OPERATIONS.map(|operation| physical.store.call_count(operation, ""));
    assert_eq!(requests, LISTING_DISCOVERY_REQUESTS);
}
