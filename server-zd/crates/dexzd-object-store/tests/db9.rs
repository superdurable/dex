// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

#![cfg(feature = "db9-integration")]

use bytes::Bytes;
use dexzd_object_store::{
    ByteRange, Db9ObjectStore, Db9ObjectStoreConfig, ObjectStore, WriteCondition, WriteOutcome,
};
use std::sync::Arc;
use tokio::sync::Barrier;

fn configuration() -> Db9ObjectStoreConfig {
    Db9ObjectStoreConfig::new(
        std::env::var("DEX_ZD_DB9_CONNECTION_STRING")
            .expect("db9 integration requires a connection string"),
        format!("db9-test-{}", uuid::Uuid::new_v4()),
    )
}

#[tokio::test]
async fn concurrent_creates_and_cas_have_one_winner() {
    let config = configuration();
    let first = Arc::new(Db9ObjectStore::new(config.clone()).await.unwrap());
    let second = Arc::new(Db9ObjectStore::new(config).await.unwrap());
    for condition in [
        WriteCondition::CreateOnly,
        WriteCondition::ReplaceIfEtagMatches {
            etag: String::new(),
        },
    ] {
        let condition = match condition {
            WriteCondition::ReplaceIfEtagMatches { .. } => WriteCondition::ReplaceIfEtagMatches {
                etag: first.head("lease").await.unwrap().unwrap().etag,
            },
            condition => condition,
        };
        let barrier = Arc::new(Barrier::new(8));
        let mut tasks = Vec::new();
        for index in 0..8 {
            let store = if index % 2 == 0 {
                first.clone()
            } else {
                second.clone()
            };
            let barrier = barrier.clone();
            let condition = condition.clone();
            let task = tokio::spawn(async move {
                barrier.wait().await;
                store
                    .put("lease", Bytes::from(vec![index]), condition)
                    .await
            });
            tasks.push(task);
        }
        let mut winners = 0;
        for task in tasks {
            match task.await.unwrap() {
                Ok(WriteOutcome::Written { .. }) => winners += 1,
                Ok(WriteOutcome::PreconditionFailed) => {}
                Err(error) if error.kind == dexzd_object_store::ObjectStoreErrorKind::Transient => {
                }
                other => panic!("unexpected race outcome: {other:?}"),
            }
        }
        assert_eq!(winners, 1);
    }
    let before = first.head("lease").await.unwrap().unwrap().etag;
    first.delete("lease").await.unwrap();
    first
        .put(
            "lease",
            Bytes::from_static(b"new"),
            WriteCondition::CreateOnly,
        )
        .await
        .unwrap();
    assert_eq!(
        second
            .put(
                "lease",
                Bytes::from_static(b"old"),
                WriteCondition::ReplaceIfEtagMatches { etag: before }
            )
            .await
            .unwrap(),
        WriteOutcome::PreconditionFailed
    );
    first.delete("lease").await.unwrap();
}

#[tokio::test]
async fn chunk_boundaries_prefixes_and_reconnect() {
    let mut config = configuration();
    config.key_prefix = "scope/%_/".to_owned();
    let store = Db9ObjectStore::new(config.clone()).await.unwrap();
    let bytes = (0..(700 * 1024))
        .map(|index| (index % 251) as u8)
        .collect::<Vec<_>>();
    store
        .put(
            "数据",
            Bytes::from(bytes.clone()),
            WriteCondition::CreateOnly,
        )
        .await
        .unwrap();
    let range = store
        .get_range(
            "数据",
            ByteRange {
                start: 262140,
                end_inclusive: 262150,
            },
        )
        .await
        .unwrap()
        .unwrap();
    assert_eq!(range.bytes, &bytes[262140..262151]);
    drop(store);
    let store = Db9ObjectStore::new(config.clone()).await.unwrap();
    assert_eq!(store.get("数据").await.unwrap().unwrap().bytes, bytes);
    let directory = tempfile::tempdir().unwrap();
    let path = directory.path().join("snapshot");
    assert_eq!(
        store.get_to_file("数据", &path).await.unwrap(),
        Some(bytes.len() as u64)
    );
    assert_eq!(tokio::fs::read(&path).await.unwrap(), bytes);
    store
        .put("empty", Bytes::new(), WriteCondition::CreateOnly)
        .await
        .unwrap();
    assert!(store.get("empty").await.unwrap().unwrap().bytes.is_empty());
    for key in ["list/a", "list/b", "list/c", "list_escaped"] {
        store
            .put(key, Bytes::new(), WriteCondition::CreateOnly)
            .await
            .unwrap();
    }
    let page = store.list("list/", None, 2).await.unwrap();
    assert_eq!(
        page.objects
            .iter()
            .map(|object| object.key.as_str())
            .collect::<Vec<_>>(),
        ["list/a", "list/b"]
    );
    let last = store
        .list("list/", page.next_start_after.as_deref(), 2)
        .await
        .unwrap();
    assert_eq!(last.objects[0].key, "list/c");
    assert!(last.next_start_after.is_none());
    config.key_prefix = "different/".to_owned();
    let isolated = Db9ObjectStore::new(config).await.unwrap();
    assert!(
        isolated
            .list("", None, 100)
            .await
            .unwrap()
            .objects
            .is_empty()
    );
    let objects = store.list("", None, 100).await.unwrap().objects;
    store
        .delete_many(
            &objects
                .into_iter()
                .map(|object| object.key)
                .collect::<Vec<_>>(),
        )
        .await
        .unwrap();
}

#[tokio::test]
async fn failed_upload_rolls_back_metadata_and_chunks() {
    let store = Db9ObjectStore::new(configuration()).await.unwrap();
    let original = Bytes::from(vec![19; 300_000]);
    store
        .put("snapshot", original.clone(), WriteCondition::CreateOnly)
        .await
        .unwrap();
    let before = store.head("snapshot").await.unwrap().unwrap();
    let directory = tempfile::tempdir().unwrap();
    assert!(
        store
            .put_file("snapshot", directory.path(), WriteCondition::Unconditional)
            .await
            .is_err()
    );
    let after = store.get("snapshot").await.unwrap().unwrap();
    assert_eq!(after.etag, before.etag);
    assert_eq!(after.bytes, original);
    store.delete("snapshot").await.unwrap();
}

#[tokio::test]
async fn concurrent_reads_never_mix_object_versions() {
    let config = configuration();
    let writer = Arc::new(Db9ObjectStore::new(config.clone()).await.unwrap());
    let reader = Db9ObjectStore::new(config).await.unwrap();
    writer
        .put(
            "snapshot",
            Bytes::from(vec![0; 600_000]),
            WriteCondition::CreateOnly,
        )
        .await
        .unwrap();
    let task_store = writer.clone();
    let task = tokio::spawn(async move {
        for version in 1..4 {
            task_store
                .put(
                    "snapshot",
                    Bytes::from(vec![version; 600_000]),
                    WriteCondition::Unconditional,
                )
                .await
                .unwrap();
        }
    });
    for _ in 0..5 {
        let content = reader.get("snapshot").await.unwrap().unwrap();
        assert_eq!(content.bytes.len(), 600_000);
        assert!(content.bytes.iter().all(|byte| *byte == content.bytes[0]));
    }
    task.await.unwrap();
    writer.delete("snapshot").await.unwrap();
}
