// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use super::{map_flow_config, map_pull_flow_config};
use crate::FlowConfig;

#[test]
fn start_flow_without_config_always_maps_pull_target() {
    assert_eq!(
        map_pull_flow_config(None).expect("map default StartFlow config"),
        dex_protocol::dex::FlowConfig {
            worker_target: Some(dex_protocol::dex::WorkerTarget {
                is_pull_mode: true,
                ..dex_protocol::dex::WorkerTarget::default()
            }),
            ..Default::default()
        }
    );
}

#[test]
fn start_flow_override_always_maps_pull_target() {
    assert_eq!(
        map_pull_flow_config(Some(&FlowConfig::new())).expect("map explicit Flow config"),
        dex_protocol::dex::FlowConfig {
            worker_target: Some(dex_protocol::dex::WorkerTarget {
                is_pull_mode: true,
                ..dex_protocol::dex::WorkerTarget::default()
            }),
            ..Default::default()
        }
    );
}

#[test]
fn update_flow_config_leaves_worker_target_unset() {
    for (config, expected_window_events) in [
        (FlowConfig::new(), None),
        (FlowConfig::new().history_window_events(100), Some(100)),
    ] {
        assert_eq!(
            map_flow_config(Some(&config)).expect("map Flow config update"),
            dex_protocol::dex::FlowConfig {
                history_window_events: expected_window_events,
                worker_target: None,
                ..Default::default()
            }
        );
    }
}

#[test]
fn flow_config_maps_history_window_events() {
    for (config, expected_window_events) in [
        (FlowConfig::new(), None),
        (FlowConfig::new().history_window_events(2_500), Some(2_500)),
    ] {
        assert_eq!(
            map_flow_config(Some(&config))
                .expect("map Flow config update")
                .history_window_events,
            expected_window_events
        );
    }
    assert!(
        map_flow_config(Some(&FlowConfig::new().history_window_events(u32::MAX))).is_err(),
        "a window above int32 must fail locally"
    );
}

#[test]
fn flow_config_maps_offload_history() {
    for (config, expected_offload_history) in [
        (FlowConfig::new(), None),
        (FlowConfig::new().offload_history(false), Some(false)),
        (FlowConfig::new().offload_history(true), Some(true)),
    ] {
        assert_eq!(
            map_pull_flow_config(Some(&config))
                .expect("map StartFlow config")
                .offload_history,
            expected_offload_history
        );
        assert_eq!(
            map_flow_config(Some(&config))
                .expect("map Flow config update")
                .offload_history,
            expected_offload_history
        );
    }
}

#[test]
#[expect(deprecated, reason = "verify the deprecated builder is not forwarded")]
fn flow_config_drops_delete_expired_history_objects() {
    for config in [
        FlowConfig::new(),
        FlowConfig::new().delete_expired_history_objects(false),
        FlowConfig::new().delete_expired_history_objects(true),
    ] {
        assert_eq!(
            map_pull_flow_config(Some(&config))
                .expect("map StartFlow config")
                .delete_expired_history_objects,
            None
        );
        assert_eq!(
            map_flow_config(Some(&config))
                .expect("map Flow config update")
                .delete_expired_history_objects,
            None
        );
    }
}
