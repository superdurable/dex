// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! A relaxed store after an abrupt exit, simulated by copying its files while it is open.

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

/// A strict store still removes superseded files at once.
#[test]
fn strict_mode_removes_superseded_files_at_once() {
    let directory = tempfile::tempdir().unwrap();
    let store = Store::open(&config(directory.path(), Durability::Strict)).unwrap();
    store.create_bucket(BUCKET, false).unwrap();
    for revision in 0..3 {
        put(&store, "lease", format!("revision {revision}").as_bytes());
    }
    assert_eq!(count_data_files(directory.path()), 1);
}

/// The guarded open refuses every later open, in either mode, once a relaxed session ends abruptly.
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

    for durability in [Durability::Relaxed, Durability::Relaxed, Durability::Strict] {
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

/// A strict session needs no marker, so a strict store copied while open still opens.
#[test]
fn a_guarded_open_accepts_a_strict_store_copied_while_open() {
    let directory = tempfile::tempdir().unwrap();
    let root_directory = directory.path().join("store");
    let crashed_directory = directory.path().join("crashed");
    {
        let store = Store::open_refusing_unclean_relaxed_session(&config(
            &root_directory,
            Durability::Strict,
        ))
        .unwrap();
        store.create_bucket(BUCKET, false).unwrap();
        put(&store, "object", b"body");
        copy_open_store(&root_directory, &crashed_directory);
    }
    let reopened = Store::open_refusing_unclean_relaxed_session(&config(
        &crashed_directory,
        Durability::Relaxed,
    ))
    .expect("a strict session leaves no marker");
    assert_eq!(read(&reopened, "object").unwrap(), b"body");
}
