// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Typed, validated configuration for the dex-server-zd executable.

mod attribute_store_config;
mod auth_config;
mod engine_config;
mod service_config;
mod source_config;
mod standby_config;
mod storage_config;
mod validation;

#[cfg(test)]
mod tests;
#[cfg(test)]
mod tunable_tests;

pub use attribute_store_config::{
    AttributeStoreColumnNaming, AttributeStoreConfig, AttributeStoreEntryConfig,
    AttributeStoreMapTableConfig, AttributeStoreRetryPolicyConfig, AttributeStoreType,
    dsn_environment_variable,
};
pub use auth_config::{AuthConfig, AuthenticatorConfig, AuthenticatorKind};
pub use engine_config::{
    ClusterConfig, CommitConfig, DeprecatedConfigKey, HistoryConfig, IgnoredConfigKey, LeaseConfig,
    LimitsConfig, RecoveryConfig, RetentionConfig, SnapshotConfig, ValuesConfig,
};
pub use service_config::{
    ApiConfig, LifecycleConfig, MatchingConfig, MetricsConfig, SearchBackend, SearchConfig,
    StreamStoreBackend, StreamStoreConfig,
};
pub(crate) use source_config::SINGLE_NODE_PARTITION_COUNT;
pub use source_config::{ConfigError, SingleNodeProfile};
pub use standby_config::{StandbyConfig, StandbyMode};
pub use storage_config::{
    CredentialSource, Db9ConnectionString, DevConfig, DevDurabilityMode, LocalStorageConfig,
    ObjectStoreConfig, ObjectStoreCredentials, ObjectStoreLocationConfig, ObjectStoreProvider,
};

use serde::{Deserialize, Serialize};

/// Groups every server-zd configuration section for validation and component injection.
#[derive(Clone, Debug, Default, Deserialize, Serialize, PartialEq)]
#[serde(default, rename_all = "camelCase", deny_unknown_fields)]
pub struct Config {
    /// Cluster identity and partition layout. Defaults to an unset node ID and 1024 partitions, or 32 partitions for `dev` and `local`.
    pub cluster: ClusterConfig,
    /// Durable S3 storage and optional hard-delete destination. Defaults to the AWS chain and an unset bucket.
    pub object_store: ObjectStoreConfig,
    /// Rebuildable RocksDB cache settings. Defaults to an unset directory, a 256 MiB block cache, and a 256 MiB write-ahead log cap.
    pub local_storage: LocalStorageConfig,
    /// Group commit and multipart thresholds. Defaults to a 20 ms window and four in-flight writes.
    pub commit: CommitConfig,
    /// Owner lease settings. Defaults to a ten-second lease, or 30 seconds for `dev` and `local`.
    pub lease: LeaseConfig,
    /// How a process waits for the lease and steps down. Defaults to a cold standby.
    pub standby: StandbyConfig,
    /// Planned shutdown and step-down. Defaults to a 30-second Step attempt drain.
    pub lifecycle: LifecycleConfig,
    /// Snapshot cadence and log cleanup. Defaults to 256 MiB or ten minutes.
    pub snapshot: SnapshotConfig,
    /// Log replay read-ahead at startup. Defaults to 64 batches and 64 MiB.
    pub recovery: RecoveryConfig,
    /// Per-run semantic history window and checkpoints. Defaults to 10000 events.
    pub history: HistoryConfig,
    /// Value sizes and wire hydration behavior. Defaults to 100-byte inline values and a 16 MiB maximum.
    pub values: ValuesConfig,
    /// Closed Flow retention. Defaults to seven days.
    pub retention: RetentionConfig,
    /// Public gRPC listener and request limits. Defaults to port 8801 and 32 MiB messages.
    pub api: ApiConfig,
    /// Public API authentication. Defaults to no authenticators, which disables authentication.
    pub auth: AuthConfig,
    /// Separate unauthenticated OpenMetrics HTTP listener. Defaults to disabled.
    pub metrics: MetricsConfig,
    /// Pull Worker matching budgets and capacities. Defaults to ten-second dispatch and five-second sticky waits.
    pub matching: MatchingConfig,
    /// Stream storage and trimming. Defaults to disabled storage.
    pub stream_store: StreamStoreConfig,
    /// Search backend and query budgets. Defaults to builtin search.
    pub search: SearchConfig,
    /// Flow, speculative state, wait, and deduplication limits.
    /// Defaults to the values in [`LimitsConfig::default`].
    pub limits: LimitsConfig,
    /// PostgreSQL Attribute synchronization with the Go Dex Server's keys. Defaults to no stores.
    pub attribute_store: AttributeStoreConfig,
    /// Paths and embedded S3 durability of `dev` and `local`. Defaults to unset paths and strict durability.
    pub dev: DevConfig,
}

impl Config {
    /// Logs one warning for each deprecated or removed key that the parsed YAML used.
    pub(crate) fn warn_about_deprecated_keys(&self) {
        for key in self.history.deprecated_keys() {
            tracing::warn!(
                deprecated_key = key.deprecated_key,
                replacement_key = key.replacement_key,
                "configuration uses a deprecated key; rename it to the replacement key"
            );
        }
        for key in self.history.ignored_keys() {
            tracing::warn!(
                ignored_key = key.ignored_key,
                reason = key.reason,
                "configuration uses a removed key that has no effect; remove it"
            );
        }
    }
}

/// Returns the CPUs available to this process, including container CPU quotas, or None when
/// they cannot be read; CPU-based defaults then use their fixed fallbacks.
pub(crate) fn available_cpu_count() -> Option<u32> {
    match std::thread::available_parallelism() {
        Ok(cpus) => Some(u32::try_from(cpus.get()).unwrap_or(u32::MAX)),
        Err(error) => {
            tracing::warn!(%error, "cannot read the available CPU count; using fixed defaults");
            None
        }
    }
}
