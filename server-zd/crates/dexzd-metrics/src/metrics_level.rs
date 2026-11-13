// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use prometheus_client::collector::Collector;
use prometheus_client::registry::{Metric, Registry};
use serde::{Deserialize, Serialize};

/// Which metric families a node exports; each level includes the levels below it.
#[derive(
    Clone, Copy, Debug, Default, PartialEq, Eq, PartialOrd, Ord, Hash, Serialize, Deserialize,
)]
#[serde(rename_all = "camelCase")]
pub enum MetricsLevel {
    /// Golden signals for SLOs and page-worthy alerts.
    Basic,
    /// The default; adds capacity, cost, and each optional Component's lag and failures.
    #[default]
    Advanced,
    /// Adds internal mechanisms, invariant detectors, throughput detail, and process metrics.
    Verbose,
}

impl MetricsLevel {
    /// Returns the YAML spelling of the level.
    pub fn yaml_name(self) -> &'static str {
        match self {
            Self::Basic => "basic",
            Self::Advanced => "advanced",
            Self::Verbose => "verbose",
        }
    }
}

/// The lowest level that exports each family, keyed by the name on its `# TYPE` line.
const FAMILY_LEVELS: &[(&str, MetricsLevel)] = &[
    ("dexzd_build_info", MetricsLevel::Basic),
    ("dexzd_behavior_version", MetricsLevel::Advanced),
    ("dexzd_node_role", MetricsLevel::Basic),
    ("dexzd_lease_epoch", MetricsLevel::Advanced),
    ("dexzd_lease_renewals", MetricsLevel::Basic),
    ("dexzd_api_requests", MetricsLevel::Basic),
    ("dexzd_api_request_latency_seconds", MetricsLevel::Basic),
    ("dexzd_commit_records", MetricsLevel::Advanced),
    ("dexzd_commit_bytes", MetricsLevel::Advanced),
    ("dexzd_commit_latency_seconds", MetricsLevel::Basic),
    ("dexzd_object_store_requests", MetricsLevel::Advanced),
    ("dexzd_object_store_request_errors", MetricsLevel::Basic),
    ("dexzd_object_store_put_bytes", MetricsLevel::Advanced),
    ("dexzd_snapshots", MetricsLevel::Advanced),
    (
        "dexzd_snapshot_last_duration_seconds",
        MetricsLevel::Advanced,
    ),
    ("dexzd_snapshot_last_bytes", MetricsLevel::Verbose),
    (
        "dexzd_recovery_last_duration_seconds",
        MetricsLevel::Advanced,
    ),
    ("dexzd_startup_read_retries", MetricsLevel::Verbose),
    ("dexzd_recovery_last_replayed_bytes", MetricsLevel::Verbose),
    ("dexzd_local_storage_bytes", MetricsLevel::Advanced),
    ("dexzd_timer_lag_seconds", MetricsLevel::Basic),
    ("dexzd_matching_queued_tasks", MetricsLevel::Advanced),
    ("dexzd_matching_worker_streams", MetricsLevel::Basic),
    (
        "dexzd_matching_unsupported_type_entries",
        MetricsLevel::Verbose,
    ),
    (
        "dexzd_matching_unsupported_type_evictions",
        MetricsLevel::Advanced,
    ),
    ("dexzd_step_task_queue_entries", MetricsLevel::Advanced),
    (
        "dexzd_step_task_queue_oldest_age_seconds",
        MetricsLevel::Basic,
    ),
    ("dexzd_matching_unserved_task_queues", MetricsLevel::Basic),
    ("dexzd_matching_heads_restored_refs", MetricsLevel::Verbose),
    (
        "dexzd_matching_heads_incomplete_scans",
        MetricsLevel::Verbose,
    ),
    (
        "dexzd_step_task_queue_incomplete_seeks",
        MetricsLevel::Verbose,
    ),
    ("dexzd_step_attempts_returned", MetricsLevel::Advanced),
    ("dexzd_worker_stream_protocol_errors", MetricsLevel::Verbose),
    ("dexzd_step_attempt_failures", MetricsLevel::Basic),
    ("dexzd_step_retries_exhausted", MetricsLevel::Advanced),
    ("dexzd_flow_closes", MetricsLevel::Basic),
    (
        "dexzd_flow_end_to_end_latency_seconds",
        MetricsLevel::Advanced,
    ),
    (
        "dexzd_step_schedule_to_start_latency_seconds",
        MetricsLevel::Advanced,
    ),
    ("dexzd_worker_stream_events", MetricsLevel::Verbose),
    ("dexzd_step_stream_writes_dropped", MetricsLevel::Advanced),
    (
        "dexzd_attribute_sync_backlog_entries",
        MetricsLevel::Advanced,
    ),
    (
        "dexzd_attribute_sync_oldest_pending_age_seconds",
        MetricsLevel::Advanced,
    ),
    (
        "dexzd_attribute_sync_write_failures",
        MetricsLevel::Advanced,
    ),
    (
        "dexzd_attribute_sync_dropped_entries",
        MetricsLevel::Advanced,
    ),
    ("dexzd_attribute_sync_fenced_writes", MetricsLevel::Verbose),
    ("dexzd_search_skipped_documents", MetricsLevel::Advanced),
    ("dexzd_search_outbox_entries", MetricsLevel::Advanced),
    (
        "dexzd_search_outbox_max_partition_entries",
        MetricsLevel::Verbose,
    ),
    (
        "dexzd_search_outbox_oldest_entry_age_seconds",
        MetricsLevel::Advanced,
    ),
    ("dexzd_search_outbox_batches", MetricsLevel::Advanced),
    (
        "dexzd_search_outbox_projected_entries",
        MetricsLevel::Verbose,
    ),
    ("dexzd_search_outbox_projected_runs", MetricsLevel::Verbose),
    ("dexzd_search_outbox_ack_proposals", MetricsLevel::Verbose),
    (
        "dexzd_search_initial_load_pending_partitions",
        MetricsLevel::Advanced,
    ),
    ("dexzd_search_initial_load_documents", MetricsLevel::Verbose),
    ("dexzd_search_initial_loads", MetricsLevel::Verbose),
    (
        "dexzd_search_projection_mode_skipped_partitions",
        MetricsLevel::Verbose,
    ),
    (
        "dexzd_history_upload_backlog_segments",
        MetricsLevel::Advanced,
    ),
    ("dexzd_history_upload_backlog_bytes", MetricsLevel::Advanced),
    (
        "dexzd_history_upload_oldest_sealed_age_seconds",
        MetricsLevel::Advanced,
    ),
    (
        "dexzd_history_upload_concurrency_limit",
        MetricsLevel::Verbose,
    ),
    (
        "dexzd_history_upload_in_flight_bytes",
        MetricsLevel::Verbose,
    ),
    ("dexzd_history_uploads", MetricsLevel::Advanced),
    ("dexzd_history_upload_bytes", MetricsLevel::Verbose),
    (
        "dexzd_history_upload_checksum_mismatches",
        MetricsLevel::Advanced,
    ),
    ("dexzd_history_reads", MetricsLevel::Verbose),
    ("dexzd_history_value_revivals", MetricsLevel::Verbose),
    ("dexzd_history_upload_rescan_errors", MetricsLevel::Advanced),
    (
        "dexzd_history_expired_objects_deleted",
        MetricsLevel::Verbose,
    ),
    (
        "dexzd_history_orphan_objects_deleted",
        MetricsLevel::Verbose,
    ),
    ("dexzd_hot_standby_state", MetricsLevel::Advanced),
    ("dexzd_hot_standby_lag_batches", MetricsLevel::Verbose),
    ("dexzd_hot_standby_lag_seconds", MetricsLevel::Advanced),
    ("dexzd_hot_standby_error", MetricsLevel::Advanced),
    ("dexzd_takeovers", MetricsLevel::Advanced),
    ("dexzd_takeover_duration_seconds", MetricsLevel::Verbose),
    ("process_resident_memory_bytes", MetricsLevel::Verbose),
    ("process_cpu_seconds", MetricsLevel::Verbose),
    ("process_start_time_seconds", MetricsLevel::Verbose),
    ("process_open_fds", MetricsLevel::Verbose),
];

