// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! A redb file backend that writes every commit to the file but never fsyncs it.

use std::fs::{File, OpenOptions};
use std::io;
use std::ops::Bound;
use std::path::Path;

use redb::backends::FileBackend;
use redb::{BackendError, DatabaseError, StorageBackend};

/// Delegates to redb's file backend, except that syncing returns at once.
///
/// Commits still reach the page cache before returning, so they survive a process kill.
#[derive(Debug)]
pub(crate) struct UnsyncedFileBackend {
    file_backend: FileBackend,
}

impl UnsyncedFileBackend {
    /// Opens or creates the database file at `path`, as `redb::Database::create` does.
    pub(crate) fn open(path: &Path) -> Result<UnsyncedFileBackend, DatabaseError> {
        let file: File = OpenOptions::new()
            .read(true)
            .write(true)
            .create(true)
            .truncate(false)
            .open(path)?;
        Ok(UnsyncedFileBackend {
            file_backend: FileBackend::new(file)?,
        })
    }
}

impl StorageBackend for UnsyncedFileBackend {
    fn len(&self) -> Result<u64, io::Error> {
        self.file_backend.len()
    }

    fn read(&self, offset: u64, out: &mut [u8]) -> Result<(), io::Error> {
        self.file_backend.read(offset, out)
    }

    fn set_len(&self, len: u64) -> Result<(), io::Error> {
        self.file_backend.set_len(len)
    }

    fn sync_data(&self) -> Result<(), io::Error> {
        Ok(())
    }

    fn write(&self, offset: u64, data: &[u8]) -> Result<(), io::Error> {
        self.file_backend.write(offset, data)
    }

    fn close(&self) -> Result<(), io::Error> {
        self.file_backend.close()
    }

    fn try_lock_range(&self, start: Bound<u64>, end: Bound<u64>) -> Result<bool, BackendError> {
        self.file_backend.try_lock_range(start, end)
    }

    fn try_lock_shared_range(
        &self,
        start: Bound<u64>,
        end: Bound<u64>,
    ) -> Result<bool, BackendError> {
        self.file_backend.try_lock_shared_range(start, end)
    }

    fn lock_range(&self, start: Bound<u64>, end: Bound<u64>) -> Result<(), BackendError> {
        self.file_backend.lock_range(start, end)
    }

    fn lock_shared_range(&self, start: Bound<u64>, end: Bound<u64>) -> Result<(), BackendError> {
        self.file_backend.lock_shared_range(start, end)
    }

    fn unlock_range(&self, start: Bound<u64>, end: Bound<u64>) -> Result<(), BackendError> {
        self.file_backend.unlock_range(start, end)
    }

    fn query_lock_range(&self, start: Bound<u64>, end: Bound<u64>) -> Result<bool, BackendError> {
        self.file_backend.query_lock_range(start, end)
    }
}
