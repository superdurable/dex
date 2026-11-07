// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::time::Duration;

use serde::{Deserialize, Serialize};

/// Identifies this node and the cluster's immutable partition layout.
#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(default, rename_all = "camelCase", deny_unknown_fields)]
pub struct ClusterConfig {
    /// Defaults to empty and must be set before serving; identifies one lease owner across restarts.
    pub node_id: String,
    /// Defaults to 1024, or to 32 for `dev` and `local` unless the `--config` file sets it; a positive power of two used only when a cluster is created, then checked against its cluster object.
    ///
    /// A data directory that server-zd before v0.1.20 or dexcli before 1.37.0 created has 1024
    /// partitions.
    pub partition_count: u32,
}

impl Default for ClusterConfig {
    fn default() -> Self {
        Self {
            node_id: String::new(),
            partition_count: 1024,
        }
    }
}

/// Controls batching and object uploads for durable commands.
#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(default, rename_all = "camelCase", deny_unknown_fields)]
pub struct CommitConfig {
    /// Defaults to None, which enables checks in debug builds and disables them in release builds.
    /// An explicit value overrides the build default for commit replay and time-travel checks.
    pub debug_replay_checks: Option<bool>,
    /// Defaults to 20 ms; maximum time an unsealed batch waits for more commands.
    #[serde(with = "humantime_serde")]
    pub batch_window: Duration,
    /// Defaults to 4; maximum conditional object writes in flight for one partition.
    pub max_in_flight: u32,
    /// Defaults to 4 MiB; seals a batch at this many serialized bytes unless a command itself is larger.
    pub seal_at_bytes: u64,
    /// Defaults to 8 MiB; object bodies at or above this size use multipart upload, not a hard object-size cap.
    pub multipart_threshold_bytes: u64,
    /// Defaults to 10000; seals a batch once it holds this many records. Must be positive.
    pub max_batch_records: u32,
    /// Defaults to 64 MiB; encoded bytes of submitted commands that are not yet applied locally.
    /// A submission that does not fit fails with UNAVAILABLE, and so does any single command
    /// larger than this value. Must be at least the smaller of `api.grpcMaxMessageBytes` and
    /// 32 MiB, the largest log record, so that every accepted request can fit.
    pub max_unapplied_bytes: u32,
    /// Defaults to None, which uses the larger of 8 and the number of CPUs available to the
    /// process, clamped to between 1 and the smaller of `cluster.partitionCount` and 1024. If the
    /// CPU count cannot be read, None uses 8, also clamped. Apply is local-store read-modify-write
    /// work, not pure CPU work, so small hosts keep at least 8 lanes. Number of applier lanes that
    /// apply committed commands to the local store in parallel. Partition `p` is always applied by
    /// lane `p % applierLanes`, in log order, so the value changes throughput only, never apply
    /// results. An explicit value must be between 1 and the smaller of `cluster.partitionCount`
    /// and 1024.
    pub applier_lanes: Option<u32>,
    /// Defaults to 64; committed partition frames queued per applier lane before the commit
    /// pipeline waits for that lane. Must be positive.
    pub applier_queue_frames: u32,
}

impl Default for CommitConfig {
    fn default() -> Self {
        Self {
            debug_replay_checks: None,
            batch_window: Duration::from_millis(20),
            max_in_flight: 4,
            seal_at_bytes: 4 * 1024 * 1024,
            multipart_threshold_bytes: 8 * 1024 * 1024,
            max_batch_records: 10_000,
            max_unapplied_bytes: 64 * 1024 * 1024,
            applier_lanes: None,
            applier_queue_frames: 64,
        }
    }
}

/// Fewest applier lanes that an unset `commit.applierLanes` uses before the partition clamp.
const MIN_DEFAULT_APPLIER_LANES: u32 = 8;

/// Most applier lanes; each lane is a task with its own queue.
pub(crate) const MAX_APPLIER_LANES: u32 = 1024;

impl CommitConfig {
    /// Returns `applierLanes` when set. Otherwise returns the larger of 8 and `available_cpus`
    /// (8 when the CPU count is unknown), clamped to between 1 and the smaller of
    /// `partition_count` and 1024.
    pub fn effective_applier_lanes(
        &self,
        partition_count: u32,
        available_cpus: Option<u32>,
    ) -> u32 {
        self.applier_lanes.unwrap_or_else(|| {
            available_cpus
                .unwrap_or(MIN_DEFAULT_APPLIER_LANES)
                .max(MIN_DEFAULT_APPLIER_LANES)
                .clamp(1, partition_count.clamp(1, MAX_APPLIER_LANES))
        })
    }
}

