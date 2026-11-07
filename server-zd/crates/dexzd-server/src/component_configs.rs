// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::path::{Path, PathBuf};
use std::time::Duration;

use dexzd_api::{CredentialAuthenticatorConfig, FlowServiceConfig};
use dexzd_cluster_metadata::NodeConfig;
use dexzd_engine::{
    AttributeSyncConfig, AttributeSyncRetryPolicy, BlockingWorkPlacement, CommitConfig,
    EffectsConfig, EngineNodeConfig, FlowCoordinationConfig, HistoryReadsConfig, HotStandbyConfig,
    LocalStoreConfig, RecoveryConfig, SnapshotConfig, default_run_settings,
};
use dexzd_log::LogReadAheadConfig;
use dexzd_protocol::dexzd::v1::RunSettings;
use dexzd_request_validation::ValidationConfig;
use dexzd_storage::RocksDbStoreOptions;
use dexzd_stream_store::StreamStoreConfig;
use dexzd_worker_dispatch::MatchingConfig;

use crate::ServerError;
use crate::config::{Config, StandbyConfig, StandbyMode, StreamStoreBackend, available_cpu_count};

/// The incarnation of this node's log stream; one node writes one stream until multi-node support.
const LOG_STREAM_INCARNATION: u32 = 1;

/// Suffix of the history read cache directory, a sibling of `localStorage.directory`.
const HISTORY_READ_CACHE_DIRECTORY_SUFFIX: &str = "-history-read-cache";

/// Typed configurations of every component, derived from one validated [`Config`].
pub(crate) struct ComponentConfigs {
    pub(crate) engine_node: EngineNodeConfig,
    pub(crate) flow_coordination: FlowCoordinationConfig,
    pub(crate) effects: EffectsConfig,
    pub(crate) history_reads: HistoryReadsConfig,
    pub(crate) matching: MatchingConfig,
    pub(crate) stream_store: StreamStoreConfig,
    pub(crate) flow_service: FlowServiceConfig,
    pub(crate) grpc_max_message_bytes: usize,
    /// Empty when public API authentication is disabled.
    pub(crate) authenticators: Vec<CredentialAuthenticatorConfig>,
    /// Has no store names when no Attribute Store is configured; orphaned entries still drop.
    pub(crate) attribute_sync: AttributeSyncConfig,
    /// How this process waits for the lease and steps down.
    pub(crate) standby: StandbyConfig,
    /// Set in `hot` standby mode; `None` waits for the lease as a cold standby.
    pub(crate) hot_standby: Option<HotStandbyConfig>,
}

