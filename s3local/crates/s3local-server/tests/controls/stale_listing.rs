// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! The `stale_listing` fault serves cached listings per prefix, as Tencent COS does.

use std::time::Duration;

use serde_json::json;

use super::support::RunningServer;

const LONG_TTL_MILLISECONDS: u64 = 600_000;

struct Listing {
    keys: Vec<String>,
    is_truncated: bool,
}

async fn list(
    server: &RunningServer,
    prefix: &str,
    start_after: Option<&str>,
    max_keys: i32,
) -> Listing {
    let output = server
        .client
        .list_objects_v2()
        .bucket("testing")
        .prefix(prefix)
        .set_start_after(start_after.map(str::to_owned))
        .max_keys(max_keys)
        .send()
        .await
        .expect("the listing succeeds");
    Listing {
        keys: output
            .contents()
            .iter()
            .map(|object| object.key().expect("listed objects have keys").to_owned())
            .collect(),
        is_truncated: output.is_truncated().unwrap_or(false),
    }
}

async fn stale_listing_rule(server: &RunningServer, ttl_milliseconds: u64) {
    server
        .rules(json!([{
            "operation": "ListObjectsV2",
            "action": {"type": "stale_listing", "ttl_milliseconds": ttl_milliseconds},
        }]))
        .await;
}

#[tokio::test]
async fn stale_listing_misses_new_keys_and_keeps_deleted_keys_per_prefix() {
    let server = RunningServer::start(&[]).await;
    server.put("stream/1", "one").await;
    stale_listing_rule(&server, LONG_TTL_MILLISECONDS).await;
    assert_eq!(
        list(&server, "stream/", None, 1000).await.keys,
        ["stream/1"]
    );

    server.put("stream/2", "two").await;
    assert_eq!(
        list(&server, "stream/", None, 1000).await.keys,
        ["stream/1"],
        "a new key is missing from the cached listing"
    );
    assert_eq!(
        list(&server, "stream/2", None, 1000).await.keys,
        ["stream/2"],
        "another prefix string is listed fresh"
    );

    server
        .client
        .delete_object()
        .bucket("testing")
        .key("stream/1")
        .send()
        .await
        .expect("the delete succeeds");
    assert_eq!(
        list(&server, "stream/", None, 1000).await.keys,
        ["stream/1"],
        "a deleted key is still listed"
    );
    server.server.shutdown().await;
}

#[tokio::test]
async fn stale_listing_ignores_max_keys_and_start_after() {
    let server = RunningServer::start(&[]).await;
    for key in ["page/1", "page/2", "page/3"] {
        server.put(key, "batch").await;
    }
    stale_listing_rule(&server, LONG_TTL_MILLISECONDS).await;
    let first = list(&server, "page/", None, 1).await;
    assert_eq!(first.keys, ["page/1"]);
    assert!(first.is_truncated);

    let full = list(&server, "page/", None, 1000).await;
    assert_eq!(
        full.keys,
        ["page/1"],
        "a stale page has fewer keys than max-keys"
    );
    assert!(full.is_truncated, "the stale page still reports truncation");

    let next = list(&server, "page/", Some("page/1"), 1000).await;
    assert_eq!(next.keys, ["page/1"], "start-after is ignored");
    server.server.shutdown().await;
}

#[tokio::test]
async fn stale_listing_expires_after_its_ttl_and_resets_with_the_rules() {
    let server = RunningServer::start(&[]).await;
    stale_listing_rule(&server, 200).await;
    assert!(list(&server, "expiry/", None, 1000).await.keys.is_empty());
    server.put("expiry/1", "one").await;
    assert!(list(&server, "expiry/", None, 1000).await.keys.is_empty());
    // The TTL is the behavior under test, so the test waits for it in real time.
    tokio::time::sleep(Duration::from_millis(300)).await;
    assert_eq!(
        list(&server, "expiry/", None, 1000).await.keys,
        ["expiry/1"]
    );

    stale_listing_rule(&server, LONG_TTL_MILLISECONDS).await;
    assert_eq!(list(&server, "reset/", None, 1000).await.keys.len(), 0);
    server.put("reset/1", "one").await;
    server.admin("DELETE", "faults", json!(null)).await;
    assert_eq!(
        list(&server, "reset/", None, 1000).await.keys,
        ["reset/1"],
        "deleting the rules drops cached listings"
    );
    stale_listing_rule(&server, LONG_TTL_MILLISECONDS).await;
    server.put("reset/2", "two").await;
    assert_eq!(
        list(&server, "reset/", None, 1000).await.keys,
        ["reset/1", "reset/2"],
        "replacing the rules starts with an empty cache"
    );
    server.server.shutdown().await;
}

#[tokio::test]
async fn stale_listing_requires_list_objects_and_a_bounded_ttl() {
    let server = RunningServer::start(&[]).await;
    for rule in [
        json!({"action": {"type": "stale_listing", "ttl_milliseconds": 1000}}),
        json!({"operation": "GetObject", "action": {"type": "stale_listing", "ttl_milliseconds": 1000}}),
        json!({"operation": "ListObjectsV2", "action": {"type": "stale_listing", "ttl_milliseconds": 0}}),
        json!({"operation": "ListObjectsV2", "action": {"type": "stale_listing", "ttl_milliseconds": 600_001}}),
    ] {
        let (status, body) = server
            .admin("PUT", "faults", json!({"rules": [rule]}))
            .await;
        assert_eq!(status, 400, "{body}");
    }
    let log = {
        stale_listing_rule(&server, LONG_TTL_MILLISECONDS).await;
        list(&server, "observed/", None, 1000).await;
        server.requests(1).await
    };
    let requests = log["requests"].as_array().expect("requests are an array");
    assert_eq!(
        requests.last().expect("the listing was observed")["injected_fault"],
        "stale_listing"
    );
    server.server.shutdown().await;
}
