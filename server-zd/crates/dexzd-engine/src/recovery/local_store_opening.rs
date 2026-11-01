// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::ffi::OsString;
use std::fmt;
use std::path::{Path, PathBuf};
use std::sync::Arc;

use dexzd_object_store::ObjectStore;
use dexzd_protocol::dexzd::v1::SnapshotReference;
use dexzd_storage::{
    InMemoryLocalStore, LocalReadView, RestoredLocalStore, RocksDbLocalStore, RocksDbStoreOptions,
    restore_snapshot_with_lineage_id,
};
use uuid::{Uuid, Version};

use crate::blocking_work::BlockingWorkPlacement;
use crate::commit::EngineLocalStore;
use crate::recovery::EngineStartError;
use crate::recovery::node_streams::NodeStreams;

/// Separates the store directory name from the UUIDv7 of a set-aside copy.
const SET_ASIDE_INFIX: &str = ".discarded-";

/// Where the node keeps its local state.
#[derive(Clone)]
pub enum LocalStoreConfig {
    /// RocksDB in `directory`. A missing, corrupt, or outdated store is set aside and
    /// restored from the newest snapshot. Other open failures, such as a lock that another
    /// process holds, fail startup and leave the directory in place.
    RocksDb {
        /// Store directory; created when missing.
        directory: PathBuf,
        /// RocksDB tuning.
        options: RocksDbStoreOptions,
    },
    /// A new in-memory store restored from the newest snapshot, for simulations and tests.
    InMemory,
    /// A store an earlier simulated process left; one the retained log cannot update gives way
    /// to the newest snapshot.
    #[cfg(feature = "test-util")]
    ExistingForTest(Arc<EngineLocalStore>),
}

impl fmt::Debug for LocalStoreConfig {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::RocksDb { directory, options } => formatter
                .debug_struct("RocksDb")
                .field("directory", directory)
                .field("options", options)
                .finish(),
            Self::InMemory => formatter.write_str("InMemory"),
            #[cfg(feature = "test-util")]
            Self::ExistingForTest(_) => formatter.write_str("ExistingForTest"),
        }
    }
}

/// What recovery needs to decide whether the local store can be reused.
pub(crate) struct LocalStoreRequirements<'a> {
    pub(crate) snapshot: Option<&'a SnapshotReference>,
    pub(crate) streams: &'a NodeStreams,
    pub(crate) partition_count: u32,
    /// Lineage of a store this start creates empty or restores from a snapshot.
    pub(crate) new_store_lineage_id: Uuid,
}

/// The lowest applied batch over every partition, ordered by incarnation, then seq.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(crate) struct AppliedPosition {
    pub(crate) incarnation: u32,
    pub(crate) seq: u64,
    /// Epoch of that batch; see `PartitionMeta.applied_epoch`.
    pub(crate) epoch: u64,
}

/// A local store that the retained log can update.
pub(crate) struct OpenedLocalStore {
    pub(crate) store: Arc<EngineLocalStore>,
    /// UUIDv7 in the name of the copy that this start set aside, if any.
    pub(crate) set_aside_id: Option<Uuid>,
}

pub(crate) async fn open_local_store(
    config: &LocalStoreConfig,
    object_store: &dyn ObjectStore,
    requirements: &LocalStoreRequirements<'_>,
) -> Result<OpenedLocalStore, EngineStartError> {
    let (store, set_aside_id) = match config {
        LocalStoreConfig::RocksDb { directory, options } => {
            open_rocksdb_store(directory, *options, object_store, requirements).await?
        }
        #[cfg(feature = "test-util")]
        LocalStoreConfig::ExistingForTest(store) => {
            match reusable_existing_store(store, requirements)? {
                Some(store) => {
                    return Ok(OpenedLocalStore {
                        store,
                        set_aside_id: None,
                    });
                }
                None => (restore_in_memory(object_store, requirements).await?, None),
            }
        }
        LocalStoreConfig::InMemory => (restore_in_memory(object_store, requirements).await?, None),
    };
    if let Some(gap) =
        find_unreplayable_gap(&store, requirements.streams, requirements.partition_count)?
    {
        return Err(gap);
    }
    Ok(OpenedLocalStore {
        store: Arc::new(store),
        set_aside_id,
    })
}

