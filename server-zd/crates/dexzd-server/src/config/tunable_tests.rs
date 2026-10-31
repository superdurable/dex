// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use std::path::PathBuf;

use dexzd_engine::LocalStoreConfig;

use super::{CommitConfig, Config, ConfigError, LocalStorageConfig, available_cpu_count};
use crate::component_configs::ComponentConfigs;

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
    let configuration = valid_config();
    assert_eq!(configuration.commit.applier_lanes, None);
    assert_eq!(configuration.local_storage.max_background_jobs, None);
    let components =
        ComponentConfigs::from_config(&configuration, false).expect("component config");
    let commit = &components.engine_node.commit;
    assert_eq!(commit.max_batch_records, 10_000);
    assert_eq!(commit.max_unapplied_bytes, 64 * 1024 * 1024);
    assert_eq!(commit.applier_queue_frames, 64);
    let available_cpus = available_cpu_count();
    assert_eq!(
        commit.applier_lanes,
        configuration
            .commit
            .effective_applier_lanes(configuration.cluster.partition_count, available_cpus)
            as usize
    );
    let LocalStoreConfig::RocksDb { options, .. } = components.engine_node.local_store else {
        panic!("serve uses a RocksDB local store");
    };
    assert_eq!(options.write_buffer_bytes, 64 * 1024 * 1024);
    assert_eq!(options.max_subcompactions, 1);
    assert_eq!(
        i64::from(options.max_background_jobs),
        i64::from(
            configuration
                .local_storage
                .effective_max_background_jobs(available_cpus)
        )
    );

    let commit_defaults = &configuration.commit;
    assert_eq!(commit_defaults.effective_applier_lanes(1024, Some(16)), 16);
    assert_eq!(commit_defaults.effective_applier_lanes(1024, Some(2)), 2);
    assert_eq!(commit_defaults.effective_applier_lanes(4, Some(16)), 4);
    assert_eq!(commit_defaults.effective_applier_lanes(1024, Some(0)), 1);
    assert_eq!(commit_defaults.effective_applier_lanes(1024, None), 8);
    assert_eq!(commit_defaults.effective_applier_lanes(2, None), 2);
    let explicit_lanes = CommitConfig {
        applier_lanes: Some(3),
        ..CommitConfig::default()
    };
    assert_eq!(explicit_lanes.effective_applier_lanes(1024, Some(16)), 3);

    let storage_defaults = &configuration.local_storage;
    assert_eq!(storage_defaults.effective_max_background_jobs(Some(16)), 8);
    assert_eq!(storage_defaults.effective_max_background_jobs(Some(2)), 2);
    assert_eq!(storage_defaults.effective_max_background_jobs(Some(1)), 2);
    assert_eq!(storage_defaults.effective_max_background_jobs(None), 2);
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
    assert!(matches!(
        configuration.validate(),
        Err(ConfigError::Invalid {
            field: "commit.applierLanes",
            ..
        })
    ));
}
