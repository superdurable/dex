// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Static bearer tokens listed by SHA-256 hash in the Go Server's YAML file format.

use aws_lc_rs::constant_time;
use serde::Deserialize;

use super::watched_file::WatchedFileContent;

/// Length of a SHA-256 digest in bytes.
pub(crate) const SHA256_LENGTH: usize = 32;

/// The SHA-256 hashes of the accepted static tokens.
#[derive(Debug, Default, PartialEq, Eq)]
pub(crate) struct StaticTokenHashes {
    hashes: Vec<[u8; SHA256_LENGTH]>,
}

impl StaticTokenHashes {
    /// Reports whether `credential_hash` is listed, comparing every entry in constant time.
    pub(crate) fn contains(&self, credential_hash: &[u8; SHA256_LENGTH]) -> bool {
        self.hashes.iter().fold(false, |is_listed, hash| {
            is_listed | constant_time::verify_slices_are_equal(hash, credential_hash).is_ok()
        })
    }
}

/// Why a static token file is malformed.
#[derive(Debug, thiserror::Error)]
pub(crate) enum StaticTokenFileError {
    /// The file is not YAML of the expected shape, or it has unknown fields.
    #[error("invalid static token YAML: {source}")]
    Yaml {
        /// Decoding error.
        #[source]
        source: serde_yaml_ng::Error,
    },
    /// An entry's hash is not 64 hexadecimal characters.
    #[error("tokens[{index}].sha256 must be 64 hexadecimal characters")]
    InvalidHash {
        /// Entry index.
        index: usize,
    },
    /// An entry repeats an earlier hash.
    #[error("tokens[{index}]: duplicate sha256")]
    DuplicateHash {
        /// Entry index.
        index: usize,
    },
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct StaticTokenFileDocument {
    #[serde(default)]
    tokens: Option<Vec<StaticTokenEntry>>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct StaticTokenEntry {
    sha256: String,
}

impl WatchedFileContent for StaticTokenHashes {
    type ParseError = StaticTokenFileError;

    fn parse(contents: &[u8]) -> Result<Self, Self::ParseError> {
        if contents.iter().all(u8::is_ascii_whitespace) {
            return Ok(Self::empty());
        }
        let document: StaticTokenFileDocument = serde_yaml_ng::from_slice(contents)
            .map_err(|source| StaticTokenFileError::Yaml { source })?;
        let mut hashes = Vec::new();
        for (index, entry) in document.tokens.unwrap_or_default().iter().enumerate() {
            let hash = decode_sha256_hex(&entry.sha256)
                .ok_or(StaticTokenFileError::InvalidHash { index })?;
            if hashes.contains(&hash) {
                return Err(StaticTokenFileError::DuplicateHash { index });
            }
            hashes.push(hash);
        }
        Ok(Self { hashes })
    }

    fn empty() -> Self {
        Self::default()
    }
}

/// Decodes 64 hexadecimal characters of either case.
fn decode_sha256_hex(text: &str) -> Option<[u8; SHA256_LENGTH]> {
    let digits = text.as_bytes();
    if digits.len() != 2 * SHA256_LENGTH {
        return None;
    }
    let mut hash = [0_u8; SHA256_LENGTH];
    for (byte, pair) in hash.iter_mut().zip(digits.chunks_exact(2)) {
        let high = char::from(pair[0]).to_digit(16)?;
        let low = char::from(pair[1]).to_digit(16)?;
        *byte = u8::try_from(high << 4 | low).ok()?;
    }
    Some(hash)
}

#[cfg(test)]
mod tests {
    use super::*;

    const HASH_OF_TOKEN: &str = "5e884898da28047151d0e56f8dc6292773603d0d6aabbdd62a11ef721d1542d8";

    fn hash_bytes(hex: &str) -> [u8; SHA256_LENGTH] {
        decode_sha256_hex(hex).expect("the test hash is valid hexadecimal")
    }

    #[test]
    fn the_go_server_format_parses_in_either_case() {
        let file = format!(
            "tokens:\n  - sha256: {HASH_OF_TOKEN}\n  - sha256: {}\n",
            "AB".repeat(32)
        );
        let hashes = StaticTokenHashes::parse(file.as_bytes()).expect("the file is valid");
        assert!(hashes.contains(&hash_bytes(HASH_OF_TOKEN)));
        assert!(hashes.contains(&[0xab; SHA256_LENGTH]));
        assert!(!hashes.contains(&[0; SHA256_LENGTH]));
    }

    #[test]
    fn blank_files_and_absent_lists_accept_nothing() {
        for contents in ["", "  \n\t\n", "tokens: []\n", "tokens:\n", "{}\n"] {
            let hashes = StaticTokenHashes::parse(contents.as_bytes())
                .unwrap_or_else(|error| panic!("{contents:?} is valid: {error}"));
            assert_eq!(hashes, StaticTokenHashes::empty(), "{contents:?}");
        }
    }

    #[test]
    fn malformed_files_are_rejected() {
        let short_hash = "tokens:\n  - sha256: abcd\n";
        let non_hex = format!("tokens:\n  - sha256: {}\n", "zz".repeat(32));
        let duplicate =
            format!("tokens:\n  - sha256: {HASH_OF_TOKEN}\n  - sha256: {HASH_OF_TOKEN}\n");
        let unknown_field = format!("tokens:\n  - sha256: {HASH_OF_TOKEN}\n    note: x\n");
        let unknown_top_level = "tokenz: []\n";
        let plaintext = "tokens:\n  - token: secret\n";
        assert!(matches!(
            StaticTokenHashes::parse(short_hash.as_bytes()),
            Err(StaticTokenFileError::InvalidHash { index: 0 })
        ));
        assert!(matches!(
            StaticTokenHashes::parse(non_hex.as_bytes()),
            Err(StaticTokenFileError::InvalidHash { index: 0 })
        ));
        assert!(matches!(
            StaticTokenHashes::parse(duplicate.as_bytes()),
            Err(StaticTokenFileError::DuplicateHash { index: 1 })
        ));
        for contents in [unknown_field.as_str(), unknown_top_level, plaintext] {
            assert!(matches!(
                StaticTokenHashes::parse(contents.as_bytes()),
                Err(StaticTokenFileError::Yaml { .. })
            ));
        }
    }
}
