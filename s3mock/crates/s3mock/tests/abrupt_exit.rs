// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Stores after an abrupt exit, simulated by copying their files while they are open.

use std::fs;
use std::io::Read;
use std::path::Path;

use s3mock::{
    ChecksumAlgorithm, DeleteObjectRequest, Durability, ListObjectsRequest, ObjectReadRequest,
    PutObjectRequest, Store, StoreConfig, StoreError, UploadChecksumExpectations,
};

const BUCKET: &str = "abrupt";

fn config(root_directory: &Path, durability: Durability) -> StoreConfig {
    StoreConfig {
        root_directory: root_directory.to_path_buf(),
        durability,
        etag_mode: s3mock::EtagMode::Md5,
    }
}

fn put(store: &Store, key: &str, body: &[u8]) {
    let mut writer = store
        .begin_put(BUCKET, key, ChecksumAlgorithm::Crc32)
        .expect("the bucket exists");
    writer
        .write_all(body)
        .expect("the temporary file is writable");
    let staged = writer
        .finish(&UploadChecksumExpectations::default())
        .expect("no digests are declared");
    store
        .commit_put(BUCKET, key, staged, PutObjectRequest::default())
        .expect("the unconditional write commits");
}

fn read(store: &Store, key: &str) -> Result<Vec<u8>, StoreError> {
    let object = store.read_object(BUCKET, key, &ObjectReadRequest::default())?;
    let mut body = Vec::new();
    object
        .file
        .take(object.length)
        .read_to_end(&mut body)
        .expect("the data file is readable");
    Ok(body)
}

fn listed_keys(store: &Store) -> Vec<String> {
    store
        .list_objects(
            BUCKET,
            &ListObjectsRequest {
                prefix: String::new(),
                delimiter: String::new(),
                max_keys: 1000,
                continuation_token: None,
                start_after: None,
            },
        )
        .expect("the bucket lists")
        .objects
        .into_iter()
        .map(|object| object.key)
        .collect()
}

/// Copies every file of an open store, which is what a SIGKILL leaves on disk.
fn copy_open_store(from: &Path, to: &Path) {
    fs::create_dir_all(to).expect("the copy directory is created");
    for entry in fs::read_dir(from).expect("the store directory lists") {
        let entry = entry.expect("the directory entry is readable");
        let target = to.join(entry.file_name());
        if entry
            .file_type()
            .expect("the entry type is readable")
            .is_dir()
        {
            copy_open_store(&entry.path(), &target);
        } else {
            fs::copy(entry.path(), &target).expect("the store file is copied");
        }
    }
}

fn count_data_files(root_directory: &Path) -> usize {
    fn count(directory: &Path) -> usize {
        fs::read_dir(directory)
            .expect("the data directory lists")
            .map(|entry| {
                let entry = entry.expect("the directory entry is readable");
                if entry
                    .file_type()
                    .expect("the entry type is readable")
                    .is_dir()
                {
                    count(&entry.path())
                } else {
                    1
                }
            })
            .sum()
    }
    count(&root_directory.join("data"))
}

/// Rolled-back metadata still finds the files that the lost relaxed commits superseded.
#[test]
fn every_listed_object_is_readable_after_an_abrupt_exit_of_a_relaxed_session() {
    let directory = tempfile::tempdir().unwrap();
    let root_directory = directory.path().join("store");
    {
        let store = Store::open(&config(&root_directory, Durability::Relaxed)).unwrap();
        store.create_bucket(BUCKET, false).unwrap();
        put(&store, "overwritten", b"durable version");
        put(&store, "deleted", b"durable object");
    }
    let crashed_directory = directory.path().join("crashed");
    {
        let store = Store::open(&config(&root_directory, Durability::Relaxed)).unwrap();
        put(&store, "overwritten", b"lost version");
        store
            .delete_object(BUCKET, "deleted", &DeleteObjectRequest::default())
            .unwrap();
        put(&store, "created", b"lost object");
        copy_open_store(&root_directory, &crashed_directory);
    }

    let reopened = Store::open(&config(&crashed_directory, Durability::Relaxed)).unwrap();
    let keys = listed_keys(&reopened);
    assert_eq!(keys, ["deleted", "overwritten"], "metadata rolls back");
    for key in &keys {
        read(&reopened, key)
            .unwrap_or_else(|error| panic!("{key} is listed but unreadable: {error}"));
    }
    assert_eq!(read(&reopened, "overwritten").unwrap(), b"durable version");
    assert_eq!(read(&reopened, "deleted").unwrap(), b"durable object");
}

