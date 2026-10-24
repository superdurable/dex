// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Direct access to a closed store's files, independent of `Store`.

use std::collections::BTreeMap;
use std::fs;
use std::path::{Path, PathBuf};

use redb::{Database, ReadOnlyDatabase, ReadableDatabase, TableDefinition, TableError};
use s3mock::{Durability, EtagMode, Store, StoreConfig};

/// The on-disk version marker, spelled out so a renamed table or key fails these tests.
const STORE_FORMAT: TableDefinition<&str, u64> = TableDefinition::new("store_format");
const STORE_FORMAT_VERSION_KEY: &str = "version";
const METADATA_FILE_NAME: &str = "metadata.redb";

pub(crate) fn open_store(root_directory: &Path, durability: Durability) -> Store {
    Store::open(&store_config(root_directory, durability)).expect("the store opens")
}

pub(crate) fn store_config(root_directory: &Path, durability: Durability) -> StoreConfig {
    StoreConfig {
        root_directory: root_directory.to_path_buf(),
        durability,
        etag_mode: EtagMode::Md5,
    }
}

/// Returns the recorded format version, or `None` when the marker table does not exist.
pub(crate) fn read_store_format_version(root_directory: &Path) -> Option<u64> {
    let database = open_metadata(root_directory);
    let transaction = database.begin_read().expect("a read transaction starts");
    let table = match transaction.open_table(STORE_FORMAT) {
        Ok(table) => table,
        Err(TableError::TableDoesNotExist(_)) => return None,
        Err(error) => panic!("the store format table is unreadable: {error}"),
    };
    let version = table
        .get(STORE_FORMAT_VERSION_KEY)
        .expect("the store format table is readable")
        .expect("a store format table always holds the version")
        .value();
    Some(version)
}

/// Records `version` and returns the still open database, so the caller decides how it closes.
pub(crate) fn write_store_format_version(root_directory: &Path, version: u64) -> Database {
    let database = open_metadata(root_directory);
    let transaction = database.begin_write().expect("a write transaction starts");
    transaction
        .open_table(STORE_FORMAT)
        .expect("the store format table opens")
        .insert(STORE_FORMAT_VERSION_KEY, version)
        .expect("the version is written");
    transaction.commit().expect("the version commits");
    database
}

/// Turns a store into one written before the format version existed.
pub(crate) fn remove_store_format_table(root_directory: &Path) {
    let database = open_metadata(root_directory);
    let transaction = database.begin_write().expect("a write transaction starts");
    let is_deleted = transaction
        .delete_table(STORE_FORMAT)
        .expect("the store format table is deletable");
    assert!(is_deleted, "the store format table existed");
    transaction.commit().expect("the deletion commits");
}

/// Returns whether redb can open the metadata without a writable open, which a crash prevents.
pub(crate) fn is_metadata_readable_read_only(root_directory: &Path) -> bool {
    ReadOnlyDatabase::open(root_directory.join(METADATA_FILE_NAME)).is_ok()
}

/// Reclaims free pages so checked-in metadata stays small.
pub(crate) fn compact_metadata(root_directory: &Path) {
    let mut database = open_metadata(root_directory);
    database.compact().expect("the metadata database compacts");
}

fn open_metadata(root_directory: &Path) -> Database {
    Database::open(root_directory.join(METADATA_FILE_NAME)).expect("the metadata database opens")
}

/// Copies a directory tree, such as recorded testdata, into `destination`.
pub(crate) fn copy_directory(source: &Path, destination: &Path) {
    fs::create_dir_all(destination).expect("the destination directory is created");
    for entry in fs::read_dir(source).expect("the source directory is readable") {
        let entry = entry.expect("the directory entry is readable");
        let target = destination.join(entry.file_name());
        if entry
            .file_type()
            .expect("the file type is readable")
            .is_dir()
        {
            copy_directory(&entry.path(), &target);
        } else {
            fs::copy(entry.path(), &target).expect("the file is copied");
        }
    }
}

/// Returns every entry under `root` by relative path, with file bytes and `None` for directories.
pub(crate) fn snapshot_files(root: &Path) -> BTreeMap<PathBuf, Option<Vec<u8>>> {
    let mut entries = BTreeMap::new();
    collect_entries(root, root, &mut entries);
    entries
}

fn collect_entries(
    root: &Path,
    directory: &Path,
    entries: &mut BTreeMap<PathBuf, Option<Vec<u8>>>,
) {
    for entry in fs::read_dir(directory).expect("the directory is readable") {
        let path = entry.expect("the directory entry is readable").path();
        let relative = path
            .strip_prefix(root)
            .expect("collected paths are under the root")
            .to_path_buf();
        if path.is_dir() {
            entries.insert(relative, None);
            collect_entries(root, &path, entries);
        } else {
            entries.insert(
                relative,
                Some(fs::read(&path).expect("the file is readable")),
            );
        }
    }
}
