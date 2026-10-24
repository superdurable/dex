// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! How `Store::open` stamps and enforces the store format version.

use std::fs;
use std::io::Read;
use std::path::{Path, PathBuf};
use std::process::Command;

use s3mock::{
    CURRENT_STORE_FORMAT_VERSION, ChecksumAlgorithm, Durability, ObjectReadRequest,
    PutObjectRequest, Store, StoreError, UploadChecksumExpectations,
};

use crate::store_files::{
    is_metadata_readable_read_only, open_store, read_store_format_version,
    remove_store_format_table, snapshot_files, store_config, write_store_format_version,
};

const BUCKET: &str = "format-version";
const KEY: &str = "object.txt";
const BODY: &[u8] = b"kept across reopening";
/// Names the store that the child test process stamps and leaves open.
const UNCLEAN_STORE_DIRECTORY_VARIABLE: &str = "S3MOCK_UNCLEAN_STORE_DIRECTORY";
const UNCLEAN_STORE_CHILD_TEST: &str = "format_version::stamp_version_2_without_closing";

#[test]
fn new_store_is_stamped_with_version_1() {
    for durability in [Durability::Strict, Durability::Relaxed] {
        let directory = tempfile::tempdir().expect("a temporary directory is created");
        drop(open_store(directory.path(), durability));
        assert_eq!(read_store_format_version(directory.path()), Some(1));
        drop(open_store(directory.path(), durability));
        assert_eq!(read_store_format_version(directory.path()), Some(1));
    }
}

#[test]
fn store_without_format_version_opens_and_is_stamped_with_version_1() {
    let directory = tempfile::tempdir().expect("a temporary directory is created");
    write_object(&open_store(directory.path(), Durability::Strict));
    remove_store_format_table(directory.path());
    assert_eq!(read_store_format_version(directory.path()), None);

    let store = open_store(directory.path(), Durability::Strict);
    assert_eq!(read_object(&store), BODY);
    drop(store);
    assert_eq!(read_store_format_version(directory.path()), Some(1));
}

#[test]
fn store_with_unsupported_format_version_is_refused_without_changes() {
    for unsupported_version in [2, 0, u64::MAX] {
        for durability in [Durability::Strict, Durability::Relaxed] {
            let directory = tempfile::tempdir().expect("a temporary directory is created");
            let root = directory.path().join("store");
            write_object(&open_store(&root, durability));
            drop(write_store_format_version(&root, unsupported_version));
            // `Store::open` removes leftover temporary files, so a survivor proves it stopped first.
            fs::write(root.join("tmp").join("leftover"), b"left by a crash")
                .expect("the temporary file is written");
            let before = snapshot_files(&root);

            let error = match Store::open(&store_config(&root, durability)) {
                Ok(_) => panic!("version {unsupported_version} must be refused"),
                Err(error) => error,
            };

            assert_unsupported_version_error(&error, &root, unsupported_version);
            assert!(
                snapshot_files(&root) == before,
                "a refused open must leave every file and directory unchanged"
            );
            assert_eq!(read_store_format_version(&root), Some(unsupported_version));
        }
    }
}

/// A crashed newer store needs redb's repair, so the read-only check cannot refuse it first.
#[test]
fn store_with_unsupported_format_version_is_refused_after_an_unclean_shutdown() {
    let directory = tempfile::tempdir().expect("a temporary directory is created");
    let root = directory.path().join("store");
    write_object(&open_store(&root, Durability::Strict));
    let child = Command::new(std::env::current_exe().expect("the test binary path is known"))
        .args(["--exact", UNCLEAN_STORE_CHILD_TEST, "--ignored", "--quiet"])
        .env(UNCLEAN_STORE_DIRECTORY_VARIABLE, &root)
        .output()
        .expect("the child test process runs");
    assert!(
        child.status.success(),
        "the child test process failed with {}: {}{}",
        child.status,
        String::from_utf8_lossy(&child.stdout),
        String::from_utf8_lossy(&child.stderr)
    );
    assert!(
        !is_metadata_readable_read_only(&root),
        "the child must leave the metadata needing repair"
    );
    fs::write(root.join("tmp").join("leftover"), b"left by a crash")
        .expect("the temporary file is written");
    let data_before = snapshot_files(&root.join("data"));

    let error = match Store::open(&store_config(&root, Durability::Strict)) {
        Ok(_) => panic!("version 2 must be refused"),
        Err(error) => error,
    };

    assert_unsupported_version_error(&error, &root, 2);
    assert!(root.join("tmp").join("leftover").is_file());
    assert!(snapshot_files(&root.join("data")) == data_before);
    assert_eq!(read_store_format_version(&root), Some(2));
}

#[test]
#[ignore = "runs only as the child process of the unclean shutdown test"]
fn stamp_version_2_without_closing() {
    let root = PathBuf::from(
        std::env::var_os(UNCLEAN_STORE_DIRECTORY_VARIABLE)
            .unwrap_or_else(|| panic!("{UNCLEAN_STORE_DIRECTORY_VARIABLE} is set by the parent")),
    );
    // Skipping the drop leaves no clean-shutdown record, as when a process is killed.
    std::mem::forget(write_store_format_version(&root, 2));
}

fn assert_unsupported_version_error(error: &StoreError, root: &Path, unsupported_version: u64) {
    match error {
        StoreError::UnsupportedStoreFormatVersion {
            root_directory,
            found,
            supported,
        } => {
            assert_eq!(root_directory, root);
            assert_eq!(*found, unsupported_version);
            assert_eq!(*supported, CURRENT_STORE_FORMAT_VERSION);
        }
        other => panic!("expected an unsupported store format version, got {other:?}"),
    }
    let message = error.to_string();
    let expected_fragments = [
        format!("store format version {unsupported_version}"),
        "supports only version 1".to_owned(),
        format!("remove {} to start empty", root.display()),
    ];
    for fragment in expected_fragments {
        assert!(
            message.contains(&fragment),
            "{message:?} must contain {fragment:?}"
        );
    }
}

fn write_object(store: &Store) {
    store
        .create_bucket(BUCKET, false)
        .expect("the bucket name is valid");
    let mut writer = store
        .begin_put(BUCKET, KEY, ChecksumAlgorithm::Crc32)
        .expect("the bucket exists");
    writer
        .write_all(BODY)
        .expect("the temporary file is writable");
    let staged = writer
        .finish(&UploadChecksumExpectations::default())
        .expect("no digests are declared");
    store
        .commit_put(BUCKET, KEY, staged, PutObjectRequest::default())
        .expect("the unconditional write commits");
}

fn read_object(store: &Store) -> Vec<u8> {
    let object = store
        .read_object(BUCKET, KEY, &ObjectReadRequest::default())
        .expect("the object exists");
    let mut body = Vec::new();
    object
        .file
        .take(object.length)
        .read_to_end(&mut body)
        .expect("the data file is readable");
    body
}
