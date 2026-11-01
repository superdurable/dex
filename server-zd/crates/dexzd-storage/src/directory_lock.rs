// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Who holds the lock of a RocksDB store directory, probed without opening the store.

use std::collections::BTreeSet;
use std::fs::OpenOptions;
use std::io;
use std::path::{Path, PathBuf};
use std::sync::{Mutex, MutexGuard, PoisonError};

use rustix::fs::{FlockOperation, fcntl_lock};
use rustix::io::Errno;

use crate::local_store::StorageResult;

/// Canonical directories of the RocksDB stores this process opened for writing.
static OPEN_DIRECTORIES: Mutex<BTreeSet<PathBuf>> = Mutex::new(BTreeSet::new());

/// Who holds the lock of a RocksDB store directory.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[non_exhaustive]
pub enum DirectoryLockHolder {
    /// No process holds it; opening the store can take it.
    Nobody,
    /// A store this process opened holds it, such as one a stopping task still uses.
    ThisProcess,
    /// Another process holds it.
    AnotherProcess,
}

/// Returns who holds the lock of the RocksDB store in `directory`, without opening the store.
///
/// Opening a store whose lock another process holds first renames that process's info `LOG`,
/// so a caller that may race another process probes here first. The probe takes the lock the way
/// RocksDB does, an exclusive `fcntl` lock on `LOCK`, and releases it at once.
///
/// Returns an I/O error when the directory or its `LOCK` file cannot be read.
pub fn probe_directory_lock(directory: &Path) -> StorageResult<DirectoryLockHolder> {
    let directory = match std::fs::canonicalize(directory) {
        Ok(directory) => directory,
        Err(error) if error.kind() == io::ErrorKind::NotFound => {
            return Ok(DirectoryLockHolder::Nobody);
        }
        Err(error) => return Err(error.into()),
    };
    // Held until the probe's file closes: closing any descriptor of `LOCK` releases every
    // `fcntl` lock this process holds on it, so no store of this process may open meanwhile.
    let open_directories = lock_open_directories();
    if open_directories.contains(&directory) {
        return Ok(DirectoryLockHolder::ThisProcess);
    }
    let lock_file = match OpenOptions::new()
        .read(true)
        .write(true)
        .open(directory.join("LOCK"))
    {
        Ok(lock_file) => lock_file,
        Err(error) if error.kind() == io::ErrorKind::NotFound => {
            return Ok(DirectoryLockHolder::Nobody);
        }
        Err(error) => return Err(error.into()),
    };
    let holder = match fcntl_lock(&lock_file, FlockOperation::NonBlockingLockExclusive) {
        Ok(()) => {
            fcntl_lock(&lock_file, FlockOperation::NonBlockingUnlock).map_err(io::Error::from)?;
            DirectoryLockHolder::Nobody
        }
        Err(error) if error == Errno::AGAIN || error == Errno::ACCESS => {
            DirectoryLockHolder::AnotherProcess
        }
        Err(error) => return Err(io::Error::from(error).into()),
    };
    drop(lock_file);
    drop(open_directories);
    Ok(holder)
}

/// Returns whether a store this process opened in `directory` is still open, or is closing.
///
/// A store leaves this set only after its database closed and released the directory's lock.
pub fn is_directory_open_in_this_process(directory: &Path) -> bool {
    std::fs::canonicalize(directory)
        .is_ok_and(|directory| lock_open_directories().contains(&directory))
}

/// Registers a store this process opens in `directory` until the returned value drops. Creates
/// the last level of the directory first, so every alias of it, such as a symbolic link, names
/// one entry.
///
/// Returns an I/O error when the directory cannot be created or canonicalized, and a
/// [`io::ErrorKind::WouldBlock`] error when this process already has a store open there, even
/// through another path: RocksDB's own lock table compares only the path text.
pub(crate) fn register_open_directory(directory: &Path) -> StorageResult<OpenDirectory> {
    match std::fs::create_dir(directory) {
        Err(error) if error.kind() != io::ErrorKind::AlreadyExists => return Err(error.into()),
        _ => {}
    }
    let canonical = std::fs::canonicalize(directory)?;
    if !lock_open_directories().insert(canonical.clone()) {
        return Err(io::Error::new(
            io::ErrorKind::WouldBlock,
            format!(
                "this process already has the store in {} open",
                canonical.display()
            ),
        )
        .into());
    }
    Ok(OpenDirectory {
        directory: canonical,
        #[cfg(test)]
        opened_as: directory.to_path_buf(),
    })
}

/// A store directory this process has open; dropping it unregisters the directory.
#[derive(Debug)]
pub(crate) struct OpenDirectory {
    directory: PathBuf,
    /// The path the store was opened through, which RocksDB keys its own lock table by.
    #[cfg(test)]
    opened_as: PathBuf,
}

impl Drop for OpenDirectory {
    fn drop(&mut self) {
        #[cfg(test)]
        unregister_checks::check_store_closed(&self.directory, &self.opened_as);
        lock_open_directories().remove(&self.directory);
    }
}

fn lock_open_directories() -> MutexGuard<'static, BTreeSet<PathBuf>> {
    OPEN_DIRECTORIES
        .lock()
        .unwrap_or_else(PoisonError::into_inner)
}

/// Lets a test check that a store's database closed before its directory is unregistered.
#[cfg(test)]
pub(crate) mod unregister_checks {
    use std::collections::BTreeMap;
    use std::path::{Path, PathBuf};
    use std::sync::{Mutex, PoisonError};

    /// Directories under check, with whether their database still held the lock at unregister.
    static CHECKED_DIRECTORIES: Mutex<BTreeMap<PathBuf, Option<bool>>> =
        Mutex::new(BTreeMap::new());

    /// Checks `directory` at its next unregister.
    pub(crate) fn watch(directory: &Path) {
        let directory = std::fs::canonicalize(directory).expect("canonicalize the directory");
        lock().insert(directory, None);
    }

    /// Returns whether the database still held the lock when `directory` was unregistered.
    pub(crate) fn was_open_at_unregister(directory: &Path) -> Option<bool> {
        let directory = std::fs::canonicalize(directory).expect("canonicalize the directory");
        lock().get(&directory).copied().flatten()
    }

    pub(super) fn check_store_closed(directory: &Path, opened_as: &Path) {
        if !lock().contains_key(directory) {
            return;
        }
        // RocksDB checks its own lock table, keyed by the opening path, before the directory.
        let reopened = rocksdb::DB::open_default(opened_as);
        let was_open = reopened
            .as_ref()
            .is_err_and(|error| error.to_string().contains("lock hold by current process"));
        drop(reopened);
        lock().insert(directory.to_path_buf(), Some(was_open));
    }

    fn lock() -> std::sync::MutexGuard<'static, BTreeMap<PathBuf, Option<bool>>> {
        CHECKED_DIRECTORIES
            .lock()
            .unwrap_or_else(PoisonError::into_inner)
    }
}
