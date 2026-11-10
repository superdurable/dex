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

use s3local::{Durability, EtagMode, Store, StoreConfig, StoreError};
use s3local_server::{DurabilityFlag, ServerConfig, ServerHandle};
use tokio::net::TcpListener;

use crate::ServerError;
use crate::config::{DevConfig, DevDurabilityMode};

/// Lock file under the dev or local data directory; its holder is the only process serving that directory.
const DATA_DIRECTORY_LOCK_FILE_NAME: &str = "dev.lock";

/// The in-process S3 emulator of `dex-server-zd dev` and `dex-server-zd local`.
///
/// The engine uses the production S3 client over HTTP to reach it.
pub(crate) struct EmbeddedS3 {
    server: ServerHandle,
    /// The emulator of `dev.application_s3`, with its own store and address.
    application_server: Option<ServerHandle>,
    data_directory_lock: DataDirectoryLock,
    #[cfg(feature = "test-util")]
    store_for_test: Arc<Store>,
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
    /// With `dev.application_s3`, it also serves the application bucket from its own store and
    /// address. Fails when another process holds the data directory lock.
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
                "the data directory cannot be locked; a restart waits for the previous node lease to expire"
            );
        }
        let (server, store) = start_emulator(
            config,
            &config.s3_data_directory,
            bucket,
            SocketAddr::from(([127, 0, 0, 1], 0)),
        )
        .await?;
        tracing::info!(address = %server.local_address(), directory = %config.s3_data_directory.display(), "embedded S3 emulator serving");
        let application_server = match &config.application_s3 {
            None => None,
            Some(application) => match start_emulator(
                config,
                &application.data_directory,
                &application.bucket,
                application.listen,
            )
            .await
            {
                Ok((application_server, application_store)) => {
                    // Only the server keeps the store, so its shutdown closes it.
                    drop(application_store);
                    tracing::info!(
                        address = %application_server.local_address(),
                        bucket = %application.bucket,
                        directory = %application.data_directory.display(),
                        "application S3 bucket serving"
                    );
                    Some(application_server)
                }
                Err(error) => {
                    server.shutdown().await;
                    return Err(error);
                }
            },
        };
        // Only the server keeps the store outside tests, so its shutdown closes it.
        #[cfg(not(feature = "test-util"))]
        drop(store);
        Ok(Self {
            server,
            application_server,
            data_directory_lock,
            #[cfg(feature = "test-util")]
            store_for_test: store,
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

    #[cfg(feature = "test-util")]
    pub(crate) fn store_for_test(&self) -> Arc<Store> {
        self.store_for_test.clone()
    }

    /// Returns the HTTP endpoint of the application bucket, or none when it is not served.
    #[cfg(test)]
    fn application_endpoint(&self) -> Option<String> {
        self.application_server
            .as_ref()
            .map(|server| format!("http://{}", server.local_address()))
    }

    /// Stops both emulators at once: each stops accepting, waits for in-flight requests, and
    /// closes its store, which flushes relaxed commits to disk.
    ///
    /// The data directory lock is released only after both stores close. Dropping this future,
    /// such as at the end of a shutdown budget, does not cancel that work.
    pub(crate) async fn shutdown(self) {
        let Self {
            server,
            application_server,
            data_directory_lock,
            #[cfg(feature = "test-util")]
            store_for_test,
        } = self;
        // The server's accept loop then holds the last reference, so it closes the store.
        #[cfg(feature = "test-util")]
        drop(store_for_test);
        // A spawned task keeps closing, and keeps the lock, after a timed-out caller drops this future.
        let closing = tokio::spawn(close_emulators_then_unlock(
            server,
            application_server,
            data_directory_lock,
        ));
        if let Err(error) = closing.await {
            tracing::error!(%error, "closing the embedded S3 emulators failed");
        }
    }
}

/// Shuts both emulators down concurrently, then releases the data directory lock.
async fn close_emulators_then_unlock(
    server: ServerHandle,
    application_server: Option<ServerHandle>,
    data_directory_lock: DataDirectoryLock,
) {
    let application_closed = async {
        if let Some(application_server) = application_server {
            application_server.shutdown().await;
        }
    };
    tokio::join!(server.shutdown(), application_closed);
    drop(data_directory_lock);
}

/// Opens the store under `root_directory`, creates `bucket`, and serves it on `listen`.
///
/// Returns the running server and its store. Errors name `root_directory`.
async fn start_emulator(
    config: &DevConfig,
    root_directory: &Path,
    bucket: &str,
    listen: SocketAddr,
) -> Result<(ServerHandle, Arc<Store>), ServerError> {
    let durability = match config.durability_mode {
        DevDurabilityMode::Strict => Durability::Strict,
        DevDurabilityMode::ProcessCrashSafe => Durability::ProcessCrashSafe,
        DevDurabilityMode::Relaxed => Durability::Relaxed,
    };
    let store_config = StoreConfig {
        root_directory: root_directory.to_path_buf(),
        durability,
        etag_mode: EtagMode::Md5,
    };
    let emulator_failure = |detail: String| ServerError::EmbeddedS3 {
        directory: root_directory.to_path_buf(),
        detail,
    };
    let owned_bucket = bucket.to_owned();
    let store = tokio::task::spawn_blocking(move || open_store(&store_config, &owned_bucket))
        .await
        .map_err(|source| emulator_failure(source.to_string()))?
        .map_err(|source| {
            emulator_failure(store_open_failure_detail(source, &config.data_directory))
        })?;
    let listener = TcpListener::bind(listen)
        .await
        .map_err(|source| emulator_failure(format!("binding {listen}: {source}")))?;
    let server_config = ServerConfig {
        data_directory: root_directory.to_path_buf(),
        durability: match durability {
            Durability::Strict => DurabilityFlag::Strict,
            Durability::ProcessCrashSafe => DurabilityFlag::ProcessCrashSafe,
            Durability::Relaxed => DurabilityFlag::Relaxed,
        },
        ..ServerConfig::default()
    };
    let store = Arc::new(store);
    let server = s3local_server::start_with_config(store.clone(), listener, server_config)
        .map_err(|source| emulator_failure(source.to_string()))?;
    Ok((server, store))
}

/// Returns the open failure as text; a newer store format names the dev remedy for the whole data directory.
fn store_open_failure_detail(error: StoreError, data_directory: &Path) -> String {
    match error {
        // The local store kept state that the rolled-back object store lost, so both must go.
        StoreError::UncleanRelaxedSession { .. } => format!(
            "the previous dev or local run in relaxed durability mode did not stop cleanly, so the \
             embedded object store lost recent writes; delete the whole data directory {} to start \
             empty, and use the default strict or the processCrashSafe durability mode to survive \
             abrupt exits",
            data_directory.display()
        ),
        StoreError::UncleanSessionWithoutRelaxedSessionMarkers { .. } => format!(
            "the previous dev run, from a dex-server-zd release that defaulted to relaxed \
             durability, did not stop cleanly, so the embedded object store may have lost recent \
             writes; delete the whole data directory {} to start empty",
            data_directory.display()
        ),
        StoreError::DirectoryEntryFlushFailed { directory, source } => format!(
            "strict durability cannot flush the directory entries in {}: {source}; fix the \
             permissions, or choose --durability process-crash-safe (dev.durabilityMode: \
             processCrashSafe), which does not flush them",
            directory.display()
        ),
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
            "another dex-server-zd dev or local process holds {}; stop it before starting another \
             on this data directory",
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
    let store = Store::open_refusing_unclean_relaxed_session(config)?;
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

    use std::net::SocketAddr;
    use std::time::Duration;

    use s3local::{Durability, EtagMode, Store, StoreConfig, StoreError};
    use tokio::io::{AsyncReadExt, AsyncWriteExt};
    use tokio::net::TcpStream;

    use super::{
        DataDirectoryLock, EmbeddedS3, LockAttemptOutcome, classify_lock_attempt,
        lock_data_directory, store_open_failure_detail,
    };
    use crate::config::{ApplicationS3Config, DevConfig, DevDurabilityMode};

    const SERVER_BUCKET: &str = "dex-dev";
    const APPLICATION_BUCKET: &str = "dex-dev-media";

    fn dev_config_with_application_bucket(data_directory: &Path) -> DevConfig {
        DevConfig {
            data_directory: data_directory.to_path_buf(),
            s3_data_directory: data_directory.join("s3"),
            durability_mode: DevDurabilityMode::ProcessCrashSafe,
            log_directory: None,
            application_s3: Some(ApplicationS3Config {
                listen: SocketAddr::from(([127, 0, 0, 1], 0)),
                bucket: APPLICATION_BUCKET.to_owned(),
                data_directory: data_directory.join("application-s3"),
            }),
        }
    }

    /// Sends one unsigned HTTP/1.1 request and returns the status code and body.
    async fn send_http_request(
        endpoint: &str,
        method: &str,
        path: &str,
        body: &[u8],
    ) -> (u16, Vec<u8>) {
        let address = endpoint.trim_start_matches("http://");
        let mut stream = TcpStream::connect(address).await.expect("connect");
        let head = format!(
            "{method} {path} HTTP/1.1\r\nHost: {address}\r\nContent-Length: {}\r\nConnection: close\r\n\r\n",
            body.len()
        );
        stream.write_all(head.as_bytes()).await.expect("write head");
        stream.write_all(body).await.expect("write body");
        let mut response = Vec::new();
        stream
            .read_to_end(&mut response)
            .await
            .expect("read response");
        let header_end = response
            .windows(4)
            .position(|window| window == b"\r\n\r\n")
            .expect("response headers end");
        let status_line = String::from_utf8_lossy(&response[..header_end]).into_owned();
        let status = status_line
            .split_whitespace()
            .nth(1)
            .and_then(|code| code.parse().ok())
            .expect("status code");
        (status, response[header_end + 4..].to_vec())
    }

    #[tokio::test]
    async fn the_application_bucket_is_separate_from_the_server_bucket_and_survives_a_restart() {
        let directory = tempfile::tempdir().expect("test directory");
        let config = dev_config_with_application_bucket(&directory.path().join("data"));
        let emulator = EmbeddedS3::start(&config, SERVER_BUCKET)
            .await
            .expect("the emulators start");
        let application_endpoint = emulator
            .application_endpoint()
            .expect("the application bucket is served");
        assert_ne!(application_endpoint, emulator.endpoint());
        let object_path = format!("/{APPLICATION_BUCKET}/agents/sha256/abc");
        let (status, _) =
            send_http_request(&application_endpoint, "PUT", &object_path, b"image bytes").await;
        assert_eq!(status, 200);
        let (status, _) = send_http_request(
            &application_endpoint,
            "PUT",
            &format!("/{SERVER_BUCKET}/written-by-an-application"),
            b"not server data",
        )
        .await;
        assert_eq!(
            status, 404,
            "the application endpoint must not serve the server bucket"
        );
        let (status, _) = send_http_request(&emulator.endpoint(), "GET", &object_path, b"").await;
        assert_eq!(
            status, 404,
            "the server endpoint must not serve the application bucket"
        );
        emulator.shutdown().await;

        let restarted = EmbeddedS3::start(&config, SERVER_BUCKET)
            .await
            .expect("the emulators restart");
        let application_endpoint = restarted
            .application_endpoint()
            .expect("the application bucket is served again");
        let (status, body) =
            send_http_request(&application_endpoint, "GET", &object_path, b"").await;
        assert_eq!(status, 200);
        assert_eq!(body, b"image bytes");
        restarted.shutdown().await;
    }

    /// Retries a blocking `attempt` until it succeeds; real threads advance while paused time stands still.
    async fn retry_blocking_until_ok<T: Send + 'static, E: Send + 'static>(
        attempt: impl Fn() -> Result<T, E> + Clone + Send + 'static,
    ) -> Option<T> {
        for _ in 0..100_000 {
            let attempt = attempt.clone();
            if let Ok(value) = tokio::task::spawn_blocking(attempt).await.expect("attempt") {
                return Some(value);
            }
        }
        None
    }

    #[tokio::test(start_paused = true)]
    async fn an_abandoned_shutdown_closes_the_server_store_and_keeps_the_lock_until_both_close() {
        let directory = tempfile::tempdir().expect("test directory");
        let data_directory = directory.path().join("data");
        let mut config = dev_config_with_application_bucket(&data_directory);
        // Strict stores lock their metadata file, so a reopen succeeds only after a close.
        config.durability_mode = DevDurabilityMode::Strict;
        let emulator = EmbeddedS3::start(&config, SERVER_BUCKET)
            .await
            .expect("the emulators start");
        let application_endpoint = emulator
            .application_endpoint()
            .expect("the application bucket is served");
        let address = application_endpoint
            .trim_start_matches("http://")
            .to_owned();
        let mut slow_request = TcpStream::connect(&address).await.expect("connect");
        let head = format!(
            "PUT /{APPLICATION_BUCKET}/agents/sha256/slow HTTP/1.1\r\nHost: {address}\r\nContent-Length: 100\r\n\r\npartial"
        );
        slow_request
            .write_all(head.as_bytes())
            .await
            .expect("write a partial request");
        let upload_directory = data_directory.join("application-s3").join("tmp");
        let has_upload_started = retry_blocking_until_ok(move || {
            let entries = std::fs::read_dir(&upload_directory).map_err(drop)?;
            if entries.count() > 0 { Ok(()) } else { Err(()) }
        })
        .await;
        assert!(has_upload_started.is_some(), "the upload is in flight");

        let caller_budget = Duration::from_secs(1);
        let (abandoned, ()) = tokio::join!(
            tokio::time::timeout(caller_budget, emulator.shutdown()),
            tokio::time::advance(caller_budget),
        );
        assert!(
            abandoned.is_err(),
            "the in-flight upload outlasts the caller's budget"
        );
        let server_store_config = StoreConfig {
            root_directory: config.s3_data_directory.clone(),
            durability: Durability::Strict,
            etag_mode: EtagMode::Md5,
        };
        let reopened =
            retry_blocking_until_ok(move || Store::open(&server_store_config).map(drop)).await;
        assert!(
            reopened.is_some(),
            "the server store closes although the caller abandoned shutdown"
        );
        let lock_attempt = tokio::task::spawn_blocking({
            let data_directory = data_directory.clone();
            move || lock_data_directory(&data_directory)
        })
        .await
        .expect("lock attempt");
        assert!(
            lock_attempt.is_err(),
            "the lock stays held while the application store has an upload in flight"
        );

        drop(slow_request);
        let released_lock =
            retry_blocking_until_ok(move || lock_data_directory(&data_directory)).await;
        assert!(
            released_lock.is_some(),
            "the lock is released once the application store closes"
        );
    }

    #[tokio::test]
    async fn an_occupied_application_address_fails_the_start_and_names_the_address() {
        let directory = tempfile::tempdir().expect("test directory");
        let occupied = std::net::TcpListener::bind("127.0.0.1:0").expect("occupy a port");
        let mut config = dev_config_with_application_bucket(&directory.path().join("data"));
        let occupied_address = occupied.local_addr().expect("occupied address");
        config
            .application_s3
            .as_mut()
            .expect("the application bucket is configured")
            .listen = occupied_address;
        let Err(error) = EmbeddedS3::start(&config, SERVER_BUCKET).await else {
            panic!("an occupied application address must fail the start");
        };
        assert!(
            error.to_string().contains(&occupied_address.to_string()),
            "{error}"
        );
        assert!(error.to_string().contains("application-s3"), "{error}");
    }

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
                .contains("another dex-server-zd dev or local process holds"),
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
    fn an_unflushable_directory_names_the_process_crash_safe_choice() {
        let detail = store_open_failure_detail(
            StoreError::DirectoryEntryFlushFailed {
                directory: PathBuf::from("/persist"),
                source: io::Error::from(io::ErrorKind::PermissionDenied),
            },
            Path::new("/persist/new-dev"),
        );
        assert!(detail.contains("/persist"), "{detail}");
        assert!(
            detail.contains("--durability process-crash-safe"),
            "{detail}"
        );
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

    #[test]
    fn unclean_relaxed_session_names_the_whole_data_directory() {
        let detail = store_open_failure_detail(
            StoreError::UncleanRelaxedSession {
                root_directory: PathBuf::from("/data/s3"),
            },
            Path::new("/data"),
        );
        assert!(detail.contains("did not stop cleanly"), "{detail}");
        assert!(detail.contains("whole data directory /data "), "{detail}");
        assert!(!detail.contains("/data/s3"), "{detail}");
    }

    #[test]
    fn unclean_session_without_markers_names_the_whole_data_directory() {
        let detail = store_open_failure_detail(
            StoreError::UncleanSessionWithoutRelaxedSessionMarkers {
                root_directory: PathBuf::from("/data/s3"),
            },
            Path::new("/data"),
        );
        assert!(detail.contains("defaulted to relaxed"), "{detail}");
        assert!(detail.contains("whole data directory /data "), "{detail}");
        assert!(!detail.contains("/data/s3"), "{detail}");
    }
}
