// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

use std::error::Error;
use std::fmt::{Display, Formatter};
use std::io;

#[derive(Debug)]
pub enum BlobCacheError {
    Closed,
    InvalidConfig(String),
    InvalidBlob(String),
    ContentMismatch(String),
    Corrupt(String),
    Reconciliation(String),
    Policy(String),
    Io {
        operation: String,
        source: io::Error,
    },
}

impl BlobCacheError {
    pub(crate) fn io(operation: impl Into<String>, source: io::Error) -> Self {
        Self::Io {
            operation: operation.into(),
            source,
        }
    }

    pub(crate) fn is_missing_or_corrupt(&self) -> bool {
        matches!(self, Self::Corrupt(_))
            || matches!(self, Self::Io { source, .. } if source.kind() == io::ErrorKind::NotFound)
    }
}

impl Display for BlobCacheError {
    fn fmt(&self, formatter: &mut Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Closed => formatter.write_str("blob cache is closed"),
            Self::InvalidConfig(message) => {
                write!(formatter, "invalid blob cache configuration: {message}")
            }
            Self::InvalidBlob(message) => write!(formatter, "invalid blob: {message}"),
            Self::ContentMismatch(blob_id) => {
                write!(formatter, "blob ID content mismatch: {blob_id:?}")
            }
            Self::Corrupt(message) => write!(formatter, "corrupt blob cache entry: {message}"),
            Self::Reconciliation(message) => {
                write!(formatter, "blob cache reconciliation failed: {message}")
            }
            Self::Policy(message) => write!(formatter, "blob cache policy failed: {message}"),
            Self::Io { operation, source } => write!(formatter, "{operation}: {source}"),
        }
    }
}

impl Error for BlobCacheError {
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        match self {
            Self::Io { source, .. } => Some(source),
            _ => None,
        }
    }
}
