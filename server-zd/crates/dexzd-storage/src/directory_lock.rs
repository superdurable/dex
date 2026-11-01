// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Who holds the lock of a RocksDB store directory, probed without opening the store.

use std::collections::BTreeMap;
use std::fs::OpenOptions;
use std::io;
use std::path::{Path, PathBuf};
use std::sync::{Mutex, MutexGuard, PoisonError};

use rustix::fs::{FlockOperation, fcntl_lock};
use rustix::io::Errno;

use crate::local_store::StorageResult;

/// RocksDB stores this process opened for writing, by absolute directory, with their counts.
static OPEN_DIRECTORIES: Mutex<BTreeMap<PathBuf, usize>> = Mutex::new(BTreeMap::new());

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
    let directory = std::path::absolute(directory)?;
    // Held while probing, so this process cannot open the store between the check and the probe.
    let open_directories = lock_open_directories();
    if open_directories.contains_key(&directory) {
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
    drop(open_directories);
    Ok(holder)
}

/// Registers a store this process opens in `directory` until the returned value drops.
///
/// Returns an I/O error when the directory has no absolute form.
pub(crate) fn register_open_directory(directory: &Path) -> StorageResult<OpenDirectory> {
    let directory = std::path::absolute(directory)?;
    *lock_open_directories()
        .entry(directory.clone())
        .or_default() += 1;
    Ok(OpenDirectory { directory })
}

/// A store directory this process has open; dropping it unregisters the directory.
#[derive(Debug)]
pub(crate) struct OpenDirectory {
    directory: PathBuf,
}

impl Drop for OpenDirectory {
    fn drop(&mut self) {
        let mut open_directories = lock_open_directories();
        if let Some(count) = open_directories.get_mut(&self.directory) {
            *count -= 1;
            if *count == 0 {
                open_directories.remove(&self.directory);
            }
        }
    }
}

fn lock_open_directories() -> MutexGuard<'static, BTreeMap<PathBuf, usize>> {
    OPEN_DIRECTORIES
        .lock()
        .unwrap_or_else(PoisonError::into_inner)
}
