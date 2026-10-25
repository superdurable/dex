// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The object store: redb metadata plus one self-describing data file per object version.

mod bucket;
mod credential;
mod lifecycle;
mod maintenance;
mod multipart;
mod multipart_complete;
mod object_delete;
mod object_listing;
mod object_read;
mod object_write;

use std::fs;
use std::path::{Path, PathBuf};
use std::str::FromStr;
use std::sync::atomic::AtomicI64;
use std::sync::{Mutex, MutexGuard};

use redb::{
    Database, ReadOnlyDatabase, ReadableDatabase, ReadableTable, Table, TableDefinition,
    TableError, WriteTransaction,
};
use serde::Serialize;
use serde::de::DeserializeOwned;
use ulid::Ulid;

use crate::error::StoreError;
use crate::key_codec::{encode_object_key, prefix_successor};
use crate::record::{BucketRecord, VersionRecord};
use crate::unsynced_file_backend::UnsyncedFileBackend;

pub use credential::{CredentialRecord, CredentialStatus};
pub use lifecycle::LifecycleRun;
pub use maintenance::OrphanCollection;
pub use multipart::{
    CompletedPart, CreateMultipartUploadRequest, ListMultipartUploadsRequest, ListPartsRequest,
    MAX_PART_NUMBER, MultipartUploadsPage, PartsPage,
};
pub use multipart_complete::CompleteMultipartUploadRequest;
pub use object_delete::{DeleteObjectOutcome, DeleteObjectRequest};
pub use object_listing::MAX_KEYS_LIMIT;
pub use object_listing::{
    ListObjectVersionsPage, ListObjectVersionsRequest, ListObjectsPage, ListObjectsRequest,
    ListedObject, ListedVersion,
};
pub use object_read::{
    ByteRangeRequest, ObjectBody, ObjectReadRequest, ObjectSelection, ReadConditions, ResolvedRange,
};
pub use object_write::{
    CopyObjectOutcome, CopyObjectRequest, MetadataDirective, PutObjectOutcome, PutObjectRequest,
    WriteCondition,
};

/// Holds the format version; never change its name or types, since older versions read it to
/// refuse newer stores.
const STORE_FORMAT: TableDefinition<&str, u64> = TableDefinition::new("store_format");
const STORE_FORMAT_VERSION_KEY: &str = "version";
/// The metadata layout version this s3mock reads and writes; stores predating the version marker
/// are version 1.
pub const CURRENT_STORE_FORMAT_VERSION: u64 = 1;
const BUCKETS: TableDefinition<&str, &[u8]> = TableDefinition::new("buckets");
const CREDENTIALS: TableDefinition<&str, &[u8]> = TableDefinition::new("credentials");
/// Every version and delete marker, keyed by `encode_version_key`, newest first per key.
const VERSIONS: TableDefinition<&[u8], &[u8]> = TableDefinition::new("versions");
/// The current version of every key whose newest version is not a delete marker.
const LATEST: TableDefinition<&[u8], &[u8]> = TableDefinition::new("latest");
/// In-progress multipart uploads, keyed by `encode_upload_key`.
const UPLOADS: TableDefinition<&[u8], &[u8]> = TableDefinition::new("uploads");
/// Uploads aborted by AbortMultipartUpload, keyed like `UPLOADS`; S3 accepts a repeated abort.
const ABORTED_UPLOADS: TableDefinition<&[u8], &[u8]> = TableDefinition::new("aborted_uploads");
/// Uploaded parts, keyed by `encode_part_key`.
const PARTS: TableDefinition<&[u8], &[u8]> = TableDefinition::new("parts");
/// Bucket subdirectory holding one directory of part files per upload.
const UPLOADS_DIRECTORY_NAME: &str = "uploads";

const METADATA_FILE_NAME: &str = "metadata.redb";
const DATA_DIRECTORY_NAME: &str = "data";
const TEMPORARY_DIRECTORY_NAME: &str = "tmp";
const NULL_VERSION_ID: &str = "null";
/// Exists while a guarded relaxed session is open; only its durable close removes it.
const RELAXED_SESSION_MARKER_FILE_NAME: &str = "relaxed-session-open";
/// Shows that every guarded open of the directory wrote relaxed session markers.
const RELAXED_SESSION_MARKERS_STAMP_FILE_NAME: &str = "relaxed-session-markers";
/// Deferred removals that force a durable commit, bounding the disk that superseded files hold.
const DEFERRED_REMOVAL_LIMIT: usize = 256;

