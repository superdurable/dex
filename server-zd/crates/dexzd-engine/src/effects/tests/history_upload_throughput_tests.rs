// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! History upload throughput with 30 ms object storage writes and the real apply state machine:
//! the adaptive limit keeps up with short-run closes, bounds in-flight bytes, backs off on
//! throttling, and the backlog gauge follows the real backlog.

use std::sync::atomic::{AtomicI64, AtomicUsize, Ordering};
use std::sync::{Arc, OnceLock};
use std::time::Duration;

use dexzd_apply::FlowStateReader;
use dexzd_history_segment::HISTORY_SEGMENT_OBJECT_KEY_PREFIX;
use dexzd_keyspace::row_keys::prefix_range;
use dexzd_keyspace::{ColumnFamily, IndexKind, PartitionId, RunId, partition_index_prefix};
use dexzd_object_store::{
    FaultAction, FaultInjector, InMemoryObjectStore, ObjectStore, ObjectStoreOperation,
};
use dexzd_protocol::dex::{AttributeWrite, SetAttributesRequest};
use dexzd_protocol::dexzd::v1::{AttributesSetCommand, FlowCommand, flow_command};
use prometheus_client::metrics::gauge::Gauge;

use super::history_upload_tests::{close_run, enable_history_offload, wait_until};
use super::{DurableState, EffectHarness, is_error_reply, settings, start_request, string_value};
use crate::effects::{EffectsConfig, HistoryUploadLimits};
use crate::flow_coordination::CommitUrgency;

/// One object storage write round trip, as measured against S3.
const PUT_LATENCY: Duration = Duration::from_millis(30);
/// Close rate of the short-run benchmark that outran uploads.
const CLOSES_PER_SECOND: usize = 6_000;
const TICK: Duration = Duration::from_millis(10);
/// Long enough for the limit to grow and settle; the in-memory state limits longer loads.
const LOAD_DURATION: Duration = Duration::from_millis(250);
const RESCAN_INTERVAL: Duration = Duration::from_millis(100);

/// Delays history segment writes like a remote store, throttles a given number of them, and
/// records the largest in-flight byte reservation any write saw.
#[derive(Default)]
struct RemoteStoreFaults {
    throttled_puts: AtomicUsize,
    /// The node's in-flight bytes gauge, set once the node exists.
    in_flight_bytes: OnceLock<Gauge>,
    max_in_flight_bytes_seen: AtomicI64,
}

impl FaultInjector for RemoteStoreFaults {
    fn action(&self, operation: ObjectStoreOperation, key: &str) -> FaultAction {
        if operation != ObjectStoreOperation::Put
            || !key.starts_with(HISTORY_SEGMENT_OBJECT_KEY_PREFIX)
        {
            return FaultAction::Proceed;
        }
        if let Some(in_flight_bytes) = self.in_flight_bytes.get() {
            self.max_in_flight_bytes_seen
                .fetch_max(in_flight_bytes.get(), Ordering::SeqCst);
        }
        let is_throttled = self
            .throttled_puts
            .fetch_update(Ordering::SeqCst, Ordering::SeqCst, |remaining| {
                remaining.checked_sub(1)
            })
            .is_ok();
        if is_throttled {
            // S3 503 SlowDown maps to a transient failure before effect.
            FaultAction::FailBeforeEffect
        } else {
            FaultAction::Delay(PUT_LATENCY)
        }
    }
}

/// Starts a node whose history object store delays every write by [`PUT_LATENCY`].
fn remote_store_harness(limits: HistoryUploadLimits) -> (EffectHarness, Arc<RemoteStoreFaults>) {
    let faults = Arc::new(RemoteStoreFaults::default());
    let durable = DurableState {
        objects: Arc::new(InMemoryObjectStore::with_injector(Arc::clone(&faults) as _)),
        ..DurableState::new()
    };
    let harness = EffectHarness::with_config(
        durable,
        EffectsConfig {
            history_upload_limits: limits,
            history_upload_rescan_interval: RESCAN_INTERVAL,
            ..EffectsConfig::default()
        },
    );
    let is_first_set = faults
        .in_flight_bytes
        .set(harness.metrics.history_upload_in_flight_bytes.clone())
        .is_ok();
    assert!(is_first_set, "each harness has its own faults");
    (harness, faults)
}

