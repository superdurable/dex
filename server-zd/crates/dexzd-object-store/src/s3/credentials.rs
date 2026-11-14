// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Credentials of an S3 object store.

/// Credentials used by an S3 object store.
#[derive(Clone)]
pub enum S3Credentials {
    /// Resolve credentials from the AWS default provider chain.
    DefaultChain,
    /// Sign requests with fixed credentials, usually for a local S3-compatible endpoint.
    Static {
        /// Access key identifier used for signing.
        access_key_id: String,
        /// Secret key used for signing.
        secret_access_key: String,
        /// Session token of temporary credentials, sent as `x-amz-security-token`.
        session_token: Option<String>,
    },
    /// Send unsigned requests, for a local emulator or a bucket that allows anonymous access.
    Anonymous,
}

impl std::fmt::Debug for S3Credentials {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::DefaultChain => formatter.write_str("DefaultChain"),
            Self::Static { session_token, .. } => formatter
                .debug_struct("Static")
                .field("access_key_id", &"[REDACTED]")
                .field("secret_access_key", &"[REDACTED]")
                .field(
                    "session_token",
                    &session_token.as_ref().map(|_| "[REDACTED]"),
                )
                .finish(),
            Self::Anonymous => formatter.write_str("Anonymous"),
        }
    }
}
