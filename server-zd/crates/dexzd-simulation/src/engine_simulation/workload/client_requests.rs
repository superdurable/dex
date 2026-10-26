// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! FlowService requests of simulated clients. Each write carries a unique operation marker.

use dexzd_protocol::dex::{
    AttributeWrite, ChannelMessage, DeleteChannelMessageRequest, EncodedObject,
    ExecuteMethodFailurePolicy, FlowAlreadyStartedOptions, FlowConfig, FlowStartOptions,
    FlowTimeoutHandlerOptions, FlowTimeoutPolicy, IdReusePolicy, InvokeRpcRequest,
    PublishToChannelRequest, RetryPolicy, SetAttributesRequest, SkipTimerRequest, StartFlowRequest,
    StopFlowRequest, Value, WorkerTarget, value,
};
use dexzd_protocol::dexzd::v1::{FlowCommand, flow_command::Command};
use rand::RngExt;
use rand_chacha::ChaCha8Rng;

use crate::engine_simulation::workload::worker_responses::{
    RECOVERY_STEP_TYPE, engine_step_options, recovery_step_options, retry_policy,
};
use crate::model::{
    ATTRIBUTE_KEYS, CHANNEL_NAMES, STEP_TYPES, int_value, null_value, pick, random_value,
};

pub(crate) fn start_flow_request(
    rng: &mut ChaCha8Rng,
    flow_id: &str,
    marker: &str,
    has_step_failure_policies: bool,
) -> StartFlowRequest {
    // Step failure profile timeouts expire before most runs close, so timeout handlers run.
    let flow_timeout_seconds = if rng.random_bool(0.2) {
        rng.random_range(if has_step_failure_policies {
            5..30
        } else {
            20..120
        })
    } else {
        0
    };
    let mut policy = match rng.random_range(0..6) {
        _ if flow_timeout_seconds == 0 => FlowTimeoutPolicy::Unspecified,
        0 => FlowTimeoutPolicy::Fail,
        1 => FlowTimeoutPolicy::Cancel,
        2 => FlowTimeoutPolicy::Handler,
        _ => FlowTimeoutPolicy::Unspecified,
    };
    // Every Step failure profile timeout runs a handler, whose failure policy then applies.
    if has_step_failure_policies && flow_timeout_seconds > 0 {
        policy = FlowTimeoutPolicy::Handler;
    }
    let id_reuse_policy = match rng.random_range(0..4) {
        0 => IdReusePolicy::AllowIfNoRunning,
        1 => IdReusePolicy::AllowTerminateIfRunning,
        _ => IdReusePolicy::Unspecified,
    };
    let mut attributes = vec![AttributeWrite {
        key: "counter".to_owned(),
        value: Some(int_value(0)),
        ..AttributeWrite::default()
    }];
    let key_count = rng.random_range(0..3);
    for key in distinct_keys(rng, key_count) {
        attributes.push(AttributeWrite {
            key,
            value: Some(random_value(rng)),
            ..AttributeWrite::default()
        });
    }
    if rng.random_bool(0.2) {
        attributes.push(hard_delete_write(rng, false));
    }
    let start_step_type = pick(rng, STEP_TYPES);
    StartFlowRequest {
        flow_id: flow_id.to_owned(),
        flow_type: "OrderFlow".to_owned(),
        flow_timeout_seconds,
        flow_timeout_policy: policy as i32,
        start_step_type: start_step_type.to_owned(),
        step_input: rng.random_bool(0.8).then(|| random_value(rng)),
        step_options: Some(engine_step_options(
            rng,
            start_step_type,
            has_step_failure_policies,
        )),
        flow_start_options: Some(FlowStartOptions {
            id_reuse_policy: id_reuse_policy as i32,
            attributes,
            flow_already_started_options: Some(FlowAlreadyStartedOptions {
                ignore_already_started_error: rng.random_bool(0.3),
            }),
            flow_config_override: Some(FlowConfig {
                worker_target: Some(WorkerTarget {
                    is_pull_mode: true,
                    ..WorkerTarget::default()
                }),
                ..FlowConfig::default()
            }),
            timeout_handler_options: (policy == FlowTimeoutPolicy::Handler)
                .then(|| timeout_handler_options(rng, has_step_failure_policies)),
            ..FlowStartOptions::default()
        }),
        request_id: marker.to_owned(),
    }
}

