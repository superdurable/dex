// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Own the fixed, process-local OpenMetrics registry and typed recording handles for a server-zd node.

macro_rules! label_value {
    ($name:ident { $($variant:ident => $value:expr),+ $(,)? }) => {
        impl prometheus_client::encoding::EncodeLabelValue for $name {
            fn encode(&self, encoder: &mut prometheus_client::encoding::LabelValueEncoder) -> Result<(), std::fmt::Error> {
                let value = match self { $(Self::$variant => $value,)+ };
                prometheus_client::encoding::EncodeLabelValue::encode(&value, encoder)
            }
        }
    };
}

mod api_requests;
mod collectors;
mod commit_latency;
mod fixed_labels;
mod histogram;
mod lag_gauges;
mod metrics_level;
mod object_requests;
mod semantic;
mod worker_stream;

use std::path::PathBuf;

pub use api_requests::{ApiLatency, ApiRequests, GrpcCode, RpcClass, classify_rpc};
use collectors::{LOCAL_STORAGE_FAMILY_NAME, LocalStorageCollector};
pub use commit_latency::CommitLatency;
pub use fixed_labels::{
    AttributeSyncFailureKind, AttributeSyncWriteFailures, HistoryReadKind, HistoryReadSource,
    HistoryReads, HistoryUploadOutcome, HistoryUploadOutcomes, HotStandbyMetricState,
    HotStandbyStateGauges, ObjectErrorCounters, OutcomeCounters, RoleGauges, TakeoverCounters,
    TakeoverDurations, TakeoverStandbyMode, TakeoverStoreSource,
};
pub use histogram::AtomicHistogram;
use histogram::{FLOW_LATENCY_BOUNDS, SCHEDULE_LATENCY_BOUNDS};
pub use lag_gauges::{
    HistoryUploadBacklog, HistoryUploadBacklogBytes, HistoryUploadBacklogSegments,
    HistoryUploadOldestSealedAge, SearchOutboxBacklog, SearchOutboxEntries,
    SearchOutboxMaxPartitionEntries, SearchOutboxOldestEntryAge, TimerLag,
};
use metrics_level::LeveledRegistry;
pub use metrics_level::MetricsLevel;
#[cfg(feature = "test-util")]
pub use metrics_level::exported_family_names_for_test;
pub use object_requests::{ObjectKind, ObjectOperation, ObjectRequests, classify_object_kind};
use prometheus_client::encoding::{EncodeLabelSet, text::encode};
use prometheus_client::metrics::counter::Counter;
use prometheus_client::metrics::family::Family;
use prometheus_client::metrics::gauge::Gauge;
use prometheus_client::registry::Registry;
pub use semantic::{
    FlowCloseStatus, FlowCloses, StepAttemptFailures, StepDurability, StepFailureKind,
    StepRetriesExhausted,
};
pub use worker_stream::{
    StepAttemptReturnReason, StepAttemptsReturned, StepStreamWriteDropReason,
    StepStreamWritesDropped, UnservedTaskQueueReason, UnservedTaskQueues, WorkerStreamEvent,
    WorkerStreamEvents, WorkerStreamProtocolError, WorkerStreamProtocolErrors,
};

