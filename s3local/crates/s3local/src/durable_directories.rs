// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Directories under a strict store root whose entries this process has flushed into their parents.

use std::collections::{BTreeMap, BTreeSet};
use std::fs;
use std::io;
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex, MutexGuard};

#[cfg(test)]
use std::sync::atomic::{AtomicU64, Ordering};

use crate::data_file::sync_directory;
use crate::error::StoreError;

/// Flushes each directory's entry into its parent once per process; concurrent callers wait for it.
///
/// Existing directories are not trusted, since their creator may not have flushed the parent yet.
pub(crate) struct DurableDirectories {
    /// Already durable when the store opens; never flushed again here.
    root: PathBuf,
    /// One cell per directory; `true` once its entry is durable in its parent.
    cells: Mutex<BTreeMap<PathBuf, Arc<Mutex<bool>>>>,
    #[cfg(test)]
    parent_sync_count: AtomicU64,
}

impl DurableDirectories {
    /// Creates an empty set for the directories under `root`, whose own entry the caller made durable.
    pub(crate) fn new(root: PathBuf) -> Self {
        Self {
            root,
            cells: Mutex::new(BTreeMap::new()),
            #[cfg(test)]
            parent_sync_count: AtomicU64::new(0),
        }
    }

    /// Creates `directory` and missing ancestors, returning once every entry below the root is durable.
    ///
    /// Returns the I/O error of a failed creation or flush; a later call retries the flush.
    ///
    /// # Panics
    ///
    /// Panics when `directory` is not under the root, which is a store programming error.
    pub(crate) fn create_durably(&self, directory: &Path) -> io::Result<()> {
        if directory == self.root {
            return Ok(());
        }
        assert!(
            directory.starts_with(&self.root),
            "store directories live under the store root"
        );
        let cell = self.cell(directory);
        let mut is_durable = cell
            .lock()
            .expect("no code panics while flushing a directory entry");
        if *is_durable {
            return Ok(());
        }
        let parent = directory
            .parent()
            .expect("a directory under the store root has a parent");
        self.create_durably(parent)?;
        match fs::create_dir(directory) {
            Ok(()) => {}
            Err(error) if error.kind() == io::ErrorKind::AlreadyExists => {}
            Err(error) => return Err(error),
        }
        sync_directory(parent)?;
        #[cfg(test)]
        self.parent_sync_count.fetch_add(1, Ordering::Relaxed);
        *is_durable = true;
        Ok(())
    }

    /// Forgets `directory` and every directory below it, after the store removed them.
    pub(crate) fn forget(&self, directory: &Path) {
        let mut cells = self.lock_cells();
        let removed: Vec<PathBuf> = cells
            .range(directory.to_path_buf()..)
            .map(|(path, _)| path)
            .take_while(|path| path.starts_with(directory))
            .cloned()
            .collect();
        for path in removed {
            cells.remove(&path);
        }
    }

    fn cell(&self, directory: &Path) -> Arc<Mutex<bool>> {
        self.lock_cells()
            .entry(directory.to_path_buf())
            .or_default()
            .clone()
    }

    fn lock_cells(&self) -> MutexGuard<'_, BTreeMap<PathBuf, Arc<Mutex<bool>>>> {
        self.cells
            .lock()
            .expect("no code panics while holding the directory cell lock")
    }
}

/// Flushes the entry of the existing `directory` and of each ancestor into its parent.
///
/// It walks the configured path's ancestors, then the canonical path's ancestors.
///
/// Returns the set of directories flushed. Fails with
/// [`StoreError::DirectoryEntryFlushFailed`] when a directory cannot be resolved, opened, or flushed.
pub(crate) fn flush_entries_up_to_filesystem_root(
    directory: &Path,
) -> Result<BTreeSet<PathBuf>, StoreError> {
    let flush_failure =
        |directory: &Path, source: io::Error| StoreError::DirectoryEntryFlushFailed {
            directory: directory.to_path_buf(),
            source,
        };
    let configured =
        std::path::absolute(directory).map_err(|source| flush_failure(directory, source))?;
    let canonical =
        fs::canonicalize(directory).map_err(|source| flush_failure(directory, source))?;
    let mut flushed = BTreeSet::new();
    // Symlinks at intermediate components are covered only through these two walks.
    for path in [configured, canonical] {
        for parent in path.ancestors().skip(1) {
            if parent.as_os_str().is_empty() || flushed.contains(parent) {
                continue;
            }
            sync_directory(parent).map_err(|source| flush_failure(parent, source))?;
            flushed.insert(parent.to_path_buf());
        }
    }
    Ok(flushed)
}

#[cfg(test)]
mod tests {
    use std::fs;
    use std::sync::Arc;
    use std::sync::atomic::Ordering;
    use std::thread;

    use super::{DurableDirectories, flush_entries_up_to_filesystem_root};
    use crate::error::StoreError;

