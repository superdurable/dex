// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::fs::{self, File, OpenOptions, TryLockError};
use std::io;
use std::net::SocketAddr;
use std::path::Path;
use std::sync::Arc;

use s3mock::{Durability, EtagMode, Store, StoreConfig, StoreError};
use s3mock_server::{DurabilityFlag, ServerConfig, ServerHandle};
use tokio::net::TcpListener;

use crate::ServerError;
use crate::config::{DevConfig, DevDurabilityMode};

/// Lock file under the dev data directory; its holder is the only process serving that directory.
const DATA_DIRECTORY_LOCK_FILE_NAME: &str = "dev.lock";

/// The in-process S3 emulator of `dex-server-zd dev`.
///
/// The engine uses the production S3 client over HTTP to reach it.
pub(crate) struct EmbeddedS3 {
    server: ServerHandle,
    data_directory_lock: DataDirectoryLock,
}

/// Whether this process proved that it alone serves the dev data directory.
enum DataDirectoryLock {
    /// The exclusive lock on the lock file, held until the emulator is dropped.
    Held { _lock_file: File },
    /// The filesystem cannot lock files, so another process might serve the same directory.
    Unsupported,
}

/// The outcome of one non-blocking attempt to lock the data directory lock file.
#[derive(Debug, PartialEq, Eq)]
enum LockAttemptOutcome {
    Acquired,
    HeldByAnotherProcess,
    Unsupported,
}

impl EmbeddedS3 {
    /// Locks the dev data directory, opens the emulator's data under `dev.s3DataDirectory`,
    /// creates `bucket`, and serves it on a free loopback port.
    ///
    /// Fails when another process holds the data directory lock.
    pub(crate) async fn start(config: &DevConfig, bucket: &str) -> Result<Self, ServerError> {
        let data_directory = config.data_directory.clone();
        let data_directory_lock =
            tokio::task::spawn_blocking(move || lock_data_directory(&data_directory))
                .await
                .map_err(|source| ServerError::EmbeddedS3 {
                    directory: config.data_directory.clone(),
                    detail: source.to_string(),
                })??;
        if matches!(data_directory_lock, DataDirectoryLock::Unsupported) {
            tracing::warn!(
                data_directory = %config.data_directory.display(),
                "the dev data directory cannot be locked; a restart waits for the previous node lease to expire"
            );
        }
        let durability = match config.durability_mode {
            DevDurabilityMode::Strict => Durability::Strict,
            DevDurabilityMode::Relaxed => Durability::Relaxed,
        };
        let store_config = StoreConfig {
            root_directory: config.s3_data_directory.clone(),
            durability,
            etag_mode: EtagMode::Md5,
        };
        let bucket = bucket.to_owned();
        let directory = config.s3_data_directory.clone();
        let store = tokio::task::spawn_blocking(move || open_store(&store_config, &bucket))
            .await
            .map_err(|source| ServerError::EmbeddedS3 {
                directory: directory.clone(),
                detail: source.to_string(),
            })?
            .map_err(|source| ServerError::EmbeddedS3 {
                directory: directory.clone(),
                detail: store_open_failure_detail(source, &config.data_directory),
            })?;
        let listener = TcpListener::bind(SocketAddr::from(([127, 0, 0, 1], 0)))
            .await
            .map_err(|source| ServerError::EmbeddedS3 {
                directory: directory.clone(),
                detail: source.to_string(),
            })?;
        let server_config = ServerConfig {
            data_directory: directory.clone(),
            durability: match durability {
                Durability::Strict => DurabilityFlag::Strict,
                Durability::Relaxed => DurabilityFlag::Relaxed,
            },
            ..ServerConfig::default()
        };
        let server = s3mock_server::start_with_config(Arc::new(store), listener, server_config)
            .map_err(|source| ServerError::EmbeddedS3 {
                directory: directory.clone(),
                detail: source.to_string(),
            })?;
        tracing::info!(address = %server.local_address(), directory = %directory.display(), "embedded S3 emulator serving");
        Ok(Self {
            server,
            data_directory_lock,
        })
    }

    /// Returns whether this process holds the data directory lock.
    ///
    /// When true, no other process can write the embedded object store, so a node lease that it
    /// still shows as held belongs to a stopped process.
    pub(crate) fn has_exclusive_data_directory(&self) -> bool {
        matches!(self.data_directory_lock, DataDirectoryLock::Held { .. })
    }

    /// Returns the HTTP endpoint of the emulator.
    pub(crate) fn endpoint(&self) -> String {
        format!("http://{}", self.server.local_address())
    }

    /// Stops accepting requests and waits for in-flight requests.
    ///
    /// Then it waits while the emulator's store flushes relaxed commits to disk.
    pub(crate) async fn shutdown(self) {
        self.server.shutdown().await;
    }
}

/// Returns the open failure as text; a newer store format names the dev remedy for the whole data directory.
fn store_open_failure_detail(error: StoreError, data_directory: &Path) -> String {
    match error {
        StoreError::UnsupportedStoreFormatVersion {
            found, supported, ..
        } => format!(
            "the embedded object store holds store format version {found}, but this dex-server-zd \
             supports only version {supported}; upgrade dex-server-zd, or remove the whole data \
             directory {} to start empty",
            data_directory.display()
        ),
        other => other.to_string(),
    }
}