/// The node's fixed set of metric handles.
#[derive(Clone, Debug)]
pub struct NodeMetrics {
    /// Current behavior barrier.
    pub behavior_version: Gauge,
    /// Current node role, with one series for each closed role.
    pub node_roles: RoleGauges,
    /// Epoch held by this node, zero when it holds none.
    pub lease_epoch: Gauge,
    /// Lease renewal attempts.
    pub lease_renewals: OutcomeCounters,
    /// Public gRPC request completions.
    pub api_requests: ApiRequests,
    /// Completed unary gRPC request latency.
    pub api_latency: ApiLatency,
    /// Durably committed commands.
    pub commit_records: Counter,
    /// Durably committed encoded batch bytes.
    pub commit_bytes: Counter,
    /// Time from batch seal to durable acknowledgement.
    pub commit_latency: CommitLatency,
    /// Object storage requests, including failed requests.
    pub object_requests: ObjectRequests,
    /// Failed object storage requests after SDK retries.
    pub object_errors: ObjectErrorCounters,
    /// Uploaded object bytes.
    pub object_put_bytes: Counter,
    /// Snapshot attempts.
    pub snapshots: OutcomeCounters,
    /// Duration of the last successful snapshot.
    pub snapshot_last_duration: Gauge<f64, std::sync::atomic::AtomicU64>,
    /// Bytes in the last successful snapshot.
    pub snapshot_last_bytes: Gauge,
    /// Duration of the last completed recovery or replay.
    pub recovery_last_duration: Gauge<f64, std::sync::atomic::AtomicU64>,
    /// Transient object storage reads retried during startup.
    pub startup_read_retries: Counter,
    /// Uncompressed command record bytes replayed during the last completed recovery.
    pub recovery_last_replayed_bytes: Gauge,
    /// Local SST and WAL bytes, updated before encoding.
    pub local_storage_bytes: Gauge,
    /// Age of the oldest due timer that has not fired.
    pub timer_lag: TimerLag,
    /// Worker tasks waiting for a poll.
    pub matching_queued_tasks: Gauge,
    /// Open Worker poll streams.
    pub matching_worker_streams: Gauge,
    /// (task type, app Worker ID) pairs in the unsupported type cache.
    pub matching_unsupported_type_entries: Gauge,
    /// Unsupported type cache entries evicted because the cache was full.
    pub matching_unsupported_type_evictions: Counter,
    /// Committed returns of started Step attempts by reason.
    pub step_attempts_returned: StepAttemptsReturned,
    /// DispatchQueue index rows of the partitions this node owns.
    pub step_task_queue_entries: Gauge,
    /// Age of the oldest queued Step task that matching holds a ref for.
    pub step_task_queue_oldest_age_seconds: Gauge,
    /// Queues with backlog that no connected stream can serve, by reason.
    pub matching_unserved_task_queues: UnservedTaskQueues,
    /// Refs a periodic Heads scan restored because matching had lost them; nonzero is a bug.
    pub matching_heads_restored_refs: Counter,
    /// Queues a periodic Heads scan skipped because a seek met too many deleted entries or a
    /// read failed.
    pub matching_heads_incomplete_scans: Counter,
    /// Next-row seeks of Claim replies and owed refs that met too many deleted entries; each is
    /// retried after a backoff.
    pub step_task_queue_incomplete_seeks: Counter,
    /// Worker frames that break the reserved-slot dispatch protocol.
    pub worker_stream_protocol_errors: WorkerStreamProtocolErrors,
    /// Failed Step method attempts.
    pub step_attempt_failures: StepAttemptFailures,
    /// Step executions that exhausted their retries.
    pub step_retries_exhausted: StepRetriesExhausted,
    /// Closed Flow runs.
    pub flow_closes: FlowCloses,
    /// Run start to close latency.
    pub flow_end_to_end_latency: AtomicHistogram<8>,
    /// Committed Step task to Worker delivery latency.
    pub step_schedule_to_start_latency: AtomicHistogram<7>,
    /// Task deliveries and persistent Worker stream closes.
    pub worker_stream_events: WorkerStreamEvents,
    /// Step Stream writes the server dropped, by reason.
    pub step_stream_writes_dropped: StepStreamWritesDropped,
    /// Attribute Sync entries waiting to be written, as of the projector's last scan.
    pub attribute_sync_backlog_entries: Gauge,
    /// Age of the oldest pending Attribute Sync entry's retry window, 0 when none.
    pub attribute_sync_oldest_pending_age: Gauge<f64, std::sync::atomic::AtomicU64>,
    /// Failed Attribute Store write transactions.
    pub attribute_sync_write_failures: AttributeSyncWriteFailures,
    /// Attribute Sync entries dropped after their retries were exhausted.
    pub attribute_sync_dropped_entries: Counter,
    /// Attribute Store writes rejected because a newer owner raised the fencing token.
    pub attribute_sync_fenced_writes: Counter,
    /// Committed search documents omitted because their indexed values are unsupported.
    pub search_skipped_documents: Counter,
    /// Unprojected SearchOutbox entries and the oldest one's seq, per partition.
    pub search_outbox_backlog: SearchOutboxBacklog,
    /// External search projection SQL transactions, by outcome.
    pub search_outbox_batches: OutcomeCounters,
    /// SearchOutbox entries that committed SQL transactions covered.
    pub search_outbox_projected_entries: Counter,
    /// Run rows that committed SQL transactions upserted or deleted, after per-batch deduplication.
    pub search_outbox_projected_runs: Counter,
    /// SearchOutboxAckCommand proposals, by outcome.
    pub search_outbox_ack_proposals: OutcomeCounters,
    /// Partitions whose initial load into an empty SQL visibility projection has not finished.
    pub search_initial_load_pending_partitions: Gauge,
    /// Documents written by initial loads.
    pub search_initial_load_documents: Counter,
    /// Initial loads started, including loads that replace progress a schema cannot continue from.
    pub search_initial_loads: Counter,
    /// Stopped partitions that kept their previous search projection mode at startup.
    pub search_projection_mode_skipped_partitions: Counter,
    /// Sealed history segments waiting for upload, per partition; the count follows seals and
    /// acknowledgments, and bytes and age are as of the uploader's last scan.
    pub history_upload_backlog: HistoryUploadBacklog,
    /// History segment uploads the uploader currently allows at once.
    pub history_upload_concurrency_limit: Gauge,
    /// Bytes that history segment uploads in flight reserve for encoding and encoded objects.
    pub history_upload_in_flight_bytes: Gauge,
    /// History segment uploads by outcome: each upload once its acknowledgment committed, and
    /// each failed attempt.
    pub history_uploads: HistoryUploadOutcomes,
    /// Bytes of the history segment objects this node wrote.
    pub history_upload_bytes: Counter,
    /// Existing history segment objects whose content checksum differed from committed state.
    pub history_upload_checksum_mismatches: Counter,
    /// History reads by kind and by where they found their data.
    pub history_reads: HistoryReads,
    /// Offloaded values the lease holder restored locally before a command that references them.
    pub history_value_revivals: Counter,
    /// Runs or index rows that the history upload rescan could not read and skipped.
    pub history_upload_rescan_errors: Counter,
    /// Objects of expired history segments this node deleted.
    pub history_expired_objects_deleted: Counter,
    /// History objects of runs that no longer exist that this node's orphan sweep deleted.
    pub history_orphan_objects_deleted: Counter,
    /// Hot standby state, with one series for each closed state.
    pub hot_standby_states: HotStandbyStateGauges,
    /// Log batches the hot standby has yet to apply, as of its last listing.
    pub hot_standby_lag_batches: Gauge,
    /// How long the hot standby has trailed the end of the log, zero when caught up.
    pub hot_standby_lag_seconds: Gauge<f64, std::sync::atomic::AtomicU64>,
    /// One while the hot standby reports the error of a recent restart, zero otherwise.
    pub hot_standby_error: Gauge,
    /// Completed takeovers by the standby mode of the process and the store it served from.
    pub takeovers: TakeoverCounters,
    /// Time from attempting an available lease to serving, by the store served from.
    pub takeover_duration: TakeoverDurations,
}

