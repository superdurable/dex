// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Pages read while uploads continue return every event once, in order.
//!
//! A page holds at most 1000 events.

use std::net::SocketAddr;
use std::time::Duration;

use dexzd_protocol::dex::{
    AttributeWrite, GetHistoryEventsRequest, SetAttributesRequest, StepOptions,
};
use dexzd_storage::{RocksDbLocalStore, RocksDbStoreOptions};
use s3local::ListObjectsRequest;
use tokio::io::{AsyncReadExt, AsyncWriteExt};

use super::harness::{DevServer, free_loopback_address, start_pull_flow};
use super::string_value;

const FLOW_ID: &str = "offloaded-history-pages";
/// The bucket of a dev server's embedded object store.
const DEV_BUCKET: &str = "dex-dev";
const WRITES: usize = 1_100;
const MAX_PAGE_EVENTS: usize = 1_000;
const UPLOAD_BUDGET: Duration = Duration::from_secs(60);
const OBJECT_STORE_PAGE_READS: &str =
    "dexzd_history_reads_total{kind=\"page\",source=\"object_store\"} ";

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn history_pages_read_uploaded_segments_and_local_rows_without_gaps_or_duplicates() {
    let metrics = free_loopback_address();
    let overrides =
        format!("history:\n  segmentTargetBytes: 4096\nmetrics:\n  listen: '{metrics}'\n");
    let server = DevServer::start_with_config_overrides(Some(&overrides)).await;
    let mut flow_client = server.flow_client();
    start_pull_flow(
        &mut flow_client,
        FLOW_ID,
        "OffloadedPagesFlow",
        "IdleStep",
        string_value("idle"),
        StepOptions::default(),
    )
    .await;
    for write in 0..WRITES {
        flow_client
            .set_attributes(SetAttributesRequest {
                flow_id: FLOW_ID.to_owned(),
                attributes: vec![AttributeWrite {
                    key: "counter".to_owned(),
                    value: Some(string_value(&format!("write {write}"))),
                    ..AttributeWrite::default()
                }],
                request_id: format!("write-{write}"),
                ..SetAttributesRequest::default()
            })
            .await
            .expect("SetAttributes commits");
    }

    wait_for_history_objects(&server, 3).await;
    // Uploads keep acknowledging segments while the pages below are read.
    let mut event_ids = Vec::new();
    let mut token = Vec::new();
    loop {
        let page = flow_client
            .get_history_events(GetHistoryEventsRequest {
                flow_id: FLOW_ID.to_owned(),
                estimate_page_size: 5_000,
                next_page_token: token,
                ..GetHistoryEventsRequest::default()
            })
            .await
            .expect("GetHistoryEvents reads a page")
            .into_inner();
        assert!(
            page.events.len() <= MAX_PAGE_EVENTS,
            "a page holds {} events",
            page.events.len()
        );
        assert_eq!(page.pruned_event_id_range, None);
        event_ids.extend(page.events.iter().map(|event| event.event_id));
        token = page.next_page_token;
        if token.is_empty() {
            break;
        }
    }
    let expected: Vec<i64> = (1..=i64::try_from(event_ids.len()).expect("few events")).collect();
    assert_eq!(event_ids, expected, "every event once, in order");
    assert!(event_ids.len() > WRITES);

    wait_for_object_store_page_reads(metrics).await;

    let data_directory = server.shutdown_keeping_data().await;
    let store_directory = data_directory.path().join("store");
    let cache_entries = std::fs::read_dir(data_directory.path().join("store-history-read-cache"))
        .expect("the read cache is a sibling of localStorage.directory")
        .count();
    assert!(cache_entries > 0, "the read cache kept segment parts");
    RocksDbLocalStore::open(&store_directory, RocksDbStoreOptions::default())
        .expect("the local store opens beside its read cache");
}

/// Waits until the embedded store holds at least `count` history objects.
async fn wait_for_history_objects(server: &DevServer, count: usize) {
    let store = server.dev_object_store_for_test();
    let listing = ListObjectsRequest {
        prefix: "history/".to_owned(),
        delimiter: String::new(),
        max_keys: 1000,
        continuation_token: None,
        start_after: None,
    };
    let waited = tokio::time::timeout(UPLOAD_BUDGET, async {
        loop {
            let page = store
                .list_objects(DEV_BUCKET, &listing)
                .expect("the history objects can be listed");
            if page.objects.len() >= count {
                return;
            }
            tokio::time::sleep(Duration::from_millis(20)).await;
        }
    })
    .await;
    waited.unwrap_or_else(|_| panic!("{count} history objects did not appear"));
}

/// Waits until the node reports a history page read that reached object storage.
async fn wait_for_object_store_page_reads(metrics: SocketAddr) {
    let waited = tokio::time::timeout(UPLOAD_BUDGET, async {
        loop {
            let mut flow_reads = None;
            if let Ok(mut stream) = tokio::net::TcpStream::connect(metrics).await
                && stream
                    .write_all(
                        b"GET /metrics HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n",
                    )
                    .await
                    .is_ok()
            {
                let mut response = Vec::new();
                if stream.read_to_end(&mut response).await.is_ok() {
                    flow_reads = String::from_utf8_lossy(&response)
                        .lines()
                        .find_map(|line| line.strip_prefix(OBJECT_STORE_PAGE_READS))
                        .and_then(|value| value.trim().parse::<u64>().ok());
                }
            }
            if flow_reads.is_some_and(|reads| reads > 0) {
                return;
            }
            tokio::time::sleep(Duration::from_millis(50)).await;
        }
    })
    .await;
    waited.unwrap_or_else(|_| panic!("no history page read reached object storage"));
}