/// Returns the lowest level that exports `family_name`.
fn family_level(family_name: &str) -> MetricsLevel {
    FAMILY_LEVELS
        .iter()
        .find(|(name, _)| *name == family_name)
        .map(|(_, level)| *level)
        .expect("every registered metric family has a row in FAMILY_LEVELS")
}

/// Registers in one registry only the families that `level` exports.
pub(crate) struct LeveledRegistry<'registry> {
    registry: &'registry mut Registry,
    level: MetricsLevel,
}

impl<'registry> LeveledRegistry<'registry> {
    pub(crate) fn new(registry: &'registry mut Registry, level: MetricsLevel) -> Self {
        Self { registry, level }
    }

    /// Registers `metric` as `name` when the level exports that family.
    pub(crate) fn register(&mut self, name: &str, help: &str, metric: impl Metric) {
        if self.exports(name) {
            self.registry.register(name, help, metric);
        }
    }

    /// Registers `collector` when the level exports every family in `family_names`.
    pub(crate) fn register_collector(
        &mut self,
        family_names: &[&str],
        collector: Box<dyn Collector>,
    ) {
        if family_names.iter().all(|name| self.exports(name)) {
            self.registry.register_collector(collector);
        }
    }

    fn exports(&self, family_name: &str) -> bool {
        family_level(family_name) <= self.level
    }
}

/// Returns the `# TYPE` family names a node exports at `level` on this platform.
#[cfg(feature = "test-util")]
pub fn exported_family_names_for_test(
    level: MetricsLevel,
) -> std::collections::BTreeSet<&'static str> {
    FAMILY_LEVELS
        .iter()
        .filter(|(name, family_level)| {
            *family_level <= level
                // Only Linux reads process metrics from /proc.
                && (cfg!(target_os = "linux") || !name.starts_with("process_"))
        })
        .map(|(name, _)| *name)
        .collect()
}
