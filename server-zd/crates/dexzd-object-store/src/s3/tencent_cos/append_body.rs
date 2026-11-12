// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Bodies of AppendObject requests: size limits, digests, and streamed file contents.

use std::path::{Path, PathBuf};

use aws_lc_rs::digest::{Context, SHA256};
use base64::Engine;
use base64::engine::general_purpose::STANDARD as BASE64;
use bytes::Bytes;
use md5::{Digest, Md5};
use tokio::io::AsyncReadExt;

use super::MAX_TENCENT_COS_APPENDABLE_OBJECT_BYTES;
use crate::{ObjectStoreError, ObjectStoreErrorKind, ObjectStoreOperation, ObjectStoreResult};

/// Bytes read from a file per chunk, both for digests and for the request body.
const FILE_CHUNK_BYTES: usize = 1024 * 1024;

/// A checked AppendObject body: non-empty and within the appendable object limit.
pub(super) enum AppendBody {
    Bytes(Bytes),
    File { path: PathBuf, size: u64 },
}

/// Digests of an AppendObject body, computed before the request is signed.
pub(super) struct AppendBodyDigests {
    pub(super) size: u64,
    /// Base64 MD5, sent as `Content-MD5`.
    pub(super) content_md5: String,
    /// Hex SHA-256, signed as the payload hash.
    pub(super) sha256_hex: String,
}

impl AppendBody {
    pub(super) fn from_bytes(key: &str, bytes: Bytes) -> ObjectStoreResult<Self> {
        check_append_size(key, bytes.len() as u64)?;
        Ok(Self::Bytes(bytes))
    }

    /// Checks the file size before anything is read or sent.
    pub(super) async fn from_file(key: &str, path: &Path) -> ObjectStoreResult<Self> {
        let size = tokio::fs::metadata(path)
            .await
            .map_err(|error| file_error(key, path, &error))?
            .len();
        check_append_size(key, size)?;
        Ok(Self::File {
            path: path.to_path_buf(),
            size,
        })
    }

    /// Reads the whole body once to compute its digests.
    pub(super) async fn digests(&self, key: &str) -> ObjectStoreResult<AppendBodyDigests> {
        let mut md5 = Md5::new();
        let mut sha256 = Context::new(&SHA256);
        let size = match self {
            Self::Bytes(bytes) => {
                md5.update(bytes);
                sha256.update(bytes);
                bytes.len() as u64
            }
            Self::File { path, size } => {
                let mut file = tokio::fs::File::open(path)
                    .await
                    .map_err(|error| file_error(key, path, &error))?;
                let mut buffer = vec![0; FILE_CHUNK_BYTES];
                let mut read_bytes: u64 = 0;
                loop {
                    let read = file
                        .read(&mut buffer)
                        .await
                        .map_err(|error| file_error(key, path, &error))?;
                    if read == 0 {
                        break;
                    }
                    md5.update(&buffer[..read]);
                    sha256.update(&buffer[..read]);
                    read_bytes += read as u64;
                }
                if read_bytes != *size {
                    return Err(changed_file(key, path));
                }
                read_bytes
            }
        };
        Ok(AppendBodyDigests {
            size,
            content_md5: BASE64.encode(md5.finalize()),
            sha256_hex: sha256
                .finish()
                .as_ref()
                .iter()
                .map(|byte| format!("{byte:02x}"))
                .collect(),
        })
    }

    /// Returns the request body; a file is streamed in chunks rather than read into memory.
    pub(super) async fn into_request_body(self, key: &str) -> ObjectStoreResult<reqwest::Body> {
        match self {
            Self::Bytes(bytes) => Ok(reqwest::Body::from(bytes)),
            Self::File { path, size } => {
                let file = tokio::fs::File::open(&path)
                    .await
                    .map_err(|error| file_error(key, &path, &error))?;
                let chunks =
                    futures::stream::try_unfold((file, size), |(mut file, remaining)| async move {
                        if remaining == 0 {
                            return Ok::<_, std::io::Error>(None);
                        }
                        let wanted = usize::try_from(remaining.min(FILE_CHUNK_BYTES as u64))
                            .expect("a chunk is at most FILE_CHUNK_BYTES");
                        let mut chunk = vec![0; wanted];
                        let read = file.read(&mut chunk).await?;
                        if read == 0 {
                            return Err(std::io::Error::new(
                                std::io::ErrorKind::UnexpectedEof,
                                "the file shrank while it was uploaded",
                            ));
                        }
                        chunk.truncate(read);
                        Ok(Some((Bytes::from(chunk), (file, remaining - read as u64))))
                    });
                Ok(reqwest::Body::wrap_stream(chunks))
            }
        }
    }
}

/// Rejects bodies that AppendObject cannot create exclusively.
fn check_append_size(key: &str, size: u64) -> ObjectStoreResult<()> {
    let detail = if size == 0 {
        "Tencent COS cannot create an empty object exclusively: an empty appendable object \
         accepts another AppendObject at position 0, so a second create-only write would also \
         succeed"
            .to_owned()
    } else if size > MAX_TENCENT_COS_APPENDABLE_OBJECT_BYTES {
        format!(
            "the body has {size} bytes, more than the 5 GiB limit of a Tencent COS appendable \
             object, so it cannot be created with AppendObject"
        )
    } else {
        return Ok(());
    };
    Err(ObjectStoreError::new(
        ObjectStoreOperation::Put,
        key,
        ObjectStoreErrorKind::Permanent,
        detail,
    ))
}

fn file_error(key: &str, path: &Path, error: &std::io::Error) -> ObjectStoreError {
    ObjectStoreError::new(
        ObjectStoreOperation::Put,
        key,
        ObjectStoreErrorKind::Permanent,
        format!("reading {}: {error}", path.display()),
    )
}

fn changed_file(key: &str, path: &Path) -> ObjectStoreError {
    ObjectStoreError::new(
        ObjectStoreOperation::Put,
        key,
        ObjectStoreErrorKind::Permanent,
        format!("{} changed size while it was read", path.display()),
    )
}