/// Creates the data directory and takes a non-blocking exclusive lock on its lock file.
///
/// Returns [`DataDirectoryLock::Unsupported`] when the filesystem cannot lock files, and fails
/// when another process holds the lock or the file cannot be opened.
fn lock_data_directory(data_directory: &Path) -> Result<DataDirectoryLock, ServerError> {
    let lock_path = data_directory.join(DATA_DIRECTORY_LOCK_FILE_NAME);
    let lock_failure = |detail: String| ServerError::EmbeddedS3 {
        directory: data_directory.to_path_buf(),
        detail,
    };
    fs::create_dir_all(data_directory)
        .map_err(|error| lock_failure(format!("creating the data directory: {error}")))?;
    let lock_file = OpenOptions::new()
        .create(true)
        .truncate(false)
        .write(true)
        .open(&lock_path)
        .map_err(|error| lock_failure(format!("opening {}: {error}", lock_path.display())))?;
    match classify_lock_attempt(lock_file.try_lock())
        .map_err(|error| lock_failure(format!("locking {}: {error}", lock_path.display())))?
    {
        LockAttemptOutcome::Acquired => Ok(DataDirectoryLock::Held {
            _lock_file: lock_file,
        }),
        LockAttemptOutcome::Unsupported => Ok(DataDirectoryLock::Unsupported),
        LockAttemptOutcome::HeldByAnotherProcess => Err(lock_failure(format!(
            "another dex-server-zd dev process holds {}; stop it before starting another on this \
             data directory",
            lock_path.display()
        ))),
    }
}

/// Returns the lock attempt outcome, or the I/O error that is neither contention nor lack of support.
fn classify_lock_attempt(
    attempt: Result<(), TryLockError>,
) -> Result<LockAttemptOutcome, io::Error> {
    match attempt {
        Ok(()) => Ok(LockAttemptOutcome::Acquired),
        Err(TryLockError::WouldBlock) => Ok(LockAttemptOutcome::HeldByAnotherProcess),
        Err(TryLockError::Error(error)) if is_lock_unsupported_error(&error) => {
            Ok(LockAttemptOutcome::Unsupported)
        }
        Err(TryLockError::Error(error)) => Err(error),
    }
}

/// Returns whether `error` means that the filesystem does not support file locks.
fn is_lock_unsupported_error(error: &io::Error) -> bool {
    let unsupported_codes = [
        rustix::io::Errno::NOLCK,
        rustix::io::Errno::NOTSUP,
        rustix::io::Errno::OPNOTSUPP,
        rustix::io::Errno::NOSYS,
    ]
    .map(rustix::io::Errno::raw_os_error);
    error.kind() == io::ErrorKind::Unsupported
        || error
            .raw_os_error()
            .is_some_and(|code| unsupported_codes.contains(&code))
}

fn open_store(config: &StoreConfig, bucket: &str) -> Result<Store, StoreError> {
    let store = Store::open(config)?;
    match store.create_bucket(bucket, false) {
        Ok(_) | Err(StoreError::BucketAlreadyOwnedByYou(_)) => Ok(store),
        Err(error) => Err(error),
    }
}

#[cfg(test)]
mod tests {
    use std::fs::TryLockError;
    use std::io;
    use std::path::{Path, PathBuf};

    use s3mock::StoreError;

    use super::{
        DataDirectoryLock, LockAttemptOutcome, classify_lock_attempt, lock_data_directory,
        store_open_failure_detail,
    };

    #[test]
    fn a_held_data_directory_lock_fails_a_second_locker_until_it_is_released() {
        let directory = tempfile::tempdir().expect("test directory");
        let data_directory = directory.path().join("data");
        let first = lock_data_directory(&data_directory).expect("the first lock succeeds");
        assert!(matches!(first, DataDirectoryLock::Held { .. }));
        let Err(second) = lock_data_directory(&data_directory) else {
            panic!("a second lock on a held data directory must fail");
        };
        assert!(
            second
                .to_string()
                .contains("another dex-server-zd dev process holds"),
            "{second}"
        );
        drop(first);
        assert!(matches!(
            lock_data_directory(&data_directory).expect("the released lock is taken again"),
            DataDirectoryLock::Held { .. }
        ));
    }

    #[test]
    fn filesystems_without_locks_fall_back_and_other_lock_errors_fail() {
        assert_eq!(
            classify_lock_attempt(Ok(())).expect("acquired"),
            LockAttemptOutcome::Acquired
        );
        assert_eq!(
            classify_lock_attempt(Err(TryLockError::WouldBlock)).expect("contended"),
            LockAttemptOutcome::HeldByAnotherProcess
        );
        let unsupported_errors = [
            io::Error::from(io::ErrorKind::Unsupported),
            io::Error::from_raw_os_error(rustix::io::Errno::NOLCK.raw_os_error()),
            io::Error::from_raw_os_error(rustix::io::Errno::NOTSUP.raw_os_error()),
        ];
        for error in unsupported_errors {
            assert_eq!(
                classify_lock_attempt(Err(TryLockError::Error(error))).expect("unsupported"),
                LockAttemptOutcome::Unsupported
            );
        }
        let denied = io::Error::from(io::ErrorKind::PermissionDenied);
        assert!(classify_lock_attempt(Err(TryLockError::Error(denied))).is_err());
    }

    #[test]
    fn unsupported_store_format_names_the_whole_data_directory() {
        let detail = store_open_failure_detail(
            StoreError::UnsupportedStoreFormatVersion {
                root_directory: PathBuf::from("/data/s3"),
                found: 2,
                supported: 1,
            },
            Path::new("/data"),
        );
        assert!(detail.contains("upgrade dex-server-zd"), "{detail}");
        assert!(detail.contains("whole data directory /data "), "{detail}");
        assert!(!detail.contains("/data/s3"), "{detail}");
    }
}
