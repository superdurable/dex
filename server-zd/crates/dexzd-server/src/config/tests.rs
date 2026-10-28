// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::fs;
use std::net::{Ipv4Addr, SocketAddr};
use std::path::PathBuf;
use std::time::Duration;

use dexzd_engine::LocalStoreConfig;

use super::{
    AttributeStoreColumnNaming, AttributeStoreType, Config, ConfigError, CredentialSource,
    DevDurabilityMode, ObjectStoreLocationConfig, SearchBackend, StreamStoreBackend,
    dsn_environment_variable,
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
    assert_eq!(parsed.stream_store.backend, StreamStoreBackend::Disabled);
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
        listen: SocketAddr::from((Ipv4Addr::LOCALHOST, 8801)),
        log_dir: Some(log_directory.clone()),
        config: Some(yaml_path),
        attribute_store_config: None,
    };
    let configuration = Config::load_dev(&options).expect("dev configuration");
    assert_eq!(configuration.cluster.node_id, "dev");
    assert_eq!(configuration.api.listen, options.listen);
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
    assert_eq!(
        defaults.dev.durability_mode,
        DevDurabilityMode::ProcessCrashSafe
    );
    assert_eq!(defaults.search.backend, SearchBackend::Builtin);
    assert_eq!(defaults.stream_store.backend, StreamStoreBackend::Memory);
    assert_eq!(defaults.object_store.endpoint, "http://127.0.0.1:0");
    let empty_data_directory = Config::load_dev(&DevCommandOptions {
        data_dir: PathBuf::new(),
        listen: options.listen,
        log_dir: None,
        config: None,
        attribute_store_config: None,
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
        listen: SocketAddr::from((Ipv4Addr::LOCALHOST, 8801)),
        log_dir: None,
        config,
        attribute_store_config: None,
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
        listen: SocketAddr::from((Ipv4Addr::LOCALHOST, 8801)),
        log_dir: None,
        config: Some(yaml_path),
        attribute_store_config: None,
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
        ("history.windowSemanticEvents", |config| {
            config.history.window_semantic_events = 0
        }),
        ("history.pruneChunkSize", |config| {
            config.history.prune_chunk_size = 10_001
        }),
        ("history.checkpointMinWrittenBytes", |config| {
            config.history.checkpoint_min_written_bytes = 0
        }),
        ("history.checkpointCommandInterval", |config| {
            config.history.checkpoint_command_interval = 0
        }),
        ("history.maxRetainedSupersededBytes", |config| {
            config.history.max_retained_superseded_bytes = 1
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
        ("limits.maxFlowIdBytes", |config| {
            config.limits.max_flow_id_bytes = 0
        }),
        ("dev.s3DataDirectory", |config| {
            config.dev.data_directory = PathBuf::from("/tmp/dev")
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
        listen: SocketAddr::from((Ipv4Addr::LOCALHOST, 8801)),
        log_dir: None,
        config: None,
        attribute_store_config: Some(go_config.clone()),
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