/// Returns `store` when the retained log can update it, like a reopened RocksDB directory.
#[cfg(feature = "test-util")]
fn reusable_existing_store(
    store: &Arc<EngineLocalStore>,
    requirements: &LocalStoreRequirements<'_>,
) -> Result<Option<Arc<EngineLocalStore>>, EngineStartError> {
    if requirements.snapshot.is_some()
        && lowest_applied_position(store.as_ref(), requirements.partition_count)?.is_none()
    {
        tracing::warn!(
            "a partition of the existing local store applied no batch; restoring the newest snapshot"
        );
        return Ok(None);
    }
    match find_unreplayable_gap(
        store.as_ref(),
        requirements.streams,
        requirements.partition_count,
    )? {
        None => Ok(Some(Arc::clone(store))),
        Some(gap) => {
            tracing::warn!(gap = %gap, "the retained log cannot update the existing local store; restoring the newest snapshot");
            Ok(None)
        }
    }
}

/// Restores the newest snapshot into a new in-memory store, or returns an empty store.
async fn restore_in_memory(
    object_store: &dyn ObjectStore,
    requirements: &LocalStoreRequirements<'_>,
) -> Result<RestoredLocalStore, EngineStartError> {
    Ok(match requirements.snapshot {
        Some(snapshot) => {
            let directory = std::env::temp_dir().join(format!("dexzd-restore-{}", Uuid::now_v7()));
            let restored = restore_snapshot_with_lineage_id(
                object_store,
                &snapshot.manifest_key,
                &directory,
                RocksDbStoreOptions::default(),
                requirements.new_store_lineage_id,
            )
            .await?;
            if matches!(restored, RestoredLocalStore::Memory(_))
                && let Err(error) = std::fs::remove_dir_all(&directory)
            {
                tracing::warn!(path = %directory.display(), error = %error, "failed to remove snapshot restore directory");
            }
            restored
        }
        None => RestoredLocalStore::Memory(InMemoryLocalStore::with_lineage_id(
            requirements.new_store_lineage_id,
        )),
    })
}

/// Opens the RocksDB store in `directory`, or sets it aside and rebuilds it.
///
/// Returns the store and the UUIDv7 of the copy set aside, if any. Returns
/// [`EngineStartError::LocalStoreUnavailable`] without renaming when opening fails for a reason
/// other than the store's contents.
async fn open_rocksdb_store(
    directory: &Path,
    options: RocksDbStoreOptions,
    object_store: &dyn ObjectStore,
    requirements: &LocalStoreRequirements<'_>,
) -> Result<(RestoredLocalStore, Option<Uuid>), EngineStartError> {
    let mut set_aside_id = None;
    if is_nonempty_directory(directory)? {
        match RocksDbLocalStore::open(directory, options) {
            Ok(store)
                if requirements.snapshot.is_some()
                    && lowest_applied_position(&store, requirements.partition_count)?.is_none() =>
            {
                // Log cleanup may have deleted streams older than the oldest recorded one.
                tracing::warn!(
                    "a partition of the local store applied no batch; restoring the newest snapshot"
                );
            }
            Ok(store) => match find_unreplayable_gap(
                &store,
                requirements.streams,
                requirements.partition_count,
            )? {
                None => return Ok((RestoredLocalStore::RocksDb(store), None)),
                // The copy may be the only evidence that object storage metadata went backwards.
                Some(gap @ EngineStartError::InvalidMetadata { .. }) => {
                    tracing::error!(gap = %gap, "the local store is newer than the recorded metadata; restoring the newest snapshot");
                }
                Some(gap) => {
                    tracing::warn!(gap = %gap, "the retained log cannot update the local store; restoring the newest snapshot");
                }
            },
            Err(error) if error.is_unusable_store_content(directory) => {
                tracing::warn!(error = %error, "local store cannot be opened; restoring the newest snapshot");
            }
            Err(source) => {
                return Err(EngineStartError::LocalStoreUnavailable {
                    directory: directory.to_path_buf(),
                    source,
                });
            }
        }
        set_aside_id = Some(set_aside(directory)?);
    }
    let store = match requirements.snapshot {
        Some(snapshot) => {
            restore_snapshot_with_lineage_id(
                object_store,
                &snapshot.manifest_key,
                directory,
                options,
                requirements.new_store_lineage_id,
            )
            .await?
        }
        None => RestoredLocalStore::RocksDb(RocksDbLocalStore::open_with_new_store_lineage_id(
            directory,
            options,
            requirements.new_store_lineage_id,
        )?),
    };
    Ok((store, set_aside_id))
}