impl ComponentConfigs {
    /// Converts the configuration sections; fails when a value does not fit its component.
    ///
    /// Pass `should_take_over_held_lease` only when this process provably alone writes the object store.
    pub(crate) fn from_config(
        config: &Config,
        should_take_over_held_lease: bool,
    ) -> Result<Self, ServerError> {
        let grpc_max_message_bytes =
            to_usize("api.grpcMaxMessageBytes", config.api.grpc_max_message_bytes)?;
        let max_value_bytes = config.values.max_value_bytes;
        let max_wait = Duration::from_secs(config.api.max_wait_seconds);
        let validation = ValidationConfig {
            maximum_flow_id_bytes: to_usize(
                "limits.maxFlowIdBytes",
                u64::from(config.limits.max_flow_id_bytes),
            )?,
            attribute_store_names: config.attribute_store.stores.keys().cloned().collect(),
            has_blob_store: true,
            keyword_value_limit: (config.search.backend == crate::config::SearchBackend::Postgres)
                .then(dexzd_request_validation::KeywordValueLimit::postgres),
            max_history_window_events: config.history.max_window_events,
            ..ValidationConfig::default()
        };
        let available_cpus = available_cpu_count();
        let applier_lanes = config
            .commit
            .effective_applier_lanes(config.cluster.partition_count, available_cpus);
        let max_background_jobs = config
            .local_storage
            .effective_max_background_jobs(available_cpus);
        tracing::info!(
            available_cpus,
            applier_lanes,
            max_background_jobs,
            "resolved CPU-based performance settings"
        );
        let mut run_settings = default_run_settings();
        run_settings.history_window_events = config.history.window_events;
        run_settings.history_prune_chunk_events = config.history.prune_chunk_size;
        run_settings.max_retained_superseded_bytes = config.history.max_retained_superseded_bytes;
        run_settings.retention_after_close_micros = duration_micros(
            "retention.closedFlowDuration",
            config.retention.closed_flow_duration,
        )?;
        run_settings.wire_inline_threshold_bytes =
            u32::try_from(config.values.wire_inline_threshold_bytes).map_err(|_| {
                ServerError::ConfigValueOutOfRange {
                    field: "values.wireInlineThresholdBytes",
                }
            })?;
        run_settings.max_value_bytes = max_value_bytes;
        run_settings.checkpoint_row_write_bytes_threshold =
            config.history.checkpoint_row_write_bytes_threshold;
        run_settings.checkpoint_command_threshold = config.history.checkpoint_command_threshold;
        run_settings.request_dedup_capacity = config.limits.max_request_ids_per_run;
        run_settings.max_run_state_bytes = config.limits.max_flow_state_bytes;
        run_settings.max_active_step_executions = config.limits.max_concurrent_steps_per_flow;
        run_settings.max_child_flow_result_bytes = config.limits.max_child_flow_result_bytes;
        run_settings.history_segment_target_bytes = config.history.segment_target_bytes;
        run_settings.delete_expired_history_objects = config.history.should_delete_expired_objects;
        let engine_node = EngineNodeConfig {
            node: NodeConfig {
                node_id: config.cluster.node_id.clone(),
                incarnation: LOG_STREAM_INCARNATION,
                internal_address: config.api.listen.to_string(),
            },
            cluster: dexzd_cluster_metadata::ClusterConfig {
                partition_count: config.cluster.partition_count,
            },
            lease: dexzd_cluster_metadata::LeaseConfig {
                duration: config.lease.duration,
                fail_fast: config.lease.fail_if_held,
                should_take_over_held_lease,
                held_lease_poll_limit: match config.standby.mode {
                    StandbyMode::Hot => config.standby.held_lease_poll_interval,
                    StandbyMode::Cold => {
                        dexzd_cluster_metadata::LeaseConfig::default().held_lease_poll_limit
                    }
                },
                ..dexzd_cluster_metadata::LeaseConfig::default()
            },
            local_store: LocalStoreConfig::RocksDb {
                directory: config.local_storage.directory.clone(),
                options: RocksDbStoreOptions {
                    block_cache_bytes: to_usize(
                        "localStorage.blockCacheBytes",
                        config.local_storage.block_cache_bytes,
                    )?,
                    max_total_wal_bytes: config.local_storage.max_wal_bytes,
                    write_buffer_bytes: to_usize(
                        "localStorage.writeBufferBytes",
                        config.local_storage.write_buffer_bytes,
                    )?,
                    max_background_jobs: i32::try_from(max_background_jobs).map_err(|_| {
                        ServerError::ConfigValueOutOfRange {
                            field: "localStorage.maxBackgroundJobs",
                        }
                    })?,
                    max_subcompactions: config.local_storage.max_subcompactions,
                },
            },
            commit: CommitConfig {
                debug_replay_checks: config
                    .commit
                    .debug_replay_checks
                    .unwrap_or(cfg!(debug_assertions)),
                batch_window: config.commit.batch_window,
                seal_at_bytes: to_usize("commit.sealAtBytes", config.commit.seal_at_bytes)?,
                max_in_flight_batches: to_usize(
                    "commit.maxInFlight",
                    u64::from(config.commit.max_in_flight),
                )?,
                max_batch_records: to_usize(
                    "commit.maxBatchRecords",
                    u64::from(config.commit.max_batch_records),
                )?,
                max_unapplied_bytes: config.commit.max_unapplied_bytes,
                applier_lanes: to_usize("commit.applierLanes", u64::from(applier_lanes))?,
                applier_queue_frames: to_usize(
                    "commit.applierQueueFrames",
                    u64::from(config.commit.applier_queue_frames),
                )?,
                ..CommitConfig::default()
            },
            snapshots: SnapshotConfig {
                log_threshold_bytes: config.snapshot.log_threshold_bytes,
                interval: config.snapshot.interval,
                retained_count: to_usize(
                    "snapshot.retainedCount",
                    u64::from(config.snapshot.retained_count),
                )?,
                deletion_delay: config.snapshot.log_deletion_delay,
                ..SnapshotConfig::default()
            },
            recovery: RecoveryConfig {
                startup_read_retry_budget: config.recovery.startup_read_retry_budget,
                log_read_ahead: LogReadAheadConfig {
                    max_read_ahead_batches: to_usize(
                        "recovery.readAheadBatches",
                        u64::from(config.recovery.read_ahead_batches),
                    )?,
                    max_buffered_bytes: to_usize(
                        "recovery.readAheadBytes",
                        config.recovery.read_ahead_bytes,
                    )?,
                },
                compact_after_replay_bytes: config.local_storage.compact_after_replay_bytes,
                retained_set_aside_store_count: to_usize(
                    "localStorage.retainedSetAsideStoreCount",
                    u64::from(config.local_storage.retained_set_aside_store_count),
                )?,
            },
            blocking_work: BlockingWorkPlacement::BlockingThreadPool,
        };
        let flow_coordination = FlowCoordinationConfig {
            max_speculative_commands_per_flow: to_usize(
                "limits.maxSpeculativeCommandsPerFlow",
                u64::from(config.limits.max_speculative_commands_per_flow),
            )?,
            max_speculative_bytes_per_node: to_usize(
                "limits.maxSpeculativeBytesPerNode",
                config.limits.max_speculative_bytes_per_node,
            )?,
            max_long_poll_waiters_per_node: to_usize(
                "limits.maxLongPollWaitersPerNode",
                u64::from(config.limits.max_long_poll_waiters_per_node),
            )?,
            max_long_poll_waiters_per_flow: to_usize(
                "limits.maxLongPollWaitersPerFlow",
                u64::from(config.limits.max_long_poll_waiters_per_flow),
            )?,
            long_poll_default_wait: max_wait,
            long_poll_max_wait: max_wait,
            rpc_default_timeout: config.api.default_rpc_timeout,
            max_in_flight_rpcs_per_node: to_usize(
                "limits.maxInFlightRpcsPerNode",
                u64::from(config.limits.max_in_flight_rpcs_per_node),
            )?,
            max_worker_rpc_request_bytes: grpc_max_message_bytes,
            max_history_events_response_bytes: grpc_max_message_bytes,
            hydrate_value_references: config.values.hydrate_before_dispatch,
            time_travel_replay_memory_bytes: config.history.time_travel_replay_memory_bytes,
            run_settings,
            validation: validation.clone(),
            ..FlowCoordinationConfig::default()
        };
        let effects = EffectsConfig {
            heartbeat_batch_window: config.commit.batch_window,
            max_worker_request_bytes: grpc_max_message_bytes,
            default_max_value_bytes: max_value_bytes,
            hydrate_before_dispatch: config.values.hydrate_before_dispatch,
            max_blocking_state_reads: to_usize(
                "limits.maxBlockingStateReads",
                u64::from(config.limits.max_blocking_state_reads),
            )?,
            // Children keep built-in settings; only these newer settings follow configuration.
            child_run_settings: RunSettings {
                max_child_flow_result_bytes: config.limits.max_child_flow_result_bytes,
                history_segment_target_bytes: config.history.segment_target_bytes,
                delete_expired_history_objects: config.history.should_delete_expired_objects,
                ..EffectsConfig::default().child_run_settings
            },
            heads_resync_interval: config.matching.heads_resync_interval,
            history_upload_concurrency: to_usize(
                "history.uploadConcurrency",
                u64::from(config.history.upload_concurrency),
            )?,
            ..EffectsConfig::default()
        };
        let matching = MatchingConfig {
            dispatch_wait: config.matching.dispatch_wait,
            sticky_wait: config.matching.sticky_wait,
            sync_match_wait: config.matching.sync_match_wait,
            ping_interval: config.matching.ping_interval,
            max_streams: to_usize(
                "matching.maxStreams",
                u64::from(config.matching.max_streams),
            )?,
            max_concurrent_tasks_cap: to_usize(
                "matching.maxConcurrentTasksCap",
                u64::from(config.matching.max_concurrent_tasks_cap),
            )?,
            max_queue_length: to_usize(
                "matching.maxQueueLength",
                u64::from(config.matching.max_queue_length),
            )?,
            max_flow_types: to_usize(
                "matching.maxFlowTypes",
                u64::from(config.matching.max_flow_types),
            )?,
            max_flow_type_length: to_usize(
                "matching.maxFlowTypeLength",
                u64::from(config.matching.max_flow_type_length),
            )?,
            unsupported_type_cache_entries: to_usize(
                "matching.unsupportedTypeCacheEntries",
                u64::from(config.matching.unsupported_type_cache_entries),
            )?,
            ..MatchingConfig::default()
        };
        let stream_store = StreamStoreConfig {
            enabled: config.stream_store.backend == StreamStoreBackend::Memory,
            max_message_bytes: to_usize(
                "streamStore.maxMessageBytes",
                config.stream_store.max_message_bytes,
            )?,
            max_read_messages: to_usize(
                "streamStore.maxReadMessages",
                u64::from(config.stream_store.max_read_messages),
            )?,
            trim_trigger_percent: config.stream_store.trim_trigger_percent,
            trim_target_percent: config.stream_store.trim_target_percent,
            max_total_bytes: config.stream_store.max_total_bytes,
            max_stream_capacity_bytes: config.stream_store.max_stream_capacity_bytes,
            estimated_message_overhead_bytes: config.stream_store.estimated_message_overhead_bytes,
            background_trim_batch_size: to_usize(
                "streamStore.backgroundTrimBatchSize",
                u64::from(config.stream_store.background_trim_batch_size),
            )?,
            trim_batch_yield_time: config.stream_store.trim_batch_yield_time,
            trim_workers: to_usize(
                "streamStore.trimWorkers",
                u64::from(config.stream_store.trim_workers),
            )?,
            ..StreamStoreConfig::default()
        };
        let flow_service = FlowServiceConfig {
            node_name: config.cluster.node_id.clone(),
            max_wait,
            ..FlowServiceConfig::default()
        };
        let history_reads = HistoryReadsConfig {
            read_cache_directory: config
                .history
                .read_cache_directory
                .clone()
                .or_else(|| history_read_cache_directory(&config.local_storage.directory)),
            read_cache_max_bytes: config.history.read_cache_max_bytes,
            object_read_concurrency: to_usize(
                "history.objectReadConcurrency",
                u64::from(config.history.object_read_concurrency),
            )?,
            ..HistoryReadsConfig::default()
        };
        Ok(Self {
            engine_node,
            flow_coordination,
            effects,
            history_reads,
            matching,
            stream_store,
            flow_service,
            grpc_max_message_bytes,
            authenticators: config.auth.credential_authenticators(),
            attribute_sync: attribute_sync_config(config)?,
            standby: config.standby.clone(),
            hot_standby: (config.standby.mode == StandbyMode::Hot).then(|| HotStandbyConfig {
                behind_lag_threshold: config.standby.behind_lag_threshold,
                ..HotStandbyConfig::default()
            }),
        })
    }

