// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Snapshots that leave out empty write-ahead log files, for stores without empty create-only objects.

use std::sync::Mutex;

use dexzd_keyspace::ColumnFamily;
use dexzd_object_store::{InMemoryObjectStore, ObjectStore};
use dexzd_protocol::dexzd::v1::SnapshotManifest;

use crate::{
    LocalReadView, LocalStore, RestoredLocalStore, RocksDbLocalStore, RocksDbStoreOptions,
    SnapshotIdentity, SnapshotUploadOptions, WriteBatch, create_snapshot, restore_snapshot,
};

/// Returns the zero-byte write-ahead log files that `manifest` lists.
fn empty_write_ahead_log_files(manifest: &SnapshotManifest) -> Vec<String> {
    manifest
        .files
        .iter()
        .filter(|file| file.relative_path.ends_with(".log") && file.size_bytes == 0)
        .map(|file| file.relative_path.clone())
        .collect()
}

#[tokio::test]
async fn rocksdb_snapshot_lists_its_empty_write_ahead_log_unless_told_to_skip_it() {
    for should_skip_empty_write_ahead_log_files in [false, true] {
        let directory = tempfile::tempdir().expect("temporary directory exists");
        let store =
            RocksDbLocalStore::open(directory.path().join("db"), RocksDbStoreOptions::default())
                .expect("RocksDB opens");
        let object_store = InMemoryObjectStore::new();
        let mut batch = WriteBatch::new();
        batch.put(ColumnFamily::Flow, b"sample".to_vec(), b"data".to_vec());
        store.write(batch).expect("sample row writes");
        let identity = SnapshotIdentity {
            cluster_id: [7; 16],
            node_id: "node-a".into(),
            created_at_unix_micros: 10,
        };
        let (manifest_key, manifest) = create_snapshot(
            &store,
            &object_store,
            &identity,
            1,
            &Mutex::new(()),
            SnapshotUploadOptions {
                should_skip_empty_write_ahead_log_files,
            },
        )
        .await
        .expect("snapshot uploads");
        // The checkpoint flushes first, so its newest write-ahead log is empty.
        let empty_logs = empty_write_ahead_log_files(&manifest);
        assert_eq!(
            empty_logs.is_empty(),
            should_skip_empty_write_ahead_log_files,
            "{empty_logs:?}"
        );
        assert!(
            manifest
                .files
                .iter()
                .any(|file| file.relative_path.ends_with(".sst")),
            "the flushed row is in a table file"
        );
        for file in &manifest.files {
            assert!(
                object_store
                    .head(&file.object_key)
                    .await
                    .expect("head succeeds")
                    .is_some(),
                "{}",
                file.object_key
            );
        }
        let restored = restore_snapshot(
            &object_store,
            &manifest_key,
            &directory.path().join("restored"),
            RocksDbStoreOptions::default(),
        )
        .await
        .expect("snapshot restores");
        assert!(matches!(restored, RestoredLocalStore::RocksDb(_)));
        assert_eq!(
            restored
                .get(ColumnFamily::Flow, b"sample")
                .expect("restored row reads"),
            Some(b"data".to_vec())
        );
    }
}