/// What one load phase observed.
#[derive(Debug, Default)]
struct LoadObservation {
    closes: u64,
    uploads_at_end: u64,
    backlog_at_end: u64,
    max_backlog: u64,
    max_concurrency_limit: i64,
}

impl LoadObservation {
    fn uploads_per_second(&self, elapsed: Duration) -> f64 {
        self.uploads_at_end as f64 / elapsed.as_secs_f64()
    }
}

/// Closes short runs at [`CLOSES_PER_SECOND`] for `duration` of paused time, checking the backlog
/// gauge against committed state every 100 ms.
async fn close_short_runs_at_benchmark_rate(
    harness: &EffectHarness,
    first_index: usize,
    duration: Duration,
) -> LoadObservation {
    let runs_per_tick =
        CLOSES_PER_SECOND * usize::try_from(TICK.as_millis()).expect("small") / 1000;
    let ticks = usize::try_from(duration.as_millis() / TICK.as_millis()).expect("few ticks");
    let mut observation = LoadObservation::default();
    let mut index = first_index;
    for tick in 0..ticks {
        for _ in 0..runs_per_tick {
            let flow_id = format!("short-{index}");
            let run_id = harness.start_flow(start_request(&flow_id), settings());
            close_run(harness, &flow_id, run_id);
            index += 1;
            observation.closes += 1;
        }
        tokio::time::sleep(TICK).await;
        harness.settle().await;
        let backlog = harness.metrics.history_upload_backlog.segments();
        observation.max_backlog = observation.max_backlog.max(backlog);
        observation.max_concurrency_limit = observation
            .max_concurrency_limit
            .max(harness.metrics.history_upload_concurrency_limit.get());
        if tick % 10 == 9 {
            assert_eq!(
                backlog,
                pending_upload_runs(harness),
                "the backlog gauge equals the committed backlog at tick {tick}"
            );
        }
    }
    observation.backlog_at_end = harness.metrics.history_upload_backlog.segments();
    observation.uploads_at_end = uploads(harness);
    observation
}

/// Returns the runs with a SEALED segment in committed state; each short run seals one segment.
fn pending_upload_runs(harness: &EffectHarness) -> u64 {
    let snapshot = harness.flow_effect_operations.snapshot();
    (0..harness.hasher.partition_count())
        .map(|partition| {
            let partition = PartitionId(u16::try_from(partition).expect("few partitions"));
            let prefix = partition_index_prefix(partition, IndexKind::HistoryUploadPending);
            let (start, end) = prefix_range(&prefix);
            snapshot
                .scan(ColumnFamily::PartitionIndex, &start, &end, usize::MAX)
                .expect("memory reads never fail")
                .len() as u64
        })
        .sum()
}

fn uploads(harness: &EffectHarness) -> u64 {
    harness
        .durable
        .objects
        .call_count(ObjectStoreOperation::Put, HISTORY_SEGMENT_OBJECT_KEY_PREFIX)
}

async fn wait_until_backlog_drains(harness: &EffectHarness) {
    wait_until(harness, "the upload backlog drains", || {
        pending_upload_runs(harness) == 0
    })
    .await;
    assert_eq!(harness.metrics.history_upload_backlog.segments(), 0);
}