/// Returns why the retained log cannot bring `store` up to date, or `None` when it can.
pub(crate) fn find_unreplayable_gap(
    store: &dyn LocalReadView,
    streams: &NodeStreams,
    partition_count: u32,
) -> Result<Option<EngineStartError>, EngineStartError> {
    let applied = lowest_applied_position(store, partition_count)?;
    let open_incarnation = streams.open_segment().incarnation;
    if let Some(applied) = applied
        && applied.incarnation > open_incarnation
    {
        return Ok(Some(EngineStartError::InvalidMetadata {
            detail: format!(
                "the local store applied incarnation {}, newer than the recorded incarnation \
                 {open_incarnation}",
                applied.incarnation
            ),
        }));
    }
    let segments = &streams.segments;
    // Cleanup drops older segments whole, so the rest of an unrecorded stream is gone.
    if let Some(applied) = applied
        && !segments
            .iter()
            .any(|segment| segment.incarnation == applied.incarnation)
    {
        return Ok(Some(EngineStartError::LogStreamDeleted {
            incarnation: applied.incarnation,
            resumes_at: applied.seq + 1,
            oldest_retained_incarnation: segments[0].incarnation,
        }));
    }
    for segment in segments {
        let resumes_at = match applied {
            Some(applied) if applied.incarnation > segment.incarnation => continue,
            Some(applied) if applied.incarnation == segment.incarnation => applied.seq + 1,
            _ => 1,
        };
        if resumes_at < segment.start_seq {
            return Ok(Some(EngineStartError::LogUnavailable {
                incarnation: segment.incarnation,
                first_retained_seq: segment.start_seq,
                resumes_at,
            }));
        }
    }
    Ok(None)
}

/// Returns the lowest applied position over every partition, or `None` when a partition
/// applied nothing.
pub(crate) fn lowest_applied_position(
    store: &dyn LocalReadView,
    partition_count: u32,
) -> Result<Option<AppliedPosition>, EngineStartError> {
    let metas = store.partition_metas()?;
    if metas.len() < partition_count as usize {
        return Ok(None);
    }
    let mut lowest: Option<AppliedPosition> = None;
    for meta in &metas {
        let Some(applied) = &meta.applied else {
            return Ok(None);
        };
        let position = AppliedPosition {
            incarnation: applied.incarnation,
            seq: applied.seq,
            epoch: meta.applied_epoch,
        };
        if lowest.is_none_or(|lowest| {
            (position.incarnation, position.seq) < (lowest.incarnation, lowest.seq)
        }) {
            lowest = Some(position);
        }
    }
    Ok(lowest)
}

fn is_nonempty_directory(directory: &Path) -> Result<bool, EngineStartError> {
    if !directory.exists() {
        return Ok(false);
    }
    Ok(std::fs::read_dir(directory)?.next().transpose()?.is_some())
}

/// Renames `directory` to its set-aside sibling.
///
/// Returns the UUIDv7 in the new name.
fn set_aside(directory: &Path) -> Result<Uuid, EngineStartError> {
    let Some((parent, mut discarded_name)) = set_aside_location(directory) else {
        return Err(EngineStartError::InvalidConfig {
            detail: format!(
                "local store directory {} has no final name to set aside",
                directory.display()
            ),
        });
    };
    let id = Uuid::now_v7();
    discarded_name.push(id.to_string());
    let discarded = parent.join(discarded_name);
    std::fs::rename(directory, &discarded)?;
    tracing::warn!(path = %discarded.display(), "set aside the previous local store");
    Ok(id)
}

/// Returns the directory that holds set-aside copies of `directory`, and their name prefix
/// `<store>.discarded-`.
///
/// Returns `None` when `directory` has no final name, such as `/` or a path ending in `..`.
fn set_aside_location(directory: &Path) -> Option<(&Path, OsString)> {
    let mut prefix = directory.file_name()?.to_owned();
    prefix.push(SET_ASIDE_INFIX);
    let parent = match directory.parent() {
        Some(parent) if !parent.as_os_str().is_empty() => parent,
        _ => Path::new("."),
    };
    Some((parent, prefix))
}