/// Controls how a partition owner renews its object-store lease.
#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(default, rename_all = "camelCase", deny_unknown_fields)]
pub struct LeaseConfig {
    /// Defaults to 10 s, or 30 s for `dev` and `local` unless the `--config` file sets it; owner renews after one third of this interval and stops serving when validity ends.
    ///
    /// Validity ends this long after the last successful renewal was sent, on the local monotonic
    /// clock. Each renewal attempt times out after one sixth of this interval, from 1 s to 5 s,
    /// while a retry still fits; the last attempt may use the remaining validity. Transient
    /// failures are retried with a jittered backoff from 200 ms doubling to 2 s until 100 ms before
    /// validity ends. A conditional write lost to another holder
    /// stops the node at once. Once validity ends, the node acknowledges no commits and reports
    /// NOT_SERVING. A process still running 2 s later aborts with SIGABRT, even when the async
    /// runtime is stalled.
    #[serde(with = "humantime_serde")]
    pub duration: Duration,
    /// Defaults to false; when true, startup fails if another unexpired owner holds the lease.
    ///
    /// `dev` and `local` ignore it while they hold the exclusive lock on `<data-dir>/dev.lock`: no
    /// other process can then write its embedded object store, so a held lease belongs to a
    /// stopped process, and `dev` or `local` takes it over at once. Where the filesystem cannot
    /// lock files, `dev` and `local` wait like `serve`.
    pub fail_if_held: bool,
}

impl Default for LeaseConfig {
    fn default() -> Self {
        Self {
            duration: Duration::from_secs(10),
            fail_if_held: false,
        }
    }
}

/// Controls snapshot creation, retention, and delayed log deletion.
#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(default, rename_all = "camelCase", deny_unknown_fields)]
pub struct SnapshotConfig {
    /// Defaults to 256 MiB; a snapshot is requested after at least this much new log data.
    pub log_threshold_bytes: u64,
    /// Defaults to 10 min; a snapshot is requested when this time has elapsed since the previous one.
    #[serde(with = "humantime_serde")]
    pub interval: Duration,
    /// Defaults to 3; number of completed snapshot manifests retained for recovery.
    pub retained_count: u32,
    /// Defaults to 24 h for `serve` and 1 h for `dev` and `local`; obsolete log objects and
    /// superseded snapshots wait this long in the deletion queue after snapshot coverage.
    ///
    /// A longer delay keeps more fallback to older snapshots and lets a standby or a long-stopped
    /// node catch up from the log, and must exceed the longest snapshot upload. A shorter delay
    /// frees object storage sooner. Must be greater than zero.
    #[serde(with = "humantime_serde")]
    pub log_deletion_delay: Duration,
}

impl Default for SnapshotConfig {
    fn default() -> Self {
        Self {
            log_threshold_bytes: 256 * 1024 * 1024,
            interval: Duration::from_secs(10 * 60),
            retained_count: 3,
            log_deletion_delay: Duration::from_secs(24 * 60 * 60),
        }
    }
}

/// Controls how startup reads the log batches it replays.
#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(default, rename_all = "camelCase", deny_unknown_fields)]
pub struct RecoveryConfig {
    /// Defaults to 30 s. Each startup read retries transient errors until this much wall time
    /// passes from its first transient or unknown-outcome failure; concurrent and later reads have independent windows. Must be positive.
    #[serde(with = "humantime_serde")]
    pub startup_read_retry_budget: Duration,
    /// Defaults to 64; most log batches read ahead of the batch being applied, counting GETs in
    /// flight. Replay reads about this many batches per object-store GET round trip, so 64 at a
    /// 10 ms GET latency allows about 6,400 batches per second. Must be between 1 and 1024.
    pub read_ahead_batches: u32,
    /// Defaults to 64 MiB; log object bytes that replay may hold read ahead. Each read reserves
    /// its batch's listed object size, and the reservation lasts until the batch is applied. A
    /// batch larger than this value is read alone, so the log objects held never exceed the
    /// larger of this value and the largest batch. Must be positive.
    pub read_ahead_bytes: u64,
}

impl Default for RecoveryConfig {
    fn default() -> Self {
        Self {
            startup_read_retry_budget: Duration::from_secs(30),
            read_ahead_batches: 64,
            read_ahead_bytes: 64 * 1024 * 1024,
        }
    }
}