/// How much the store waits for stable storage before acknowledging a write.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum Durability {
    /// Every acknowledged write survives a crash: data files and metadata are flushed with fsync.
    #[default]
    Strict,
    /// Every acknowledged write reaches the files without fsync: it survives a process kill, not a host crash.
    ProcessCrashSafe,
    /// Writes skip fsync; a crash may lose acknowledged writes, so crash tests are invalid.
    Relaxed,
}

/// ETag representation assigned when an object version commits.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
#[non_exhaustive]
pub enum EtagMode {
    /// Derive ETags from object content.
    #[default]
    Md5,
    /// Assign a stable opaque ETag to each stored version.
    Opaque,
}

/// Settings fixed when a store is opened.
#[derive(Clone, Debug)]
pub struct StoreConfig {
    /// Directory holding `metadata.redb`, `data/`, and `tmp/`; created when missing.
    pub root_directory: PathBuf,
    pub durability: Durability,
    /// Default `Md5`; `Opaque` assigns a stable identifier to each committed version.
    pub etag_mode: EtagMode,
}

/// A local-disk object store with S3 semantics. Share it across threads with `Arc`.
///
/// Every method blocks on disk I/O; async callers should run them on a blocking thread.
pub struct Store {
    data_directory: PathBuf,
    temporary_directory: PathBuf,
    database: Database,
    durability: Durability,
    etag_mode: EtagMode,
    /// Monotonic so sequential writes to one key always order by request arrival.
    version_generator: Mutex<ulid::Generator>,
    /// Every later ULID is at least this old because the generator is monotonic, even if the clock steps back.
    opened_at_unix_ms: u64,
    /// Added to wall-clock time for lifecycle decisions only.
    lifecycle_clock_offset_ms: AtomicI64,
    /// Relaxed mode keeps superseded files until a durable commit, so a rollback finds them.
    deferred_removals: Mutex<DeferredRemovals>,
    /// Set by [`Store::open_refusing_unclean_relaxed_session`] in relaxed mode.
    relaxed_session_marker: Option<PathBuf>,
}

/// A data file or bucket directory whose metadata a commit dropped.
#[derive(Clone, Debug, PartialEq, Eq)]
enum SupersededPath {
    DataFile(PathBuf),
    BucketDirectory(PathBuf),
}

impl SupersededPath {
    fn path(&self) -> &Path {
        match self {
            SupersededPath::DataFile(path) | SupersededPath::BucketDirectory(path) => path,
        }
    }
}

/// Paths that relaxed commits superseded, each waiting for a durable commit after its own.
#[derive(Default)]
struct DeferredRemovals {
    next_sequence: u64,
    /// In increasing sequence order.
    paths: Vec<(u64, SupersededPath)>,
}

impl DeferredRemovals {
    fn defer(&mut self, paths: Vec<SupersededPath>) {
        for path in paths {
            self.paths.push((self.next_sequence, path));
            self.next_sequence += 1;
        }
    }

    /// Returns and forgets the paths deferred before `covered_sequence` was read.
    fn take_covered(&mut self, covered_sequence: u64) -> Vec<SupersededPath> {
        let covered = self
            .paths
            .partition_point(|(sequence, _)| *sequence < covered_sequence);
        self.paths.drain(..covered).map(|(_, path)| path).collect()
    }

    fn contains(&self, path: &Path) -> bool {
        self.paths
            .iter()
            .any(|(_, deferred)| deferred.path() == path)
    }
}

impl Store {
    /// Returns the store-owned directory for temporary request bodies.
    pub fn temporary_directory(&self) -> &std::path::Path {
        &self.temporary_directory
    }

