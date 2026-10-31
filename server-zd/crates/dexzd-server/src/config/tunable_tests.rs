// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::path::PathBuf;

use dexzd_engine::LocalStoreConfig;

use super::{CommitConfig, Config, ConfigError, LocalStorageConfig};
use crate::component_configs::ComponentConfigs;

type OutOfBoundsCase = (&'static str, fn(&mut Config));

fn valid_config() -> Config {
    let mut configuration = Config::default();
    configuration.cluster.node_id = "node-a".to_owned();
    configuration.object_store.bucket = "dex-test".to_owned();
    configuration.local_storage.directory = PathBuf::from("/tmp/dexzd-test-store");
    configuration
}

#[test]
fn commit_and_local_store_tunables_parse_and_reach_the_engine_node() {
    let parsed = Config::parse_yaml(
        "cluster:\n  nodeId: node-a\n  partitionCount: 64\nobjectStore:\n  bucket: dex-test\nlocalStorage:\n  directory: /tmp/dexzd\n  writeBufferBytes: 134217728\n  maxBackgroundJobs: 6\n  maxSubcompactions: 4\ncommit:\n  maxBatchRecords: 500\n  maxUnappliedBytes: 33554432\n  applierLanes: 16\n  applierQueueFrames: 128\n",
    )
    .expect("parse commit and local store tunables");
    assert_eq!(parsed.commit.applier_lanes, Some(16));
    parsed.validate().expect("valid tunables");
    let components = ComponentConfigs::from_config(&parsed, false).expect("component config");
    let commit = &components.engine_node.commit;
    assert_eq!(commit.max_batch_records, 500);
    assert_eq!(commit.max_unapplied_bytes, 32 * 1024 * 1024);
    assert_eq!(commit.applier_lanes, 16);
    assert_eq!(commit.applier_queue_frames, 128);
    let LocalStoreConfig::RocksDb { options, .. } = components.engine_node.local_store else {
        panic!("serve uses a RocksDB local store");
    };
    assert_eq!(options.write_buffer_bytes, 128 * 1024 * 1024);
    assert_eq!(options.max_background_jobs, 6);
    assert_eq!(options.max_subcompactions, 4);
}

#[test]
fn unset_tunables_use_current_and_cpu_based_defaults() {
    let mut configuration = valid_config();
    configuration.cluster.partition_count = 4;
    assert_eq!(configuration.commit.applier_lanes, None);
    assert_eq!(configuration.local_storage.max_background_jobs, None);
    configuration.validate().expect("valid defaults");
    let components =
        ComponentConfigs::from_config(&configuration, false).expect("component config");
    let commit = &components.engine_node.commit;
    assert_eq!(commit.max_batch_records, 10_000);
    assert_eq!(commit.max_unapplied_bytes, 64 * 1024 * 1024);
    assert_eq!(commit.applier_queue_frames, 64);
    // At least 8 lanes before the clamp, so four partitions always give four lanes.
    assert_eq!(commit.applier_lanes, 4);
    let LocalStoreConfig::RocksDb { options, .. } = components.engine_node.local_store else {
        panic!("serve uses a RocksDB local store");
    };
    assert_eq!(options.write_buffer_bytes, 64 * 1024 * 1024);
    assert_eq!(options.max_subcompactions, 1);
    assert!((2..=1024).contains(&options.max_background_jobs));

    let commit_defaults = CommitConfig::default();
    assert_eq!(commit_defaults.effective_applier_lanes(1024, Some(16)), 16);
    assert_eq!(commit_defaults.effective_applier_lanes(1024, Some(2)), 8);
    assert_eq!(commit_defaults.effective_applier_lanes(1024, Some(0)), 8);
    assert_eq!(commit_defaults.effective_applier_lanes(1024, None), 8);
    assert_eq!(commit_defaults.effective_applier_lanes(4, Some(16)), 4);
    assert_eq!(commit_defaults.effective_applier_lanes(2, None), 2);
    assert_eq!(
        commit_defaults.effective_applier_lanes(65_536, Some(4096)),
        1024
    );
    let explicit_lanes = CommitConfig {
        applier_lanes: Some(3),
        ..CommitConfig::default()
    };
    assert_eq!(explicit_lanes.effective_applier_lanes(1024, Some(16)), 3);

    let storage_defaults = LocalStorageConfig::default();
    assert_eq!(storage_defaults.effective_max_background_jobs(Some(16)), 8);
    assert_eq!(storage_defaults.effective_max_background_jobs(Some(2)), 2);
    assert_eq!(storage_defaults.effective_max_background_jobs(Some(1)), 2);
    assert_eq!(storage_defaults.effective_max_background_jobs(None), 2);
    assert_eq!(
        storage_defaults.effective_max_background_jobs(Some(4096)),
        1024
    );
    let explicit_jobs = LocalStorageConfig {
        max_background_jobs: Some(1),
        ..LocalStorageConfig::default()
    };
    assert_eq!(explicit_jobs.effective_max_background_jobs(Some(16)), 1);
}

#[test]
fn zero_applier_lanes_in_yaml_fail_validation() {
    let mut configuration = valid_config();
    configuration.commit = Config::parse_yaml("commit:\n  applierLanes: 0\n")
        .expect("parse zero applier lanes")
        .commit;
    assert_rejected(&configuration, "commit.applierLanes");
}

#[test]
fn tunables_outside_their_bounds_fail_validation() {
    let cases: [OutOfBoundsCase; 7] = [
        ("commit.applierLanes", |config| {
            config.cluster.partition_count = 2048;
            config.commit.applier_lanes = Some(1025);
        }),
        ("commit.applierLanes", |config| {
            config.cluster.partition_count = 16;
            config.commit.applier_lanes = Some(17);
        }),
        ("localStorage.maxBackgroundJobs", |config| {
            config.local_storage.max_background_jobs = Some(1025)
        }),
        ("localStorage.maxBackgroundJobs", |config| {
            config.local_storage.max_background_jobs = Some(u32::MAX)
        }),
        ("localStorage.writeBufferBytes", |config| {
            config.local_storage.write_buffer_bytes = 64 * 1024 - 1
        }),
        ("commit.maxUnappliedBytes", |config| {
            config.commit.max_unapplied_bytes = 32 * 1024 * 1024 - 1
        }),
        ("commit.maxUnappliedBytes", |config| {
            config.values.max_value_bytes = 1024 * 1024;
            config.api.grpc_max_message_bytes = 2 * 1024 * 1024;
            config.commit.max_unapplied_bytes = 2 * 1024 * 1024 - 1;
        }),
    ];
    for (field, mutate) in cases {
        let mut configuration = valid_config();
        mutate(&mut configuration);
        assert_rejected(&configuration, field);
    }

    let mut at_bounds = valid_config();
    at_bounds.cluster.partition_count = 2048;
    at_bounds.commit.applier_lanes = Some(1024);
    at_bounds.local_storage.max_background_jobs = Some(1024);
    at_bounds.local_storage.write_buffer_bytes = 64 * 1024;
    at_bounds.values.max_value_bytes = 1024 * 1024;
    at_bounds.api.grpc_max_message_bytes = 2 * 1024 * 1024;
    at_bounds.commit.max_unapplied_bytes = 2 * 1024 * 1024;
    at_bounds
        .validate()
        .expect("values at their bounds are valid");
    let components = ComponentConfigs::from_config(&at_bounds, false).expect("component config");
    assert_eq!(components.engine_node.commit.applier_lanes, 1024);
    let LocalStoreConfig::RocksDb { options, .. } = components.engine_node.local_store else {
        panic!("serve uses a RocksDB local store");
    };
    assert_eq!(options.max_background_jobs, 1024);
}

fn assert_rejected(configuration: &Config, expected_field: &str) {
    match configuration.validate() {
        Err(ConfigError::Invalid { field, .. }) => assert_eq!(field, expected_field),
        other => panic!("{expected_field} must be rejected, got {other:?}"),
    }
}

#[test]
fn projection_queue_limits_parse_and_validate() {
    let parsed = Config::parse_yaml("search:\n  projectionQueue:\n    softFrames: 4\n    softBytes: 1024\n    hardFrames: 8\n    hardBytes: 2048\n").expect("queue limits parse");
    assert_eq!(parsed.search.projection_queue.soft_frames, 4);
    assert_eq!(parsed.search.projection_queue.soft_bytes, 1024);
    assert_eq!(parsed.search.projection_queue.hard_frames, 8);
    assert_eq!(parsed.search.projection_queue.hard_bytes, 2048);
    let cases: [OutOfBoundsCase; 4] = [
        ("search.projectionQueue.softFrames", |configuration| {
            configuration.search.projection_queue.soft_frames = 0
        }),
        ("search.projectionQueue.softBytes", |configuration| {
            configuration.search.projection_queue.soft_bytes = 0
        }),
        ("search.projectionQueue.hardFrames", |configuration| {
            configuration.search.projection_queue.hard_frames = 1
        }),
        ("search.projectionQueue.hardBytes", |configuration| {
            configuration.search.projection_queue.hard_bytes = 1
        }),
    ];
    for (field, change) in cases {
        let mut configuration = valid_config();
        change(&mut configuration);
        let error = configuration
            .validate()
            .expect_err("invalid queue limit fails validation");
        assert!(error.to_string().contains(field), "{error}");
    }
}