/// Bounds reconstructable semantic history and checkpoint frequency for one run.
///
/// A checkpoint is written when either checkpoint threshold is reached, whichever comes first.
#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(try_from = "HistoryConfigSource", rename_all = "camelCase")]
pub struct HistoryConfig {
    /// Defaults to 10000; approximate number of the newest history events in each run's window.
    ///
    /// The window serves history reads and Time Travel. A history record counts its events, and
    /// a semantic record without events counts as one. When a run holds more than this many
    /// events plus one prune chunk, the oldest events are pruned until about this many remain.
    /// A run's `FlowConfig.history_window_events` overrides it. Must be positive.
    /// Recorded in RunRecord at FlowStart; changes affect only later runs.
    /// Set by `history.windowEvents`; the deprecated key `history.windowSemanticEvents` is still
    /// accepted with a startup warning.
    pub window_events: u32,
    /// Defaults to 1000; largest number of old history events pruned at once, no greater than
    /// `window_events`.
    ///
    /// A run prunes at most a tenth of its window at once, and at least one event, when that is
    /// smaller.
    /// Recorded in RunRecord at FlowStart; changes affect only later runs.
    pub prune_chunk_size: u32,
    /// Defaults to 100000; largest window that `FlowConfig.history_window_events` may set for a
    /// run.
    ///
    /// StartFlow and UpdateFlowConfig with a larger value fail with INVALID_ARGUMENT. A Step
    /// result that starts a SubFlow with a larger value is an invalid Worker response, and the
    /// Step attempt is retried under the Step's retry policy. It does not limit `window_events`. Must be positive. Changes apply to
    /// later requests only; runs keep the window they already have.
    pub max_window_events: u32,
    /// Defaults to 1 MiB; a checkpoint is written once the run has written this many bytes of
    /// attempt-scoped rows since the last checkpoint.
    ///
    /// Attempt-scoped rows are the Attribute, ChannelMessage, ChannelMeta, StepExecution,
    /// StepCounter, Lock, Waiter, and Timer rows of the current attempt. Every write of such a row
    /// adds its encoded size, so rewriting one row counts each time. A large value stored apart
    /// from its row counts only as its reference. The effective threshold is the larger of this
    /// value and the smaller of the run's current state row bytes and the row bytes of the last
    /// checkpoint, so a larger state makes checkpoints sparser. Must be positive.
    /// Recorded in RunRecord at FlowStart; changes affect only later runs.
    /// Set by `history.checkpointRowWriteBytesThreshold`; the deprecated key
    /// `history.checkpointMinWrittenBytes` is still accepted with a startup warning.
    pub checkpoint_row_write_bytes_threshold: u64,
    /// Defaults to 1000; a checkpoint is written once this many commands have been applied to
    /// the run since the last checkpoint, even when the byte threshold is not reached.
    ///
    /// Every command counts, including bookkeeping commands such as acknowledgments, not only
    /// commands that record history events. Time Travel replays at most this many commands from
    /// the nearest checkpoint. Must be positive.
    /// Recorded in RunRecord at FlowStart; changes affect only later runs.
    /// Set by `history.checkpointCommandThreshold`; the deprecated key
    /// `history.checkpointCommandInterval` is still accepted with a startup warning.
    pub checkpoint_command_threshold: u32,
    /// Defaults to 256 MiB per run; cap on superseded values retained for reconstruction within the window.
    /// Recorded in RunRecord at FlowStart; changes affect only later runs.
    pub max_retained_superseded_bytes: u64,
    /// Defaults to 1 GiB; most bytes that one Time Travel computation holds in memory: the
    /// writes it replays from the checkpoint before its target, the rebuilt state and its
    /// values, the external input it reapplies, and the staged result.
    ///
    /// The lease holder computes each Time Travel and replays up to
    /// `checkpoint_command_threshold` commands. A ResetFlow whose computation needs more fails
    /// with RESOURCE_EXHAUSTED; choose a later target point, skip reapplying writes, or raise
    /// this value. Must be positive. Changes apply to later ResetFlow requests.
    pub time_travel_replay_memory_bytes: u64,
    /// Defaults to false; experimental switch that offloads run history to object storage.
    /// Do not enable it until the release notes announce the feature: enabling it early can grow
    /// local disk or fail Steps. At start, the lease holder commits this value to every partition
    /// whose stored mode differs, so turning it off again takes effect at the next start.
    #[serde(rename = "offloadEnabled")]
    pub is_offload_enabled: bool,
    /// Defaults to 4 MiB; from 1 byte to 256 MiB. While history offload is on, a run seals its
    /// open history segment for upload once the segment's history records, checkpoints, and
    /// superseded values reach this many bytes. Recorded in RunRecord at FlowStart; changes
    /// affect only later runs.
    pub segment_target_bytes: u64,
    /// Defaults to 16; from 1 to 1024. Most sealed history segments the lease holder reads,
    /// encodes, and uploads to object storage at once. Higher values drain an upload backlog
    /// faster and hold more segments in memory; each in-flight segment holds about its encoded
    /// size, usually up to `segmentTargetBytes` plus one command's history.
    pub upload_concurrency: u32,
    /// Deprecated keys that the parsed YAML used; never serialized.
    #[serde(skip)]
    deprecated_keys: Vec<DeprecatedConfigKey>,
}