    /// Opens or creates a store, removing temporary files left by an earlier process.
    ///
    /// Fails with [`StoreError::UnsupportedStoreFormatVersion`], leaving stored data unchanged,
    /// when the directory holds a store format version other than
    /// [`CURRENT_STORE_FORMAT_VERSION`].
    pub fn open(config: &StoreConfig) -> Result<Store, StoreError> {
        let data_directory = config.root_directory.join(DATA_DIRECTORY_NAME);
        let temporary_directory = config.root_directory.join(TEMPORARY_DIRECTORY_NAME);
        let is_durable = config.durability == Durability::Strict;
        create_directory_durably(&config.root_directory, is_durable)?;
        let metadata_path = config.root_directory.join(METADATA_FILE_NAME);
        // A writable open rewrites the redb header, so refuse unsupported stores read-only first.
        if metadata_path.try_exists()? {
            check_store_format_version_read_only(&metadata_path, &config.root_directory)?;
        }
        let database = match config.durability {
            Durability::ProcessCrashSafe => Database::builder()
                .create_with_backend(UnsyncedFileBackend::open(&metadata_path)?)?,
            Durability::Strict | Durability::Relaxed => Database::create(&metadata_path)?,
        };
        // redb flushes file contents, not the directory entry of a newly created database file.
        if is_durable {
            crate::data_file::sync_directory(&config.root_directory)?;
        }
        // Checked before `Store` exists, because its drop commits in relaxed mode.
        let is_format_version_stamped =
            check_store_format_version(&database, &config.root_directory)?;
        create_directory_durably(&data_directory, is_durable)?;
        create_directory_durably(&temporary_directory, is_durable)?;
        let mut version_generator = ulid::Generator::new();
        let opened_at = version_generator
            .generate()
            .expect("a fresh ULID generator cannot overflow");
        let store = Store {
            data_directory,
            temporary_directory,
            database,
            durability: config.durability,
            etag_mode: config.etag_mode,
            version_generator: Mutex::new(version_generator),
            opened_at_unix_ms: opened_at.timestamp_ms(),
            lifecycle_clock_offset_ms: AtomicI64::new(0),
            deferred_removals: Mutex::new(DeferredRemovals::default()),
            relaxed_session_marker: None,
        };
        store.create_tables(is_format_version_stamped)?;
        store.remove_temporary_files()?;
        Ok(store)
    }

    /// Opens like [`Store::open`], but refuses a store whose last relaxed session ended abruptly.
    ///
    /// Returns [`StoreError::UncleanRelaxedSession`], in any durability mode, while the marker of
    /// an earlier relaxed session opened this way remains. Only that session's durable close
    /// removes its marker, so a crash or kill leaves it behind. Returns
    /// [`StoreError::UncleanSessionWithoutRelaxedSessionMarkers`] for a store that no guarded open
    /// stamped and that was not closed cleanly, unless the configured mode is strict. The
    /// configuration is trusted then, because a strict store fsynced every earlier commit.
    pub fn open_refusing_unclean_relaxed_session(
        config: &StoreConfig,
    ) -> Result<Store, StoreError> {
        let root_directory = &config.root_directory;
        let marker_path = root_directory.join(RELAXED_SESSION_MARKER_FILE_NAME);
        let stamp_path = root_directory.join(RELAXED_SESSION_MARKERS_STAMP_FILE_NAME);
        // Refused before any `Store` exists, because its drop would make the rollback durable.
        if marker_path.try_exists()? {
            return Err(StoreError::UncleanRelaxedSession {
                root_directory: root_directory.clone(),
            });
        }
        let metadata_path = root_directory.join(METADATA_FILE_NAME);
        if !stamp_path.try_exists()? {
            if metadata_path.try_exists()? && was_closed_uncleanly(&metadata_path)? {
                // A strict configuration fsynced every earlier commit, so redb's repair loses none.
                if config.durability != Durability::Strict {
                    return Err(StoreError::UncleanSessionWithoutRelaxedSessionMarkers {
                        root_directory: root_directory.clone(),
                    });
                }
                tracing::info!(directory = %root_directory.display(), "accepting an unstamped strict store that was not closed cleanly, and stamping it");
            }
            create_directory_durably(root_directory, true)?;
            create_file_durably(&stamp_path, root_directory)?;
        }
        let is_relaxed = config.durability == Durability::Relaxed;
        if is_relaxed {
            create_file_durably(&marker_path, root_directory)?;
        }
        match Store::open(config) {
            Ok(mut store) => {
                store.relaxed_session_marker = is_relaxed.then_some(marker_path);
                Ok(store)
            }
            Err(error) => {
                // No write happened, so the marker records nothing to refuse.
                if is_relaxed && let Err(removal_error) = fs::remove_file(&marker_path) {
                    tracing::warn!(path = %marker_path.display(), %removal_error, "failed to remove the relaxed session marker after a failed open");
                }
                Err(error)
            }
        }
    }

