// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

#![cfg(test)]

use std::fs;
use std::net::{Ipv4Addr, SocketAddr};
use std::path::PathBuf;
use std::sync::{Arc, Mutex};
use std::time::Duration;

use dexzd_engine::{EffectsConfig, HistoryUploadLimits, LocalStoreConfig};

use super::engine_config::SERVE_HISTORY_WINDOW_EVENTS;
use super::{
    AttributeStoreColumnNaming, AttributeStoreType, Config, ConfigError, CredentialSource,
    DevDurabilityMode, HistoryConfig, LifecycleConfig, ObjectStoreLocationConfig, SearchBackend,
    StandbyMode, StreamStoreBackend, dsn_environment_variable,
};
use crate::DevCommandOptions;
use crate::component_configs::ComponentConfigs;

type InvalidConfigCase = (&'static str, fn(&mut Config));

fn valid_config() -> Config {
    let mut configuration = Config::default();
    configuration.cluster.node_id = "node-a".to_owned();
    configuration.object_store.bucket = "dex-test".to_owned();
    configuration.local_storage.directory = PathBuf::from("/tmp/dexzd-test-store");
    configuration
}

#[test]
fn example_declares_every_default_key_and_value() {
    let sample = include_str!("../../../../config/dex-server-zd.example.yaml");
    let parsed_directory = Config::parse_yaml("history:\n  readCacheDirectory: /cache/history\n")
        .expect("parse the history read cache directory");
    let mut with_directory = valid_config();
    with_directory.history.read_cache_directory = parsed_directory.history.read_cache_directory;
    assert_eq!(
        ComponentConfigs::from_config(&with_directory, false)
            .expect("component config")
            .history_reads
            .read_cache_directory,
        Some(std::path::PathBuf::from("/cache/history"))
    );
    let parsed = Config::parse_yaml(sample).expect("sample YAML must parse");
    assert_eq!(parsed, Config::default());
    let declared: serde_yaml_ng::Value =
        serde_yaml_ng::from_str(sample).expect("sample YAML value");
    let expected = serde_yaml_ng::to_value(Config::default()).expect("default YAML value");
    assert_same_yaml_keys(&declared, &expected);
    assert_eq!(parsed.cluster.partition_count, 1024);
    assert_eq!(parsed.commit.batch_window, Duration::from_millis(20));
    assert_eq!(parsed.values.wire_inline_threshold_bytes, 100);
    assert_eq!(parsed.values.max_value_bytes, 16 * 1024 * 1024);
    assert_eq!(parsed.api.grpc_max_message_bytes, 32 * 1024 * 1024);
    assert_eq!(parsed.api.max_wait_seconds, 60);
    assert!(parsed.metrics.listen.is_empty());
    assert_eq!(parsed.matching.dispatch_wait, Duration::from_secs(10));
    assert_eq!(parsed.stream_store.backend, StreamStoreBackend::Memory);
    assert_eq!(
        parsed.stream_store.trim_batch_yield_time,
        Duration::from_millis(1)
    );
}

fn assert_same_yaml_keys(declared: &serde_yaml_ng::Value, expected: &serde_yaml_ng::Value) {
    if let (serde_yaml_ng::Value::Mapping(declared), serde_yaml_ng::Value::Mapping(expected)) =
        (declared, expected)
    {
        assert_eq!(declared.len(), expected.len());
        for (key, expected_value) in expected {
            let declared_value = declared.get(key).expect("sample must declare every key");
            assert_same_yaml_keys(declared_value, expected_value);
        }
    }
}

#[test]
fn yaml_overrides_sections_and_rejects_unknown_keys() {
    let parsed = Config::parse_yaml("cluster:\n  nodeId: node-a\nobjectStore:\n  bucket: dex-test\nlocalStorage:\n  directory: /tmp/dexzd\napi:\n  maxWaitSeconds: 60\nmatching:\n  dispatchWait: 12s\n  stickyWait: 4s\nvalues:\n  maxValueBytes: 20000000\nsearch:\n  backend: paradedb\n  paradeDbConnectionString: postgres://localhost/dex\n")
        .expect("valid YAML override");
    assert_eq!(parsed.matching.dispatch_wait, Duration::from_secs(12));
    assert_eq!(parsed.matching.sticky_wait, Duration::from_secs(4));
    assert_eq!(parsed.values.max_value_bytes, 20_000_000);
    assert_eq!(parsed.api.max_wait_seconds, 60);
    assert_eq!(parsed.search.backend, SearchBackend::Paradedb);
    assert!(!format!("{parsed:?}").contains("postgres://localhost/dex"));
    let serialized = serde_yaml_ng::to_string(&parsed).expect("serialize redacted config");
    assert!(!serialized.contains("postgres://localhost/dex"));
    assert!(serialized.contains("[redacted]"));
    parsed.validate().expect("complete YAML configuration");

    for yaml in [
        "unexpected: true",
        "matching:\n  dispachWait: 10s",
        "values:\n  maxValueBytes: -1",
        "api:\n  maxWaitSeconds: 60s",
    ] {
        assert!(
            matches!(Config::parse_yaml(yaml), Err(ConfigError::Yaml { .. })),
            "{yaml}"
        );
    }
}

#[test]
fn blocking_state_read_limit_parses_defaults_and_rejects_zero() {
    assert_eq!(Config::default().limits.max_blocking_state_reads, 64);

    let parsed = Config::parse_yaml("limits:\n  maxBlockingStateReads: 7\n")
        .expect("parse blocking state read limit");
    assert_eq!(parsed.limits.max_blocking_state_reads, 7);
    let mut configuration = valid_config();
    configuration.limits.max_blocking_state_reads = parsed.limits.max_blocking_state_reads;
    configuration.validate().expect("positive read limit");
    let components =
        ComponentConfigs::from_config(&configuration, false).expect("component config");
    assert_eq!(components.effects.max_blocking_state_reads, 7);

    let zero = Config::parse_yaml("limits:\n  maxBlockingStateReads: 0\n")
        .expect("parse zero blocking state read limit");
    configuration.limits.max_blocking_state_reads = zero.limits.max_blocking_state_reads;
    assert!(matches!(
        configuration.validate(),
        Err(ConfigError::Invalid {
            field: "limits.maxBlockingStateReads",
            ..
        })
    ));
}

#[test]
fn history_segment_target_bytes_reaches_run_and_child_settings() {
    let parsed = Config::parse_yaml("history:\n  segmentTargetBytes: 65536\n")
        .expect("parse the history segment target");
    let mut configuration = valid_config();
    configuration.history.segment_target_bytes = parsed.history.segment_target_bytes;
    configuration
        .validate()
        .expect("a valid history segment target");
    let components =
        ComponentConfigs::from_config(&configuration, false).expect("component config");
    assert_eq!(
        components
            .flow_coordination
            .run_settings
            .history_segment_target_bytes,
        65536
    );
    assert_eq!(
        components
            .effects
            .child_run_settings
            .history_segment_target_bytes,
        65536
    );
}

#[test]
fn superseded_value_grace_period_reaches_run_and_child_settings() {
    let default_components =
        ComponentConfigs::from_config(&valid_config(), false).expect("component config");
    assert_eq!(
        default_components
            .flow_coordination
            .run_settings
            .superseded_value_grace_micros,
        10 * 60 * 1_000_000
    );
    let parsed = Config::parse_yaml("history:\n  supersededValueGracePeriod: 90s\n")
        .expect("parse the superseded value grace period");
    let mut configuration = valid_config();
    configuration.history.superseded_value_grace_period =
        parsed.history.superseded_value_grace_period;
    configuration.validate().expect("a valid grace period");
    let components =
        ComponentConfigs::from_config(&configuration, false).expect("component config");
    assert_eq!(
        components
            .flow_coordination
            .run_settings
            .superseded_value_grace_micros,
        90 * 1_000_000
    );
    assert_eq!(
        components
            .effects
            .child_run_settings
            .superseded_value_grace_micros,
        90 * 1_000_000
    );
    for too_short in [Duration::ZERO, Duration::from_nanos(500)] {
        configuration.history.superseded_value_grace_period = too_short;
        assert!(
            matches!(
                configuration.validate(),
                Err(ConfigError::Invalid {
                    field: "history.supersededValueGracePeriod",
                    ..
                })
            ),
            "{too_short:?} is rejected"
        );
    }
    configuration.history.superseded_value_grace_period = Duration::from_micros(1);
    configuration
        .validate()
        .expect("one microsecond is the shortest grace period");
}

#[test]
fn removed_history_keys_are_accepted_ignored_and_logged_at_startup() {
    let yaml = "history:\n  offloadEnabled: true\n  deleteExpiredObjects: true\n  \
                maxRetainedSupersededBytes: 1048576\n";
    let parsed = Config::parse_yaml(yaml).expect("removed history keys still parse");
    let ignored_keys: Vec<_> = parsed
        .history
        .ignored_keys()
        .iter()
        .map(|key| key.ignored_key)
        .collect();
    assert_eq!(
        ignored_keys,
        [
            "history.offloadEnabled",
            "history.deleteExpiredObjects",
            "history.maxRetainedSupersededBytes",
        ]
    );
    assert!(parsed.history.deprecated_keys().is_empty());
    let log = captured_warnings(&parsed);
    for key in &ignored_keys {
        assert!(
            log.contains("configuration uses a removed key that has no effect")
                && log.contains(&format!("ignored_key=\"{key}\"")),
            "{log}"
        );
    }
    let mut configuration = valid_config();
    configuration.history = parsed.history;
    configuration
        .validate()
        .expect("removed keys never fail validation");
    let components =
        ComponentConfigs::from_config(&configuration, false).expect("component config");
    for settings in [
        components.flow_coordination.run_settings,
        components.effects.child_run_settings,
    ] {
        assert_eq!(
            (
                settings.max_retained_superseded_bytes,
                settings.delete_expired_history_objects,
                settings.offload_history,
            ),
            (256 * 1024 * 1024, false, Some(true)),
            "runs record the built-in values, which runs with an offload choice ignore"
        );
    }

    let temporary = tempfile::tempdir().expect("test directory");
    let overrides_path = temporary.path().join("removed-history-keys.yaml");
    fs::write(&overrides_path, yaml).expect("write test config");
    for load in [Config::load_dev, Config::load_local] {
        let loaded = load(&single_node_options(
            temporary.path().join("data"),
            Some(overrides_path.clone()),
        ))
        .expect("single-node commands accept the removed keys");
        assert_eq!(loaded.history.ignored_keys().len(), 3);
    }
}

#[test]
fn history_offload_set_to_false_fails_startup_in_every_mode() {
    let yaml = "history:\n  offloadEnabled: false\n";
    let assert_offload_error = |error: ConfigError| {
        assert!(matches!(error, ConfigError::Yaml { .. }), "{error}");
        let message = error.to_string();
        assert!(
            message.contains("the server can no longer turn history offload off")
                && message.contains("remove history.offloadEnabled"),
            "{message}"
        );
    };
    assert_offload_error(
        Config::parse_yaml(yaml).expect_err("serve rejects offloadEnabled: false"),
    );
    let temporary = tempfile::tempdir().expect("test directory");
    let overrides_path = temporary.path().join("offload-disabled.yaml");
    fs::write(&overrides_path, yaml).expect("write test config");
    for load in [Config::load_dev, Config::load_local] {
        assert_offload_error(
            load(&single_node_options(
                temporary.path().join("data"),
                Some(overrides_path.clone()),
            ))
            .expect_err("single-node commands reject offloadEnabled: false"),
        );
    }
}

#[test]
fn each_mode_gets_its_default_history_window() {
    let max_window_events = HistoryConfig::default().max_window_events;
    let serve = Config::parse_yaml("cluster:\n  nodeId: node-a\n").expect("serve YAML");
    assert_eq!(serve.history.window_events, SERVE_HISTORY_WINDOW_EVENTS);
    assert_eq!(serve.history.window_events, 50_000);
    assert_eq!(Config::default().history.window_events, 50_000);
    let temporary = tempfile::tempdir().expect("test directory");
    let other_history_key_path = temporary.path().join("history-upload-concurrency.yaml");
    fs::write(
        &other_history_key_path,
        "history:\n  uploadConcurrency: 5\n",
    )
    .expect("write test config");
    let window_path = temporary.path().join("history-window.yaml");
    fs::write(&window_path, "history:\n  windowEvents: 2000\n").expect("write test config");
    let deprecated_window_path = temporary.path().join("history-semantic-window.yaml");
    fs::write(
        &deprecated_window_path,
        "history:\n  windowSemanticEvents: 3000\n",
    )
    .expect("write test config");
    for load in [Config::load_dev, Config::load_local] {
        let load_with = |overrides: Option<PathBuf>| {
            load(&single_node_options(
                temporary.path().join("data"),
                overrides,
            ))
            .expect("single-node configuration")
        };
        let without_overrides = load_with(None);
        assert_eq!(without_overrides.history.window_events, 5_000);
        assert_eq!(
            load_with(Some(other_history_key_path.clone()))
                .history
                .window_events,
            5_000
        );
        assert_eq!(
            load_with(Some(window_path.clone())).history.window_events,
            2_000
        );
        assert_eq!(
            load_with(Some(deprecated_window_path.clone()))
                .history
                .window_events,
            3_000,
            "a window under the deprecated key is not replaced by the default"
        );
        let components =
            ComponentConfigs::from_config(&without_overrides, false).expect("component config");
        assert_eq!(
            components
                .flow_coordination
                .run_settings
                .history_window_events,
            5_000
        );
        assert_eq!(
            components.effects.child_run_settings.history_window_events,
            5_000
        );
        assert!(without_overrides.history.window_events <= max_window_events);
        assert!(
            without_overrides.history.prune_chunk_size <= without_overrides.history.window_events
        );
    }
    assert!(serve.history.window_events <= max_window_events);
    assert!(serve.history.prune_chunk_size <= serve.history.window_events);
}

#[test]
fn history_upload_limits_reach_the_effect_runner() {
    let defaults = Config::default().history;
    assert_eq!(defaults.upload_concurrency, 256);
    assert_eq!(defaults.upload_min_concurrency, 16);
    assert_eq!(defaults.upload_max_in_flight_bytes, 128 * 1024 * 1024);
    assert_eq!(
        ComponentConfigs::from_config(&valid_config(), false)
            .expect("component config")
            .effects
            .history_upload_limits,
        HistoryUploadLimits {
            max_concurrency: 256,
            min_concurrency: 16,
            max_in_flight_bytes: 128 * 1024 * 1024,
        }
    );
    // A configuration from before the adaptive limit still validates, with a smaller cap.
    let parsed = Config::parse_yaml("history:\n  uploadConcurrency: 3\n")
        .expect("parse the history upload concurrency");
    let mut configuration = valid_config();
    configuration.history.upload_concurrency = parsed.history.upload_concurrency;
    configuration
        .validate()
        .expect("a valid history upload concurrency");
    let components =
        ComponentConfigs::from_config(&configuration, false).expect("component config");
    assert_eq!(components.effects.history_upload_limits.max_concurrency, 3);
    assert_eq!(components.effects.history_upload_limits.min_concurrency, 16);

    let parsed = Config::parse_yaml(
        "history:\n  uploadMinConcurrency: 8\n  uploadMaxInFlightBytes: 33554432\n",
    )
    .expect("parse the history upload limits");
    let mut configuration = valid_config();
    configuration.history.upload_min_concurrency = parsed.history.upload_min_concurrency;
    configuration.history.upload_max_in_flight_bytes = parsed.history.upload_max_in_flight_bytes;
    configuration
        .validate()
        .expect("valid history upload limits");
    let components =
        ComponentConfigs::from_config(&configuration, false).expect("component config");
    assert_eq!(components.effects.history_upload_limits.min_concurrency, 8);
    assert_eq!(
        components.effects.history_upload_limits.max_in_flight_bytes,
        32 * 1024 * 1024
    );
}

#[test]
fn history_read_settings_reach_the_segment_reader() {
    let defaults = Config::default();
    assert_eq!(defaults.history.read_cache_max_bytes, 1 << 30);
    assert_eq!(defaults.history.object_read_concurrency, 32);
    let mut configuration = valid_config();
    let reads = ComponentConfigs::from_config(&configuration, false)
        .expect("component config")
        .history_reads;
    assert_eq!(reads.read_cache_max_bytes, 1 << 30);
    assert_eq!(reads.object_read_concurrency, 32);
    assert_eq!(
        reads.read_cache_directory,
        Some(
            configuration
                .local_storage
                .directory
                .with_file_name(format!(
                    "{}-history-read-cache",
                    configuration
                        .local_storage
                        .directory
                        .file_name()
                        .expect("the test store directory has a name")
                        .to_string_lossy()
                ))
        )
    );
    let parsed =
        Config::parse_yaml("history:\n  readCacheMaxBytes: 0\n  objectReadConcurrency: 4\n")
            .expect("parse the history read settings");
    configuration.history.read_cache_max_bytes = parsed.history.read_cache_max_bytes;
    configuration.history.object_read_concurrency = parsed.history.object_read_concurrency;
    configuration
        .validate()
        .expect("valid history read settings");
    let reads = ComponentConfigs::from_config(&configuration, false)
        .expect("component config")
        .history_reads;
    assert_eq!(reads.read_cache_max_bytes, 0);
    assert_eq!(reads.object_read_concurrency, 4);
}

#[cfg(unix)]
#[test]
fn a_read_cache_directory_inside_the_local_store_is_rejected_through_any_alias() {
    let root = tempfile::tempdir().expect("a temporary directory");
    let store = root.path().join("store");
    std::fs::create_dir_all(&store).expect("the store directory is created");
    let alias = root.path().join("alias");
    std::os::unix::fs::symlink(&store, &alias).expect("the alias links to the store");
    let is_rejected = |local_storage_directory: PathBuf, read_cache_directory: PathBuf| {
        let mut configuration = valid_config();
        configuration.local_storage.directory = local_storage_directory;
        configuration.history.read_cache_directory = Some(read_cache_directory);
        matches!(
            configuration.validate(),
            Err(ConfigError::Invalid {
                field: "history.readCacheDirectory",
                ..
            })
        )
    };
    assert!(
        is_rejected(store.clone(), alias.join("cache")),
        "a symlink alias"
    );
    assert!(
        is_rejected(store.clone(), root.path().join("other/../store/cache")),
        "parent components"
    );
    assert!(
        is_rejected(store.clone(), root.path().join("missing/../alias/cache")),
        "parent components after a missing directory, through a symlink"
    );
    assert!(
        is_rejected(store.clone(), root.path().join("elsewhere/../outside")),
        "any parent component, even one that would resolve outside the store"
    );
    assert!(
        is_rejected(alias.clone(), store.join("not-yet-created/cache")),
        "a local store named through a symlink"
    );
    let current = std::env::current_dir().expect("a current directory");
    assert!(
        is_rejected(
            PathBuf::from("relative-store-for-validation"),
            current.join("relative-store-for-validation/cache"),
        ),
        "a relative local store and an absolute cache"
    );
    assert!(
        !is_rejected(store.clone(), root.path().join("store-history-read-cache")),
        "a sibling with the store's name as a prefix is accepted"
    );
}

#[test]
fn run_limits_and_the_child_result_limit_reach_run_settings() {
    assert_eq!(
        Config::default().limits.max_child_flow_result_bytes,
        16 * 1024 * 1024
    );
    let parsed = Config::parse_yaml(
        "values:\n  maxValueBytes: 1048576\nlimits:\n  maxFlowStateBytes: 4194304\n  maxChildFlowResultBytes: 2048\n",
    )
    .expect("parse run limits");
    let mut configuration = valid_config();
    configuration.values.max_value_bytes = parsed.values.max_value_bytes;
    configuration.limits.max_flow_state_bytes = parsed.limits.max_flow_state_bytes;
    configuration.limits.max_child_flow_result_bytes = parsed.limits.max_child_flow_result_bytes;
    configuration.validate().expect("valid run limits");
    let components =
        ComponentConfigs::from_config(&configuration, false).expect("component config");
    let run_settings = components.flow_coordination.run_settings;
    assert_eq!(run_settings.max_run_state_bytes, 4 * 1024 * 1024);
    assert_eq!(run_settings.max_value_bytes, 1024 * 1024);
    assert_eq!(run_settings.max_child_flow_result_bytes, 2048);
    let child_run_settings = components.effects.child_run_settings;
    assert_eq!(child_run_settings.max_child_flow_result_bytes, 2048);
    assert_eq!(child_run_settings.history_segment_target_bytes, 4 << 20);
    assert_eq!(run_settings.history_segment_target_bytes, 4 << 20);
    assert_eq!(
        child_run_settings.max_run_state_bytes,
        EffectsConfig::default()
            .child_run_settings
            .max_run_state_bytes
    );
}

#[test]
fn checkpoint_thresholds_default_and_reach_run_settings() {
    let defaults = ComponentConfigs::from_config(&valid_config(), false).expect("component config");
    let default_run_settings = defaults.flow_coordination.run_settings;
    assert_eq!(default_run_settings.checkpoint_command_threshold, 1_000);
    assert_eq!(
        default_run_settings.checkpoint_row_write_bytes_threshold,
        1024 * 1024
    );
    assert_eq!(
        defaults
            .effects
            .child_run_settings
            .checkpoint_command_threshold,
        1_000
    );
    let parsed = Config::parse_yaml(
        "history:\n  checkpointRowWriteBytesThreshold: 4096\n  checkpointCommandThreshold: 250\n",
    )
    .expect("parse checkpoint thresholds");
    assert!(parsed.history.deprecated_keys().is_empty());
    let mut configuration = valid_config();
    configuration.history = parsed.history;
    configuration
        .validate()
        .expect("valid checkpoint thresholds");
    let run_settings = ComponentConfigs::from_config(&configuration, false)
        .expect("component config")
        .flow_coordination
        .run_settings;
    assert_eq!(run_settings.checkpoint_row_write_bytes_threshold, 4096);
    assert_eq!(run_settings.checkpoint_command_threshold, 250);
}

#[test]
fn deprecated_checkpoint_keys_apply_and_log_a_startup_warning() {
    let parsed = Config::parse_yaml(
        "history:\n  checkpointMinWrittenBytes: 4096\n  checkpointCommandInterval: 250\n",
    )
    .expect("deprecated checkpoint keys still parse");
    assert_eq!(parsed.history.checkpoint_row_write_bytes_threshold, 4096);
    assert_eq!(parsed.history.checkpoint_command_threshold, 250);
    let deprecated_keys: Vec<_> = parsed
        .history
        .deprecated_keys()
        .iter()
        .map(|key| (key.deprecated_key, key.replacement_key))
        .collect();
    assert_eq!(
        deprecated_keys,
        [
            (
                "history.checkpointMinWrittenBytes",
                "history.checkpointRowWriteBytesThreshold"
            ),
            (
                "history.checkpointCommandInterval",
                "history.checkpointCommandThreshold"
            ),
        ]
    );
    let log = captured_warnings(&parsed);
    assert!(
        log.contains("configuration uses a deprecated key")
            && log.contains("deprecated_key=\"history.checkpointMinWrittenBytes\"")
            && log.contains("replacement_key=\"history.checkpointCommandThreshold\""),
        "{log}"
    );
    assert!(captured_warnings(&Config::default()).is_empty());
}

#[test]
fn a_checkpoint_key_set_under_both_names_is_rejected() {
    for (yaml, deprecated_key) in [
        (
            "history:\n  checkpointMinWrittenBytes: 4096\n  checkpointRowWriteBytesThreshold: 4096\n",
            "history.checkpointMinWrittenBytes",
        ),
        (
            "history:\n  checkpointCommandThreshold: 250\n  checkpointCommandInterval: 250\n",
            "history.checkpointCommandInterval",
        ),
        (
            "history:\n  checkpointCommandThreshold: 250\n  checkpointCommandInterval: null\n",
            "history.checkpointCommandInterval",
        ),
        (
            "history:\n  checkpointCommandThreshold: null\n  checkpointCommandInterval: 250\n",
            "history.checkpointCommandInterval",
        ),
        (
            "history:\n  checkpointRowWriteBytesThreshold: 4096\n  checkpointMinWrittenBytes: null\n",
            "history.checkpointMinWrittenBytes",
        ),
        (
            "history:\n  checkpointRowWriteBytesThreshold: null\n  checkpointMinWrittenBytes: 4096\n",
            "history.checkpointMinWrittenBytes",
        ),
    ] {
        let error = Config::parse_yaml(yaml).expect_err("both names of one key must fail");
        assert!(matches!(error, ConfigError::Yaml { .. }), "{error}");
        assert!(
            error.to_string().contains(deprecated_key) && error.to_string().contains("both set"),
            "{error}"
        );
    }
}

#[test]
fn a_null_checkpoint_key_is_rejected_by_name() {
    for key in [
        "checkpointRowWriteBytesThreshold",
        "checkpointMinWrittenBytes",
        "checkpointCommandThreshold",
        "checkpointCommandInterval",
    ] {
        let error = Config::parse_yaml(&format!("history:\n  {key}: null\n"))
            .expect_err("a null checkpoint key must fail");
        assert!(matches!(error, ConfigError::Yaml { .. }), "{error}");
        assert!(
            error
                .to_string()
                .contains(&format!("history.{key} is null")),
            "{error}"
        );
    }
}

#[test]
fn time_travel_replay_memory_reaches_the_flow_runtime_and_must_be_positive() {
    assert_eq!(
        Config::default().history.time_travel_replay_memory_bytes,
        1024 * 1024 * 1024
    );
    assert_eq!(
        ComponentConfigs::from_config(&valid_config(), false)
            .expect("component config")
            .flow_coordination
            .time_travel_replay_memory_bytes,
        1024 * 1024 * 1024
    );
    let parsed = Config::parse_yaml("history:\n  timeTravelReplayMemoryBytes: 4096\n")
        .expect("parse the time travel replay memory");
    let mut configuration = valid_config();
    configuration.history.time_travel_replay_memory_bytes =
        parsed.history.time_travel_replay_memory_bytes;
    configuration
        .validate()
        .expect("a positive replay memory is valid");
    let components =
        ComponentConfigs::from_config(&configuration, false).expect("component config");
    assert_eq!(
        components.flow_coordination.time_travel_replay_memory_bytes,
        4096
    );
    configuration.history.time_travel_replay_memory_bytes = 0;
    let error = configuration
        .validate()
        .expect_err("a zero replay memory is invalid");
    assert!(
        error
            .to_string()
            .contains("history.timeTravelReplayMemoryBytes"),
        "{error}"
    );
}

#[test]
fn history_window_settings_default_and_reach_run_settings_and_validation() {
    let defaults = ComponentConfigs::from_config(&valid_config(), false).expect("component config");
    let default_run_settings = defaults.flow_coordination.run_settings;
    assert_eq!(default_run_settings.history_window_events, 50_000);
    assert_eq!(default_run_settings.history_prune_chunk_events, 1_000);
    assert_eq!(defaults.validation().max_history_window_events, 100_000);
    assert_eq!(
        defaults.effects.child_run_settings.history_window_events,
        50_000
    );
    let parsed = Config::parse_yaml(
        "history:\n  windowEvents: 2000\n  pruneChunkSize: 50\n  maxWindowEvents: 5000\n",
    )
    .expect("parse history window settings");
    assert!(parsed.history.deprecated_keys().is_empty());
    let mut configuration = valid_config();
    configuration.history = parsed.history;
    configuration.validate().expect("valid history window");
    let components =
        ComponentConfigs::from_config(&configuration, false).expect("component config");
    let run_settings = components.flow_coordination.run_settings;
    assert_eq!(run_settings.history_window_events, 2000);
    assert_eq!(run_settings.history_prune_chunk_events, 50);
    assert_eq!(components.validation().max_history_window_events, 5000);
    let child_run_settings = components.effects.child_run_settings;
    assert_eq!(child_run_settings.history_window_events, 2000);
    assert_eq!(child_run_settings.history_prune_chunk_events, 50);
    // The per-run maximum does not limit the server window.
    let mut configuration = valid_config();
    configuration.history.window_events = 200_000;
    configuration.history.max_window_events = 1000;
    configuration
        .validate()
        .expect("a server window above the per-run maximum stays valid");
}

#[test]
fn the_deprecated_window_key_applies_and_logs_a_startup_warning() {
    let parsed = Config::parse_yaml("history:\n  windowSemanticEvents: 2000\n")
        .expect("the deprecated window key still parses");
    assert_eq!(parsed.history.window_events, 2000);
    let deprecated_keys: Vec<_> = parsed
        .history
        .deprecated_keys()
        .iter()
        .map(|key| (key.deprecated_key, key.replacement_key))
        .collect();
    assert_eq!(
        deprecated_keys,
        [("history.windowSemanticEvents", "history.windowEvents")]
    );
    let log = captured_warnings(&parsed);
    assert!(
        log.contains("configuration uses a deprecated key")
            && log.contains("deprecated_key=\"history.windowSemanticEvents\"")
            && log.contains("replacement_key=\"history.windowEvents\""),
        "{log}"
    );
}

#[test]
fn the_window_key_set_under_both_names_or_null_is_rejected() {
    for yaml in [
        "history:\n  windowEvents: 2000\n  windowSemanticEvents: 2000\n",
        "history:\n  windowEvents: 2000\n  windowSemanticEvents: null\n",
        "history:\n  windowEvents: null\n  windowSemanticEvents: 2000\n",
    ] {
        let error = Config::parse_yaml(yaml).expect_err("both names of one key must fail");
        assert!(matches!(error, ConfigError::Yaml { .. }), "{error}");
        assert!(
            error.to_string().contains("history.windowSemanticEvents")
                && error.to_string().contains("both set"),
            "{error}"
        );
    }
    for key in ["windowEvents", "windowSemanticEvents"] {
        let error = Config::parse_yaml(&format!("history:\n  {key}: null\n"))
            .expect_err("a null window key must fail");
        assert!(matches!(error, ConfigError::Yaml { .. }), "{error}");
        assert!(
            error
                .to_string()
                .contains(&format!("history.{key} is null")),
            "{error}"
        );
    }
}

/// Returns what the deprecated-key startup check logs for `configuration`.
fn captured_warnings(configuration: &Config) -> String {
    let buffer = Arc::new(Mutex::new(Vec::new()));
    let writer_buffer = buffer.clone();
    let subscriber = tracing_subscriber::fmt()
        .with_ansi(false)
        .with_writer(move || CapturedLog(writer_buffer.clone()))
        .finish();
    tracing::subscriber::with_default(subscriber, || {
        configuration.warn_about_deprecated_keys();
    });
    let bytes = buffer
        .lock()
        .expect("the log buffer lock is not poisoned")
        .clone();
    String::from_utf8(bytes).expect("logs are UTF-8")
}

struct CapturedLog(Arc<Mutex<Vec<u8>>>);

impl std::io::Write for CapturedLog {
    fn write(&mut self, bytes: &[u8]) -> std::io::Result<usize> {
        self.0
            .lock()
            .expect("the log buffer lock is not poisoned")
            .extend_from_slice(bytes);
        Ok(bytes.len())
    }

    fn flush(&mut self) -> std::io::Result<()> {
        Ok(())
    }
}

#[test]
fn replay_and_local_store_disk_settings_reach_the_engine_node() {
    let defaults = ComponentConfigs::from_config(&valid_config(), false).expect("component config");
    assert_eq!(
        defaults
            .engine_node
            .recovery
            .log_read_ahead
            .max_read_ahead_batches,
        64
    );
    assert_eq!(
        defaults
            .engine_node
            .recovery
            .log_read_ahead
            .max_buffered_bytes,
        64 * 1024 * 1024
    );
    assert_eq!(
        defaults.engine_node.recovery.compact_after_replay_bytes,
        16 * 1024 * 1024
    );
    assert_eq!(
        defaults.engine_node.recovery.retained_set_aside_store_count,
        1
    );

    let parsed = Config::parse_yaml(
        "localStorage:\n  maxWalBytes: 134217728\n  compactAfterReplayBytes: 0\n  retainedSetAsideStoreCount: 0\nrecovery:\n  readAheadBatches: 8\n  readAheadBytes: 1048576\n",
    )
    .expect("parse replay settings");
    let mut configuration = valid_config();
    configuration.local_storage.max_wal_bytes = parsed.local_storage.max_wal_bytes;
    configuration.local_storage.compact_after_replay_bytes =
        parsed.local_storage.compact_after_replay_bytes;
    configuration.local_storage.retained_set_aside_store_count =
        parsed.local_storage.retained_set_aside_store_count;
    configuration.recovery = parsed.recovery;
    configuration.validate().expect("valid replay settings");
    let components =
        ComponentConfigs::from_config(&configuration, false).expect("component config");
    let recovery = components.engine_node.recovery;
    assert_eq!(recovery.startup_read_retry_budget, Duration::from_secs(30));
    assert_eq!(recovery.log_read_ahead.max_read_ahead_batches, 8);
    assert_eq!(recovery.log_read_ahead.max_buffered_bytes, 1024 * 1024);
    assert_eq!(recovery.compact_after_replay_bytes, 0);
    assert_eq!(recovery.retained_set_aside_store_count, 0);
    let LocalStoreConfig::RocksDb { options, .. } = components.engine_node.local_store else {
        panic!("serve uses a RocksDB local store");
    };
    assert_eq!(options.max_total_wal_bytes, 128 * 1024 * 1024);
}

#[test]
fn replay_check_yaml_override_reaches_the_engine_node() {
    let mut configuration = valid_config();
    assert_eq!(configuration.commit.debug_replay_checks, None);
    let parsed = Config::parse_yaml("commit:\n  debugReplayChecks: false\n")
        .expect("parse replay check override");
    configuration.commit = parsed.commit;
    let components =
        ComponentConfigs::from_config(&configuration, false).expect("component config");
    assert!(!components.engine_node.commit.debug_replay_checks);
}

#[test]
fn dev_flags_override_yaml_and_create_documented_local_layout() {
    let temporary = tempfile::tempdir().expect("test directory");
    let yaml_path = temporary.path().join("config.yaml");
    fs::write(&yaml_path, "cluster:\n  nodeId: other\nobjectStore:\n  endpoint: 'https://external.invalid'\n  region: other-region\napi:\n  listen: '0.0.0.0:9000'\nsearch:\n  backend: paradedb\n  paradeDbConnectionString: postgres://localhost/dex\nstreamStore:\n  backend: disabled\ndev:\n  durabilityMode: relaxed\n").expect("write test config");
    let data_directory = temporary.path().join("data");
    let log_directory = temporary.path().join("logs");
    let options = DevCommandOptions {
        data_dir: data_directory.clone(),
        listen: Some(SocketAddr::from((Ipv4Addr::LOCALHOST, 8801))),
        log_dir: Some(log_directory.clone()),
        config: Some(yaml_path),
        attribute_store_config: None,
        durability: None,
    };
    let configuration = Config::load_dev(&options).expect("dev configuration");
    let flag_override = Config::load_dev(&DevCommandOptions {
        data_dir: options.data_dir.clone(),
        listen: options.listen,
        log_dir: None,
        config: options.config.clone(),
        attribute_store_config: None,
        durability: Some(DevDurabilityMode::ProcessCrashSafe),
    })
    .expect("dev configuration with a durability flag");
    assert_eq!(
        flag_override.dev.durability_mode,
        DevDurabilityMode::ProcessCrashSafe,
        "--durability overrides dev.durabilityMode from --config"
    );
    assert_eq!(configuration.cluster.node_id, "dev");
    assert_eq!(Some(configuration.api.listen), options.listen);
    assert_eq!(configuration.dev.data_directory, data_directory);
    assert_eq!(
        configuration.dev.s3_data_directory,
        options.data_dir.join("s3")
    );
    assert_eq!(
        configuration.local_storage.directory,
        options.data_dir.join("store")
    );
    assert_eq!(configuration.dev.log_directory, Some(log_directory));
    assert_eq!(
        configuration.dev.durability_mode,
        DevDurabilityMode::Relaxed
    );
    assert_eq!(configuration.object_store.bucket, "dex-dev");
    assert_eq!(configuration.object_store.endpoint, "http://127.0.0.1:0");
    assert_eq!(configuration.object_store.region, "us-east-1");
    assert!(configuration.object_store.force_path_style);
    assert_eq!(
        configuration.object_store.credentials_source,
        CredentialSource::Anonymous
    );
    assert_eq!(configuration.search.backend, SearchBackend::Paradedb);
    assert_eq!(
        configuration.stream_store.backend,
        StreamStoreBackend::Disabled
    );
    let defaults = Config::load_dev(&DevCommandOptions {
        data_dir: temporary.path().join("other-data"),
        listen: options.listen,
        log_dir: None,
        config: None,
        attribute_store_config: None,
        durability: None,
    })
    .expect("dev defaults without YAML");
    assert_eq!(
        defaults.dev.s3_data_directory,
        temporary.path().join("other-data/s3")
    );
    assert_eq!(
        defaults.local_storage.directory,
        temporary.path().join("other-data/store")
    );
    assert_eq!(defaults.dev.durability_mode, DevDurabilityMode::Strict);
    assert_eq!(defaults.search.backend, SearchBackend::Builtin);
    assert_eq!(defaults.stream_store.backend, StreamStoreBackend::Memory);
    assert_eq!(defaults.object_store.endpoint, "http://127.0.0.1:0");
    let empty_data_directory = Config::load_dev(&DevCommandOptions {
        data_dir: PathBuf::new(),
        listen: options.listen,
        log_dir: None,
        config: None,
        attribute_store_config: None,
        durability: None,
    })
    .expect_err("empty dev data directory");
    assert!(matches!(
        empty_data_directory,
        ConfigError::Invalid {
            field: "dev.dataDirectory",
            ..
        }
    ));
}

#[test]
fn dev_defaults_to_a_thirty_second_lease_unless_the_overrides_set_its_duration() {
    let temporary = tempfile::tempdir().expect("test directory");
    let dev_options = |config: Option<PathBuf>| DevCommandOptions {
        data_dir: temporary.path().join("data"),
        listen: Some(SocketAddr::from((Ipv4Addr::LOCALHOST, 8801))),
        log_dir: None,
        config,
        attribute_store_config: None,
        durability: None,
    };
    let without_overrides =
        Config::load_dev(&dev_options(None)).expect("dev defaults without YAML");
    assert_eq!(without_overrides.lease.duration, Duration::from_secs(30));

    let other_lease_key_path = temporary.path().join("fail-if-held.yaml");
    fs::write(&other_lease_key_path, "lease:\n  failIfHeld: true\n").expect("write test config");
    let with_other_lease_key = Config::load_dev(&dev_options(Some(other_lease_key_path)))
        .expect("dev configuration with another lease key");
    assert_eq!(with_other_lease_key.lease.duration, Duration::from_secs(30));
    assert!(with_other_lease_key.lease.fail_if_held);

    let explicit_duration_path = temporary.path().join("lease-duration.yaml");
    fs::write(&explicit_duration_path, "lease:\n  duration: 10s\n").expect("write test config");
    let with_explicit_duration = Config::load_dev(&dev_options(Some(explicit_duration_path)))
        .expect("dev configuration with an explicit lease duration");
    assert_eq!(
        with_explicit_duration.lease.duration,
        Duration::from_secs(10)
    );

    let serve = Config::parse_yaml("cluster:\n  nodeId: node-a\n").expect("serve YAML");
    assert_eq!(serve.lease.duration, Duration::from_secs(10));
}

#[test]
fn dev_defaults_to_a_two_second_attempt_drain_unless_the_overrides_set_it() {
    let temporary = tempfile::tempdir().expect("test directory");
    let dev_options = |config: Option<PathBuf>| DevCommandOptions {
        data_dir: temporary.path().join("data"),
        listen: Some(SocketAddr::from((Ipv4Addr::LOCALHOST, 8801))),
        log_dir: None,
        config,
        attribute_store_config: None,
        durability: None,
    };
    let without_overrides =
        Config::load_dev(&dev_options(None)).expect("dev defaults without YAML");
    assert_eq!(
        without_overrides.lifecycle.attempt_drain_timeout,
        Duration::from_secs(2)
    );

    let explicit_timeout_path = temporary.path().join("attempt-drain.yaml");
    fs::write(
        &explicit_timeout_path,
        "lifecycle:\n  attemptDrainTimeout: 20s\n",
    )
    .expect("write test config");
    let with_explicit_timeout = Config::load_dev(&dev_options(Some(explicit_timeout_path)))
        .expect("dev configuration with an explicit attempt drain timeout");
    assert_eq!(
        with_explicit_timeout.lifecycle.attempt_drain_timeout,
        Duration::from_secs(20)
    );

    let serve = Config::parse_yaml("cluster:\n  nodeId: node-a\n").expect("serve YAML");
    assert_eq!(
        serve.lifecycle.attempt_drain_timeout,
        Duration::from_secs(30)
    );
}

fn single_node_options(data_dir: PathBuf, config: Option<PathBuf>) -> DevCommandOptions {
    DevCommandOptions {
        data_dir,
        listen: Some(SocketAddr::from((Ipv4Addr::LOCALHOST, 8801))),
        log_dir: None,
        config,
        attribute_store_config: None,
        durability: None,
    }
}

#[test]
fn local_differs_from_dev_only_in_the_serve_attempt_drain_default() {
    let temporary = tempfile::tempdir().expect("test directory");
    let options = single_node_options(temporary.path().join("data"), None);
    let local = Config::load_local(&options).expect("local defaults without YAML");
    let dev = Config::load_dev(&options).expect("dev defaults without YAML");
    assert_eq!(
        local.lifecycle.attempt_drain_timeout,
        Duration::from_secs(30)
    );
    assert_eq!(local.lifecycle, LifecycleConfig::default());
    assert_eq!(dev.lifecycle.attempt_drain_timeout, Duration::from_secs(2));
    assert_eq!(local.lease.duration, Duration::from_secs(30));
    assert!(!local.lease.fail_if_held);
    assert_eq!(local.dev.durability_mode, DevDurabilityMode::Strict);
    assert_eq!(local.stream_store.backend, StreamStoreBackend::Memory);
    assert_eq!(local.standby.mode, StandbyMode::Cold);
    assert_eq!(local.cluster.node_id, "dev");
    assert_eq!(Some(local.api.listen), options.listen);
    assert_eq!(local.object_store.bucket, "dex-dev");
    assert_eq!(local.dev.data_directory, options.data_dir);
    assert_eq!(local.dev.s3_data_directory, options.data_dir.join("s3"));
    assert_eq!(
        local.local_storage.directory,
        options.data_dir.join("store")
    );
    assert!(local.auth.authenticators.is_empty());
    assert!(local.metrics.listen.is_empty());
    let dev_with_serve_drain = Config {
        lifecycle: local.lifecycle.clone(),
        ..dev
    };
    assert_eq!(
        local, dev_with_serve_drain,
        "local shares every other dev default, so either command serves the same data directory"
    );
}

#[test]
fn single_node_partition_count_defaults_to_32_unless_the_overrides_set_it() {
    let temporary = tempfile::tempdir().expect("test directory");
    let other_cluster_key_path = temporary.path().join("cluster-node.yaml");
    fs::write(&other_cluster_key_path, "cluster:\n  nodeId: other\n").expect("write test config");
    let explicit_count_path = temporary.path().join("partition-count.yaml");
    fs::write(&explicit_count_path, "cluster:\n  partitionCount: 1024\n")
        .expect("write test config");
    for load in [Config::load_dev, Config::load_local] {
        let without_overrides = load(&single_node_options(temporary.path().join("data"), None))
            .expect("configuration without YAML");
        assert_eq!(without_overrides.cluster.partition_count, 32);
        let with_other_cluster_key = load(&single_node_options(
            temporary.path().join("data"),
            Some(other_cluster_key_path.clone()),
        ))
        .expect("configuration with another cluster key");
        assert_eq!(with_other_cluster_key.cluster.partition_count, 32);
        let with_explicit_count = load(&single_node_options(
            temporary.path().join("data"),
            Some(explicit_count_path.clone()),
        ))
        .expect("configuration with an explicit partition count");
        assert_eq!(with_explicit_count.cluster.partition_count, 1024);
    }

    let serve = Config::parse_yaml("cluster:\n  nodeId: node-a\n").expect("serve YAML");
    assert_eq!(serve.cluster.partition_count, 1024);
}

#[test]
fn single_node_log_deletion_delay_defaults_to_one_hour_unless_the_overrides_set_it() {
    let temporary = tempfile::tempdir().expect("test directory");
    let other_snapshot_key_path = temporary.path().join("snapshot-interval.yaml");
    fs::write(&other_snapshot_key_path, "snapshot:\n  interval: 5m\n").expect("write test config");
    let explicit_delay_path = temporary.path().join("log-deletion-delay.yaml");
    fs::write(&explicit_delay_path, "snapshot:\n  logDeletionDelay: 24h\n")
        .expect("write test config");
    for load in [Config::load_dev, Config::load_local] {
        let without_overrides = load(&single_node_options(temporary.path().join("data"), None))
            .expect("configuration without YAML");
        assert_eq!(
            without_overrides.snapshot.log_deletion_delay,
            Duration::from_secs(60 * 60)
        );
        let with_other_snapshot_key = load(&single_node_options(
            temporary.path().join("data"),
            Some(other_snapshot_key_path.clone()),
        ))
        .expect("configuration with another snapshot key");
        assert_eq!(
            with_other_snapshot_key.snapshot.log_deletion_delay,
            Duration::from_secs(60 * 60)
        );
        assert_eq!(
            with_other_snapshot_key.snapshot.interval,
            Duration::from_secs(5 * 60)
        );
        let with_explicit_delay = load(&single_node_options(
            temporary.path().join("data"),
            Some(explicit_delay_path.clone()),
        ))
        .expect("configuration with an explicit log deletion delay");
        assert_eq!(
            with_explicit_delay.snapshot.log_deletion_delay,
            Duration::from_secs(24 * 60 * 60)
        );
    }

    let serve = Config::parse_yaml("cluster:\n  nodeId: node-a\n").expect("serve YAML");
    assert_eq!(
        serve.snapshot.log_deletion_delay,
        Duration::from_secs(24 * 60 * 60)
    );
}

#[test]
fn single_node_applier_lanes_above_the_default_partition_count_need_an_explicit_count() {
    let temporary = tempfile::tempdir().expect("test directory");
    let lanes_path = temporary.path().join("lanes.yaml");
    fs::write(&lanes_path, "commit:\n  applierLanes: 64\n").expect("write test config");
    let lanes_and_count_path = temporary.path().join("lanes-and-count.yaml");
    fs::write(
        &lanes_and_count_path,
        "cluster:\n  partitionCount: 1024\ncommit:\n  applierLanes: 64\n",
    )
    .expect("write test config");
    let at_default_path = temporary.path().join("lanes-at-default.yaml");
    fs::write(&at_default_path, "commit:\n  applierLanes: 32\n").expect("write test config");
    for (load, command, override_file) in [
        (
            Config::load_dev as fn(&DevCommandOptions) -> Result<Config, ConfigError>,
            "dev",
            "the --config file (with dexcli dev, the --zd-config file)",
        ),
        (Config::load_local, "local", "the --config file"),
    ] {
        let error = load(&single_node_options(
            temporary.path().join("data"),
            Some(lanes_path.clone()),
        ))
        .expect_err("64 lanes exceed the 32 default partitions");
        assert!(
            matches!(
                error,
                ConfigError::SingleNodeApplierLanesExceedDefaultPartitionCount {
                    command: actual_command,
                    override_file: actual_override_file,
                    applier_lanes: 64,
                    default_partition_count: 32,
                } if actual_command == command && actual_override_file == override_file
            ),
            "{error:?}"
        );
        let message = error.to_string();
        assert!(
            message.contains("since server-zd v0.1.20")
                && message.contains("set cluster.partitionCount")
                && message.contains("1024 for a data directory")
                && message.contains("lower commit.applierLanes to at most 32"),
            "{message}"
        );
        let with_count = load(&single_node_options(
            temporary.path().join("data"),
            Some(lanes_and_count_path.clone()),
        ))
        .expect("an explicit partition count admits 64 lanes");
        assert_eq!(with_count.commit.applier_lanes, Some(64));
        let at_default = load(&single_node_options(
            temporary.path().join("data"),
            Some(at_default_path.clone()),
        ))
        .expect("32 lanes fit the default partition count");
        assert_eq!(at_default.commit.applier_lanes, Some(32));
    }
}

#[test]
fn single_node_listen_prefers_the_flag_then_api_listen_then_loopback() {
    let temporary = tempfile::tempdir().expect("test directory");
    let yaml_path = temporary.path().join("listen.yaml");
    fs::write(&yaml_path, "api:\n  listen: '0.0.0.0:9000'\n").expect("write test config");
    let without_api_path = temporary.path().join("without-api.yaml");
    fs::write(&without_api_path, "cluster:\n  nodeId: x\n").expect("write test config");
    let flag_listen = SocketAddr::from((Ipv4Addr::LOCALHOST, 9901));
    for load in [Config::load_dev, Config::load_local] {
        let without_either = load(&DevCommandOptions {
            listen: None,
            ..single_node_options(temporary.path().join("data"), None)
        })
        .expect("configuration without a listen address");
        assert_eq!(
            without_either.api.listen,
            SocketAddr::from((Ipv4Addr::LOCALHOST, 8801))
        );
        let yaml_without_api = load(&DevCommandOptions {
            listen: None,
            ..single_node_options(
                temporary.path().join("data"),
                Some(without_api_path.clone()),
            )
        })
        .expect("configuration with YAML but no api section");
        assert_eq!(
            yaml_without_api.api.listen,
            SocketAddr::from((Ipv4Addr::LOCALHOST, 8801))
        );
        let from_yaml = load(&DevCommandOptions {
            listen: None,
            ..single_node_options(temporary.path().join("data"), Some(yaml_path.clone()))
        })
        .expect("configuration with api.listen");
        assert_eq!(
            from_yaml.api.listen,
            SocketAddr::from((Ipv4Addr::UNSPECIFIED, 9000))
        );
        let from_flag = load(&DevCommandOptions {
            listen: Some(flag_listen),
            ..single_node_options(temporary.path().join("data"), Some(yaml_path.clone()))
        })
        .expect("configuration with --listen and api.listen");
        assert_eq!(from_flag.api.listen, flag_listen);
    }
}

#[test]
fn local_applies_the_overrides_and_flags_that_dev_applies() {
    let temporary = tempfile::tempdir().expect("test directory");
    let yaml_path = temporary.path().join("local.yaml");
    fs::write(
        &yaml_path,
        "lifecycle:\n  attemptDrainTimeout: 5s\nlease:\n  duration: 12s\ndev:\n  durabilityMode: processCrashSafe\nstreamStore:\n  backend: disabled\nobjectStore:\n  bucket: dex-local\n",
    )
    .expect("write test config");
    let options = DevCommandOptions {
        log_dir: Some(temporary.path().join("logs")),
        ..single_node_options(temporary.path().join("data"), Some(yaml_path))
    };
    let local = Config::load_local(&options).expect("local configuration with overrides");
    let dev = Config::load_dev(&options).expect("dev configuration with overrides");
    assert_eq!(local, dev, "explicit overrides leave no profile default");
    assert_eq!(
        local.lifecycle.attempt_drain_timeout,
        Duration::from_secs(5)
    );
    assert_eq!(local.lease.duration, Duration::from_secs(12));
    assert_eq!(
        local.dev.durability_mode,
        DevDurabilityMode::ProcessCrashSafe
    );
    assert_eq!(local.stream_store.backend, StreamStoreBackend::Disabled);
    assert_eq!(local.object_store.bucket, "dex-local");
    assert_eq!(local.dev.log_directory, options.log_dir);
    let flag_override = Config::load_local(&DevCommandOptions {
        durability: Some(DevDurabilityMode::Relaxed),
        ..single_node_options(options.data_dir.clone(), options.config.clone())
    })
    .expect("local configuration with a durability flag");
    assert_eq!(
        flag_override.dev.durability_mode,
        DevDurabilityMode::Relaxed
    );
}

#[test]
fn local_rejects_hot_standby_and_a_separate_hard_delete_store_by_its_name() {
    let temporary = tempfile::tempdir().expect("test directory");
    for (file_name, contents, rejected_field) in [
        ("hot.yaml", "standby:\n  mode: hot\n", "standby.mode"),
        (
            "hard-delete.yaml",
            "objectStore:\n  hardDeleteStore:\n    bucket: external\n",
            "objectStore.hardDeleteStore",
        ),
    ] {
        let yaml_path = temporary.path().join(file_name);
        fs::write(&yaml_path, contents).expect("write test config");
        let error = Config::load_local(&single_node_options(
            temporary.path().join("data"),
            Some(yaml_path),
        ))
        .expect_err("local runs one process on its embedded object store");
        let ConfigError::Invalid { field, message } = &error else {
            panic!("expected an invalid field, got {error}");
        };
        assert_eq!(*field, rejected_field);
        assert!(message.starts_with("local "), "{message}");
    }
}

#[test]
fn dev_rejects_a_separate_hard_delete_store_before_resolving_credentials() {
    let temporary = tempfile::tempdir().expect("test directory");
    let yaml_path = temporary.path().join("config.yaml");
    fs::write(
        &yaml_path,
        "objectStore:\n  hardDeleteStore:\n    bucket: external\n    credentialsSource: environment\n",
    )
    .expect("write test config");
    let error = Config::load_dev(&DevCommandOptions {
        data_dir: temporary.path().join("data"),
        listen: Some(SocketAddr::from((Ipv4Addr::LOCALHOST, 8801))),
        log_dir: None,
        config: Some(yaml_path),
        attribute_store_config: None,
        durability: None,
    })
    .expect_err("dev must reject a separate hard-delete destination");
    assert!(matches!(
        error,
        ConfigError::Invalid {
            field: "objectStore.hardDeleteStore",
            ..
        }
    ));
}

#[test]
fn hot_standby_mode_reaches_the_engine_and_cold_keeps_the_earlier_lease_poll() {
    let cold = ComponentConfigs::from_config(&valid_config(), false).expect("cold components");
    assert!(cold.hot_standby.is_none());
    assert_eq!(
        cold.engine_node.lease.held_lease_poll_limit,
        Duration::from_secs(1)
    );
    let mut configuration = Config::parse_yaml(
        "standby:\n  mode: hot\n  heldLeasePollInterval: 200ms\n  behindLagThreshold: 4s\n",
    )
    .expect("the standby section parses");
    configuration.cluster.node_id = "node-a".to_owned();
    configuration.object_store.bucket = "dex-test".to_owned();
    configuration.local_storage.directory = PathBuf::from("/tmp/dexzd-test-store");
    configuration.validate().expect("hot standby is valid");
    let hot = ComponentConfigs::from_config(&configuration, false).expect("hot components");
    let hot_standby = hot
        .hot_standby
        .expect("hot mode configures the hot standby");
    assert_eq!(hot_standby.behind_lag_threshold, Duration::from_secs(4));
    assert_eq!(
        hot.engine_node.lease.held_lease_poll_limit,
        Duration::from_millis(200)
    );
}

#[test]
fn dev_rejects_hot_standby_mode() {
    let temporary = tempfile::tempdir().expect("test directory");
    let yaml_path = temporary.path().join("config.yaml");
    fs::write(&yaml_path, "standby:\n  mode: hot\n").expect("write test config");
    let error = Config::load_dev(&DevCommandOptions {
        data_dir: temporary.path().join("data"),
        listen: Some(SocketAddr::from((Ipv4Addr::LOCALHOST, 8801))),
        log_dir: None,
        config: Some(yaml_path),
        attribute_store_config: None,
        durability: None,
    })
    .expect_err("dev must reject hot standby mode");
    assert!(matches!(
        error,
        ConfigError::Invalid {
            field: "standby.mode",
            ..
        }
    ));
}

#[test]
fn hard_delete_location_keys_round_trip_and_lease_is_independent_of_batch_window() {
    let mut configuration = valid_config();
    configuration.object_store.hard_delete_store = Some(ObjectStoreLocationConfig {
        bucket: "hard-delete".to_owned(),
        ..ObjectStoreLocationConfig::default()
    });
    configuration.commit.batch_window = Duration::from_secs(8);
    configuration.lease.duration = Duration::from_secs(1);
    configuration
        .validate()
        .expect("lease renewal is independent of commit batching");
    let yaml = serde_yaml_ng::to_string(&configuration).expect("serialize config");
    let parsed = Config::parse_yaml(&yaml).expect("parse all hard-delete location keys");
    assert_eq!(parsed, configuration);
    let value: serde_yaml_ng::Value = serde_yaml_ng::from_str(&yaml).expect("YAML value");
    let hard_delete = value
        .get("objectStore")
        .and_then(|store| store.get("hardDeleteStore"))
        .expect("hard-delete destination");
    let expected = serde_yaml_ng::to_value(ObjectStoreLocationConfig::default())
        .expect("default hard-delete location keys");
    assert_same_yaml_keys(hard_delete, &expected);
}

#[test]
fn load_file_reports_io_and_field_failures() {
    let temporary = tempfile::tempdir().expect("test directory");
    let missing = temporary.path().join("missing.yaml");
    assert!(matches!(
        Config::load_file(&missing),
        Err(ConfigError::Read { .. })
    ));
    let invalid = temporary.path().join("invalid.yaml");
    fs::write(&invalid, "cluster:\n  nodeId: node-a\n").expect("write test config");
    let error = Config::load_file(&invalid).expect_err("unset bucket must fail");
    assert!(matches!(
        error,
        ConfigError::Invalid {
            field: "objectStore.bucket",
            ..
        }
    ));
    let complete = temporary.path().join("complete.yaml");
    fs::write(&complete, "cluster:\n  nodeId: node-a\nobjectStore:\n  bucket: dex-test\nlocalStorage:\n  directory: /tmp/dexzd\n").expect("write complete config");
    Config::load_file(&complete).expect("load complete production config");
    let with_dev = temporary.path().join("with-dev.yaml");
    fs::write(&with_dev, "cluster:\n  nodeId: node-a\nobjectStore:\n  bucket: dex-test\nlocalStorage:\n  directory: /tmp/dexzd\ndev:\n  logDirectory: /tmp/dexzd-logs\n").expect("write config with a dev section");
    assert!(matches!(
        Config::load_file(&with_dev),
        Err(ConfigError::Invalid { field: "dev", .. })
    ));
    for durability_mode in ["strict", "processCrashSafe", "relaxed"] {
        let with_durability_mode = temporary
            .path()
            .join(format!("with-{durability_mode}.yaml"));
        fs::write(&with_durability_mode, format!("cluster:\n  nodeId: node-a\nobjectStore:\n  bucket: dex-test\nlocalStorage:\n  directory: /tmp/dexzd\ndev:\n  durabilityMode: {durability_mode}\n")).expect("write config with a dev durability mode");
        Config::load_file(&with_durability_mode)
            .expect("a serve configuration copied from either example keeps loading");
    }
}

#[test]
fn every_field_constraint_names_the_rejected_yaml_path() {
    let cases: Vec<InvalidConfigCase> = vec![
        ("cluster.nodeId", |config| config.cluster.node_id.clear()),
        ("cluster.nodeId", |config| {
            config.cluster.node_id = "Node_A".to_owned()
        }),
        ("cluster.nodeId", |config| {
            config.cluster.node_id = "a".repeat(64)
        }),
        ("cluster.partitionCount", |config| {
            config.cluster.partition_count = 3
        }),
        ("cluster.partitionCount", |config| {
            config.cluster.partition_count = 131_072
        }),
        ("objectStore.region", |config| {
            config.object_store.region.clear()
        }),
        ("objectStore.bucket", |config| {
            config.object_store.bucket.clear()
        }),
        ("objectStore.endpoint", |config| {
            config.object_store.endpoint = "ftp://server".to_owned()
        }),
        ("objectStore.prefix", |config| {
            config.object_store.prefix = "/unsafe".to_owned()
        }),
        ("objectStore.credentialsSource", |config| {
            config.object_store.credentials_source = CredentialSource::Environment
        }),
        ("objectStore.hardDeleteStore.bucket", |config| {
            config.object_store.hard_delete_store = Some(ObjectStoreLocationConfig::default())
        }),
        ("objectStore.hardDeleteStore.region", |config| {
            config.object_store.hard_delete_store = Some(ObjectStoreLocationConfig {
                bucket: "hard-delete".to_owned(),
                region: String::new(),
                ..ObjectStoreLocationConfig::default()
            })
        }),
        ("objectStore.hardDeleteStore.endpoint", |config| {
            config.object_store.hard_delete_store = Some(ObjectStoreLocationConfig {
                bucket: "hard-delete".to_owned(),
                endpoint: "ftp://invalid".to_owned(),
                ..ObjectStoreLocationConfig::default()
            })
        }),
        ("objectStore.hardDeleteStore.prefix", |config| {
            config.object_store.hard_delete_store = Some(ObjectStoreLocationConfig {
                bucket: "hard-delete".to_owned(),
                prefix: "/invalid".to_owned(),
                ..ObjectStoreLocationConfig::default()
            })
        }),
        ("objectStore.hardDeleteStore.credentialsSource", |config| {
            config.object_store.hard_delete_store = Some(ObjectStoreLocationConfig {
                bucket: "hard-delete".to_owned(),
                credentials_source: CredentialSource::Environment,
                ..ObjectStoreLocationConfig::default()
            })
        }),
        ("localStorage.directory", |config| {
            config.local_storage.directory = PathBuf::new()
        }),
        ("localStorage.blockCacheBytes", |config| {
            config.local_storage.block_cache_bytes = 0
        }),
        ("localStorage.maxWalBytes", |config| {
            config.local_storage.max_wal_bytes = 64 * 1024 * 1024 - 1
        }),
        ("localStorage.writeBufferBytes", |config| {
            config.local_storage.write_buffer_bytes = 0
        }),
        ("localStorage.writeBufferBytes", |config| {
            config.local_storage.write_buffer_bytes = config.local_storage.max_wal_bytes + 1
        }),
        ("localStorage.maxBackgroundJobs", |config| {
            config.local_storage.max_background_jobs = Some(0)
        }),
        ("localStorage.maxSubcompactions", |config| {
            config.local_storage.max_subcompactions = 0
        }),
        ("commit.batchWindow", |config| {
            config.commit.batch_window = Duration::ZERO
        }),
        ("commit.maxInFlight", |config| {
            config.commit.max_in_flight = 0
        }),
        ("commit.sealAtBytes", |config| {
            config.commit.seal_at_bytes = 0
        }),
        ("commit.multipartThresholdBytes", |config| {
            config.commit.multipart_threshold_bytes = 4 * 1024 * 1024
        }),
        ("commit.maxBatchRecords", |config| {
            config.commit.max_batch_records = 0
        }),
        ("commit.maxUnappliedBytes", |config| {
            config.commit.max_unapplied_bytes = 0
        }),
        ("commit.applierLanes", |config| {
            config.commit.applier_lanes = Some(config.cluster.partition_count + 1)
        }),
        ("commit.applierQueueFrames", |config| {
            config.commit.applier_queue_frames = 0
        }),
        ("lease.duration", |config| {
            config.lease.duration = Duration::ZERO
        }),
        ("snapshot.logThresholdBytes", |config| {
            config.snapshot.log_threshold_bytes = 0
        }),
        ("snapshot.interval", |config| {
            config.snapshot.interval = Duration::ZERO
        }),
        ("snapshot.retainedCount", |config| {
            config.snapshot.retained_count = 0
        }),
        ("snapshot.logDeletionDelay", |config| {
            config.snapshot.log_deletion_delay = Duration::ZERO
        }),
        ("recovery.readAheadBatches", |config| {
            config.recovery.read_ahead_batches = 0
        }),
        ("recovery.readAheadBatches", |config| {
            config.recovery.read_ahead_batches = 1_025
        }),
        ("recovery.readAheadBytes", |config| {
            config.recovery.read_ahead_bytes = 0
        }),
        ("recovery.startupReadRetryBudget", |config| {
            config.recovery.startup_read_retry_budget = Duration::ZERO
        }),
        ("history.windowEvents", |config| {
            config.history.window_events = 0
        }),
        ("history.pruneChunkSize", |config| {
            config.history.prune_chunk_size = config.history.window_events + 1
        }),
        ("history.maxWindowEvents", |config| {
            config.history.max_window_events = 0
        }),
        ("history.checkpointRowWriteBytesThreshold", |config| {
            config.history.checkpoint_row_write_bytes_threshold = 0
        }),
        ("history.checkpointCommandThreshold", |config| {
            config.history.checkpoint_command_threshold = 0
        }),
        ("history.segmentTargetBytes", |config| {
            config.history.segment_target_bytes = 0
        }),
        ("history.segmentTargetBytes", |config| {
            config.history.segment_target_bytes = 256 * 1024 * 1024 + 1
        }),
        ("history.uploadConcurrency", |config| {
            config.history.upload_concurrency = 0
        }),
        ("history.uploadConcurrency", |config| {
            config.history.upload_concurrency = 1025
        }),
        ("history.uploadMinConcurrency", |config| {
            config.history.upload_min_concurrency = 0
        }),
        ("history.uploadMinConcurrency", |config| {
            config.history.upload_min_concurrency = 1025
        }),
        ("history.uploadMaxInFlightBytes", |config| {
            config.history.upload_max_in_flight_bytes = 1024 * 1024 - 1
        }),
        ("history.uploadMaxInFlightBytes", |config| {
            config.history.upload_max_in_flight_bytes = 64 * 1024 * 1024 * 1024 + 1
        }),
        ("history.objectReadConcurrency", |config| {
            config.history.object_read_concurrency = 0
        }),
        ("history.objectReadConcurrency", |config| {
            config.history.object_read_concurrency = 1025
        }),
        ("history.readCacheDirectory", |config| {
            config.history.read_cache_directory =
                Some(config.local_storage.directory.join("history-read-cache"));
        }),
        ("history.readCacheDirectory", |config| {
            config.history.read_cache_directory = Some(config.local_storage.directory.clone());
        }),
        ("values.maxValueBytes", |config| {
            config.values.max_value_bytes = 0
        }),
        ("values.wireInlineThresholdBytes", |config| {
            config.values.wire_inline_threshold_bytes = config.values.max_value_bytes
        }),
        ("values.blobDbMinValueBytes", |config| {
            config.values.blob_db_min_value_bytes = 10
        }),
        ("retention.closedFlowDuration", |config| {
            config.retention.closed_flow_duration = Duration::ZERO
        }),
        ("api.listen", |config| {
            config.api.listen = SocketAddr::from((Ipv4Addr::LOCALHOST, 0))
        }),
        ("api.grpcMaxMessageBytes", |config| {
            config.api.grpc_max_message_bytes = config.values.max_value_bytes
        }),
        ("api.maxWaitSeconds", |config| {
            config.api.max_wait_seconds = 0
        }),
        ("api.defaultRpcTimeout", |config| {
            config.api.default_rpc_timeout = Duration::ZERO
        }),
        ("matching.dispatchWait", |config| {
            config.matching.dispatch_wait = Duration::ZERO
        }),
        ("matching.stickyWait", |config| {
            config.matching.sticky_wait = Duration::from_secs(11)
        }),
        ("matching.syncMatchWait", |config| {
            config.matching.sync_match_wait = Duration::ZERO
        }),
        ("matching.maxConcurrentTasksCap", |config| {
            config.matching.max_concurrent_tasks_cap = 0
        }),
        ("matching.maxStreams", |config| {
            config.matching.max_streams = 1
        }),
        ("matching.maxQueueLength", |config| {
            config.matching.max_queue_length = 0
        }),
        ("matching.maxFlowTypes", |config| {
            config.matching.max_flow_types = 0
        }),
        ("matching.maxFlowTypeLength", |config| {
            config.matching.max_flow_type_length = 0
        }),
        ("matching.pingInterval", |config| {
            config.matching.ping_interval = Duration::ZERO
        }),
        ("matching.unsupportedTypeCacheEntries", |config| {
            config.matching.unsupported_type_cache_entries = 0
        }),
        ("streamStore.maxMessageBytes", |config| {
            config.stream_store.max_message_bytes = 0
        }),
        ("streamStore.maxReadMessages", |config| {
            config.stream_store.max_read_messages = 1_001
        }),
        ("streamStore.trimTriggerPercent", |config| {
            config.stream_store.trim_trigger_percent = 0
        }),
        ("streamStore.trimTargetPercent", |config| {
            config.stream_store.trim_target_percent = 90
        }),
        ("streamStore.maxTotalBytes", |config| {
            config.stream_store.max_total_bytes = 0
        }),
        ("streamStore.maxStreamCapacityBytes", |config| {
            config.stream_store.max_stream_capacity_bytes = 2 << 30
        }),
        ("streamStore.backgroundTrimBatchSize", |config| {
            config.stream_store.background_trim_batch_size = 0
        }),
        ("streamStore.trimBatchYieldTime", |config| {
            config.stream_store.trim_batch_yield_time = Duration::ZERO
        }),
        ("streamStore.trimWorkers", |config| {
            config.stream_store.trim_workers = 0
        }),
        ("search.builtinScanBudgetDocuments", |config| {
            config.search.builtin_scan_budget_documents = 0
        }),
        ("search.paradeDbConnectionString", |config| {
            config.search.backend = SearchBackend::Paradedb
        }),
        ("search.paradeDbConnectionPoolSize", |config| {
            config.search.parade_db_connection_pool_size = 0
        }),
        ("search.paradeDbSchema", |config| {
            config.search.parade_db_schema.clear()
        }),
        ("search.paradeDbSchema", |config| {
            config.search.parade_db_schema = "dex,public".to_owned()
        }),
        ("search.readYourWritesWait", |config| {
            config.search.read_your_writes_wait = Duration::ZERO
        }),
        ("search.outboxBatchSize", |config| {
            config.search.outbox_batch_size = 0
        }),
        ("search.outboxPollInterval", |config| {
            config.search.outbox_poll_interval = Duration::ZERO
        }),
        ("search.outboxAckInterval", |config| {
            config.search.outbox_ack_interval = Duration::ZERO
        }),
        ("search.outboxMaxAckEntries", |config| {
            config.search.outbox_max_ack_entries = config.search.outbox_batch_size - 1
        }),
        ("limits.maxFlowStateBytes", |config| {
            config.limits.max_flow_state_bytes = 1
        }),
        ("limits.maxChannelMessages", |config| {
            config.limits.max_channel_messages = 0
        }),
        ("limits.maxConcurrentStepsPerFlow", |config| {
            config.limits.max_concurrent_steps_per_flow = 0
        }),
        ("limits.maxSpeculativeCommandsPerFlow", |config| {
            config.limits.max_speculative_commands_per_flow = 0
        }),
        ("limits.maxSpeculativeBytesPerNode", |config| {
            config.limits.max_speculative_bytes_per_node = 1
        }),
        ("limits.maxLongPollWaitersPerNode", |config| {
            config.limits.max_long_poll_waiters_per_node = 0
        }),
        ("limits.maxLongPollWaitersPerFlow", |config| {
            config.limits.max_long_poll_waiters_per_flow = 100_001
        }),
        ("limits.maxInFlightRpcsPerNode", |config| {
            config.limits.max_in_flight_rpcs_per_node = 0
        }),
        ("limits.maxRequestIdsPerRun", |config| {
            config.limits.max_request_ids_per_run = 0
        }),
        ("limits.maxChildFlowResultBytes", |config| {
            config.limits.max_child_flow_result_bytes = 0
        }),
        ("limits.maxFlowIdBytes", |config| {
            config.limits.max_flow_id_bytes = 0
        }),
        ("dev.s3DataDirectory", |config| {
            config.dev.data_directory = PathBuf::from("/tmp/dev")
        }),
        ("standby.heldLeasePollInterval", |config| {
            config.standby.held_lease_poll_interval = Duration::ZERO
        }),
        ("standby.behindLagThreshold", |config| {
            config.standby.behind_lag_threshold = Duration::ZERO
        }),
        ("standby.stepDownHoldOff", |config| {
            config.standby.step_down_hold_off = Duration::ZERO
        }),
        ("standby.stepDownHoldOff", |config| {
            config.standby.mode = StandbyMode::Hot;
            config.lease.fail_if_held = false;
            config.standby.step_down_hold_off = Duration::from_millis(1750);
        }),
        ("standby.mode", |config| {
            config.standby.mode = StandbyMode::Hot;
            config.lease.fail_if_held = true;
        }),
    ];
    for (expected_field, mutate) in cases {
        let mut configuration = valid_config();
        mutate(&mut configuration);
        let error = configuration.validate().expect_err(expected_field);
        match error {
            ConfigError::Invalid { field, .. } => assert_eq!(field, expected_field),
            other => panic!("expected field validation, got {other:?}"),
        }
    }
    valid_config().validate().expect("valid configuration");
    let mut short_cold_hold_off = valid_config();
    short_cold_hold_off.standby.step_down_hold_off = Duration::from_millis(1750);
    short_cold_hold_off
        .validate()
        .expect("a cold standby has no lagging delay for the hold-off to exceed");
    let mut separate_hard_delete = valid_config();
    separate_hard_delete.object_store.hard_delete_store = Some(ObjectStoreLocationConfig {
        bucket: "hard-delete".to_owned(),
        prefix: "private/".to_owned(),
        ..ObjectStoreLocationConfig::default()
    });
    separate_hard_delete
        .validate()
        .expect("valid separate hard-delete destination");
}

/// A Go Dex Server configuration file with sections dex-server-zd ignores.
const GO_ATTRIBUTE_STORE_YAML: &str = "log:\n  level: info\ninterpreter:\n  temporal:\n    hostPort: localhost:7233\nattributeStore:\n  stores:\n    orders:\n      type: postgres\n      dsn: postgres://dex:secret@db.example.com/app?sslmode=verify-full\n      tableName: public.orders\n      rowKey: order-{order_id}\n      columnNaming: snake_case\n      attributeMaps:\n        Items:\n          tableName: public.order_items\n          instanceKeyColumn: item_key\n  schemaSyncInterval: 0s\n  syncBatchSize: 0\n  syncRetryPolicy:\n    initialInterval: 0s\n    backoffCoefficient: 0\n    totalDuration: 168h\n";

fn attribute_store_config_with(yaml_store_fields: &str) -> Config {
    let mut configuration = valid_config();
    configuration.attribute_store = Config::parse_yaml(&format!(
        "attributeStore:\n  stores:\n    orders:\n{yaml_store_fields}"
    ))
    .expect("store YAML parses")
    .attribute_store;
    configuration
}

#[test]
fn attribute_store_section_uses_go_keys_and_zero_defaults() {
    let defaults = Config::default().attribute_store;
    assert!(defaults.stores.is_empty());
    assert_eq!(defaults.schema_sync_interval, Duration::from_secs(60));
    assert_eq!(defaults.sync_batch_size, 100);
    assert_eq!(defaults.sync_attempt_timeout, Duration::from_secs(30));
    assert_eq!(
        defaults.sync_retry_policy.initial_interval,
        Duration::from_millis(100)
    );
    assert_eq!(
        defaults.sync_retry_policy.maximum_interval,
        Duration::from_secs(30)
    );
    assert!((defaults.sync_retry_policy.backoff_coefficient - 2.0).abs() < f64::EPSILON);
    assert_eq!(defaults.sync_retry_policy.maximum_attempts, 0);
    assert_eq!(
        defaults.sync_retry_policy.total_duration,
        Duration::from_secs(3600)
    );

    let document: serde_yaml_ng::Value =
        serde_yaml_ng::from_str(GO_ATTRIBUTE_STORE_YAML).expect("Go YAML");
    let mut configuration = valid_config();
    configuration.attribute_store = serde_yaml_ng::from_value(
        document
            .get("attributeStore")
            .cloned()
            .expect("attributeStore section"),
    )
    .expect("the Go attributeStore section parses");
    configuration.validate().expect("the Go section is valid");
    let attribute_store = &configuration.attribute_store;
    let store = &attribute_store.stores["orders"];
    assert_eq!(store.store_type, AttributeStoreType::Postgres);
    assert_eq!(store.column_naming, AttributeStoreColumnNaming::SnakeCase);
    assert_eq!(store.row_key, "order-{order_id}");
    assert_eq!(
        store.attribute_maps["Items"].instance_key_column,
        "item_key"
    );
    assert_eq!(store.fencing_table_name, "dexzd_attribute_sync_fencing");
    assert_eq!(
        attribute_store.effective_schema_sync_interval(),
        Duration::from_secs(60)
    );
    assert_eq!(attribute_store.effective_sync_batch_size(), 100);
    let policy = attribute_store.effective_sync_retry_policy();
    assert_eq!(policy.initial_interval, Duration::from_millis(100));
    assert!((policy.backoff_coefficient - 2.0).abs() < f64::EPSILON);
    assert_eq!(policy.total_duration, Duration::from_secs(168 * 3600));

    let debug = format!("{configuration:?}");
    assert!(!debug.contains("secret"), "{debug}");
    let serialized = serde_yaml_ng::to_string(&configuration).expect("serialize");
    assert!(!serialized.contains("secret"));

    let null_policy = Config::parse_yaml("attributeStore:\n  syncRetryPolicy: null\n")
        .expect("a null retry policy means every default");
    assert_eq!(
        null_policy.attribute_store.sync_retry_policy,
        defaults.sync_retry_policy
    );
    let exact_names = attribute_store_config_with(
        "      type: postgres\n      dsn: postgres://localhost/app\n      tableName: orders\n      columnNaming: \"\"\n",
    );
    assert_eq!(
        exact_names.attribute_store.stores["orders"].column_naming,
        AttributeStoreColumnNaming::Exact
    );
    exact_names
        .validate()
        .expect("exact column names are valid");
}

#[test]
fn attribute_store_validation_accepts_only_postgres_with_go_rules() {
    let cases: [(&str, &str, &str); 9] = [
        (
            "      type: mysql\n      dsn: root@tcp(localhost)/app\n      tableName: orders\n",
            "type",
            "supports only postgres",
        ),
        (
            "      type: mongodb\n      dsn: mongodb://localhost\n      databaseName: app\n      collectionName: orders\n",
            "type",
            "supports only postgres",
        ),
        (
            "      dsn: postgres://localhost/app\n      tableName: orders\n",
            "type",
            "required",
        ),
        (
            "      type: postgres\n      tableName: orders\n",
            "dsn",
            "DEX_ZD_ATTRIBUTE_STORE_DSN_ORDERS",
        ),
        (
            "      type: postgres\n      dsn: postgres://localhost/app\n",
            "tableName",
            "required",
        ),
        (
            "      type: postgres\n      dsn: postgres://localhost/app\n      tableName: db.public.orders\n",
            "tableName",
            "at most 2",
        ),
        (
            "      type: postgres\n      dsn: postgres://localhost/app\n      tableName: orders\n      flowIdColumn: flow_id\n",
            "flowIdColumn",
            "warehouse",
        ),
        (
            "      type: postgres\n      dsn: postgres://localhost/app\n      tableName: orders\n      attributeMaps:\n        Items/All:\n          tableName: items\n          instanceKeyColumn: item_key\n",
            "attributeMaps",
            "no '/'",
        ),
        (
            "      type: postgres\n      dsn: postgres://localhost/app\n      tableName: orders\n      fencingTableName: \"\"\n",
            "fencingTableName",
            "invalid identifier",
        ),
    ];
    for (store_fields, expected_field, expected_message) in cases {
        let error = attribute_store_config_with(store_fields)
            .validate()
            .expect_err(store_fields);
        match &error {
            ConfigError::InvalidAttributeStore {
                store_name,
                field,
                message,
            } => {
                assert_eq!(store_name, "orders");
                assert_eq!(*field, expected_field, "{store_fields}");
                assert!(message.contains(expected_message), "{message}");
            }
            other => panic!("expected an Attribute Store field error, got {other:?}"),
        }
        assert!(
            error
                .to_string()
                .starts_with(&format!("attributeStore.stores.orders.{expected_field}: "))
        );
    }
    let mut configuration = valid_config();
    configuration
        .attribute_store
        .sync_retry_policy
        .backoff_coefficient = -1.0;
    assert!(matches!(
        configuration.validate(),
        Err(ConfigError::Invalid {
            field: "attributeStore.syncRetryPolicy.backoffCoefficient",
            ..
        })
    ));
    let mut configuration = valid_config();
    configuration
        .attribute_store
        .sync_retry_policy
        .maximum_interval = Duration::from_millis(50);
    assert!(matches!(
        configuration.validate(),
        Err(ConfigError::Invalid {
            field: "attributeStore.syncRetryPolicy.maximumInterval",
            ..
        })
    ));
    assert!(matches!(
        Config::parse_yaml("attributeStore:\n  stores:\n    orders:\n      type: oracle\n"),
        Err(ConfigError::Yaml { .. })
    ));
    assert_eq!(
        dsn_environment_variable("order-store.v2"),
        "DEX_ZD_ATTRIBUTE_STORE_DSN_ORDER_STORE_V2"
    );
}

#[test]
fn configured_attribute_stores_reach_request_validation() {
    let configuration = attribute_store_config_with(
        "      type: postgres\n      dsn: postgres://localhost/app\n      tableName: orders\n",
    );
    configuration.validate().expect("valid store");
    let components =
        ComponentConfigs::from_config(&configuration, false).expect("component config");
    assert!(
        components
            .validation()
            .attribute_store_names
            .contains("orders")
    );
    assert_eq!(components.validation().attribute_store_names.len(), 1);
    let without_stores =
        ComponentConfigs::from_config(&valid_config(), false).expect("component config");
    assert!(without_stores.validation().attribute_store_names.is_empty());
}

#[test]
fn dev_reads_the_attribute_store_section_of_a_go_configuration_file() {
    let temporary = tempfile::tempdir().expect("test directory");
    let go_config = temporary.path().join("dex.yaml");
    fs::write(&go_config, GO_ATTRIBUTE_STORE_YAML).expect("write Go config");
    let options = DevCommandOptions {
        data_dir: temporary.path().join("data"),
        listen: Some(SocketAddr::from((Ipv4Addr::LOCALHOST, 8801))),
        log_dir: None,
        config: None,
        attribute_store_config: Some(go_config.clone()),
        durability: None,
    };
    let configuration = Config::load_dev(&options).expect("dev configuration");
    assert_eq!(
        configuration.attribute_store.stores["orders"].table_name,
        "public.orders"
    );

    let zd_config = temporary.path().join("zd.yaml");
    fs::write(&zd_config, "attributeStore:\n  syncBatchSize: 10\n").expect("write zd config");
    let conflict = Config::load_dev(&DevCommandOptions {
        config: Some(zd_config),
        attribute_store_config: Some(go_config),
        durability: None,
        data_dir: temporary.path().join("data"),
        listen: options.listen,
        log_dir: None,
    })
    .expect_err("the section may come from only one file");
    assert!(matches!(
        conflict,
        ConfigError::Invalid {
            field: "attributeStore",
            ..
        }
    ));

    let without_stores = temporary.path().join("empty.yaml");
    fs::write(&without_stores, "log:\n  level: info\n").expect("write config");
    let empty = Config::load_dev(&DevCommandOptions {
        attribute_store_config: Some(without_stores),
        durability: None,
        data_dir: temporary.path().join("data"),
        listen: options.listen,
        log_dir: None,
        config: None,
    })
    .expect_err("a file without stores is rejected");
    assert!(matches!(
        empty,
        ConfigError::Invalid {
            field: "attributeStore.stores",
            ..
        }
    ));
}

#[test]
fn postgres_configuration_parses_validates_and_redacts_credentials() {
    let mut config = valid_config();
    config.search = super::SearchConfig {
        backend: SearchBackend::Postgres,
        postgres_connection_string: "postgres://role:private-password@localhost/dex".into(),
        ..Default::default()
    };
    config.validate().unwrap();
    assert_eq!(config.search.postgres_connection_pool_size, 10);
    assert_eq!(config.search.postgres_schema, "public");
    let serialized = serde_yaml_ng::to_string(&config.search).unwrap();
    assert!(serialized.contains("postgresConnectionString: '[redacted]'"));
    assert!(!serialized.contains("private-password"));
    assert!(!format!("{:?}", config.search).contains("private-password"));
    let parsed: super::SearchConfig = serde_yaml_ng::from_str("backend: postgres\npostgresConnectionString: postgres://localhost/dex\npostgresSchema: visibility\npostgresConnectionPoolSize: 3\n").unwrap();
    assert_eq!(parsed.backend, SearchBackend::Postgres);
    assert_eq!(parsed.postgres_schema, "visibility");
    for (field, mutate) in [
        (
            "search.postgresConnectionString",
            (|config: &mut Config| config.search.postgres_connection_string.clear())
                as fn(&mut Config),
        ),
        (
            "search.postgresConnectionPoolSize",
            |config: &mut Config| config.search.postgres_connection_pool_size = 0,
        ),
        ("search.postgresSchema", |config: &mut Config| {
            config.search.postgres_schema = "invalid,schema".into()
        }),
    ] {
        let mut invalid = config.clone();
        mutate(&mut invalid);
        assert!(invalid.validate().unwrap_err().to_string().contains(field));
    }
    let components = ComponentConfigs::from_config(&config, false).unwrap();
    assert!(
        components
            .flow_coordination
            .validation
            .keyword_value_limit
            .is_some()
    );
}

#[test]
fn postgres_connection_string_uses_environment_only_when_configuration_is_empty() {
    const CHILD_MARKER: &str = "DEX_ZD_TEST_POSTGRES_ENVIRONMENT";
    if std::env::var_os(CHILD_MARKER).is_some() {
        let directory = tempfile::TempDir::new().unwrap();
        let path = directory.path().join("postgres.yaml");
        for (configured, expected) in [
            ("", "postgres://environment/dex"),
            ("postgres://configured/dex", "postgres://configured/dex"),
        ] {
            let mut config = valid_config();
            config.object_store.credentials_source = CredentialSource::Anonymous;
            config.search.backend = SearchBackend::Postgres;
            config.search.postgres_connection_string = configured.into();
            let mut value = serde_yaml_ng::to_value(config).unwrap();
            value["search"]["postgresConnectionString"] =
                serde_yaml_ng::Value::String(configured.into());
            fs::write(&path, serde_yaml_ng::to_string(&value).unwrap()).unwrap();
            assert_eq!(
                Config::load_file(&path)
                    .unwrap()
                    .search
                    .postgres_connection_string,
                expected
            );
        }
        return;
    }
    let status = std::process::Command::new(std::env::current_exe().unwrap())
        .args(["--exact", "config::tests::postgres_connection_string_uses_environment_only_when_configuration_is_empty"])
        .env(CHILD_MARKER, "1")
        .env("DEX_ZD_POSTGRES_CONNECTION_STRING", "postgres://environment/dex")
        .status().unwrap();
    assert!(status.success());
}
