// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Header that starts every version object body, and the ETag of a version.
//!
//! Header format 1 is 38 bytes, a persistent format:
//!
//! | Bytes | Field |
//! | --- | --- |
//! | 0-3 | Magic `DXZV` |
//! | 4 | Format, 1 |
//! | 5 | Flags: bit 0 tombstone, bit 1 predecessor present; other bits zero |
//! | 6-21 | Version ID, a random 128-bit big-endian integer |
//! | 22-37 | Predecessor version ID, big-endian; zero without a predecessor |
//!
//! The caller's payload follows the header. A tombstone has no payload.

use std::fmt;

use bytes::{BufMut, Bytes, BytesMut};

use crate::ByteRange;

const VERSION_HEADER_MAGIC: [u8; 4] = *b"DXZV";
const VERSION_HEADER_FORMAT: u8 = 1;
const TOMBSTONE_FLAG: u8 = 0b01;
const PREDECESSOR_FLAG: u8 = 0b10;

/// Length of a format 1 header in bytes.
pub(super) const VERSION_HEADER_LENGTH: usize = 38;

/// Byte range of the header, for reads that need only the header.
pub(super) const VERSION_HEADER_RANGE: ByteRange = ByteRange {
    start: 0,
    end_inclusive: VERSION_HEADER_LENGTH as u64 - 1,
};

/// Identity and kind of one version object.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(super) struct VersionHeader {
    /// Random ID chosen by the writer of the version.
    pub(super) version_id: u128,
    /// ID of the version the writer replaced, or `None` for a first version.
    pub(super) predecessor_id: Option<u128>,
    /// Whether the version records a deletion.
    pub(super) is_tombstone: bool,
}

/// Why a version object body is not a valid format 1 body.
#[derive(Debug, Clone, Copy, PartialEq, Eq, thiserror::Error)]
pub(super) enum VersionHeaderError {
    /// The body is shorter than a header.
    #[error("version body has {length} bytes, fewer than a {VERSION_HEADER_LENGTH}-byte header")]
    Truncated {
        /// Body length in bytes.
        length: usize,
    },
    /// The body does not start with the header magic.
    #[error("version body does not start with the version header magic")]
    MissingMagic,
    /// The header format is not 1.
    #[error("version header format {format} is not supported")]
    UnsupportedFormat {
        /// Stored format.
        format: u8,
    },
    /// The header sets unknown flag bits.
    #[error("version header flags {flags:#04x} are not supported")]
    UnsupportedFlags {
        /// Stored flags.
        flags: u8,
    },
    /// A tombstone carries a payload.
    #[error("tombstone version carries a {payload_length}-byte payload")]
    TombstoneWithPayload {
        /// Payload length in bytes.
        payload_length: u64,
    },
}

impl VersionHeader {
    /// Returns the body of a version: this header followed by `payload`.
    pub(super) fn encode_with_payload(&self, payload: &[u8]) -> Bytes {
        let mut body = BytesMut::with_capacity(VERSION_HEADER_LENGTH + payload.len());
        body.put_slice(&VERSION_HEADER_MAGIC);
        body.put_u8(VERSION_HEADER_FORMAT);
        let mut flags = 0;
        if self.is_tombstone {
            flags |= TOMBSTONE_FLAG;
        }
        if self.predecessor_id.is_some() {
            flags |= PREDECESSOR_FLAG;
        }
        body.put_u8(flags);
        body.put_u128(self.version_id);
        body.put_u128(self.predecessor_id.unwrap_or(0));
        body.put_slice(payload);
        body.freeze()
    }

    /// Decodes the header at the start of `bytes`, the whole body or its first bytes.
    ///
    /// `object_size` is the size of the whole body; a tombstone must have no payload.
    pub(super) fn decode(bytes: &[u8], object_size: u64) -> Result<Self, VersionHeaderError> {
        let Some(header) = bytes.get(..VERSION_HEADER_LENGTH) else {
            return Err(VersionHeaderError::Truncated {
                length: bytes.len(),
            });
        };
        if header[..4] != VERSION_HEADER_MAGIC {
            return Err(VersionHeaderError::MissingMagic);
        }
        if header[4] != VERSION_HEADER_FORMAT {
            return Err(VersionHeaderError::UnsupportedFormat { format: header[4] });
        }
        let flags = header[5];
        if flags & !(TOMBSTONE_FLAG | PREDECESSOR_FLAG) != 0 {
            return Err(VersionHeaderError::UnsupportedFlags { flags });
        }
        let is_tombstone = flags & TOMBSTONE_FLAG != 0;
        let payload_length = object_size.saturating_sub(VERSION_HEADER_LENGTH as u64);
        if is_tombstone && payload_length != 0 {
            return Err(VersionHeaderError::TombstoneWithPayload { payload_length });
        }
        let version_id = u128::from_be_bytes(
            header[6..22]
                .try_into()
                .expect("a 16-byte slice converts to a 16-byte array"),
        );
        let stored_predecessor_id = u128::from_be_bytes(
            header[22..38]
                .try_into()
                .expect("a 16-byte slice converts to a 16-byte array"),
        );
        Ok(Self {
            version_id,
            predecessor_id: (flags & PREDECESSOR_FLAG != 0).then_some(stored_predecessor_id),
            is_tombstone,
        })
    }
}

/// The ETag the adapter returns for a version: `"g<generation>-<version ID in hex>"`.
///
/// Writes and later reads of one version return byte-identical ETags.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(super) struct VersionEtag {
    /// Generation of the version.
    pub(super) generation: u64,
    /// Version ID from the version's header.
    pub(super) version_id: u128,
}

impl VersionEtag {
    /// Parses an ETag that this adapter returned, or returns `None` for any other string.
    pub(super) fn parse(etag: &str) -> Option<Self> {
        let unquoted = etag.strip_prefix("\"g")?.strip_suffix('"')?;
        let (generation, version_id) = unquoted.split_once('-')?;
        if version_id.len() != 32
            || !version_id
                .bytes()
                .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
        {
            return None;
        }
        let parsed = Self {
            generation: generation.parse().ok()?,
            version_id: u128::from_str_radix(version_id, 16).ok()?,
        };
        // Only the canonical spelling is an ETag this adapter returned.
        (parsed.generation >= 1 && parsed.to_string() == etag).then_some(parsed)
    }
}

impl fmt::Display for VersionEtag {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(
            formatter,
            "\"g{}-{:032x}\"",
            self.generation, self.version_id
        )
    }
}
