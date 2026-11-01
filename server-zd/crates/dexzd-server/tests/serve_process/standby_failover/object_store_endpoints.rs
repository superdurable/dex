// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Object storage endpoints that share one emulator store, so a fault reaches only one process.

use std::net::SocketAddr;
use std::sync::Arc;
use std::time::Duration;

use s3mock::Store;
use s3mock_server::{ServerConfig, ServerHandle, SignatureVerification};

use crate::PRIMARY_PREFIX;
use crate::harness::s3mock_admin_request;

/// Longer than the server's 30 s object storage request timeout, so every held request times out.
const TIMEOUT_HOLD: Duration = Duration::from_secs(35);
/// The longest delay s3mock accepts; the held requests run only when the fault is cleared.
const BLACKHOLE_HOLD: Duration = Duration::from_secs(600);
/// Operations that change object storage; a standby sends none of them.
const WRITE_OPERATIONS: [&str; 8] = [
    "PutObject",
    "CopyObject",
    "DeleteObject",
    "DeleteObjects",
    "CreateMultipartUpload",
    "UploadPart",
    "CompleteMultipartUpload",
    "AbortMultipartUpload",
];
/// A completed request that took at least this long was held by a fault.
const HELD_REQUEST_DURATION: Duration = Duration::from_secs(1);

/// A fault that one endpoint applies to every S3 operation until it is cleared.
#[derive(Clone, Copy, Debug)]
pub(crate) enum ObjectStoreFault {
    /// Delays every request by this long before s3mock handles it.
    Latency(Duration),
    /// Delays only requests for node log objects, so lease reads and writes stay fast.
    LogLatency(Duration),
    /// Delays only requests for hard-delete Attribute objects, such as a GetAttributes read.
    HardDeleteLatency(Duration),

    /// Fails each request with 503 SlowDown with this probability, before it reaches the store.
    ServiceUnavailable { probability: f64 },
    /// Holds every request past the client's timeout; s3mock then drops it unhandled.
    Timeouts,
    /// Holds requests until cleared; clearing runs only those whose connection stayed open.
    Blackhole,
}

impl ObjectStoreFault {
    /// Returns the s3mock fault rules that implement this fault.
    fn rules(self) -> String {
        let action = match self {
            Self::Latency(delay) => delay_action(delay),
            Self::LogLatency(delay) => return key_prefix_delay_rules("log/", delay),
            Self::HardDeleteLatency(delay) => {
                return key_prefix_delay_rules("hard-delete/", delay);
            }

            Self::ServiceUnavailable { probability } => {
                assert!(
                    (0.0..=1.0).contains(&probability),
                    "a probability is between 0 and 1, got {probability}"
                );
                return format!(
                    r#"[{{"probability":{probability},"action":{{"type":"error","status":503}}}}]"#
                );
            }
            Self::Timeouts => delay_action(TIMEOUT_HOLD),
            Self::Blackhole => delay_action(BLACKHOLE_HOLD),
        };
        format!(r#"[{{"action":{action}}}]"#)
    }
}

/// Returns rules that delay only requests for keys under `key_prefix` in the primary prefix.
fn key_prefix_delay_rules(key_prefix: &str, delay: Duration) -> String {
    format!(
        r#"[{{"key_prefix":"{PRIMARY_PREFIX}/{key_prefix}","action":{}}}]"#,
        delay_action(delay)
    )
}

fn delay_action(delay: Duration) -> String {
    format!(r#"{{"type":"delay","milliseconds":{}}}"#, delay.as_millis())
}

/// One s3mock server over the shared emulator store with its own fault rules and request counters.
pub(crate) struct ObjectStoreEndpoint {
    server: ServerHandle,
}

impl ObjectStoreEndpoint {
    /// Serves `store` on a free loopback port without signature checks, like the shared emulator.
    pub(crate) async fn start(store: Arc<Store>) -> Self {
        let listener = tokio::net::TcpListener::bind(SocketAddr::from(([127, 0, 0, 1], 0)))
            .await
            .expect("the endpoint binds a loopback port");
        let server = s3mock_server::start_with_config(
            store,
            listener,
            ServerConfig {
                signature_verification: SignatureVerification::Off,
                ..ServerConfig::default()
            },
        )
        .expect("the endpoint starts");
        Self { server }
    }

    pub(crate) fn endpoint(&self) -> String {
        format!("http://{}", self.server.local_address())
    }

    /// Replaces this endpoint's faults with `fault`; other endpoints keep theirs.
    pub(crate) async fn inject(&self, fault: ObjectStoreFault) {
        let response = s3mock_admin_request(
            self.server.local_address(),
            "PUT",
            "/_s3mock/faults",
            &format!(r#"{{"rules":{}}}"#, fault.rules()),
        )
        .await;
        assert!(response.starts_with("HTTP/1.1 200"), "{response}");
    }

    /// Removes this endpoint's faults and releases the requests a delay still holds.
    pub(crate) async fn clear_faults(&self) {
        let response =
            s3mock_admin_request(self.server.local_address(), "DELETE", "/_s3mock/faults", "")
                .await;
        assert!(response.starts_with("HTTP/1.1 200"), "{response}");
    }

    /// Returns how many write requests this endpoint finished so far, failed ones included.
    pub(crate) async fn write_requests(&self) -> u64 {
        let response =
            s3mock_admin_request(self.server.local_address(), "GET", "/_s3mock/counters", "").await;
        let (_, body) = response
            .split_once("\r\n\r\n")
            .unwrap_or_else(|| panic!("the counters have a body: {response}"));
        let counters: serde_json::Value =
            serde_json::from_str(body).unwrap_or_else(|error| panic!("{error}: {body}"));
        WRITE_OPERATIONS
            .iter()
            .filter_map(|operation| {
                counters["counters"]["by_operation"][operation]["requests"].as_u64()
            })
            .sum()
    }

    /// Returns the key and status of every PUT that a fault held and that then reached the store.
    pub(crate) async fn held_puts_that_completed(&self) -> Vec<(String, u64)> {
        let response =
            s3mock_admin_request(self.server.local_address(), "GET", "/_s3mock/requests", "").await;
        let (_, body) = response
            .split_once("\r\n\r\n")
            .unwrap_or_else(|| panic!("the request log has a body: {response}"));
        let log: serde_json::Value =
            serde_json::from_str(body).unwrap_or_else(|error| panic!("{error}: {body}"));
        let held_milliseconds =
            u64::try_from(HELD_REQUEST_DURATION.as_millis()).expect("the hold fits in u64");
        log["requests"]
            .as_array()
            .expect("the request log lists requests")
            .iter()
            .filter(|request| {
                request["operation"] == "PutObject"
                    && request["injected_fault"] == "delay"
                    && request["duration_ms"]
                        .as_u64()
                        .is_some_and(|duration| duration >= held_milliseconds)
            })
            .map(|request| {
                (
                    request["key"].as_str().unwrap_or_default().to_owned(),
                    request["status"].as_u64().unwrap_or_default(),
                )
            })
            .collect()
    }

    /// Clears the faults, so held requests finish, then stops serving.
    pub(crate) async fn shutdown(self) {
        self.clear_faults().await;
        self.server.shutdown().await;
    }
}
