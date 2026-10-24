// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Hot reload of credential files with the Go Server's watched file semantics.

use std::fmt::Display;
use std::fs::Metadata;
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};
use std::time::{Duration, SystemTime};

use tokio::time::Instant;

/// Bounds how often a request may stat a watched file, as in the Go Server.
pub(crate) const WATCHED_FILE_CHECK_INTERVAL: Duration = Duration::from_secs(1);

/// The parsed content of a watched credential file.
pub(crate) trait WatchedFileContent: Sized + Send + Sync + 'static {
    /// Why the bytes are malformed; never contains a credential.
    type ParseError: Display;

    /// Parses the file's bytes.
    fn parse(contents: &[u8]) -> Result<Self, Self::ParseError>;

    /// Returns the content of a missing file, which accepts no credential.
    fn empty() -> Self;
}

/// Why a watched file could not be loaded.
#[derive(Debug, thiserror::Error)]
pub(crate) enum WatchedFileLoadError {
    /// The file could not be inspected or read.
    #[error("reading {}: {source}", path.display())]
    Read {
        /// File path.
        path: PathBuf,
        /// Filesystem error.
        #[source]
        source: std::io::Error,
    },
    /// The file's content is malformed.
    #[error("parsing {}: {detail}", path.display())]
    Parse {
        /// File path.
        path: PathBuf,
        /// What is malformed.
        detail: String,
    },
}

impl WatchedFileLoadError {
    /// Describes the failure without the path.
    pub(crate) fn detail(&self) -> String {
        match self {
            Self::Read { source, .. } => source.to_string(),
            Self::Parse { detail, .. } => detail.clone(),
        }
    }
}

/// Rereads a changed file at most once per interval; a missing file accepts nothing.
pub(crate) struct WatchedFile<T> {
    path: PathBuf,
    check_interval: Duration,
    state: Mutex<WatchedFileState<T>>,
}

struct WatchedFileState<T> {
    content: Arc<T>,
    /// Fingerprint of the last version read, valid or not; `None` while the file is missing.
    fingerprint: Option<FileFingerprint>,
    checked_at: Instant,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
struct FileFingerprint {
    modified: Option<SystemTime>,
    length: u64,
}

impl FileFingerprint {
    fn of(metadata: &Metadata) -> Self {
        Self {
            // Platforms without modification times fall back to size changes alone.
            modified: metadata.modified().ok(),
            length: metadata.len(),
        }
    }
}

impl<T: WatchedFileContent> WatchedFile<T> {
    /// Loads the file, which must exist and be valid.
    pub(crate) fn open(path: &Path) -> Result<Self, WatchedFileLoadError> {
        Self::open_with_check_interval(path, WATCHED_FILE_CHECK_INTERVAL)
    }

    /// Loads the file and rereads it at most once per `check_interval`.
    pub(crate) fn open_with_check_interval(
        path: &Path,
        check_interval: Duration,
    ) -> Result<Self, WatchedFileLoadError> {
        let metadata = std::fs::metadata(path).map_err(|source| WatchedFileLoadError::Read {
            path: path.to_path_buf(),
            source,
        })?;
        let content = read_content::<T>(path)?;
        Ok(Self {
            path: path.to_path_buf(),
            check_interval,
            state: Mutex::new(WatchedFileState {
                content: Arc::new(content),
                fingerprint: Some(FileFingerprint::of(&metadata)),
                checked_at: Instant::now(),
            }),
        })
    }

    /// Returns the current content, rereading the file first when it changed.
    pub(crate) fn content(&self) -> Arc<T> {
        let mut state = self
            .state
            .lock()
            .expect("watched file lock is poisoned only if a holder panicked");
        let now = Instant::now();
        if now.saturating_duration_since(state.checked_at) < self.check_interval {
            return Arc::clone(&state.content);
        }
        state.checked_at = now;
        let metadata = match std::fs::metadata(&self.path) {
            Ok(metadata) => metadata,
            Err(error) => {
                if state.fingerprint.is_some() {
                    tracing::error!(
                        path = %self.path.display(),
                        %error,
                        "a watched credential file cannot be read; it accepts no credential until it returns"
                    );
                }
                state.content = Arc::new(T::empty());
                state.fingerprint = None;
                return Arc::clone(&state.content);
            }
        };
        let fingerprint = FileFingerprint::of(&metadata);
        if state.fingerprint == Some(fingerprint) {
            return Arc::clone(&state.content);
        }
        state.fingerprint = Some(fingerprint);
        match read_content::<T>(&self.path) {
            Ok(content) => {
                tracing::info!(path = %self.path.display(), "reloaded a watched credential file");
                state.content = Arc::new(content);
            }
            Err(error) => tracing::error!(
                %error,
                "a watched credential file changed but cannot be loaded; keeping its previous content"
            ),
        }
        Arc::clone(&state.content)
    }
}

fn read_content<T: WatchedFileContent>(path: &Path) -> Result<T, WatchedFileLoadError> {
    let contents = std::fs::read(path).map_err(|source| WatchedFileLoadError::Read {
        path: path.to_path_buf(),
        source,
    })?;
    T::parse(&contents).map_err(|detail| WatchedFileLoadError::Parse {
        path: path.to_path_buf(),
        detail: detail.to_string(),
    })
}