impl HistoryConfig {
    /// Returns the deprecated keys the parsed YAML used; startup logs a warning for each.
    pub fn deprecated_keys(&self) -> &[DeprecatedConfigKey] {
        &self.deprecated_keys
    }
}

impl Default for HistoryConfig {
    fn default() -> Self {
        Self {
            window_events: 10_000,
            prune_chunk_size: 1_000,
            max_window_events: 100_000,
            checkpoint_row_write_bytes_threshold: 1024 * 1024,
            checkpoint_command_threshold: 1_000,
            max_retained_superseded_bytes: 256 * 1024 * 1024,
            time_travel_replay_memory_bytes: 1024 * 1024 * 1024,
            is_offload_enabled: false,
            segment_target_bytes: 4 * 1024 * 1024,
            upload_concurrency: 16,
            deprecated_keys: Vec::new(),
        }
    }
}

/// A renamed configuration key that is still accepted under its old name.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct DeprecatedConfigKey {
    /// Full YAML path of the old key, such as `history.checkpointCommandInterval`.
    pub deprecated_key: &'static str,
    /// Full YAML path of the key that replaces it.
    pub replacement_key: &'static str,
}

const CHECKPOINT_ROW_WRITE_BYTES_THRESHOLD_RENAME: DeprecatedConfigKey = DeprecatedConfigKey {
    deprecated_key: "history.checkpointMinWrittenBytes",
    replacement_key: "history.checkpointRowWriteBytesThreshold",
};
const WINDOW_EVENTS_RENAME: DeprecatedConfigKey = DeprecatedConfigKey {
    deprecated_key: "history.windowSemanticEvents",
    replacement_key: "history.windowEvents",
};
const CHECKPOINT_COMMAND_THRESHOLD_RENAME: DeprecatedConfigKey = DeprecatedConfigKey {
    deprecated_key: "history.checkpointCommandInterval",
    replacement_key: "history.checkpointCommandThreshold",
};

/// The YAML shape of [`HistoryConfig`], including deprecated keys.
#[derive(Deserialize)]
#[serde(default, rename_all = "camelCase", deny_unknown_fields)]
struct HistoryConfigSource {
    #[serde(deserialize_with = "deserialize_present_key")]
    window_events: PresentKey<u32>,
    #[serde(deserialize_with = "deserialize_present_key")]
    window_semantic_events: PresentKey<u32>,
    prune_chunk_size: u32,
    max_window_events: u32,
    #[serde(deserialize_with = "deserialize_present_key")]
    checkpoint_row_write_bytes_threshold: PresentKey<u64>,
    #[serde(deserialize_with = "deserialize_present_key")]
    checkpoint_min_written_bytes: PresentKey<u64>,
    #[serde(deserialize_with = "deserialize_present_key")]
    checkpoint_command_threshold: PresentKey<u32>,
    #[serde(deserialize_with = "deserialize_present_key")]
    checkpoint_command_interval: PresentKey<u32>,
    max_retained_superseded_bytes: u64,
    time_travel_replay_memory_bytes: u64,
    #[serde(rename = "offloadEnabled")]
    is_offload_enabled: bool,
    segment_target_bytes: u64,
    upload_concurrency: u32,
}

/// A key's YAML value: `None` when the key is absent, `Some(None)` when it is an explicit null.
type PresentKey<T> = Option<Option<T>>;

/// Marks a key that appears in the YAML as present, even when its value is null.
fn deserialize_present_key<'de, D, T>(deserializer: D) -> Result<PresentKey<T>, D::Error>
where
    D: serde::Deserializer<'de>,
    T: Deserialize<'de>,
{
    Option::<T>::deserialize(deserializer).map(Some)
}

