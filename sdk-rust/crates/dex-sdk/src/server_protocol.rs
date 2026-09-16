// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use dex_protocol::dex::ServerInfo;

pub(crate) const MINIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION: u32 = 1;
pub(crate) const MAXIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION: u32 = 1;
pub(crate) const SDK_VERSION: &str = env!("CARGO_PKG_VERSION");

pub(crate) fn negotiate_server_protocol(server_info: &ServerInfo) -> Result<u32, String> {
    let server_minimum = server_info.minimum_supported_protocol_version;
    let server_current = server_info.current_protocol_version;
    if MINIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION == 0
        || MAXIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION == 0
        || MINIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION > MAXIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION
    {
        return Err(compatibility_error(
            server_info,
            "Rust SDK protocol interval is invalid",
        ));
    }
    if server_minimum == 0 || server_current == 0 || server_minimum > server_current {
        return Err(compatibility_error(
            server_info,
            "Server protocol interval is invalid",
        ));
    }
    let negotiated = server_current.min(MAXIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION);
    if negotiated < server_minimum || negotiated < MINIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION {
        return Err(compatibility_error(
            server_info,
            "protocol intervals do not overlap",
        ));
    }
    Ok(negotiated)
}

pub(crate) fn server_info_request_error(error: impl std::fmt::Display) -> String {
    format!(
        "Rust SDK version \"{SDK_VERSION}\" protocol \
         [{MINIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION},{MAXIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION}] \
         is incompatible with Server version \
         \"unknown\" protocol [unknown,unknown]: GetServerInfo failed: {error}"
    )
}

fn compatibility_error(server_info: &ServerInfo, reason: &str) -> String {
    let server_version = if server_info.server_version.is_empty() {
        "unknown"
    } else {
        &server_info.server_version
    };
    format!(
        "Rust SDK version \"{SDK_VERSION}\" protocol \
         [{MINIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION},{MAXIMUM_SUPPORTED_SERVER_PROTOCOL_VERSION}] \
         is incompatible with Server version \"{server_version}\" protocol \
         [{},{}]: {reason}",
        server_info.minimum_supported_protocol_version, server_info.current_protocol_version
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn negotiates_highest_common_protocol() {
        let negotiated = negotiate_server_protocol(&ServerInfo {
            server_version: "newer".to_string(),
            minimum_supported_protocol_version: 1,
            current_protocol_version: 2,
        })
        .expect("compatible interval");
        assert_eq!(negotiated, 1);
    }

    #[test]
    fn rejects_invalid_and_disjoint_server_intervals() {
        for server_info in [
            ServerInfo {
                server_version: "zero".to_string(),
                minimum_supported_protocol_version: 0,
                current_protocol_version: 0,
            },
            ServerInfo {
                server_version: "reversed".to_string(),
                minimum_supported_protocol_version: 2,
                current_protocol_version: 1,
            },
            ServerInfo {
                server_version: "future".to_string(),
                minimum_supported_protocol_version: 2,
                current_protocol_version: 2,
            },
        ] {
            let error = negotiate_server_protocol(&server_info).expect_err("incompatible interval");
            assert!(error.contains("Rust SDK version"));
            assert!(error.contains(&server_info.server_version));
        }
    }

    #[test]
    fn runtime_version_matches_release() {
        let expected = std::env::var("DEX_EXPECTED_SDK_VERSION")
            .unwrap_or_else(|_| env!("CARGO_PKG_VERSION").to_string());
        assert_eq!(SDK_VERSION, expected);
    }
}
