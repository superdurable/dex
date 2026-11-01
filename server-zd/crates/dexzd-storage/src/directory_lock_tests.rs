// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::io::Write;
use std::path::Path;
use std::process::Command;

use rustix::fs::{FlockOperation, fcntl_lock};

use crate::directory_lock::unregister_checks;
use crate::{DirectoryLockHolder, RocksDbLocalStore, RocksDbStoreOptions, probe_directory_lock};

/// Environment variable that makes [`try_to_lock_a_directory_for_another_test`] try a lock.
const LOCK_FILE_VARIABLE: &str = "DEXZD_TEST_LOCK_FILE";

/// Tries the `fcntl` lock RocksDB takes on the file that [`LOCK_FILE_VARIABLE`] names and prints
/// `locked` or `busy`; another test runs it as a second process. Without the variable it does
/// nothing.
#[test]
fn try_to_lock_a_directory_for_another_test() {
    let Some(lock_file) = std::env::var_os(LOCK_FILE_VARIABLE) else {
        return;
    };
    let lock_file = std::fs::OpenOptions::new()
        .read(true)
        .write(true)
        .open(lock_file)
        .expect("open the LOCK file");
    let outcome = match fcntl_lock(&lock_file, FlockOperation::NonBlockingLockExclusive) {
        Ok(()) => "locked",
        Err(_) => "busy",
    };
    print!("lock-outcome:{outcome}");
    std::io::stdout().flush().expect("flush the outcome");
}

/// Returns whether another process can take the lock of the store in `directory`.
fn can_another_process_lock(directory: &Path) -> bool {
    let output = Command::new(std::env::current_exe().expect("test binary"))
        .args([
            "--exact",
            "directory_lock_tests::try_to_lock_a_directory_for_another_test",
            "--nocapture",
        ])
        .env(LOCK_FILE_VARIABLE, directory.join("LOCK"))
        .output()
        .expect("run the locking process");
    let stdout = String::from_utf8_lossy(&output.stdout);
    assert!(stdout.contains("lock-outcome:"), "{stdout}");
    stdout.contains("lock-outcome:locked")
}

#[test]
fn a_probe_of_a_store_this_process_holds_keeps_its_lock() {
    let directory = tempfile::tempdir().expect("temporary directory");
    let store_directory = directory.path().join("store");
    let store = RocksDbLocalStore::open(&store_directory, RocksDbStoreOptions::default())
        .expect("open the store");
    assert_eq!(
        probe_directory_lock(&store_directory).expect("probe the lock"),
        DirectoryLockHolder::ThisProcess
    );
    assert!(
        !can_another_process_lock(&store_directory),
        "the probe released this process's lock"
    );
    drop(store);
    assert_eq!(
        probe_directory_lock(&store_directory).expect("probe the lock"),
        DirectoryLockHolder::Nobody
    );
    assert!(can_another_process_lock(&store_directory));
}

#[test]
fn a_probe_through_an_alias_of_the_directory_finds_this_process() {
    let directory = tempfile::tempdir().expect("temporary directory");
    let store_directory = directory.path().join("store");
    let store = RocksDbLocalStore::open(&store_directory, RocksDbStoreOptions::default())
        .expect("open the store");
    let symlink = directory.path().join("alias");
    std::os::unix::fs::symlink(&store_directory, &symlink).expect("link an alias");
    let dotted = directory.path().join("store").join("..").join("store");
    for alias in [&symlink, &dotted] {
        assert_eq!(
            probe_directory_lock(alias).expect("probe the lock"),
            DirectoryLockHolder::ThisProcess,
            "{}",
            alias.display()
        );
    }
    assert!(
        !can_another_process_lock(&store_directory),
        "a probe through an alias released this process's lock"
    );
    drop(store);
}

#[test]
fn a_store_closes_its_database_before_it_unregisters_its_directory() {
    let directory = tempfile::tempdir().expect("temporary directory");
    let store_directory = directory.path().join("store");
    let store = RocksDbLocalStore::open(&store_directory, RocksDbStoreOptions::default())
        .expect("open the store");
    unregister_checks::watch(&store_directory);
    drop(store);
    assert_eq!(
        unregister_checks::was_open_at_unregister(&store_directory),
        Some(false),
        "the directory was unregistered while its database still held the lock"
    );
}

#[test]
fn a_second_open_of_a_store_through_an_alias_fails_and_keeps_the_lock() {
    let directory = tempfile::tempdir().expect("temporary directory");
    let store_directory = directory.path().join("store");
    let store = RocksDbLocalStore::open(&store_directory, RocksDbStoreOptions::default())
        .expect("open the store");
    let symlink = directory.path().join("alias");
    std::os::unix::fs::symlink(&store_directory, &symlink).expect("link an alias");
    let reopened = RocksDbLocalStore::open(&symlink, RocksDbStoreOptions::default());
    assert!(
        reopened.is_err(),
        "this process opened one store directory twice"
    );
    drop(reopened);
    assert!(
        !can_another_process_lock(&store_directory),
        "the failed second open released the first store's lock"
    );
    drop(store);
    assert!(can_another_process_lock(&store_directory));
}

#[test]
fn opening_a_store_does_not_create_missing_parent_directories() {
    let directory = tempfile::tempdir().expect("temporary directory");
    let nested = directory.path().join("missing").join("store");
    assert!(RocksDbLocalStore::open(&nested, RocksDbStoreOptions::default()).is_err());
    assert!(!directory.path().join("missing").exists());
}