impl Default for HistoryConfigSource {
    fn default() -> Self {
        let defaults = HistoryConfig::default();
        Self {
            window_events: None,
            window_semantic_events: None,
            prune_chunk_size: defaults.prune_chunk_size,
            max_window_events: defaults.max_window_events,
            checkpoint_row_write_bytes_threshold: None,
            checkpoint_min_written_bytes: None,
            checkpoint_command_threshold: None,
            checkpoint_command_interval: None,
            max_retained_superseded_bytes: defaults.max_retained_superseded_bytes,
            time_travel_replay_memory_bytes: defaults.time_travel_replay_memory_bytes,
            is_offload_enabled: defaults.is_offload_enabled,
            segment_target_bytes: defaults.segment_target_bytes,
            upload_concurrency: defaults.upload_concurrency,
        }
    }
}

impl TryFrom<HistoryConfigSource> for HistoryConfig {
    type Error = String;

    fn try_from(source: HistoryConfigSource) -> Result<Self, Self::Error> {
        let defaults = Self::default();
        let mut deprecated_keys = Vec::new();
        let window_events = read_renamed_key(
            source.window_events,
            source.window_semantic_events,
            WINDOW_EVENTS_RENAME,
            &mut deprecated_keys,
        )?
        .unwrap_or(defaults.window_events);
        let checkpoint_row_write_bytes_threshold = read_renamed_key(
            source.checkpoint_row_write_bytes_threshold,
            source.checkpoint_min_written_bytes,
            CHECKPOINT_ROW_WRITE_BYTES_THRESHOLD_RENAME,
            &mut deprecated_keys,
        )?
        .unwrap_or(defaults.checkpoint_row_write_bytes_threshold);
        let checkpoint_command_threshold = read_renamed_key(
            source.checkpoint_command_threshold,
            source.checkpoint_command_interval,
            CHECKPOINT_COMMAND_THRESHOLD_RENAME,
            &mut deprecated_keys,
        )?
        .unwrap_or(defaults.checkpoint_command_threshold);
        Ok(Self {
            window_events,
            prune_chunk_size: source.prune_chunk_size,
            max_window_events: source.max_window_events,
            checkpoint_row_write_bytes_threshold,
            checkpoint_command_threshold,
            max_retained_superseded_bytes: source.max_retained_superseded_bytes,
            time_travel_replay_memory_bytes: source.time_travel_replay_memory_bytes,
            is_offload_enabled: source.is_offload_enabled,
            segment_target_bytes: source.segment_target_bytes,
            upload_concurrency: source.upload_concurrency,
            deprecated_keys,
        })
    }
}

/// Returns the value of a renamed key, read from its new or deprecated name but not both.
/// A present key with a null value is an error, as for every other numeric key.
fn read_renamed_key<T>(
    replacement_value: PresentKey<T>,
    deprecated_value: PresentKey<T>,
    rename: DeprecatedConfigKey,
    deprecated_keys: &mut Vec<DeprecatedConfigKey>,
) -> Result<Option<T>, String> {
    match (replacement_value, deprecated_value) {
        (Some(_), Some(_)) => Err(format!(
            "{} and its deprecated name {} are both set; keep only {}",
            rename.replacement_key, rename.deprecated_key, rename.replacement_key
        )),
        (None, Some(deprecated_value)) => {
            let value = deprecated_value.ok_or_else(|| null_key_error(rename.deprecated_key))?;
            deprecated_keys.push(rename);
            Ok(Some(value))
        }
        (Some(replacement_value), None) => replacement_value
            .map(Some)
            .ok_or_else(|| null_key_error(rename.replacement_key)),
        (None, None) => Ok(None),
    }
}

fn null_key_error(key: &str) -> String {
    format!("{key} is null; set a positive number or remove the key")
}

/// Controls value size limits, wire references, and local BlobDB placement.
#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(default, rename_all = "camelCase", deny_unknown_fields)]
pub struct ValuesConfig {
    /// Defaults to 100 bytes; larger values are sent on the wire as references unless hydration is enabled.
    pub wire_inline_threshold_bytes: u64,
    /// Defaults to 16 MiB; maximum serialized size of one application value before an API or Worker rejection.
    pub max_value_bytes: u64,
    /// Defaults to false; when true, load referenced values before Worker dispatch instead of sending references.
    /// Client reads (GetAttributes, GetChannelMessages, GetFlowState, WaitForFlow results) and InvokeRPC,
    /// including its Worker request, then also return complete values. GetHistoryEvents keeps references,
    /// which LoadBlobs resolves.
    pub hydrate_before_dispatch: bool,
    /// Defaults to 64 KiB; values at or above this size use RocksDB BlobDB locally after committed apply.
    pub blob_db_min_value_bytes: u64,
}