/// Deferred removals are bounded, because enough of them force a durable commit.
#[test]
fn superseded_files_of_a_long_relaxed_session_are_removed_before_close() {
    let directory = tempfile::tempdir().unwrap();
    let root_directory = directory.path().join("store");
    let crashed_directory = directory.path().join("crashed");
    let store = Store::open(&config(&root_directory, Durability::Relaxed)).unwrap();
    store.create_bucket(BUCKET, false).unwrap();
    for revision in 0..1000 {
        put(&store, "lease", format!("revision {revision}").as_bytes());
    }
    let data_files = count_data_files(&root_directory);
    assert!(
        data_files < 500,
        "{data_files} data files remain for one key after 1000 overwrites"
    );
    assert_eq!(read(&store, "lease").unwrap(), b"revision 999");
    copy_open_store(&root_directory, &crashed_directory);
    drop(store);

    let reopened = Store::open(&config(&crashed_directory, Durability::Relaxed)).unwrap();
    let body = String::from_utf8(read(&reopened, "lease").unwrap()).unwrap();
    let revision: u32 = body
        .strip_prefix("revision ")
        .and_then(|revision| revision.parse().ok())
        .unwrap_or_else(|| panic!("unexpected body {body:?}"));
    assert!(
        revision >= 256,
        "the forced durable commits keep a recent revision, got {revision}"
    );
}

/// Strict and process-crash-safe stores remove superseded files at once.
#[test]
fn strict_and_process_crash_safe_modes_remove_superseded_files_at_once() {
    for durability in [Durability::Strict, Durability::ProcessCrashSafe] {
        let directory = tempfile::tempdir().unwrap();
        let store = Store::open(&config(directory.path(), durability)).unwrap();
        store.create_bucket(BUCKET, false).unwrap();
        for revision in 0..3 {
            put(&store, "lease", format!("revision {revision}").as_bytes());
        }
        assert_eq!(count_data_files(directory.path()), 1, "{durability:?}");
    }
}

/// A process kill loses no acknowledged write of a process-crash-safe store.
#[test]
fn a_process_crash_safe_store_keeps_every_acknowledged_write_after_an_abrupt_exit() {
    let directory = tempfile::tempdir().unwrap();
    let root_directory = directory.path().join("store");
    let crashed_directory = directory.path().join("crashed");
    {
        let store = Store::open(&config(&root_directory, Durability::ProcessCrashSafe)).unwrap();
        store.create_bucket(BUCKET, false).unwrap();
        put(&store, "overwritten", b"first version");
        put(&store, "deleted", b"deleted object");
        for revision in 0..300 {
            put(&store, "lease", format!("revision {revision}").as_bytes());
        }
        put(&store, "overwritten", b"acknowledged version");
        store
            .delete_object(BUCKET, "deleted", &DeleteObjectRequest::default())
            .unwrap();
        put(&store, "created", b"acknowledged object");
        copy_open_store(&root_directory, &crashed_directory);
    }

    let reopened = Store::open(&config(&crashed_directory, Durability::ProcessCrashSafe)).unwrap();
    assert_eq!(listed_keys(&reopened), ["created", "lease", "overwritten"]);
    assert_eq!(
        read(&reopened, "overwritten").unwrap(),
        b"acknowledged version"
    );
    assert_eq!(read(&reopened, "created").unwrap(), b"acknowledged object");
    assert_eq!(read(&reopened, "lease").unwrap(), b"revision 299");
}

/// A relaxed bucket deletion keeps the bucket directory until a durable commit covers it.
#[test]
fn a_rolled_back_relaxed_bucket_deletion_keeps_its_objects_readable() {
    let directory = tempfile::tempdir().unwrap();
    let root_directory = directory.path().join("store");
    {
        let store = Store::open(&config(&root_directory, Durability::Relaxed)).unwrap();
        store.create_bucket(BUCKET, false).unwrap();
        put(&store, "object", b"durable object");
    }
    let crashed_directory = directory.path().join("crashed");
    {
        let store = Store::open(&config(&root_directory, Durability::Relaxed)).unwrap();
        store
            .delete_object(BUCKET, "object", &DeleteObjectRequest::default())
            .unwrap();
        store.delete_bucket(BUCKET).unwrap();
        copy_open_store(&root_directory, &crashed_directory);
    }

    let reopened = Store::open(&config(&crashed_directory, Durability::Relaxed)).unwrap();
    assert_eq!(listed_keys(&reopened), ["object"], "metadata rolls back");
    assert_eq!(read(&reopened, "object").unwrap(), b"durable object");
}

