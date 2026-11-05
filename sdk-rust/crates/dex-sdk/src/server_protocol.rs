// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::sync::Arc;
use std::sync::atomic::{AtomicU32, Ordering};

use dex_protocol::dex::ServerInfo;

pub(crate) const MINIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION: u32 = 4;
pub(crate) const MAXIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION: u32 = 4;
pub(crate) const HARD_DELETE_ATTRIBUTE_PROTOCOL_VERSION: u32 = 3;
pub(crate) const SDK_VERSION: &str = env!("CARGO_PKG_VERSION");

/// Shares one negotiated Server protocol version; zero means negotiation has not succeeded.
#[derive(Clone, Default)]
pub(crate) struct NegotiatedServerProtocolVersion {
    version: Arc<AtomicU32>,
}

impl NegotiatedServerProtocolVersion {
    pub(crate) fn store(&self, version: u32) {
        self.version.store(version, Ordering::Release);
    }

    /// Returns `Ok(())` when the negotiated Server accepts hard-delete Attribute writes, and an
    /// error naming `attribute_key` when negotiation is missing or below protocol version 3.
    pub(crate) fn require_hard_delete_attribute_support(
        &self,
        attribute_key: &str,
    ) -> Result<(), String> {
        let version = self.version();
        if version >= HARD_DELETE_ATTRIBUTE_PROTOCOL_VERSION {
            return Ok(());
        }
        let negotiated = if version == 0 {
            "no protocol version was negotiated".to_string()
        } else {
            format!("the negotiated protocol version is {version}")
        };
        Err(format!(
            "hard-delete Attribute {attribute_key} requires Server protocol version \
             {HARD_DELETE_ATTRIBUTE_PROTOCOL_VERSION} or later, but {negotiated}"
        ))
    }

    fn version(&self) -> u32 {
        self.version.load(Ordering::Acquire)
    }
}

pub(crate) const MINIMUM_SERVER_ZD_RELEASE: &str = "server-zd newer than v0.1.16";

pub(crate) fn negotiate_server_protocol(server_info: &ServerInfo) -> Result<u32, String> {
    let server_minimum = server_info.minimum_supported_protocol_version;
    let server_current = server_info.current_protocol_version;
    if MINIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION == 0
        || MAXIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION == 0
        || MINIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION > MAXIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION
    {
        return Err(format!(
            "Rust SDK version \"{SDK_VERSION}\" has an invalid protocol range {}.",
            sdk_protocol_range()
        ));
    }
    if server_minimum == 0 || server_current == 0 || server_minimum > server_current {
        return Err(format!(
            "The Server reported an invalid protocol range {server_minimum}-{server_current} \
             (server version {}). Rust SDK version \"{SDK_VERSION}\" supports protocol {}.",
            reported_server_version(server_info),
            sdk_protocol_range()
        ));
    }
    if server_current < MINIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION {
        return Err(format!(
            "This SDK requires a Dex Server with protocol version \
             {MINIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION} or later ({MINIMUM_SERVER_ZD_RELEASE}). \
             The Server reports protocol versions {server_minimum}-{server_current} \
             (server version {}). Upgrade the Server before upgrading the SDK. \
             Rust SDK version \"{SDK_VERSION}\" supports protocol {}.",
            reported_server_version(server_info),
            sdk_protocol_range()
        ));
    }
    if server_minimum > MAXIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION {
        return Err(format!(
            "The Server requires protocol version {server_minimum} or later and reports protocol \
             versions {server_minimum}-{server_current} (server version {}). Rust SDK version \
             \"{SDK_VERSION}\" supports protocol {}. Upgrade the SDK.",
            reported_server_version(server_info),
            sdk_protocol_range()
        ));
    }
    Ok(server_current.min(MAXIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION))
}

fn sdk_protocol_range() -> String {
    format!(
        "[{MINIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION},{MAXIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION}]"
    )
}

fn reported_server_version(server_info: &ServerInfo) -> &str {
    if server_info.server_version.is_empty() {
        "unknown"
    } else {
        &server_info.server_version
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn negotiates_highest_common_protocol() {
        for (server_current, expected) in [(4, 4), (5, 4)] {
            let negotiated = negotiate_server_protocol(&ServerInfo {
                server_version: "newer".to_string(),
                minimum_supported_protocol_version: 1,
                current_protocol_version: server_current,
                server_engine: 0,
            })
            .expect("compatible interval");
            assert_eq!(negotiated, expected);
        }
    }

    #[test]
    fn hard_delete_attribute_writes_require_protocol_version_three() {
        let negotiated = NegotiatedServerProtocolVersion::default();
        let error = negotiated
            .require_hard_delete_attribute_support("api-key")
            .expect_err("missing negotiation fails closed");
        assert!(error.contains("api-key"));
        assert!(error.contains("no protocol version was negotiated"));
        for version in [1, 2] {
            negotiated.store(version);
            let error = negotiated
                .require_hard_delete_attribute_support("api-key")
                .expect_err("old Server is rejected");
            assert!(error.contains("protocol version 3 or later"));
            assert!(error.contains(&format!("negotiated protocol version is {version}")));
        }
        negotiated.store(3);
        negotiated
            .require_hard_delete_attribute_support("api-key")
            .expect("protocol 3 accepts hard-delete writes");
    }

    #[test]
    fn rejects_invalid_and_disjoint_server_intervals() {
        let server_info = |server_version: &str, minimum: u32, current: u32| ServerInfo {
            server_version: server_version.to_string(),
            minimum_supported_protocol_version: minimum,
            current_protocol_version: current,
            server_engine: 0,
        };
        for invalid in [
            server_info("zero", 0, 0),
            server_info("reversed", 2, 1),
            ServerInfo::default(),
        ] {
            let error = negotiate_server_protocol(&invalid).expect_err("invalid interval");
            assert!(error.contains("reported an invalid protocol range"));
            assert!(!error.contains("Upgrade"));
        }
        let old_server = negotiate_server_protocol(&server_info("old", 1, 3))
            .expect_err("Server older than the SDK minimum");
        assert!(old_server.contains(&format!(
            "protocol version {MINIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION} or later ({MINIMUM_SERVER_ZD_RELEASE})"
        )));
        assert!(old_server.contains("protocol versions 1-3 (server version old)"));
        assert!(old_server.contains("Upgrade the Server"));
        let future_server = negotiate_server_protocol(&server_info("future", 5, 5))
            .expect_err("Server newer than the SDK maximum");
        assert!(future_server.contains("requires protocol version 5 or later"));
        assert!(future_server.contains("(server version future)"));
        assert!(future_server.contains("Upgrade the SDK"));
        assert!(!future_server.contains("Upgrade the Server"));
    }

    #[test]
    fn runtime_version_matches_release() {
        let expected = std::env::var("DEX_EXPECTED_SDK_VERSION")
            .unwrap_or_else(|_| env!("CARGO_PKG_VERSION").to_string());
        assert_eq!(SDK_VERSION, expected);
    }
}
