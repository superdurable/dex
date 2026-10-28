// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use super::map_flow_config;
use crate::FlowConfig;

#[test]
fn start_flow_without_config_always_maps_pull_target() {
    assert_eq!(
        map_flow_config(None).expect("map default StartFlow config"),
        dex_protocol::dex::FlowConfig {
            worker_target: Some(dex_protocol::dex::WorkerTarget {
                is_pull_mode: true,
                address: String::new(),
                is_headless_address: false,
            }),
            ..Default::default()
        }
    );
}

#[test]
fn start_flow_override_and_update_flow_config_always_map_pull_target() {
    assert_eq!(
        map_flow_config(Some(&FlowConfig::new())).expect("map explicit Flow config"),
        dex_protocol::dex::FlowConfig {
            worker_target: Some(dex_protocol::dex::WorkerTarget {
                is_pull_mode: true,
                address: String::new(),
                is_headless_address: false,
            }),
            ..Default::default()
        }
    );
}
