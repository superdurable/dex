// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! History offload across an object storage outage and a takeover: commands commit while every
//! history upload fails, and a waiting standby that restores a snapshot finishes the uploads.

use std::fs;
use std::net::SocketAddr;
use std::time::Duration;

use dexzd_history_segment::{decode_history_segment, history_segment_object_key};
use dexzd_keyspace::{
    ColumnFamily, IndexKind, PartitionId, RunTable, TenantId, parse_run_key, partition_index_prefix,
};
use dexzd_protocol::dex::flow_service_client::FlowServiceClient;
use dexzd_protocol::dexzd::v1::{HistorySegmentRecord, HistorySegmentState};
use dexzd_storage::{LocalReadView, RocksDbLocalStore, RocksDbStoreOptions, ScanDirection};
use prost::Message;
use s3local_server::SignatureVerification;
use tempfile::TempDir;

use super::harness::{S3Emulator, ServeConfig, ServeProcess, free_loopback_address};
use super::metrics::try_scrape;
use super::{
    HELD_LEASE_DURATION, PRIMARY_BUCKET, PRIMARY_PREFIX, SHUTDOWN_BUDGET, run_greeting_flow,
    start_greeting_worker,
};

const FLOW_COUNT: usize = 4;
/// Matches `cluster.partitionCount` of [`ServeConfig`].
const PARTITION_COUNT: u16 = 16;
const SNAPSHOT_BUDGET: Duration = Duration::from_secs(30);
const STANDBY_WAIT_BUDGET: Duration = Duration::from_secs(30);
const TAKEOVER_BUDGET: Duration = Duration::from_secs(15);
const UPLOAD_BUDGET: Duration = Duration::from_secs(60);
const LEASE_WAIT_LOG_LINE: &str = "another process holds the node lease";
const UPLOAD_FAILURE_LOG_LINE: &str = "uploading a history segment failed; retrying";
/// Upload outcomes counted once the acknowledgment committed.
const ACKNOWLEDGED_OUTCOMES: [&str; 3] = ["created", "already_present", "replaced"];

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn a_standby_takeover_finishes_uploads_that_failed_on_the_previous_owner() {
    let primary = S3Emulator::start(PRIMARY_BUCKET, SignatureVerification::Strict).await;
    let history_prefix = format!("{PRIMARY_PREFIX}/history/");
    primary
        .set_fault_rules(&format!(
            r#"[{{"operation":"PutObject","key_prefix":"{history_prefix}","action":{{"type":"error","status":503}}}}]"#
        ))
        .await;
    let primary_endpoint = primary.endpoint();
    let directory = TempDir::new().expect("a temporary test directory can be created");
    let serve_config = |name: &str, listen, metrics_listen: SocketAddr| {
        let path = directory.path().join(format!("{name}.yaml"));
        ServeConfig {
            primary_endpoint: &primary_endpoint,
            hard_delete_endpoint: None,
            listen,
            local_directory: &directory.path().join(format!("{name}-store")),
            lease_duration: HELD_LEASE_DURATION,
            fail_if_held: false,
            parade_db: None,
        }
        .write_with_snapshot_schedule(&path, "1s", "1h");
        let mut contents = fs::read_to_string(&path).expect("the serve configuration is readable");
        contents.push_str(&format!(
            "history:\n  offloadEnabled: true\nmetrics:\n  listen: '{metrics_listen}'\n"
        ));
        fs::write(&path, contents).expect("the serve configuration is written");
        path
    };

    let serving_address = free_loopback_address();
    let mut serving = ServeProcess::spawn(
        &serve_config("serving", serving_address, free_loopback_address()),
        serving_address,
        directory.path().join("serving.log"),
    );
    let channel = serving.wait_until_serving().await;
    let worker = start_greeting_worker(channel.clone()).await;
    let mut flow_client = FlowServiceClient::new(channel);
    for index in 0..FLOW_COUNT {
        run_greeting_flow(&mut flow_client, &format!("offload-flow-{index}")).await;
    }
    assert!(
        primary
            .keys_under(PRIMARY_BUCKET, &history_prefix)
            .is_empty(),
        "every history upload failed, yet each Flow completed"
    );
    wait_for_snapshot(&primary).await;
    serving
        .wait_for_log_line(UPLOAD_FAILURE_LOG_LINE, UPLOAD_BUDGET)
        .await;

    let standby_address = free_loopback_address();
    let standby_metrics = free_loopback_address();
    let mut standby = ServeProcess::spawn(
        &serve_config("standby", standby_address, standby_metrics),
        standby_address,
        directory.path().join("standby.log"),
    );
    standby
        .wait_for_log_line(LEASE_WAIT_LOG_LINE, STANDBY_WAIT_BUDGET)
        .await;
    let serving_status = serving.terminate(SHUTDOWN_BUDGET).await;
    assert_eq!(serving_status.code(), Some(0), "{}", serving.log());
    worker.stop().await;
    primary.clear_fault_rules().await;
    tokio::time::timeout(TAKEOVER_BUDGET, standby.wait_until_serving())
        .await
        .unwrap_or_else(|_| panic!("the standby did not take over:\n{}", standby.log()));

    let waited = tokio::time::timeout(UPLOAD_BUDGET, async {
        while acknowledged_uploads(standby_metrics).await < FLOW_COUNT as u64 {
            tokio::time::sleep(Duration::from_millis(50)).await;
        }
    })
    .await;
    waited.unwrap_or_else(|_| {
        panic!(
            "the new owner did not acknowledge {FLOW_COUNT} uploads:\n{}",
            standby.log()
        )
    });
    let standby_status = standby.terminate(SHUTDOWN_BUDGET).await;
    assert_eq!(standby_status.code(), Some(0), "{}", standby.log());

    let local_store = RocksDbLocalStore::open(
        directory.path().join("standby-store"),
        RocksDbStoreOptions::default(),
    )
    .expect("the stopped standby's store opens");
    let expected_keys = assert_every_segment_uploaded(&local_store, &primary);
    assert_eq!(
        expected_keys.len(),
        FLOW_COUNT,
        "one closed run, one segment"
    );
    assert_eq!(
        primary.keys_under(PRIMARY_BUCKET, &history_prefix),
        expected_keys,
        "one object per segment and no other object"
    );
    primary.shutdown().await;
}

