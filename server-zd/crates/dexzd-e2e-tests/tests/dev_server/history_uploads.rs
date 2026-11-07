// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! With history offload on, the dev server uploads a closed run's sealed history segment to its
//! embedded s3local store and acknowledges it in committed state.

use std::io::Read;
use std::net::SocketAddr;
use std::time::Duration;

use dexzd_history_segment::{decode_history_segment, decode_history_segment_header};
use dexzd_keyspace::{
    ColumnFamily, IndexKind, PartitionId, RunId, RunTable, parse_run_key, partition_index_prefix,
};
use dexzd_protocol::dex::{StepOptions, StopFlowRequest, StopType};
use dexzd_protocol::dexzd::v1::{HistorySegmentRecord, HistorySegmentState, RunRecord};
use dexzd_storage::{LocalReadView, RocksDbLocalStore, RocksDbStoreOptions, ScanDirection};
use prost::Message;
use s3local::{ListObjectsRequest, ObjectReadRequest, Store};
use tokio::io::{AsyncReadExt, AsyncWriteExt};

use super::harness::{DevServer, free_loopback_address, start_pull_flow};
use super::string_value;

const FLOW_ID: &str = "history-upload-flow";
/// The bucket of a dev server's embedded object store.
const DEV_BUCKET: &str = "dex-dev";
/// The partition count of a new `dev` data directory.
const DEV_PARTITION_COUNT: u16 = 32;
const UPLOAD_BUDGET: Duration = Duration::from_secs(30);
const CREATED_UPLOADS: &str = "dexzd_history_uploads_total{outcome=\"created\"} ";

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn a_closed_runs_history_segment_uploads_to_the_embedded_object_store() {
    let metrics = free_loopback_address();
    let overrides = format!("history:\n  offloadEnabled: true\nmetrics:\n  listen: '{metrics}'\n");
    let server = DevServer::start_with_config_overrides(Some(&overrides)).await;
    let mut flow_client = server.flow_client();
    let started = start_pull_flow(
        &mut flow_client,
        FLOW_ID,
        "OffloadedFlow",
        "IdleStep",
        string_value("idle"),
        StepOptions::default(),
    )
    .await;
    let run_id: RunId = started.run_id.parse().expect("the run ID is canonical");
    flow_client
        .stop_flow(StopFlowRequest {
            flow_id: FLOW_ID.to_owned(),
            run_id: started.run_id.clone(),
            stop_type: StopType::Terminate as i32,
            ..StopFlowRequest::default()
        })
        .await
        .expect("StopFlow closes the run");

    let store = server.dev_object_store_for_test();
    let object_key = wait_for_history_object(&store).await;
    assert!(
        object_key.contains(&run_id.to_string()),
        "the key names the run: {object_key}"
    );
    let object = read_object(&store, &object_key);
    let content = decode_history_segment(&object).expect("the uploaded object decodes");
    assert_eq!(content.identity.run_id, run_id);
    assert_eq!(content.identity.first_seq, 1);
    assert!(!content.records.is_empty());
    wait_for_created_uploads(metrics, 1).await;

    let data_directory = server.shutdown_keeping_data().await;
    let local_store = RocksDbLocalStore::open(
        data_directory.path().join("store"),
        RocksDbStoreOptions::default(),
    )
    .expect("the stopped server's store opens");
    let (segments, run_record) = stored_run_history(&local_store, run_id);
    assert_eq!(segments.len(), 1, "{segments:?}");
    let segment = &segments[0];
    assert_eq!(segment.state, HistorySegmentState::Uploaded as i32);
    assert_eq!(segment.last_seq, content.identity.last_seq);
    let layout = decode_history_segment_header(&object, object.len() as u64)
        .expect("the uploaded header decodes");
    assert_eq!(segment.content_checksum, layout.content_checksum().to_vec());
    assert_eq!(segment.object_size_bytes, object.len() as u64);
    assert_eq!(run_record.history_uploaded_through_seq, segment.last_seq);
    for partition in 0..DEV_PARTITION_COUNT {
        let pending = local_store
            .scan_prefix(
                ColumnFamily::PartitionIndex,
                &partition_index_prefix(PartitionId(partition), IndexKind::HistoryUploadPending),
                ScanDirection::Forward,
                1,
            )
            .expect("the index scans");
        assert!(pending.is_empty(), "partition {partition} still uploads");
    }
}