    /// Creates missing tables and stamps the current format version on an unstamped store.
    fn create_tables(&self, is_format_version_stamped: bool) -> Result<(), StoreError> {
        let transaction = self.begin_write()?;
        transaction.open_table(BUCKETS)?;
        transaction.open_table(CREDENTIALS)?;
        transaction.open_table(VERSIONS)?;
        transaction.open_table(LATEST)?;
        transaction.open_table(UPLOADS)?;
        transaction.open_table(PARTS)?;
        transaction.open_table(ABORTED_UPLOADS)?;
        {
            let mut store_format = transaction.open_table(STORE_FORMAT)?;
            if !is_format_version_stamped {
                store_format.insert(STORE_FORMAT_VERSION_KEY, CURRENT_STORE_FORMAT_VERSION)?;
            }
        }
        transaction.commit()?;
        Ok(())
    }

    fn begin_write(&self) -> Result<WriteTransaction, StoreError> {
        let mut transaction = self.database.begin_write()?;
        transaction.set_durability(match self.durability {
            Durability::Strict | Durability::ProcessCrashSafe => redb::Durability::Immediate,
            Durability::Relaxed => redb::Durability::None,
        })?;
        Ok(transaction)
    }

    /// Returns whether writes fsync files and directories.
    fn is_durable(&self) -> bool {
        self.durability == Durability::Strict
    }

    /// Returns whether commits stay in memory until a durable commit.
    fn is_relaxed(&self) -> bool {
        self.durability == Durability::Relaxed
    }

    /// Begins a write that may supersede paths, first flushing deferred removals at their limit.
    ///
    /// Returns the flush error without writing, so a failing flush cannot grow the deferred list.
    fn begin_superseding_write(&self) -> Result<WriteTransaction, StoreError> {
        if self.is_relaxed() && self.lock_deferred_removals().paths.len() >= DEFERRED_REMOVAL_LIMIT
        {
            self.make_relaxed_commits_durable()?;
        }
        self.begin_write()
    }

    /// Commits `transaction`, then removes the data files whose metadata it dropped.
    fn commit_and_remove_superseded_files(
        &self,
        transaction: WriteTransaction,
        superseded_files: Vec<PathBuf>,
    ) -> Result<(), StoreError> {
        self.commit_and_remove_superseded_paths(
            transaction,
            superseded_files
                .into_iter()
                .map(SupersededPath::DataFile)
                .collect(),
        )
    }

    /// Commits `transaction`, then removes the paths whose metadata it dropped.
    ///
    /// Relaxed mode defers the removal until a durable commit covers this one.
    fn commit_and_remove_superseded_paths(
        &self,
        transaction: WriteTransaction,
        superseded_paths: Vec<SupersededPath>,
    ) -> Result<(), StoreError> {
        if !self.is_relaxed() || superseded_paths.is_empty() {
            transaction.commit()?;
            remove_superseded_paths(&superseded_paths);
            return Ok(());
        }
        // Held across the commit, so orphan collection sees each path referenced or deferred.
        let mut deferred_removals = self.lock_deferred_removals();
        transaction.commit()?;
        deferred_removals.defer(superseded_paths);
        Ok(())
    }

    /// Makes every earlier relaxed commit durable, then removes the paths they superseded.
    fn make_relaxed_commits_durable(&self) -> Result<(), StoreError> {
        let covered_sequence = self.lock_deferred_removals().next_sequence;
        // The default durability is immediate, which also persists every earlier relaxed commit.
        self.database.begin_write()?.commit()?;
        let removable = self.lock_deferred_removals().take_covered(covered_sequence);
        remove_superseded_paths(&removable);
        Ok(())
    }

    fn lock_deferred_removals(&self) -> MutexGuard<'_, DeferredRemovals> {
        self.deferred_removals
            .lock()
            .expect("no code panics while holding the deferred removal lock")
    }

    fn next_internal_version(&self) -> Ulid {
        self.version_generator
            .lock()
            .expect("the version generator lock is only held for infallible work")
            .generate()
            .expect("2^80 ULIDs within one millisecond cannot be exhausted")
    }

    fn bucket_directory(&self, bucket: &BucketRecord) -> PathBuf {
        self.data_directory.join(&bucket.incarnation)
    }

    fn upload_directory(&self, bucket: &BucketRecord, upload_id: &str) -> PathBuf {
        self.bucket_directory(bucket)
            .join(UPLOADS_DIRECTORY_NAME)
            .join(upload_id)
    }

    fn data_file_path(&self, bucket: &BucketRecord, record: &VersionRecord) -> Option<PathBuf> {
        record
            .data_file
            .as_ref()
            .map(|relative| self.bucket_directory(bucket).join(relative))
    }
}