/// Returns the uploads whose acknowledgment committed on the node, or 0 while its metrics
/// listener is down.
async fn acknowledged_uploads(metrics: SocketAddr) -> u64 {
    let Ok(body) = try_scrape(metrics, "/metrics").await else {
        return 0;
    };
    ACKNOWLEDGED_OUTCOMES
        .iter()
        .filter_map(|outcome| {
            let series = format!("dexzd_history_uploads_total{{outcome=\"{outcome}\"}} ");
            body.lines()
                .find_map(|line| line.strip_prefix(series.as_str()))
                .and_then(|value| value.trim().parse::<u64>().ok())
        })
        .sum()
}

/// Waits until the serving node published a snapshot, which the standby restores at takeover.
async fn wait_for_snapshot(primary: &S3Emulator) {
    let snapshot_prefix = format!("{PRIMARY_PREFIX}/snapshot/");
    tokio::time::timeout(SNAPSHOT_BUDGET, async {
        while !primary.has_object_under(PRIMARY_BUCKET, &snapshot_prefix) {
            tokio::time::sleep(Duration::from_millis(50)).await;
        }
    })
    .await
    .unwrap_or_else(|_| panic!("no snapshot was published within {SNAPSHOT_BUDGET:?}"));
}

/// Checks that every history segment of the stopped node is UPLOADED with an object whose
/// content checksum it records, and that no run waits for an upload. Returns the object keys.
fn assert_every_segment_uploaded(
    local_store: &RocksDbLocalStore,
    primary: &S3Emulator,
) -> Vec<String> {
    let mut keys = Vec::new();
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
        if parsed.table != RunTable::HistorySegment {
            continue;
        }
        let segment =
            HistorySegmentRecord::decode(value.as_slice()).expect("the segment row decodes");
        assert_eq!(
            segment.state,
            HistorySegmentState::Uploaded as i32,
            "{segment:?}"
        );
        let object_key = format!(
            "{PRIMARY_PREFIX}/{}",
            history_segment_object_key(
                TenantId::DEFAULT,
                parsed.flow_hash,
                parsed.run_id,
                segment.first_seq,
                segment.last_seq,
            )
        );
        let object = primary.read_object(PRIMARY_BUCKET, &object_key);
        let content = decode_history_segment(&object).expect("the uploaded object decodes");
        assert_eq!(content.identity.run_id, parsed.run_id);
        assert_eq!(content.identity.last_seq, segment.last_seq);
        assert_eq!(segment.object_size_bytes, object.len() as u64);
        keys.push(object_key);
    }
    for partition in 0..PARTITION_COUNT {
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
    keys.sort();
    keys
}
