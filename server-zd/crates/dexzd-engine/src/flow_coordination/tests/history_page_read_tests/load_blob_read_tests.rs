// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! LoadBlobs of values that offload deleted locally: object storage, the read cache, the entry
//! limit, and races with purges.

use std::collections::BTreeSet;

use dexzd_apply::read_model::{OffloadedValueLocation, locate_offloaded_value};
use dexzd_keyspace::row_keys::run_named_key;
use dexzd_keyspace::{ValueId, ValueReference};
use dexzd_protocol::dex::{LoadBlobRequestEntry, LoadBlobsRequest};
use dexzd_protocol::dexzd::v1::{AttributeRecord, stored_value};

use super::*;

/// The grace period of superseded values, by command time.
const GRACE_MICROS: i64 = 10 * 60 * 1_000_000;
const VALUE_BYTES: usize = 300;

impl OffloadedRun {
    pub(super) fn set_text(&mut self, key: &str, text: String) {
        self.request_count += 1;
        let request_id = format!("{key}-{}", self.request_count);
        let outcome = self.apply(Command::AttributesSet(AttributesSetCommand {
            request: Some(SetAttributesRequest {
                attributes: vec![AttributeWrite {
                    key: key.to_owned(),
                    value: Some(Value {
                        kind: Some(value::Kind::StringValue(text)),
                    }),
                    ..AttributeWrite::default()
                }],
                request_id: request_id.clone(),
                ..SetAttributesRequest::default()
            }),
            hard_delete_objects: Vec::new(),
            request_fingerprint: request_id.into_bytes(),
        }));
        assert!(
            matches!(outcome.reply.reply, Some(command_reply::Reply::Ok(_))),
            "{:?}",
            outcome.reply
        );
    }

    pub(super) fn profile_value_id(&self) -> ValueId {
        use dexzd_apply::FlowStateReader;
        let key = run_named_key(
            self.context.flow_hash,
            self.run_id,
            RunTable::Attribute,
            "profile",
        );
        let bytes = self
            .store
            .view()
            .get(ColumnFamily::Flow, &key)
            .expect("the memory view reads")
            .expect("the Attribute exists");
        let record = AttributeRecord::decode(bytes.as_slice()).expect("the Attribute decodes");
        let Some(stored_value::Kind::TableValue(reference)) = record.value.and_then(|v| v.kind)
        else {
            panic!("the profile is a table value");
        };
        let value_id = reference.value_id.expect("table values have an ID");
        ValueId {
            run_seq: value_id.run_seq,
            index_in_command: value_id.index_in_command,
        }
    }

    pub(super) fn reference(&self, value_id: ValueId) -> String {
        ValueReference {
            run_id: self.run_id,
            value_id,
        }
        .to_string()
    }

    pub(super) fn locate(&self, value_id: ValueId) -> OffloadedValueLocation {
        locate_offloaded_value(
            &self.store.view(),
            self.context.flow_hash,
            self.run_id,
            value_id,
        )
        .expect("the location reads")
    }
}

/// A run that rewrote its profile `rewrites` times with segments of `segment_target_bytes`;
/// every superseded profile is offloaded. Returns the run and the offloaded values with their
/// texts, oldest first.
async fn offloaded_profiles(
    rewrites: usize,
    segment_target_bytes: u64,
) -> (OffloadedRun, Vec<(String, String)>) {
    let mut run = OffloadedRun::start(offloading_settings(segment_target_bytes));
    let offloaded = offload_profiles(&mut run, rewrites).await;
    (run, offloaded)
}

pub(super) fn offloading_settings(segment_target_bytes: u64) -> RunSettings {
    RunSettings {
        history_segment_target_bytes: segment_target_bytes,
        wire_inline_threshold_bytes: 100,
        ..default_run_settings()
    }
}

/// Rewrites the run's profile `rewrites` times and lets offload delete every superseded
/// profile locally. Returns the offloaded value references with their texts, oldest first.
pub(super) async fn offload_profiles(
    run: &mut OffloadedRun,
    rewrites: usize,
) -> Vec<(String, String)> {
    let mut superseded = Vec::new();
    for round in 0..=rewrites {
        let text = format!("profile {round} {}", "x".repeat(VALUE_BYTES));
        run.set_text("profile", text.clone());
        superseded.push((run.profile_value_id(), text));
    }
    superseded.pop();
    for round in 0..3 {
        run.set_text("note", format!("note {round}"));
    }
    run.upload_all().await;
    run.now_micros += GRACE_MICROS;
    run.set_text("note", "after the grace period".to_owned());
    run.upload_all().await;
    superseded
        .into_iter()
        .filter(|(value_id, _)| {
            matches!(run.locate(*value_id), OffloadedValueLocation::Uploaded(_))
        })
        .map(|(value_id, text)| (run.reference(value_id), text))
        .collect()
}