impl Default for ValuesConfig {
    fn default() -> Self {
        Self {
            wire_inline_threshold_bytes: 100,
            max_value_bytes: 16 * 1024 * 1024,
            hydrate_before_dispatch: false,
            blob_db_min_value_bytes: 64 * 1024,
        }
    }
}

/// Controls how long closed Flow runs and their objects remain available.
#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(default, rename_all = "camelCase", deny_unknown_fields)]
pub struct RetentionConfig {
    /// Defaults to 7 days; closed runs and their retained data become eligible for deletion afterward.
    #[serde(with = "humantime_serde")]
    pub closed_flow_duration: Duration,
}

impl Default for RetentionConfig {
    fn default() -> Self {
        Self {
            closed_flow_duration: Duration::from_secs(7 * 24 * 60 * 60),
        }
    }
}

/// Caps in-memory and persisted resources consumed by one Flow or one node.
#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(default, rename_all = "camelCase", deny_unknown_fields)]
pub struct LimitsConfig {
    /// Defaults to 64 MiB; largest active state of one Flow run.
    ///
    /// Active state is the run's current rows plus the values they hold: Attributes, Channel
    /// messages, Step executions with their inputs, locals, and attempt snapshots, timers, locks,
    /// request deduplication rows, and outbox rows. It also counts a small bookkeeping row for
    /// each stored value, superseded ones included. History records, checkpoints, and the bytes
    /// of superseded values are not counted.
    /// A command that grows active state and leaves it above this limit is rejected, or fails its
    /// Step attempt without retry. Each run records it at start.
    pub max_flow_state_bytes: u64,
    /// Defaults to 10000; largest pending message count in one Channel.
    pub max_channel_messages: u32,
    /// Defaults to 1000; most concurrently active Step executions in one Flow.
    pub max_concurrent_steps_per_flow: u32,
    /// Defaults to 256; most uncommitted speculative commands retained for one Flow.
    pub max_speculative_commands_per_flow: u32,
    /// Defaults to 64 MiB; maximum speculative state and command bytes held by one node.
    pub max_speculative_bytes_per_node: u64,
    /// Defaults to 64; caps node-wide blocking state reads (1..=u32::MAX).
    /// Effect shutdown awaits in-progress reads; budget bounds a hung read.
    pub max_blocking_state_reads: u32,
    /// Defaults to 100000; maximum concurrent long-poll waiters served by one node.
    pub max_long_poll_waiters_per_node: u32,
    /// Defaults to 1000; maximum concurrent long-poll waiters attached to one Flow.
    pub max_long_poll_waiters_per_flow: u32,
    /// Defaults to 1000; maximum shared transactional or locking RPC tasks per node, including disconnected callers.
    pub max_in_flight_rpcs_per_node: u32,
    /// Defaults to 1000; number of recent request IDs retained for deduplication within one run.
    pub max_request_ids_per_run: u32,
    /// Defaults to 1000 UTF-8 bytes; maximum application-created Flow ID length.
    pub max_flow_id_bytes: u32,
    /// Defaults to 16 MiB; largest total size of the result outputs that a child Flow delivers to
    /// its parent. A Step completion whose output would exceed it fails without retry. Must be at
    /// least 1. Each run records it at start.
    pub max_child_flow_result_bytes: u64,
}

impl Default for LimitsConfig {
    fn default() -> Self {
        Self {
            max_flow_state_bytes: 64 * 1024 * 1024,
            max_channel_messages: 10_000,
            max_concurrent_steps_per_flow: 1_000,
            max_speculative_commands_per_flow: 256,
            max_speculative_bytes_per_node: 64 * 1024 * 1024,
            max_blocking_state_reads: 64,
            max_long_poll_waiters_per_node: 100_000,
            max_long_poll_waiters_per_flow: 1_000,
            max_in_flight_rpcs_per_node: 1_000,
            max_request_ids_per_run: 1_000,
            max_flow_id_bytes: 1_000,
            max_child_flow_result_bytes: 16 * 1024 * 1024,
        }
    }
}