/// Waits until the store holds one history object and returns its key.
async fn wait_for_history_object(store: &Store) -> String {
    let listing = ListObjectsRequest {
        prefix: "history/".to_owned(),
        delimiter: String::new(),
        max_keys: 100,
        continuation_token: None,
        start_after: None,
    };
    let waited = tokio::time::timeout(UPLOAD_BUDGET, async {
        loop {
            let page = store
                .list_objects(DEV_BUCKET, &listing)
                .expect("the history objects can be listed");
            if let Some(object) = page.objects.first() {
                assert_eq!(page.objects.len(), 1, "one run sealed one segment");
                return object.key.clone();
            }
            tokio::time::sleep(Duration::from_millis(20)).await;
        }
    })
    .await;
    waited.unwrap_or_else(|_| panic!("no history object appeared within {UPLOAD_BUDGET:?}"))
}

fn read_object(store: &Store, key: &str) -> Vec<u8> {
    let body = store
        .read_object(DEV_BUCKET, key, &ObjectReadRequest::default())
        .expect("the history object reads");
    let mut bytes = Vec::new();
    body.file
        .take(body.length)
        .read_to_end(&mut bytes)
        .expect("the history object body reads");
    bytes
}

/// Waits until the node reports `count` created history uploads.
async fn wait_for_created_uploads(metrics: SocketAddr, count: u64) {
    let mut last_seen = None;
    let waited = tokio::time::timeout(UPLOAD_BUDGET, async {
        loop {
            last_seen = scrape_counter(metrics, CREATED_UPLOADS).await;
            if last_seen == Some(count) {
                return;
            }
            tokio::time::sleep(Duration::from_millis(20)).await;
        }
    })
    .await;
    waited.unwrap_or_else(|_| {
        panic!("the node did not report {count} created uploads; last saw {last_seen:?}")
    });
}

/// Returns the sample of the line that starts with `series`, or `None` while the listener is down.
async fn scrape_counter(metrics: SocketAddr, series: &str) -> Option<u64> {
    let mut stream = tokio::net::TcpStream::connect(metrics).await.ok()?;
    stream
        .write_all(b"GET /metrics HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n")
        .await
        .ok()?;
    let mut response = Vec::new();
    stream.read_to_end(&mut response).await.ok()?;
    String::from_utf8(response)
        .ok()?
        .lines()
        .find_map(|line| line.strip_prefix(series))
        .and_then(|value| value.trim().parse().ok())
}

/// Returns the run's history segment rows and its run record from a stopped node's store.
fn stored_run_history(
    local_store: &RocksDbLocalStore,
    run_id: RunId,
) -> (Vec<HistorySegmentRecord>, RunRecord) {
    let mut segments = Vec::new();
    let mut run_record = None;
    for (key, value) in local_store
        .scan(
            ColumnFamily::Flow,
            None,
            None,
            ScanDirection::Forward,
            usize::MAX,
        )
        .expect("the Flow rows scan")
    {
        let Ok(parsed) = parse_run_key(&key) else {
            continue;
        };
        if parsed.run_id != run_id {
            continue;
        }
        match parsed.table {
            RunTable::HistorySegment => segments.push(
                HistorySegmentRecord::decode(value.as_slice()).expect("the segment row decodes"),
            ),
            RunTable::RunRecord => {
                run_record =
                    Some(RunRecord::decode(value.as_slice()).expect("the run record decodes"));
            }
            _ => {}
        }
    }
    (segments, run_record.expect("the run record is stored"))
}