/// A lifecycle role for one node.
#[derive(Clone, Copy, Debug, Hash, PartialEq, Eq)]
pub enum NodeRole {
    /// Before lease acquisition.
    Starting,
    /// Waiting for a lease.
    Standby,
    /// Replaying state under a lease.
    Recovering,
    /// Accepting public requests.
    Serving,
    /// Stopping listeners and releasing ownership.
    Draining,
}

label_value!(NodeRole { Starting => "starting", Standby => "standby", Recovering => "recovering", Serving => "serving", Draining => "draining" });

impl NodeRole {
    const ALL: [Self; 5] = [
        Self::Starting,
        Self::Standby,
        Self::Recovering,
        Self::Serving,
        Self::Draining,
    ];
}

/// A closed success or failure label.
#[derive(Clone, Copy, Debug, Hash, PartialEq, Eq)]
pub enum Outcome {
    /// Successful attempt.
    Ok,
    /// Failed attempt.
    Error,
}

label_value!(Outcome { Ok => "ok", Error => "error" });

#[derive(Clone, Debug, Hash, PartialEq, Eq, EncodeLabelSet)]
pub struct OutcomeLabel {
    outcome: Outcome,
}

#[derive(Clone, Debug, Hash, PartialEq, Eq, EncodeLabelSet)]
pub struct RoleLabel {
    role: NodeRole,
}

