// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::fmt::Write as _;

use aws_lc_rs::digest::{SHA256, SHA256_OUTPUT_LEN, digest};

/// SHA-256 of a Flow definition's content.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(super) struct FlowDefinitionDigest([u8; SHA256_OUTPUT_LEN]);

impl FlowDefinitionDigest {
    /// Hashes `content`.
    pub(super) fn of_content(content: &[u8]) -> Self {
        let output: [u8; SHA256_OUTPUT_LEN] = digest(&SHA256, content)
            .as_ref()
            .try_into()
            .expect("SHA-256 output is 32 bytes");
        Self(output)
    }

    /// Reads a stored 32-byte digest; `None` when it has another length.
    pub(super) fn from_stored_bytes(bytes: &[u8]) -> Option<Self> {
        bytes.try_into().ok().map(Self)
    }

    /// Returns the 32 digest bytes.
    pub(super) fn as_bytes(&self) -> &[u8] {
        &self.0
    }

    /// Returns the lowercase hexadecimal form that the public API reports.
    pub(super) fn to_hex(self) -> String {
        let mut hex = String::with_capacity(SHA256_OUTPUT_LEN * 2);
        for byte in self.0 {
            write!(hex, "{byte:02x}").expect("writing to a String cannot fail");
        }
        hex
    }
}

#[cfg(test)]
mod tests {
    use super::FlowDefinitionDigest;

    #[test]
    fn hex_matches_the_known_sha256_of_abc() {
        assert_eq!(
            FlowDefinitionDigest::of_content(b"abc").to_hex(),
            "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
        );
    }
}