/// Deletes set-aside stores of a RocksDB `config`, keeping `retained_count` newest with
/// `this_start_id` first; failures only log.
pub(crate) async fn delete_older_set_aside_stores(
    config: &LocalStoreConfig,
    retained_count: usize,
    this_start_id: Option<Uuid>,
    blocking_work: BlockingWorkPlacement,
) {
    let LocalStoreConfig::RocksDb { directory, .. } = config else {
        return;
    };
    let directory = directory.clone();
    let deleted = blocking_work
        .run(move || {
            delete_set_aside_stores_except_newest(&directory, retained_count, this_start_id);
        })
        .await;
    if let Err(failure) = deleted {
        tracing::warn!(error = %failure, "failed to delete older set-aside local stores");
    }
}

/// Deletes `<store>.discarded-<uuidv7>` siblings of `directory` except the newest `retained_count`.
/// The active store never matches: its name lacks the suffix.
fn delete_set_aside_stores_except_newest(
    directory: &Path,
    retained_count: usize,
    this_start_id: Option<Uuid>,
) {
    let Some((parent, prefix)) = set_aside_location(directory) else {
        return;
    };
    let Some(prefix) = prefix.to_str() else {
        tracing::warn!(path = %directory.display(), "cannot match set-aside stores of a local store without a UTF-8 name");
        return;
    };
    let mut set_aside_stores = match list_set_aside_stores(parent, prefix) {
        Ok(stores) => stores,
        Err(error) => {
            tracing::warn!(path = %parent.display(), error = %error, "failed to list set-aside local stores");
            return;
        }
    };
    let mut kept_count = retained_count;
    if retained_count > 0
        && let Some(this_start_id) = this_start_id
    {
        let listed_count = set_aside_stores.len();
        set_aside_stores.retain(|(id, _)| *id != this_start_id);
        if set_aside_stores.len() < listed_count {
            kept_count -= 1;
        }
    }
    // UUIDv7 bytes start with the creation time, so this sorts oldest first.
    set_aside_stores.sort_unstable_by_key(|(id, _)| *id);
    let deleted_count = set_aside_stores.len().saturating_sub(kept_count);
    for (_, path) in set_aside_stores.into_iter().take(deleted_count) {
        match std::fs::remove_dir_all(&path) {
            Ok(()) => tracing::info!(path = %path.display(), "deleted a set-aside local store"),
            Err(error) => {
                tracing::warn!(path = %path.display(), error = %error, "failed to delete a set-aside local store");
            }
        }
    }
}

/// Returns the UUIDv7 and path of every directory in `parent` named `<prefix><uuidv7>`, in
/// directory order, skipping entries that cannot be read.
///
/// Returns an I/O error only when `parent` cannot be listed.
fn list_set_aside_stores(
    parent: &Path,
    prefix: &str,
) -> Result<Vec<(Uuid, PathBuf)>, std::io::Error> {
    let mut stores = Vec::new();
    for entry in std::fs::read_dir(parent)? {
        let entry = match entry {
            Ok(entry) => entry,
            Err(error) => {
                tracing::warn!(path = %parent.display(), error = %error, "skipped an unreadable entry while listing set-aside local stores");
                continue;
            }
        };
        let file_type = match entry.file_type() {
            Ok(file_type) => file_type,
            Err(error) => {
                tracing::warn!(path = %entry.path().display(), error = %error, "skipped an entry of unknown type while listing set-aside local stores");
                continue;
            }
        };
        // set_aside renames a directory, so files and symbolic links are someone else's.
        if !file_type.is_dir() {
            continue;
        }
        let file_name = entry.file_name();
        let Some(suffix) = file_name
            .to_str()
            .and_then(|name| name.strip_prefix(prefix))
        else {
            continue;
        };
        if let Some(id) = parse_set_aside_id(suffix) {
            stores.push((id, entry.path()));
        }
    }
    Ok(stores)
}

/// Returns the UUIDv7 that `set_aside` wrote as `suffix`, or `None` for any other text.
fn parse_set_aside_id(suffix: &str) -> Option<Uuid> {
    let Ok(id) = Uuid::try_parse(suffix) else {
        return None;
    };
    // try_parse also accepts simple, braced, and URN forms that set_aside never writes.
    let is_set_aside_form =
        id.get_version() == Some(Version::SortRand) && id.hyphenated().to_string() == suffix;
    is_set_aside_form.then_some(id)
}
