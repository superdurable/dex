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

use std::path::PathBuf;
use std::time::SystemTime;

use super::BlobCacheError;
use super::config::validate_blob_id;

pub(crate) const FIXED_HEADER_SIZE: usize = 24;
const FILE_MAGIC: &[u8; 4] = b"DXBC";
const FILE_VERSION: u8 = 1;

#[derive(Clone, Debug)]
pub(crate) struct FileMetadata {
    pub(crate) blob_id: String,
    pub(crate) path: PathBuf,
    pub(crate) size: i64,
    pub(crate) checksum: u32,
    pub(crate) modified: SystemTime,
}

#[derive(Clone, Copy, Debug)]
pub(crate) struct FileHeader {
    pub(crate) blob_id_length: u32,
    pub(crate) payload_length: u64,
    pub(crate) checksum: u32,
}

pub(crate) fn calculate_metadata(
    blob_id: &str,
    payload: &[u8],
    path: PathBuf,
) -> Result<FileMetadata, BlobCacheError> {
    validate_blob_id(blob_id)?;
    let blob_id_size = i64::try_from(blob_id.len())
        .map_err(|_| BlobCacheError::InvalidBlob("blob ID length overflows i64".to_owned()))?;
    let payload_size = i64::try_from(payload.len())
        .map_err(|_| BlobCacheError::InvalidBlob("payload length overflows i64".to_owned()))?;
    let size = i64::try_from(FIXED_HEADER_SIZE)
        .ok()
        .and_then(|header_size| header_size.checked_add(blob_id_size))
        .and_then(|prefix_size| prefix_size.checked_add(payload_size))
        .ok_or_else(|| {
            BlobCacheError::InvalidBlob("complete file size overflows i64".to_owned())
        })?;

    Ok(FileMetadata {
        blob_id: blob_id.to_owned(),
        path,
        size,
        checksum: calculate_checksum(blob_id.as_bytes(), payload),
        modified: SystemTime::UNIX_EPOCH,
    })
}

pub(crate) fn encode_header(header: FileHeader) -> [u8; FIXED_HEADER_SIZE] {
    let mut encoded = [0_u8; FIXED_HEADER_SIZE];
    encoded[0..4].copy_from_slice(FILE_MAGIC);
    encoded[4] = FILE_VERSION;
    encoded[8..12].copy_from_slice(&header.blob_id_length.to_le_bytes());
    encoded[12..20].copy_from_slice(&header.payload_length.to_le_bytes());
    encoded[20..24].copy_from_slice(&header.checksum.to_le_bytes());
    encoded
}

pub(crate) fn decode_header(
    encoded: &[u8; FIXED_HEADER_SIZE],
) -> Result<FileHeader, BlobCacheError> {
    if &encoded[0..4] != FILE_MAGIC {
        return Err(BlobCacheError::Corrupt("invalid magic".to_owned()));
    }
    if encoded[4] != FILE_VERSION {
        return Err(BlobCacheError::Corrupt(format!(
            "unsupported version {}",
            encoded[4]
        )));
    }
    if encoded[5..8] != [0, 0, 0] {
        return Err(BlobCacheError::Corrupt(
            "non-zero reserved bytes".to_owned(),
        ));
    }

    Ok(FileHeader {
        blob_id_length: u32::from_le_bytes(encoded[8..12].try_into().unwrap()),
        payload_length: u64::from_le_bytes(encoded[12..20].try_into().unwrap()),
        checksum: u32::from_le_bytes(encoded[20..24].try_into().unwrap()),
    })
}

pub(crate) fn calculate_checksum(blob_id: &[u8], payload: &[u8]) -> u32 {
    let mut checksum = Crc32c::new();
    checksum.update(blob_id);
    checksum.update(payload);
    checksum.finish()
}

pub(crate) struct Crc32c {
    value: u32,
}

impl Crc32c {
    pub(crate) fn new() -> Self {
        Self { value: u32::MAX }
    }

    pub(crate) fn update(&mut self, bytes: &[u8]) {
        for byte in bytes {
            self.value ^= u32::from(*byte);
            for _ in 0..8 {
                let mask = 0_u32.wrapping_sub(self.value & 1);
                self.value = (self.value >> 1) ^ (0x82f6_3b78 & mask);
            }
        }
    }

    pub(crate) fn finish(self) -> u32 {
        !self.value
    }
}
