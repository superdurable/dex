// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use super::map_sub_flow_options;
use crate::server_protocol::NegotiatedServerProtocolVersion;
use crate::{Flow, FlowConfig, Registry, SubFlowOptions};

struct PullRoutingFlow;

impl Flow for PullRoutingFlow {
    type StartInput = ();
}

#[test]
fn sub_flow_without_config_overrides_only_worker_target() {
    let registry = Registry::new()
        .register(PullRoutingFlow)
        .expect("register SubFlow");
    let target = registry.flow("PullRoutingFlow").expect("lookup SubFlow");
    let options = map_sub_flow_options(
        target,
        &SubFlowOptions::new(),
        &NegotiatedServerProtocolVersion::default(),
    )
    .expect("map default SubFlow options");

    assert_eq!(
        options.flow_config_override,
        Some(dex_protocol::dex::FlowConfig {
            worker_target: Some(dex_protocol::dex::WorkerTarget { is_pull_mode: true }),
            ..Default::default()
        })
    );
}

#[test]
fn sub_flow_config_override_maps_history_window_events() {
    let registry = Registry::new()
        .register(PullRoutingFlow)
        .expect("register SubFlow");
    let target = registry.flow("PullRoutingFlow").expect("lookup SubFlow");
    let options = map_sub_flow_options(
        target,
        &SubFlowOptions::new().config_override(FlowConfig::new().history_window_events(2_500)),
        &NegotiatedServerProtocolVersion::default(),
    )
    .expect("map SubFlow options");

    let config = options.flow_config_override.expect("SubFlow config");
    assert_eq!(config.history_window_events, Some(2_500));
    assert_eq!(
        config.worker_target,
        Some(dex_protocol::dex::WorkerTarget { is_pull_mode: true })
    );
}

#[test]
fn sub_flow_config_override_maps_delete_expired_history_objects() {
    let registry = Registry::new()
        .register(PullRoutingFlow)
        .expect("register SubFlow");
    let target = registry.flow("PullRoutingFlow").expect("lookup SubFlow");
    let options = map_sub_flow_options(
        target,
        &SubFlowOptions::new()
            .config_override(FlowConfig::new().delete_expired_history_objects(false)),
        &NegotiatedServerProtocolVersion::default(),
    )
    .expect("map SubFlow options");

    let config = options.flow_config_override.expect("SubFlow config");
    assert_eq!(config.delete_expired_history_objects, Some(false));
    assert_eq!(
        config.worker_target,
        Some(dex_protocol::dex::WorkerTarget { is_pull_mode: true })
    );
}