impl Drop for Store {
    /// Relaxed commits reach disk only after a durable commit, so flush one on close.
    fn drop(&mut self) {
        if !self.is_relaxed() {
            return;
        }
        if let Err(error) = self.make_relaxed_commits_durable() {
            tracing::warn!(%error, "failed to flush relaxed metadata commits on close");
            return;
        }
        if let Some(marker_path) = &self.relaxed_session_marker {
            remove_relaxed_session_marker(marker_path);
        }
    }
}

/// Removes the marker durably, so a host crash after a clean close cannot restore it.
fn remove_relaxed_session_marker(marker_path: &Path) {
    let removed = fs::remove_file(marker_path)
        .and_then(|()| crate::data_file::sync_directory(&parent_directory(marker_path)));
    if let Err(error) = removed {
        tracing::warn!(path = %marker_path.display(), %error, "failed to remove the relaxed session marker after a durable close");
    }
}

/// Returns whether redb needs a repair, which means the last process did not close it.
fn was_closed_uncleanly(metadata_path: &Path) -> Result<bool, StoreError> {
    match ReadOnlyDatabase::open(metadata_path) {
        Ok(_) => Ok(false),
        Err(redb::DatabaseError::RepairAborted) => Ok(true),
        Err(error) => Err(error.into()),
    }
}

/// Creates an empty file and flushes it and its directory entry.
fn create_file_durably(path: &Path, directory: &Path) -> Result<(), StoreError> {
    fs::File::create(path)?.sync_all()?;
    crate::data_file::sync_directory(directory)?;
    Ok(())
}

/// Checks the format version without writing, when redb can open the file read-only.
///
/// Returns [`StoreError::UnsupportedStoreFormatVersion`] for an unsupported recorded version, and
/// `Ok(())` otherwise, including when only a writable open can read the file.
fn check_store_format_version_read_only(
    metadata_path: &Path,
    root_directory: &Path,
) -> Result<(), StoreError> {
    match ReadOnlyDatabase::open(metadata_path) {
        Ok(database) => check_store_format_version(&database, root_directory).map(|_| ()),
        Err(error) => {
            // Repair after a crash needs a writable open; the check after it still refuses.
            tracing::debug!(%error, "deferring the store format check to the writable open");
            Ok(())
        }
    }
}

/// Returns whether the store already records the current format version.
///
/// Returns `Ok(false)` for a new store or one written before format versions existed; both use
/// version 1. Returns [`StoreError::UnsupportedStoreFormatVersion`] for any other recorded version.
fn check_store_format_version(
    database: &impl ReadableDatabase,
    root_directory: &Path,
) -> Result<bool, StoreError> {
    let transaction = database.begin_read()?;
    let store_format = match transaction.open_table(STORE_FORMAT) {
        Ok(table) => table,
        Err(TableError::TableDoesNotExist(_)) => return Ok(false),
        Err(error) => return Err(error.into()),
    };
    match store_format.get(STORE_FORMAT_VERSION_KEY)? {
        None => Ok(false),
        Some(version) if version.value() == CURRENT_STORE_FORMAT_VERSION => Ok(true),
        Some(version) => Err(StoreError::UnsupportedStoreFormatVersion {
            root_directory: root_directory.to_path_buf(),
            found: version.value(),
            supported: CURRENT_STORE_FORMAT_VERSION,
        }),
    }
}

/// Relative data file path for a version; the random suffix spreads files across shard directories.
fn relative_data_file(internal_version: Ulid) -> String {
    let name = internal_version.to_string();
    let shard = &name[name.len() - 2..];
    format!("{shard}/{name}")
}

/// Parses a client-supplied version ID: `None` means the null version.
fn parse_version_id(version_id: &str) -> Result<Option<Ulid>, StoreError> {
    if version_id == NULL_VERSION_ID {
        return Ok(None);
    }
    Ulid::from_str(version_id)
        .map(Some)
        .map_err(|_| StoreError::InvalidArgument(format!("invalid version id {version_id:?}")))
}

fn validate_object_key(key: &str) -> Result<(), StoreError> {
    if key.is_empty() {
        return Err(StoreError::InvalidObjectKey("the key is empty".to_owned()));
    }
    if key.len() > 1024 {
        return Err(StoreError::InvalidObjectKey(
            "the key is longer than 1024 bytes".to_owned(),
        ));
    }
    Ok(())
}

