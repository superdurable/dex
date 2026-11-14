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
            worker_target: Some(dex_protocol::dex::WorkerTarget {
                is_pull_mode: true,
                ..dex_protocol::dex::WorkerTarget::default()
            }),
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
        Some(dex_protocol::dex::WorkerTarget {
            is_pull_mode: true,
            ..dex_protocol::dex::WorkerTarget::default()
        })
    );
}

#[test]
fn sub_flow_config_override_maps_offload_history() {
    let registry = Registry::new()
        .register(PullRoutingFlow)
        .expect("register SubFlow");
    let target = registry.flow("PullRoutingFlow").expect("lookup SubFlow");
    let options = map_sub_flow_options(
        target,
        &SubFlowOptions::new().config_override(FlowConfig::new().offload_history(false)),
        &NegotiatedServerProtocolVersion::default(),
    )
    .expect("map SubFlow options");

    let config = options.flow_config_override.expect("SubFlow config");
    assert_eq!(config.offload_history, Some(false));
    assert_eq!(
        config.worker_target,
        Some(dex_protocol::dex::WorkerTarget {
            is_pull_mode: true,
            ..dex_protocol::dex::WorkerTarget::default()
        })
    );
}

#[test]
#[expect(deprecated, reason = "verify the deprecated builder is not forwarded")]
fn sub_flow_config_override_drops_delete_expired_history_objects() {
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
    assert_eq!(config.delete_expired_history_objects, None);
    assert_eq!(
        config.worker_target,
        Some(dex_protocol::dex::WorkerTarget {
            is_pull_mode: true,
            ..dex_protocol::dex::WorkerTarget::default()
        })
    );
}

#[test]
fn sub_flow_task_queue_requires_a_valid_name_and_protocol_five() {
    let registry = Registry::new()
        .register(PullRoutingFlow)
        .expect("register SubFlow");
    let target = registry.flow("PullRoutingFlow").expect("lookup SubFlow");
    let negotiated = NegotiatedServerProtocolVersion::default();
    negotiated.store(4);
    let inherited = map_sub_flow_options(target, &SubFlowOptions::new(), &negotiated)
        .expect("an empty task queue inherits the parent's on protocol 4");
    assert_eq!(
        inherited
            .flow_config_override
            .and_then(|config| config.worker_target)
            .map(|target| target.task_queue),
        Some(String::new())
    );
    let old_server = map_sub_flow_options(
        target,
        &SubFlowOptions::new().task_queue("reports"),
        &negotiated,
    )
    .expect_err("protocol 4 ignores named task queues");
    assert!(
        old_server.to_string().contains("protocol version 5"),
        "{old_server}"
    );
    negotiated.store(5);
    for task_queue in ["reports", "dex.default"] {
        let options = map_sub_flow_options(
            target,
            &SubFlowOptions::new().task_queue(task_queue),
            &negotiated,
        )
        .expect("map SubFlow task queue");
        assert_eq!(
            options
                .flow_config_override
                .and_then(|config| config.worker_target),
            Some(dex_protocol::dex::WorkerTarget {
                is_pull_mode: true,
                task_queue: task_queue.to_string(),
            })
        );
    }
    let invalid = map_sub_flow_options(
        target,
        &SubFlowOptions::new().task_queue("dex.other"),
        &negotiated,
    )
    .expect_err("reserved task queue");
    assert!(invalid.to_string().contains("reserved"), "{invalid}");
}