/// The guarded open refuses every later open, in any mode, once a relaxed session ends abruptly.
#[test]
fn a_guarded_open_refuses_a_store_whose_relaxed_session_ended_abruptly() {
    let directory = tempfile::tempdir().unwrap();
    let root_directory = directory.path().join("store");
    let crashed_directory = directory.path().join("crashed");
    {
        let store = Store::open_refusing_unclean_relaxed_session(&config(
            &root_directory,
            Durability::Relaxed,
        ))
        .unwrap();
        store.create_bucket(BUCKET, false).unwrap();
        put(&store, "object", b"body");
        copy_open_store(&root_directory, &crashed_directory);
    }

    for durability in [
        Durability::Relaxed,
        Durability::Relaxed,
        Durability::Strict,
        Durability::ProcessCrashSafe,
    ] {
        let refused =
            Store::open_refusing_unclean_relaxed_session(&config(&crashed_directory, durability));
        assert!(
            matches!(refused, Err(StoreError::UncleanRelaxedSession { .. })),
            "a {durability:?} guarded open must refuse the store"
        );
    }
    Store::open(&config(&crashed_directory, Durability::Relaxed))
        .expect("the unguarded open keeps opening a rolled-back store");

    let reopened =
        Store::open_refusing_unclean_relaxed_session(&config(&root_directory, Durability::Relaxed))
            .expect("a durable close lets the next guarded open succeed");
    assert_eq!(read(&reopened, "object").unwrap(), b"body");
}

/// Sessions that need no marker leave a store that the guarded open accepts after an abrupt exit.
#[test]
fn a_guarded_open_accepts_strict_and_process_crash_safe_stores_copied_while_open() {
    for durability in [Durability::Strict, Durability::ProcessCrashSafe] {
        let directory = tempfile::tempdir().unwrap();
        let root_directory = directory.path().join("store");
        let crashed_directory = directory.path().join("crashed");
        {
            let store =
                Store::open_refusing_unclean_relaxed_session(&config(&root_directory, durability))
                    .unwrap();
            store.create_bucket(BUCKET, false).unwrap();
            put(&store, "object", b"body");
            copy_open_store(&root_directory, &crashed_directory);
        }
        let reopened = Store::open_refusing_unclean_relaxed_session(&config(
            &crashed_directory,
            Durability::ProcessCrashSafe,
        ))
        .unwrap_or_else(|error| panic!("a {durability:?} session leaves no marker: {error}"));
        assert_eq!(read(&reopened, "object").unwrap(), b"body");
    }
}

/// A store from a release without markers opens after a clean close and is refused after a crash.
#[test]
fn a_guarded_open_checks_a_store_from_a_release_without_markers() {
    let directory = tempfile::tempdir().unwrap();
    let clean_directory = directory.path().join("clean");
    let crashed_directory = directory.path().join("crashed");
    {
        let store = Store::open(&config(&clean_directory, Durability::Relaxed)).unwrap();
        store.create_bucket(BUCKET, false).unwrap();
        put(&store, "object", b"body");
        copy_open_store(&clean_directory, &crashed_directory);
    }

    for _ in 0..2 {
        let refused = Store::open_refusing_unclean_relaxed_session(&config(
            &crashed_directory,
            Durability::ProcessCrashSafe,
        ));
        assert!(
            matches!(
                refused,
                Err(StoreError::UncleanSessionWithoutRelaxedSessionMarkers { .. })
            ),
            "an unmarked store that was not closed cleanly must be refused"
        );
    }
    let reopened = Store::open_refusing_unclean_relaxed_session(&config(
        &clean_directory,
        Durability::ProcessCrashSafe,
    ))
    .expect("an unmarked store that was closed cleanly opens");
    assert_eq!(read(&reopened, "object").unwrap(), b"body");
}

/// A strict configuration is trusted for an unmarked store that was not closed cleanly.
#[test]
fn a_strict_guarded_open_accepts_an_unmarked_strict_store_copied_while_open() {
    let directory = tempfile::tempdir().unwrap();
    let root_directory = directory.path().join("store");
    let crashed_directory = directory.path().join("crashed");
    {
        let store = Store::open(&config(&root_directory, Durability::Strict)).unwrap();
        store.create_bucket(BUCKET, false).unwrap();
        put(&store, "object", b"body");
        copy_open_store(&root_directory, &crashed_directory);
    }
    for durability in [Durability::ProcessCrashSafe, Durability::Relaxed] {
        let refused =
            Store::open_refusing_unclean_relaxed_session(&config(&crashed_directory, durability));
        assert!(
            matches!(
                refused,
                Err(StoreError::UncleanSessionWithoutRelaxedSessionMarkers { .. })
            ),
            "a {durability:?} guarded open must refuse the unmarked store"
        );
    }
    {
        let reopened = Store::open_refusing_unclean_relaxed_session(&config(
            &crashed_directory,
            Durability::Strict,
        ))
        .expect("a strict guarded open trusts the configuration");
        assert_eq!(read(&reopened, "object").unwrap(), b"body");
    }
    let stamped = Store::open_refusing_unclean_relaxed_session(&config(
        &crashed_directory,
        Durability::ProcessCrashSafe,
    ))
    .expect("the strict open stamped the store");
    assert_eq!(read(&stamped, "object").unwrap(), b"body");
}