fn encode_json<T: Serialize>(value: &T) -> Result<Vec<u8>, StoreError> {
    Ok(serde_json::to_vec(value)?)
}

fn decode_json<T: DeserializeOwned>(bytes: &[u8]) -> Result<T, StoreError> {
    Ok(serde_json::from_slice(bytes)?)
}

fn require_bucket(
    table: &impl ReadableTable<&'static str, &'static [u8]>,
    bucket: &str,
) -> Result<BucketRecord, StoreError> {
    match table.get(bucket)? {
        Some(value) => decode_json(value.value()),
        None => Err(StoreError::NoSuchBucket(bucket.to_owned())),
    }
}

/// One stored version together with its `versions` table key.
struct StoredVersion {
    version_key: Vec<u8>,
    record: VersionRecord,
}

/// Loads every version of one key, newest first.
fn load_versions(
    versions: &impl ReadableTable<&'static [u8], &'static [u8]>,
    bucket: &str,
    key: &str,
) -> Result<Vec<StoredVersion>, StoreError> {
    let object_key = encode_object_key(bucket, key);
    let end = prefix_successor(&object_key)
        .expect("encoded object keys end with a terminator below 0xFF");
    let mut loaded = Vec::new();
    for entry in versions.range(object_key.as_slice()..end.as_slice())? {
        let (version_key, value) = entry?;
        loaded.push(StoredVersion {
            version_key: version_key.value().to_vec(),
            record: decode_json(value.value())?,
        });
    }
    Ok(loaded)
}

/// Finds the version a client addressed by version ID; `None` selects the null version.
fn find_version(
    versions: Vec<StoredVersion>,
    internal_version: Option<Ulid>,
) -> Option<StoredVersion> {
    versions.into_iter().find(|stored| match internal_version {
        None => stored.record.is_null_version,
        Some(ulid) => {
            !stored.record.is_null_version && stored.record.internal_version == ulid.to_string()
        }
    })
}

/// Rewrites the `latest` entry of one key from its newest remaining version.
fn refresh_latest(
    latest: &mut Table<&'static [u8], &'static [u8]>,
    versions: &Table<&'static [u8], &'static [u8]>,
    bucket: &str,
    key: &str,
) -> Result<(), StoreError> {
    let object_key = encode_object_key(bucket, key);
    let newest = load_versions(versions, bucket, key)?.into_iter().next();
    match newest {
        Some(stored) if !stored.record.is_delete_marker => {
            latest.insert(
                object_key.as_slice(),
                encode_json(&stored.record)?.as_slice(),
            )?;
        }
        Some(_) | None => {
            latest.remove(object_key.as_slice())?;
        }
    }
    Ok(())
}

/// Removes paths whose metadata a committed transaction already dropped.
fn remove_superseded_paths(paths: &[SupersededPath]) {
    for superseded in paths {
        let removed = match superseded {
            SupersededPath::DataFile(path) => fs::remove_file(path),
            SupersededPath::BucketDirectory(path) => match fs::remove_dir_all(path) {
                Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(()),
                other => other,
            },
        };
        if let Err(error) = removed {
            tracing::warn!(path = %superseded.path().display(), %error, "failed to remove a superseded path");
        }
    }
}

/// Creates `directory` and any missing ancestors, flushing each new entry into its parent when durable.
fn create_directory_durably(directory: &Path, is_durable: bool) -> Result<(), StoreError> {
    let mut missing = Vec::new();
    let mut current = directory;
    while !current.is_dir() {
        missing.push(current);
        match current.parent() {
            Some(parent) if !parent.as_os_str().is_empty() => current = parent,
            Some(_) | None => break,
        }
    }
    if missing.is_empty() {
        return Ok(());
    }
    fs::create_dir_all(directory)?;
    if is_durable {
        for created in missing.iter().rev() {
            crate::data_file::sync_directory(&parent_directory(created))?;
        }
    }
    Ok(())
}

/// Returns the directory that holds `path`'s entry; a bare relative name lives in `.`.
fn parent_directory(path: &Path) -> PathBuf {
    match path.parent() {
        Some(parent) if !parent.as_os_str().is_empty() => parent.to_path_buf(),
        Some(_) | None => PathBuf::from("."),
    }
}