#[tokio::test(start_paused = true)]
async fn adaptive_uploads_keep_up_with_short_run_closes_under_put_latency() {
    let (harness, _faults) = remote_store_harness(HistoryUploadLimits::default());
    harness.rebuild_all_partitions().await;
    enable_history_offload(&harness);
    let started = tokio::time::Instant::now();

    let observation = close_short_runs_at_benchmark_rate(&harness, 0, LOAD_DURATION).await;
    let rate = observation.uploads_per_second(started.elapsed());
    println!("adaptive limit: {observation:?}, {rate:.0} uploads/s");

    // 256 writes of 30 ms drain about 8500 segments a second, above the close rate.
    assert!(observation.max_concurrency_limit > 16, "{observation:?}");
    assert!(observation.max_concurrency_limit <= 256, "{observation:?}");
    assert!(observation.max_backlog < 1_000, "{observation:?}");
    assert!(
        observation.backlog_at_end < 400,
        "the backlog stays bounded: {observation:?}"
    );
    wait_until_backlog_drains(&harness).await;
    assert_eq!(uploads(&harness), observation.closes);

    // Each upload that finishes while none waits lowers the limit by one, back to the minimum.
    let limit_after_drain = harness.metrics.history_upload_concurrency_limit.get();
    for index in 0..limit_after_drain - 16 {
        let flow_id = format!("trickle-{index}");
        let run_id = harness.start_flow(start_request(&flow_id), settings());
        close_run(&harness, &flow_id, run_id);
        tokio::time::sleep(Duration::from_millis(50)).await;
    }
    wait_until_backlog_drains(&harness).await;
    assert_eq!(harness.metrics.history_upload_concurrency_limit.get(), 16);
    assert_eq!(harness.metrics.history_upload_in_flight_bytes.get(), 0);
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn a_fixed_limit_of_sixteen_falls_behind_the_same_closes() {
    let (harness, _faults) = remote_store_harness(HistoryUploadLimits {
        max_concurrency: 16,
        min_concurrency: 16,
        ..HistoryUploadLimits::default()
    });
    harness.rebuild_all_partitions().await;
    enable_history_offload(&harness);
    let started = tokio::time::Instant::now();

    let observation = close_short_runs_at_benchmark_rate(&harness, 0, LOAD_DURATION).await;
    let rate = observation.uploads_per_second(started.elapsed());
    println!("fixed limit 16: {observation:?}, {rate:.0} uploads/s");

    // 16 writes of 30 ms drain about 530 segments a second, so the backlog keeps growing.
    assert_eq!(observation.max_concurrency_limit, 16);
    assert!(rate < 700.0, "{rate}");
    assert!(
        observation.backlog_at_end > observation.closes / 2,
        "{observation:?}"
    );
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn throttled_writes_halve_the_upload_limit_then_uploads_recover() {
    let (harness, faults) = remote_store_harness(HistoryUploadLimits::default());
    harness.rebuild_all_partitions().await;
    enable_history_offload(&harness);
    let grown = close_short_runs_at_benchmark_rate(&harness, 0, Duration::from_millis(200)).await;
    let limit_before = harness.metrics.history_upload_concurrency_limit.get();
    assert!(limit_before >= 64, "{grown:?}, limit {limit_before}");

    // Every write in flight at once fails, as a burst of 503 SlowDown replies.
    faults.throttled_puts.store(1_000_000, Ordering::SeqCst);
    let throttled =
        close_short_runs_at_benchmark_rate(&harness, 100_000, Duration::from_millis(30)).await;
    let limit_after = harness.metrics.history_upload_concurrency_limit.get();
    assert!(
        limit_after <= limit_before / 2,
        "throttling lowers the limit: {limit_before} -> {limit_after}, {throttled:?}"
    );
    tokio::time::sleep(Duration::from_secs(5)).await;
    harness.settle().await;
    assert_eq!(
        harness.metrics.history_upload_concurrency_limit.get(),
        16,
        "sustained throttling keeps the minimum"
    );

    faults.throttled_puts.store(0, Ordering::SeqCst);
    wait_until_backlog_drains(&harness).await;
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn large_segments_stay_within_the_in_flight_byte_budget() {
    let budget = 1024 * 1024;
    let (harness, faults) = remote_store_harness(HistoryUploadLimits {
        max_in_flight_bytes: budget,
        ..HistoryUploadLimits::default()
    });
    harness.rebuild_all_partitions().await;
    enable_history_offload(&harness);
    let run_count = 40;
    for index in 0..run_count {
        let flow_id = format!("large-{index}");
        let run_id = harness.start_flow(start_request(&flow_id), settings());
        // The second write supersedes the first, so the closing segment holds both values.
        for round in 0..2 {
            write_attribute(
                &harness,
                &flow_id,
                run_id,
                &noise_text(index * 2 + round, 200_000),
            );
        }
        close_run(&harness, &flow_id, run_id);
    }
    wait_until_backlog_drains(&harness).await;

    let objects = harness
        .durable
        .objects
        .list(HISTORY_SEGMENT_OBJECT_KEY_PREFIX, None, 1000)
        .await
        .expect("the in-memory store lists")
        .objects;
    assert_eq!(objects.len(), run_count);
    let total_bytes: u64 = objects.iter().map(|object| object.size).sum();
    assert!(total_bytes > 4 * budget, "the budget binds: {total_bytes}");
    let max_seen = faults.max_in_flight_bytes_seen.load(Ordering::SeqCst);
    assert!(max_seen > 0);
    assert!(
        u64::try_from(max_seen).expect("positive") <= budget,
        "in-flight bytes {max_seen} exceed the budget {budget}"
    );
    assert_eq!(harness.metrics.history_upload_in_flight_bytes.get(), 0);
    harness.shutdown().await;
}

#[tokio::test(start_paused = true)]
async fn the_backlog_gauge_counts_short_run_closes_before_any_rescan() {
    let (harness, _faults) = remote_store_harness(HistoryUploadLimits::default());
    harness.rebuild_all_partitions().await;
    enable_history_offload(&harness);
    harness.settle().await;
    let held_slots = harness
        .effect_context()
        .history_upload_limiter
        .hold_every_slot_for_test()
        .await;
    for index in 0..50 {
        let flow_id = format!("held-{index}");
        let run_id = harness.start_flow(start_request(&flow_id), settings());
        close_run(&harness, &flow_id, run_id);
    }
    harness.settle().await;
    assert_eq!(harness.metrics.history_upload_backlog.segments(), 50);
    assert_eq!(pending_upload_runs(&harness), 50);
    // A rescan finds the same backlog and counts nothing twice.
    tokio::time::sleep(RESCAN_INTERVAL * 2).await;
    harness.settle().await;
    assert_eq!(harness.metrics.history_upload_backlog.segments(), 50);

    drop(held_slots);
    wait_until_backlog_drains(&harness).await;
    tokio::time::sleep(RESCAN_INTERVAL * 2).await;
    harness.settle().await;
    assert_eq!(harness.metrics.history_upload_backlog.segments(), 0);
    harness.shutdown().await;
}

fn write_attribute(harness: &EffectHarness, flow_id: &str, run_id: RunId, text: &str) {
    let command = FlowCommand {
        flow_id: flow_id.to_owned(),
        run_id: run_id.as_bytes().to_vec(),
        command: Some(flow_command::Command::AttributesSet(AttributesSetCommand {
            request: Some(SetAttributesRequest {
                flow_id: flow_id.to_owned(),
                attributes: vec![AttributeWrite {
                    key: "notes".to_owned(),
                    value: Some(string_value(text)),
                    ..AttributeWrite::default()
                }],
                request_id: uuid::Uuid::now_v7().to_string(),
                ..SetAttributesRequest::default()
            }),
            hard_delete_objects: Vec::new(),
            request_fingerprint: vec![1],
        })),
        ..FlowCommand::default()
    };
    let reply = harness
        .flow_effect_operations
        .commit(
            &EffectHarness::flow(flow_id),
            command,
            CommitUrgency::Prompt,
        )
        .expect("the write commits");
    assert!(!is_error_reply(&reply), "the write failed: {reply:?}");
}

/// Returns `length` hexadecimal characters of xorshift output, which zstd barely compresses.
fn noise_text(seed: usize, length: usize) -> String {
    let mut state = u64::try_from(seed)
        .expect("small seed")
        .wrapping_mul(0x9e37_79b9_7f4a_7c15)
        | 1;
    let mut text = String::with_capacity(length);
    while text.len() < length {
        state ^= state << 13;
        state ^= state >> 7;
        state ^= state << 17;
        text.push_str(&format!("{state:016x}"));
    }
    text.truncate(length);
    text
}