    #[test]
    fn every_ancestor_of_a_nested_directory_is_flushed() {
        let root = tempfile::tempdir().unwrap();
        let root_path = fs::canonicalize(root.path()).unwrap();
        let nested = root_path.join("persist").join("new-dev").join("s3");
        fs::create_dir_all(&nested).unwrap();
        let flushed = flush_entries_up_to_filesystem_root(&nested).unwrap();
        for directory in [
            root_path.join("persist").join("new-dev"),
            root_path.join("persist"),
            root_path.clone(),
        ] {
            assert!(
                flushed.contains(&directory),
                "{} is flushed",
                directory.display()
            );
        }
        for ancestor in root_path.ancestors().skip(1) {
            assert!(
                flushed.contains(ancestor),
                "{} is flushed",
                ancestor.display()
            );
        }
    }

    #[test]
    fn a_symlinked_root_flushes_the_alias_and_the_target_ancestors() {
        let root = tempfile::tempdir().unwrap();
        let root_path = fs::canonicalize(root.path()).unwrap();
        let target = root_path.join("persist").join("target");
        fs::create_dir_all(&target).unwrap();
        let aliases = root_path.join("aliases");
        fs::create_dir(&aliases).unwrap();
        let alias = aliases.join("dev");
        std::os::unix::fs::symlink(&target, &alias).unwrap();

        let flushed = flush_entries_up_to_filesystem_root(&alias).unwrap();
        assert!(
            flushed.contains(&aliases),
            "the alias entry lives in {}",
            aliases.display()
        );
        assert!(
            flushed.contains(&root_path.join("persist")),
            "the target entry is flushed"
        );
        let shared: Vec<_> = root_path.ancestors().collect();
        assert_eq!(
            flushed.len(),
            shared.len() + 2,
            "the shared ancestors are flushed once, plus aliases and persist"
        );
    }

    #[test]
    fn an_ancestor_that_cannot_be_opened_fails_the_flush() {
        use std::os::unix::fs::PermissionsExt;

        let root = tempfile::tempdir().unwrap();
        let locked = root.path().join("persist");
        let new_dev = locked.join("new-dev");
        fs::create_dir_all(&new_dev).unwrap();
        fs::set_permissions(&locked, fs::Permissions::from_mode(0o300)).unwrap();
        // Root reads any directory, so the denial this test needs cannot happen there.
        let is_denied = fs::File::open(&locked).is_err();
        let result = flush_entries_up_to_filesystem_root(&new_dev);
        fs::set_permissions(&locked, fs::Permissions::from_mode(0o700)).unwrap();
        if !is_denied {
            return;
        }
        match result {
            Err(StoreError::DirectoryEntryFlushFailed { directory, .. }) => {
                assert!(directory.ends_with("persist"), "{}", directory.display());
            }
            other => panic!("expected the flush to fail at persist: {other:?}"),
        }
    }

    #[test]
    fn an_existing_unconfirmed_directory_has_its_parent_flushed_once() {
        let root = tempfile::tempdir().unwrap();
        let existing = root.path().join("bucket").join("shard");
        fs::create_dir_all(&existing).unwrap();
        let directories = DurableDirectories::new(root.path().to_path_buf());

        directories.create_durably(&existing).unwrap();
        assert_eq!(
            directories.parent_sync_count.load(Ordering::Relaxed),
            2,
            "both existing entries are flushed into their parents"
        );
        directories.create_durably(&existing).unwrap();
        directories
            .create_durably(&root.path().join("bucket"))
            .unwrap();
        assert_eq!(directories.parent_sync_count.load(Ordering::Relaxed), 2);

        let sibling = root.path().join("bucket").join("other");
        directories.create_durably(&sibling).unwrap();
        assert!(sibling.is_dir());
        assert_eq!(directories.parent_sync_count.load(Ordering::Relaxed), 3);
    }

    #[test]
    fn concurrent_callers_all_return_after_one_flush_per_directory() {
        let root = tempfile::tempdir().unwrap();
        let directories = Arc::new(DurableDirectories::new(root.path().to_path_buf()));
        let shard = root.path().join("bucket").join("shard");
        let callers: Vec<_> = (0..8)
            .map(|_| {
                let directories = Arc::clone(&directories);
                let shard = shard.clone();
                thread::spawn(move || {
                    directories.create_durably(&shard).unwrap();
                    assert!(*directories.cell(&shard).lock().unwrap());
                    assert!(*directories.cell(shard.parent().unwrap()).lock().unwrap());
                })
            })
            .collect();
        for caller in callers {
            caller.join().unwrap();
        }
        assert_eq!(directories.parent_sync_count.load(Ordering::Relaxed), 2);
    }

    #[test]
    fn a_forgotten_directory_is_flushed_again_when_recreated() {
        let root = tempfile::tempdir().unwrap();
        let directories = DurableDirectories::new(root.path().to_path_buf());
        let bucket = root.path().join("bucket");
        let upload = bucket.join("uploads").join("upload");
        directories.create_durably(&upload).unwrap();
        assert_eq!(directories.parent_sync_count.load(Ordering::Relaxed), 3);

        fs::remove_dir_all(bucket.join("uploads")).unwrap();
        directories.forget(&bucket.join("uploads"));
        directories.create_durably(&upload).unwrap();
        assert!(upload.is_dir());
        assert_eq!(directories.parent_sync_count.load(Ordering::Relaxed), 5);
    }
}