fn blob_request(references: impl IntoIterator<Item = String>) -> LoadBlobsRequest {
    LoadBlobsRequest {
        entries: references
            .into_iter()
            .map(|reference| LoadBlobRequestEntry {
                flow_id: FLOW_ID.to_owned(),
                blob_value: Some(Value {
                    kind: Some(value::Kind::InternalBlobIdForStringValue(reference)),
                }),
            })
            .collect(),
    }
}

fn text_of(value: &Value) -> &str {
    match &value.kind {
        Some(value::Kind::StringValue(text)) => text,
        other => panic!("not a string value: {other:?}"),
    }
}

fn blob_reads(metrics: &NodeMetrics, source: HistoryReadSource) -> u64 {
    metrics.history_reads.count(HistoryReadKind::Blob, source)
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn offloaded_values_come_from_object_storage_then_from_the_read_cache() {
    let (run, offloaded) = offloaded_profiles(8, 2_000).await;
    assert!(offloaded.len() >= 4, "{offloaded:?}");
    let segments: BTreeSet<u64> = offloaded
        .iter()
        .map(|(reference, _)| {
            let reference: ValueReference = reference.parse().expect("a value reference");
            match run.locate(reference.value_id) {
                OffloadedValueLocation::Uploaded(segment) => segment.first_seq,
                OffloadedValueLocation::Absent => panic!("{reference} is offloaded"),
            }
        })
        .collect();
    assert!(
        segments.len() < offloaded.len(),
        "some segment holds several values: {segments:?}"
    );
    let live = run.reference(run.profile_value_id());
    let cache_directory = tempfile::tempdir().expect("a temporary directory");
    let config = cache_config(cache_directory.path(), 1 << 30);
    let metrics = Arc::new(NodeMetrics::unregistered());
    let harness = run.harness(run.reader(&config, metrics.clone()));
    let request = blob_request(
        offloaded
            .iter()
            .map(|(reference, _)| reference.clone())
            .chain([live.clone()]),
    );

    let response = harness
        .registry
        .load_blobs(TENANT, request.clone())
        .await
        .expect("the values load");
    assert_eq!(response.values.len(), offloaded.len() + 1);
    for (reference, text) in &offloaded {
        assert_eq!(text_of(&response.values[reference]), text);
    }
    assert!(response.values.contains_key(&live));
    assert_eq!(blob_reads(&metrics, HistoryReadSource::Local), 1);
    assert_eq!(
        blob_reads(&metrics, HistoryReadSource::ObjectStore),
        (offloaded.len() + segments.len()) as u64,
        "each value, plus each segment header"
    );
    assert_eq!(
        run.objects.history_reads(),
        2 * segments.len() as u64,
        "one header read and one value read per segment"
    );

    let again = harness
        .registry
        .load_blobs(TENANT, request.clone())
        .await
        .expect("the values load again");
    assert_eq!(again, response);
    assert_eq!(
        blob_reads(&metrics, HistoryReadSource::Cache),
        offloaded.len() as u64
    );
    assert_eq!(run.objects.history_reads(), 2 * segments.len() as u64);

    // A restarted node keeps the disk cache.
    let restarted_metrics = Arc::new(NodeMetrics::unregistered());
    let restarted = run.harness(run.reader(&config, restarted_metrics.clone()));
    let after_restart = restarted
        .registry
        .load_blobs(TENANT, request)
        .await
        .expect("the values load after a restart");
    assert_eq!(after_restart, response);
    assert_eq!(
        blob_reads(&restarted_metrics, HistoryReadSource::Cache),
        offloaded.len() as u64
    );
    assert_eq!(run.objects.history_reads(), 2 * segments.len() as u64);
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn a_value_whose_segment_is_not_uploaded_reads_as_absent() {
    let (run, offloaded) = offloaded_profiles(3, 1).await;
    let (reference, _) = offloaded.first().expect("a value is offloaded").clone();
    let parsed: ValueReference = reference.parse().expect("a value reference");
    let OffloadedValueLocation::Uploaded(segment) = run.locate(parsed.value_id) else {
        panic!("the value is offloaded");
    };
    // A later state, such as an expired segment, is not UPLOADED.
    let expired = HistorySegmentRecord {
        state: 3,
        ..segment.clone()
    };
    run.store.put(
        ColumnFamily::Flow,
        dexzd_keyspace::row_keys::history_segment_key(
            run.context.flow_hash,
            run.run_id,
            segment.first_seq,
        ),
        expired.encode_to_vec(),
    );
    let harness = run.harness(run.reader(
        &HistoryReadsConfig::default(),
        Arc::new(NodeMetrics::unregistered()),
    ));
    let response = harness
        .registry
        .load_blobs(TENANT, blob_request([reference]))
        .await
        .expect("the request succeeds");
    assert!(response.values.is_empty());
    assert_eq!(run.objects.history_reads(), 0);
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn load_blobs_takes_at_most_a_thousand_entries_only_while_offload_is_on() {
    let (mut run, offloaded) = offloaded_profiles(2, 1).await;
    let (reference, text) = offloaded.first().expect("a value is offloaded").clone();
    let harness = run.harness(run.reader(
        &HistoryReadsConfig::default(),
        Arc::new(NodeMetrics::unregistered()),
    ));
    let error = harness
        .registry
        .load_blobs(TENANT, blob_request(vec![reference.clone(); 1001]))
        .await
        .expect_err("1001 entries are too many while offload is on");
    assert_eq!(error.grpc_code(), grpc_code::INVALID_ARGUMENT, "{error}");
    let response = harness
        .registry
        .load_blobs(TENANT, blob_request(vec![reference.clone(); 1000]))
        .await
        .expect("1000 entries load");
    assert_eq!(text_of(&response.values[&reference]), text);

    // With offload off, LoadBlobs takes any number of entries, as before.
    let partition = u32::from(run.context.partition.0);
    run.apply(Command::SetHistoryOffloadMode(
        SetHistoryOffloadModeCommand {
            enabled: false,
            partition,
        },
    ));
    let live = run.reference(run.profile_value_id());
    let mut references = vec![reference.clone(); 1500];
    references.push(live.clone());
    let response = harness
        .registry
        .load_blobs(TENANT, blob_request(references))
        .await
        .expect("any number of entries loads while offload is off");
    assert_eq!(text_of(&response.values[&reference]), text);
    assert!(response.values.contains_key(&live));
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn a_slow_value_read_holds_no_committed_view_and_other_reads_proceed() {
    let (run, offloaded) = offloaded_profiles(2, 1).await;
    let (reference, text) = offloaded.first().expect("a value is offloaded").clone();
    let harness = run.harness(run.reader(
        &HistoryReadsConfig::default(),
        Arc::new(NodeMetrics::unregistered()),
    ));
    run.objects.close_gate();
    let registry = harness.registry.clone();
    let request = blob_request([reference.clone()]);
    let slow_read = tokio::spawn(async move { registry.load_blobs(TENANT, request).await });
    wait_for_waiting_read(&run.objects).await;
    assert_eq!(
        harness.committer.live_views.load(Ordering::SeqCst),
        0,
        "no committed view is held while the value read waits"
    );
    tokio::time::timeout(
        Duration::from_secs(5),
        harness.registry.get_flow_summary(
            TENANT,
            GetFlowSummaryRequest {
                flow_id: FLOW_ID.to_owned(),
                ..GetFlowSummaryRequest::default()
            },
        ),
    )
    .await
    .expect("a committed read finishes while the value read waits")
    .expect("the summary reads");
    run.objects.open_gate();
    let response = slow_read
        .await
        .expect("the read task finishes")
        .expect("the value loads");
    assert_eq!(text_of(&response.values[&reference]), text);
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn a_run_purged_while_its_value_is_read_omits_the_value() {
    let (run, offloaded) = offloaded_profiles(2, 1).await;
    let (reference, _) = offloaded.first().expect("a value is offloaded").clone();
    let harness = run.harness(run.reader(
        &HistoryReadsConfig::default(),
        Arc::new(NodeMetrics::unregistered()),
    ));
    run.objects.close_gate();
    let registry = harness.registry.clone();
    let request = blob_request([reference]);
    let read = tokio::spawn(async move { registry.load_blobs(TENANT, request).await });
    wait_for_waiting_read(&run.objects).await;
    // The purge marks the run purging, then deletes its objects.
    let mut record = run.run_record();
    record.is_purging = true;
    run.store.put(
        ColumnFamily::Flow,
        run.run_record_key(),
        record.encode_to_vec(),
    );
    for segment in run.uploaded_segments() {
        run.objects
            .inner
            .delete(&run.object_key(&segment))
            .await
            .expect("the object deletes");
    }
    run.objects.open_gate();
    let response = read
        .await
        .expect("the read task finishes")
        .expect("a purging run's values are omitted");
    assert!(response.values.is_empty(), "{response:?}");
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn an_uploaded_value_missing_from_object_storage_is_unavailable() {
    let (run, offloaded) = offloaded_profiles(2, 1).await;
    let (reference, _) = offloaded.first().expect("a value is offloaded").clone();
    for segment in run.uploaded_segments() {
        run.objects
            .inner
            .delete(&run.object_key(&segment))
            .await
            .expect("the object deletes");
    }
    let harness = run.harness(run.reader(
        &HistoryReadsConfig::default(),
        Arc::new(NodeMetrics::unregistered()),
    ));
    let error = harness
        .registry
        .load_blobs(TENANT, blob_request([reference]))
        .await
        .expect_err("a missing object fails the read");
    assert_eq!(error.grpc_code(), grpc_code::UNAVAILABLE, "{error}");
}