#[derive(Clone, Debug, Hash, PartialEq, Eq, EncodeLabelSet)]
pub struct ObjectOperationLabel {
    operation: ObjectOperation,
}

#[derive(Clone, Debug, Hash, PartialEq, Eq, EncodeLabelSet)]
struct BuildLabel {
    version: BuildVersion,
}

#[derive(Clone, Copy, Debug, Hash, PartialEq, Eq)]
enum BuildVersion {
    Current,
}

label_value!(BuildVersion { Current => env!("CARGO_PKG_VERSION") });

impl NodeMetrics {
    /// Creates all handles for one process-local node and a registry of the families `level`
    /// exports.
    pub fn registered(
        local_store_directory: PathBuf,
        partition_count: usize,
        level: MetricsLevel,
    ) -> (Registry, Self) {
        let mut exported_registry = Registry::default();
        let mut registry = LeveledRegistry::new(&mut exported_registry, level);
        let metrics = Self::with_partitions(partition_count);
        let build_info = Family::<BuildLabel, Gauge>::default();
        build_info
            .get_or_create(&BuildLabel {
                version: BuildVersion::Current,
            })
            .set(1);
        registry.register("dexzd_build_info", "Binary version.", build_info);
        registry.register(
            "dexzd_behavior_version",
            "Cluster behavior barrier.",
            metrics.behavior_version.clone(),
        );
        registry.register(
            "dexzd_node_role",
            "Current node lifecycle role.",
            metrics.node_roles.clone(),
        );
        registry.register(
            "dexzd_lease_epoch",
            "Held lease epoch.",
            metrics.lease_epoch.clone(),
        );
        registry.register(
            "dexzd_lease_renewals",
            "Lease renewal attempts.",
            metrics.lease_renewals.clone(),
        );
        registry.register(
            "dexzd_api_requests",
            "Completed public API requests.",
            metrics.api_requests.clone(),
        );
        registry.register(
            "dexzd_api_request_latency_seconds",
            "Completed unary public API request latency.",
            metrics.api_latency.clone(),
        );
        registry.register(
            "dexzd_commit_records",
            "Committed commands.",
            metrics.commit_records.clone(),
        );
        registry.register(
            "dexzd_commit_bytes",
            "Committed encoded batch bytes.",
            metrics.commit_bytes.clone(),
        );
        registry.register(
            "dexzd_commit_latency_seconds",
            "Time from batch seal to durable acknowledgement.",
            metrics.commit_latency.clone(),
        );
        registry.register(
            "dexzd_object_store_requests",
            "Object storage requests.",
            metrics.object_requests.clone(),
        );
        registry.register(
            "dexzd_object_store_request_errors",
            "Failed object storage requests.",
            metrics.object_errors.clone(),
        );
        registry.register(
            "dexzd_object_store_put_bytes",
            "Uploaded object bytes.",
            metrics.object_put_bytes.clone(),
        );
        registry.register(
            "dexzd_snapshots",
            "Snapshot attempts.",
            metrics.snapshots.clone(),
        );
        registry.register(
            "dexzd_snapshot_last_duration_seconds",
            "Last successful snapshot duration.",
            metrics.snapshot_last_duration.clone(),
        );
        registry.register(
            "dexzd_snapshot_last_bytes",
            "Last successful snapshot bytes.",
            metrics.snapshot_last_bytes.clone(),
        );
        registry.register(
            "dexzd_recovery_last_duration_seconds",
            "Last completed recovery duration.",
            metrics.recovery_last_duration.clone(),
        );
        registry.register(
            "dexzd_startup_read_retries",
            "Transient object storage reads retried during startup.",
            metrics.startup_read_retries.clone(),
        );
        registry.register(
            "dexzd_recovery_last_replayed_bytes",
            "Uncompressed command record bytes replayed by the last recovery.",
            metrics.recovery_last_replayed_bytes.clone(),
        );
        registry.register(
            "dexzd_timer_lag_seconds",
            "Age of oldest unfired due timer.",
            metrics.timer_lag.clone(),
        );
        registry.register(
            "dexzd_matching_queued_tasks",
            "Worker tasks waiting for a poll.",
            metrics.matching_queued_tasks.clone(),
        );
        registry.register(
            "dexzd_matching_worker_streams",
            "Open Worker poll streams.",
            metrics.matching_worker_streams.clone(),
        );
        registry.register(
            "dexzd_matching_unsupported_type_entries",
            "Task types cached as unsupported by an app Worker, counted per app Worker.",
            metrics.matching_unsupported_type_entries.clone(),
        );
        registry.register(
            "dexzd_matching_unsupported_type_evictions",
            "Unsupported type cache entries evicted because the cache was full.",
            metrics.matching_unsupported_type_evictions.clone(),
        );
        registry.register(
            "dexzd_step_task_queue_entries",
            "Step tasks in the durable task queue of the partitions this node owns.",
            metrics.step_task_queue_entries.clone(),
        );
        registry.register(
            "dexzd_step_task_queue_oldest_age_seconds",
            "Age of the oldest queued Step task at the head of a matching queue.",
            metrics.step_task_queue_oldest_age_seconds.clone(),
        );
        registry.register(
            "dexzd_matching_unserved_task_queues",
            "Queues with backlog that no connected Worker stream can serve.",
            metrics.matching_unserved_task_queues.clone(),
        );
        registry.register(
            "dexzd_matching_heads_restored_refs",
            "Queued tasks a periodic Heads scan restored to matching; nonzero means a bug.",
            metrics.matching_heads_restored_refs.clone(),
        );
        registry.register(
            "dexzd_matching_heads_incomplete_scans",
            "Queues a periodic Heads scan skipped after too many deleted entries or a read failure.",
            metrics.matching_heads_incomplete_scans.clone(),
        );
        registry.register(
            "dexzd_step_task_queue_incomplete_seeks",
            "Next queued row seeks that met too many deleted entries and retry after a backoff.",
            metrics.step_task_queue_incomplete_seeks.clone(),
        );
        registry.register(
            "dexzd_step_attempts_returned",
            "Started Step attempts returned to pending without consuming a retry.",
            metrics.step_attempts_returned.clone(),
        );
        registry.register(
            "dexzd_worker_stream_protocol_errors",
            "Worker frames that break the reserved-slot dispatch protocol.",
            metrics.worker_stream_protocol_errors.clone(),
        );
        registry.register(
            "dexzd_step_attempt_failures",
            "Failed Step method attempts.",
            metrics.step_attempt_failures.clone(),
        );
        registry.register(
            "dexzd_step_retries_exhausted",
            "Step executions that exhausted their attempts.",
            metrics.step_retries_exhausted.clone(),
        );
        registry.register(
            "dexzd_flow_closes",
            "Closed Flow runs.",
            metrics.flow_closes.clone(),
        );
        registry.register(
            "dexzd_flow_end_to_end_latency_seconds",
            "Committed Flow run start to close latency.",
            metrics.flow_end_to_end_latency.clone(),
        );
        registry.register(
            "dexzd_step_schedule_to_start_latency_seconds",
            "Committed Step task to Worker delivery latency.",
            metrics.step_schedule_to_start_latency.clone(),
        );
        registry.register(
            "dexzd_worker_stream_events",
            "Delivered Worker tasks and closed Worker streams.",
            metrics.worker_stream_events.clone(),
        );
        registry.register(
            "dexzd_step_stream_writes_dropped",
            "Step Stream writes dropped without failing the Step, by reason.",
            metrics.step_stream_writes_dropped.clone(),
        );
        registry.register(
            "dexzd_attribute_sync_backlog_entries",
            "Attribute Sync entries waiting to be written.",
            metrics.attribute_sync_backlog_entries.clone(),
        );
        registry.register(
            "dexzd_attribute_sync_oldest_pending_age_seconds",
            "Age of the oldest pending Attribute Sync entry.",
            metrics.attribute_sync_oldest_pending_age.clone(),
        );
        registry.register(
            "dexzd_attribute_sync_write_failures",
            "Failed Attribute Store write transactions.",
            metrics.attribute_sync_write_failures.clone(),
        );
        registry.register(
            "dexzd_attribute_sync_dropped_entries",
            "Attribute Sync entries dropped after retries were exhausted.",
            metrics.attribute_sync_dropped_entries.clone(),
        );
        registry.register(
            "dexzd_attribute_sync_fenced_writes",
            "Attribute Store writes rejected by a newer owner's fencing token.",
            metrics.attribute_sync_fenced_writes.clone(),
        );
        registry.register(
            "dexzd_search_skipped_documents",
            "Committed documents omitted from the search projection.",
            metrics.search_skipped_documents.clone(),
        );
        registry.register(
            "dexzd_search_outbox_entries",
            "SearchOutbox entries waiting for the external search projection.",
            SearchOutboxEntries(metrics.search_outbox_backlog.clone()),
        );
        registry.register(
            "dexzd_search_outbox_max_partition_entries",
            "SearchOutbox entries waiting in the most backlogged partition.",
            SearchOutboxMaxPartitionEntries(metrics.search_outbox_backlog.clone()),
        );
        registry.register(
            "dexzd_search_outbox_oldest_entry_age_seconds",
            "Age of the oldest SearchOutbox entry waiting for the external search projection.",
            SearchOutboxOldestEntryAge(metrics.search_outbox_backlog.clone()),
        );
        registry.register(
            "dexzd_search_outbox_batches",
            "External search projection SQL transactions, by outcome.",
            metrics.search_outbox_batches.clone(),
        );
        registry.register(
            "dexzd_search_outbox_projected_entries",
            "SearchOutbox entries covered by committed SQL transactions.",
            metrics.search_outbox_projected_entries.clone(),
        );
        registry.register(
            "dexzd_search_outbox_projected_runs",
            "Run rows upserted or deleted by committed SQL transactions.",
            metrics.search_outbox_projected_runs.clone(),
        );
        registry.register(
            "dexzd_search_outbox_ack_proposals",
            "SearchOutboxAckCommand proposals, by outcome.",
            metrics.search_outbox_ack_proposals.clone(),
        );
        registry.register(
            "dexzd_search_initial_load_pending_partitions",
            "Partitions whose initial SQL visibility load has not finished.",
            metrics.search_initial_load_pending_partitions.clone(),
        );
        registry.register(
            "dexzd_search_initial_load_documents",
            "Documents written by initial SQL visibility loads.",
            metrics.search_initial_load_documents.clone(),
        );
        registry.register(
            "dexzd_search_initial_loads",
            "Initial SQL visibility loads started, including reloads of unusable progress.",
            metrics.search_initial_loads.clone(),
        );
        registry.register(
            "dexzd_search_projection_mode_skipped_partitions",
            "Stopped partitions that kept their previous search projection mode at startup.",
            metrics.search_projection_mode_skipped_partitions.clone(),
        );
        registry.register(
            "dexzd_history_upload_backlog_segments",
            "Sealed history segments waiting for upload.",
            HistoryUploadBacklogSegments(metrics.history_upload_backlog.clone()),
        );
        registry.register(
            "dexzd_history_upload_backlog_bytes",
            "Uncompressed bytes of the sealed history segments waiting for upload.",
            HistoryUploadBacklogBytes(metrics.history_upload_backlog.clone()),
        );
        registry.register(
            "dexzd_history_upload_oldest_sealed_age_seconds",
            "Age of the oldest sealed history segment waiting for upload.",
            HistoryUploadOldestSealedAge(metrics.history_upload_backlog.clone()),
        );
        registry.register(
            "dexzd_history_upload_concurrency_limit",
            "History segment uploads the uploader currently allows at once.",
            metrics.history_upload_concurrency_limit.clone(),
        );
        registry.register(
            "dexzd_history_upload_in_flight_bytes",
            "Bytes that history segment uploads in flight reserve for encoding and encoded objects.",
            metrics.history_upload_in_flight_bytes.clone(),
        );
        registry.register(
            "dexzd_history_uploads",
            "History segment uploads by outcome: acknowledged uploads and failed attempts.",
            metrics.history_uploads.clone(),
        );
        registry.register(
            "dexzd_history_upload_bytes",
            "Bytes of the history segment objects this node wrote.",
            metrics.history_upload_bytes.clone(),
        );
        registry.register(
            "dexzd_history_upload_checksum_mismatches",
            "Existing history segment objects whose content differed from committed state.",
            metrics.history_upload_checksum_mismatches.clone(),
        );
        registry.register(
            "dexzd_history_reads",
            "History reads by kind and by where they found their data.",
            metrics.history_reads.clone(),
        );
        registry.register(
            "dexzd_history_value_revivals",
            "Offloaded values restored locally before a command that references them.",
            metrics.history_value_revivals.clone(),
        );
        registry.register(
            "dexzd_history_upload_rescan_errors",
            "Runs or index rows that the history upload rescan could not read and skipped.",
            metrics.history_upload_rescan_errors.clone(),
        );
        registry.register(
            "dexzd_history_expired_objects_deleted",
            "Objects of expired history segments this node deleted.",
            metrics.history_expired_objects_deleted.clone(),
        );
        registry.register(
            "dexzd_history_orphan_objects_deleted",
            "History objects of runs that no longer exist that this node's orphan sweep deleted.",
            metrics.history_orphan_objects_deleted.clone(),
        );
        registry.register(
            "dexzd_hot_standby_state",
            "Hot standby state of this process.",
            metrics.hot_standby_states.clone(),
        );
        registry.register(
            "dexzd_hot_standby_lag_batches",
            "Log batches the hot standby has yet to apply.",
            metrics.hot_standby_lag_batches.clone(),
        );
        registry.register(
            "dexzd_hot_standby_lag_seconds",
            "How long the hot standby has trailed the end of the log.",
            metrics.hot_standby_lag_seconds.clone(),
        );
        registry.register(
            "dexzd_hot_standby_error",
            "One while the hot standby reports a recent restart error.",
            metrics.hot_standby_error.clone(),
        );
        registry.register(
            "dexzd_takeovers",
            "Completed takeovers of the node lease.",
            metrics.takeovers.clone(),
        );
        registry.register(
            "dexzd_takeover_duration_seconds",
            "Time from attempting an available lease to serving.",
            metrics.takeover_duration.clone(),
        );
        registry.register_collector(
            &[LOCAL_STORAGE_FAMILY_NAME],
            Box::new(LocalStorageCollector {
                directory: local_store_directory,
                gauge: metrics.local_storage_bytes.clone(),
            }),
        );
        #[cfg(target_os = "linux")]
        registry.register_collector(
            &collectors::process::PROCESS_FAMILY_NAMES,
            Box::new(collectors::process::ProcessCollector::new()),
        );
        (exported_registry, metrics)
    }