/// Timeout handler options; validation allows a failure target only with the proceed policy.
fn timeout_handler_options(
    rng: &mut ChaCha8Rng,
    has_step_failure_policies: bool,
) -> FlowTimeoutHandlerOptions {
    let lock_attribute_keys = vec!["status".to_owned()];
    if !has_step_failure_policies {
        return FlowTimeoutHandlerOptions {
            lock_attribute_keys,
            ..FlowTimeoutHandlerOptions::default()
        };
    }
    let should_proceed_on_failure = rng.random_bool(0.6);
    // At most two attempts, so a failing handler exhausts its retries within a seed.
    let handler_retry_policy = RetryPolicy {
        maximum_attempts: rng.random_range(1..3),
        ..retry_policy(rng, true).unwrap_or_default()
    };
    FlowTimeoutHandlerOptions {
        lock_attribute_keys,
        retry_policy: Some(handler_retry_policy),
        failure_policy: if should_proceed_on_failure {
            ExecuteMethodFailurePolicy::ProceedToConfiguredStep as i32
        } else {
            ExecuteMethodFailurePolicy::Unspecified as i32
        },
        failure_proceed_step_type: if should_proceed_on_failure {
            RECOVERY_STEP_TYPE.to_owned()
        } else {
            String::new()
        },
        failure_proceed_step_options: should_proceed_on_failure.then(recovery_step_options),
        ..FlowTimeoutHandlerOptions::default()
    }
}

pub(crate) fn set_attributes_request(
    rng: &mut ChaCha8Rng,
    flow_id: &str,
    marker: &str,
) -> SetAttributesRequest {
    let key_count = rng.random_range(1..4);
    let mut attributes: Vec<AttributeWrite> = distinct_keys(rng, key_count)
        .into_iter()
        .map(|key| AttributeWrite {
            key,
            value: Some(if rng.random_bool(0.1) {
                null_value()
            } else {
                random_value(rng)
            }),
            ..AttributeWrite::default()
        })
        .collect();
    if rng.random_bool(0.35) {
        attributes.push(hard_delete_write(rng, true));
    }
    SetAttributesRequest {
        flow_id: flow_id.to_owned(),
        attributes,
        request_id: marker.to_owned(),
        ..SetAttributesRequest::default()
    }
}

/// The first message's value is the marker, since publishing has no request ID.
pub(crate) fn publish_request(
    rng: &mut ChaCha8Rng,
    flow_id: &str,
    marker: &str,
) -> PublishToChannelRequest {
    let mut messages = vec![ChannelMessage {
        channel_name: pick(rng, CHANNEL_NAMES).to_owned(),
        value: Some(marker_value(marker)),
        message_id: String::new(),
    }];
    for _ in 0..rng.random_range(0..3) {
        messages.push(ChannelMessage {
            channel_name: pick(rng, CHANNEL_NAMES).to_owned(),
            value: Some(random_value(rng)),
            message_id: String::new(),
        });
    }
    PublishToChannelRequest {
        flow_id: flow_id.to_owned(),
        messages,
        ..PublishToChannelRequest::default()
    }
}

pub(crate) fn delete_message_request(
    flow_id: &str,
    channel_name: &str,
    message_id: &str,
    marker: &str,
) -> DeleteChannelMessageRequest {
    DeleteChannelMessageRequest {
        flow_id: flow_id.to_owned(),
        channel_name: channel_name.to_owned(),
        message_id: message_id.to_owned(),
        request_id: marker.to_owned(),
        ..DeleteChannelMessageRequest::default()
    }
}

/// The reason carries the marker, since stopping has no request ID.
pub(crate) fn stop_request(rng: &mut ChaCha8Rng, flow_id: &str, marker: &str) -> StopFlowRequest {
    StopFlowRequest {
        flow_id: flow_id.to_owned(),
        reason: marker.to_owned(),
        stop_type: rng.random_range(0..3),
        ..StopFlowRequest::default()
    }
}

/// The Flow timeout is the timeout handler's only timer: index 0, no condition ID.
pub(crate) fn flow_timeout_skip_request(flow_id: &str) -> SkipTimerRequest {
    SkipTimerRequest {
        flow_id: flow_id.to_owned(),
        step_execution_id: "sys:timeout_handler-1".to_owned(),
        timer_condition_index: Some(0),
        ..SkipTimerRequest::default()
    }
}