    /// Returns the validation limits shared by the runtime and Worker response checks.
    pub(crate) fn validation(&self) -> &ValidationConfig {
        &self.flow_coordination.validation
    }
}

/// Bounds a write transaction so that it ends while the lease is valid; renewals keep two thirds.
fn attribute_sync_config(config: &Config) -> Result<AttributeSyncConfig, ServerError> {
    let attribute_store = &config.attribute_store;
    let policy = attribute_store.effective_sync_retry_policy();
    let lease_margin = config.lease.duration / 4;
    Ok(AttributeSyncConfig {
        store_names: attribute_store.stores.keys().cloned().collect(),
        batch_size: to_usize(
            "attributeStore.syncBatchSize",
            u64::from(attribute_store.effective_sync_batch_size()),
        )?,
        retry_policy: AttributeSyncRetryPolicy {
            initial_interval: policy.initial_interval,
            maximum_interval: policy.maximum_interval,
            backoff_coefficient: policy.backoff_coefficient,
            maximum_attempts: policy.maximum_attempts,
            total_duration: policy.total_duration,
        },
        // Within the margin, a COMMIT sent at the deadline still lands before the lease ends.
        transaction_timeout: attribute_store
            .effective_sync_attempt_timeout()
            .min(lease_margin),
        lease_margin,
    })
}

fn to_usize(field: &'static str, value: u64) -> Result<usize, ServerError> {
    usize::try_from(value).map_err(|_| ServerError::ConfigValueOutOfRange { field })
}

fn duration_micros(field: &'static str, duration: Duration) -> Result<i64, ServerError> {
    i64::try_from(duration.as_micros()).map_err(|_| ServerError::ConfigValueOutOfRange { field })
}

/// Returns `<localStorage.directory>-history-read-cache`, a sibling that a set-aside never moves,
/// or `None` when the directory has no name.
fn history_read_cache_directory(local_store_directory: &Path) -> Option<PathBuf> {
    let mut name = local_store_directory.file_name()?.to_owned();
    name.push(HISTORY_READ_CACHE_DIRECTORY_SUFFIX);
    Some(local_store_directory.with_file_name(name))
}