    /// Creates unregistered handles for tests and simulations.
    pub fn unregistered() -> Self {
        Self::with_partitions(0)
    }

    /// Creates unregistered handles whose per-partition gauges track `partition_count`
    /// partitions, for tests and simulations that read those gauges.
    pub fn unregistered_with_partitions(partition_count: usize) -> Self {
        Self::with_partitions(partition_count)
    }

    fn with_partitions(partition_count: usize) -> Self {
        Self {
            behavior_version: Gauge::default(),
            node_roles: RoleGauges::default(),
            lease_epoch: Gauge::default(),
            lease_renewals: OutcomeCounters::default(),
            api_requests: ApiRequests::default(),
            api_latency: ApiLatency::default(),
            commit_records: Counter::default(),
            commit_bytes: Counter::default(),
            commit_latency: CommitLatency::default(),
            object_requests: ObjectRequests::default(),
            object_errors: ObjectErrorCounters::default(),
            object_put_bytes: Counter::default(),
            snapshots: OutcomeCounters::default(),
            snapshot_last_duration: Gauge::default(),
            snapshot_last_bytes: Gauge::default(),
            recovery_last_duration: Gauge::default(),
            startup_read_retries: Counter::default(),
            recovery_last_replayed_bytes: Gauge::default(),
            local_storage_bytes: Gauge::default(),
            timer_lag: TimerLag::new(partition_count),
            matching_queued_tasks: Gauge::default(),
            matching_worker_streams: Gauge::default(),
            matching_unsupported_type_entries: Gauge::default(),
            matching_unsupported_type_evictions: Counter::default(),
            step_attempts_returned: StepAttemptsReturned::default(),
            step_task_queue_entries: Gauge::default(),
            step_task_queue_oldest_age_seconds: Gauge::default(),
            matching_unserved_task_queues: UnservedTaskQueues::default(),
            matching_heads_restored_refs: Counter::default(),
            matching_heads_incomplete_scans: Counter::default(),
            step_task_queue_incomplete_seeks: Counter::default(),
            worker_stream_protocol_errors: WorkerStreamProtocolErrors::default(),
            step_attempt_failures: StepAttemptFailures::default(),
            step_retries_exhausted: StepRetriesExhausted::default(),
            flow_closes: FlowCloses::default(),
            flow_end_to_end_latency: AtomicHistogram::new(&FLOW_LATENCY_BOUNDS),
            step_schedule_to_start_latency: AtomicHistogram::new(&SCHEDULE_LATENCY_BOUNDS),
            worker_stream_events: WorkerStreamEvents::default(),
            step_stream_writes_dropped: StepStreamWritesDropped::default(),
            attribute_sync_backlog_entries: Gauge::default(),
            attribute_sync_oldest_pending_age: Gauge::default(),
            attribute_sync_write_failures: AttributeSyncWriteFailures::default(),
            attribute_sync_dropped_entries: Counter::default(),
            attribute_sync_fenced_writes: Counter::default(),
            search_skipped_documents: Counter::default(),
            search_outbox_backlog: SearchOutboxBacklog::new(partition_count),
            search_outbox_batches: OutcomeCounters::default(),
            search_outbox_projected_entries: Counter::default(),
            search_outbox_projected_runs: Counter::default(),
            search_outbox_ack_proposals: OutcomeCounters::default(),
            search_initial_load_pending_partitions: Gauge::default(),
            search_initial_load_documents: Counter::default(),
            search_initial_loads: Counter::default(),
            search_projection_mode_skipped_partitions: Counter::default(),
            history_upload_backlog: HistoryUploadBacklog::new(partition_count),
            history_upload_concurrency_limit: Gauge::default(),
            history_upload_in_flight_bytes: Gauge::default(),
            history_uploads: HistoryUploadOutcomes::default(),
            history_upload_bytes: Counter::default(),
            history_upload_checksum_mismatches: Counter::default(),
            history_reads: HistoryReads::default(),
            history_value_revivals: Counter::default(),
            history_upload_rescan_errors: Counter::default(),
            history_expired_objects_deleted: Counter::default(),
            history_orphan_objects_deleted: Counter::default(),
            hot_standby_states: HotStandbyStateGauges::default(),
            hot_standby_lag_batches: Gauge::default(),
            hot_standby_lag_seconds: Gauge::default(),
            hot_standby_error: Gauge::default(),
            takeovers: TakeoverCounters::default(),
            takeover_duration: TakeoverDurations::default(),
        }
    }

    /// Changes the active lifecycle role without exposing dynamic labels.
    pub fn set_role(&self, current_role: NodeRole) {
        self.node_roles.set(current_role);
    }

    /// Records one lease renewal outcome.
    pub fn record_lease_renewal(&self, outcome: Outcome) {
        self.lease_renewals.record(outcome);
    }

    /// Records the outcome of one external search projection SQL transaction.
    pub fn record_search_outbox_batch(&self, outcome: Outcome) {
        self.search_outbox_batches.record(outcome);
    }

    /// Records the outcome of one SearchOutboxAckCommand proposal.
    pub fn record_search_outbox_ack(&self, outcome: Outcome) {
        self.search_outbox_ack_proposals.record(outcome);
    }

    /// Records one snapshot outcome.
    pub fn record_snapshot(&self, outcome: Outcome) {
        self.snapshots.record(outcome);
    }

    /// Records a failed object request.
    pub fn record_object_error(&self, operation: ObjectOperation) {
        self.object_errors.record(operation);
    }
}

/// Encodes one process-local registry in OpenMetrics text format.
pub fn encode_registry(registry: &Registry) -> Result<String, std::fmt::Error> {
    let mut output = String::new();
    encode(&mut output, registry)?;
    Ok(output)
}