pub(crate) fn rpc_request(rng: &mut ChaCha8Rng, flow_id: &str, marker: &str) -> InvokeRpcRequest {
    InvokeRpcRequest {
        flow_id: flow_id.to_owned(),
        rpc_name: "getStatus".to_owned(),
        input: Some(random_value(rng)),
        request_id: marker.to_owned(),
        timeout_seconds: rng.random_range(1..10),
        lock_attribute_keys: if rng.random_bool(0.3) {
            vec!["status".to_owned()]
        } else {
            Vec::new()
        },
        ..InvokeRpcRequest::default()
    }
}

/// RPC name of the child-wait probe, whose Worker holds it while the run waits for a child Flow.
pub(crate) const AWAIT_CHILD_RESULT_RPC_NAME: &str = "awaitChildResult";

/// A locking RPC of the child-wait probe. Its timeout ends it before the client gives up.
pub(crate) fn await_child_result_rpc_request(
    flow_id: &str,
    run_id: &str,
    marker: &str,
) -> InvokeRpcRequest {
    InvokeRpcRequest {
        flow_id: flow_id.to_owned(),
        run_id: run_id.to_owned(),
        rpc_name: AWAIT_CHILD_RESULT_RPC_NAME.to_owned(),
        request_id: marker.to_owned(),
        timeout_seconds: 10,
        lock_attribute_keys: vec!["status".to_owned()],
        ..InvokeRpcRequest::default()
    }
}

/// Up to `count` distinct Attribute keys, since a request may write each key once.
fn distinct_keys(rng: &mut ChaCha8Rng, count: usize) -> Vec<String> {
    let mut keys: Vec<String> = Vec::new();
    for _ in 0..count {
        let key = pick(rng, ATTRIBUTE_KEYS);
        if !keys.iter().any(|existing| existing == key) {
            keys.push(key.to_owned());
        }
    }
    keys
}

/// A write of the hard-delete Attribute: a string or an object, or null to delete it.
fn hard_delete_write(rng: &mut ChaCha8Rng, may_delete: bool) -> AttributeWrite {
    let label: u32 = rng.random();
    let value = match rng.random_range(0..10) {
        0..=2 if may_delete => null_value(),
        0..=6 => Value {
            kind: Some(value::Kind::StringValue(format!("secret {label}"))),
        },
        _ => Value {
            kind: Some(value::Kind::ObjValue(EncodedObject {
                encoding: "json".to_owned(),
                payload: format!("{{\"secret\":{label}}}").into_bytes(),
            })),
        },
    };
    AttributeWrite {
        key: HARD_DELETE_ATTRIBUTE_KEY.to_owned(),
        value: Some(value),
        is_hard_delete_on_null: true,
        ..AttributeWrite::default()
    }
}

fn marker_value(marker: &str) -> Value {
    Value {
        kind: Some(value::Kind::StringValue(marker.to_owned())),
    }
}

/// Returns the operation marker of a client write command, if it has one.
pub(crate) fn operation_marker(command: &FlowCommand) -> Option<&str> {
    let marker = match command.command.as_ref()? {
        Command::FlowStart(start) => start.request.as_ref()?.request_id.as_str(),
        Command::AttributesSet(set) => set.request.as_ref()?.request_id.as_str(),
        Command::ChannelMessageDelete(delete) => delete.request.as_ref()?.request_id.as_str(),
        Command::FlowStop(stop) => stop.request.as_ref()?.reason.as_str(),
        Command::RpcCommitted(rpc) => rpc.request.as_ref()?.request_id.as_str(),
        Command::ChannelPublish(publish) => {
            let first = publish.request.as_ref()?.messages.first()?;
            match first.value.as_ref()?.kind.as_ref()? {
                value::Kind::StringValue(text) => text.as_str(),
                _ => return None,
            }
        }
        _ => return None,
    };
    marker
        .starts_with(OPERATION_MARKER_PREFIX)
        .then_some(marker)
}

/// Prefix of every operation marker.
pub(crate) const OPERATION_MARKER_PREFIX: &str = "client-op-";

/// The Attribute clients write only as a hard-delete Attribute, whose values live in object
/// storage.
const HARD_DELETE_ATTRIBUTE_KEY: &str = "secret";
