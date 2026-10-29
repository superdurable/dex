// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

//! Commands that client API calls produce after the owner validated them.

use dexzd_keyspace::RunId;
use dexzd_protocol::dex::{
    ActionPermissionMapping, ActionPermissionMappings, AttributeStoreNames, AttributeWrite,
    ChannelMessage, DeleteChannelMessageRequest, ExecuteMethodFailurePolicy,
    FlowAlreadyStartedOptions, FlowConfig, FlowResetType, FlowStartOptions,
    FlowTimeoutHandlerOptions, FlowTimeoutPolicy, IdReusePolicy, InvokeRpcRequest,
    InvokeWorkerRpcResponse, PublishToChannelRequest, ResetFlowRequest, SetAttributesRequest,
    SkipTimerRequest, StartFlowRequest, StopFlowRequest, UpdateFlowConfigRequest, WorkerTarget,
};
use dexzd_protocol::dexzd::v1::{
    AttributeSyncAckCommand, AttributesSetCommand, ChannelMessageDeleteCommand,
    ChannelPublishCommand, FlowConfigUpdateCommand, FlowStartCommand, FlowStopCommand,
    HardDeleteObjectReference, RpcCommittedCommand, RunSettings, TimeTravelCommand,
    TimerSkipCommand, flow_command::Command,
};
use rand::RngExt;
use rand_chacha::ChaCha8Rng;

use crate::model::value_generation::{
    ATTRIBUTE_KEYS, ATTRIBUTE_STORE_NAMES, CHANNEL_NAMES, MAX_VALUE_BYTES, STEP_TYPES,
    WIRE_INLINE_THRESHOLD_BYTES, fingerprint, int_value, null_value, oversized_value, pick,
    random_bytes, random_identifier, random_value, retry_policy, step_options, sync_config_for,
};
use crate::state_view::RunView;

const SECOND: i64 = 1_000_000;

/// Request IDs come from a small pool so reuse with the same and different content happens.
fn request_id(rng: &mut ChaCha8Rng, prefix: &str) -> String {
    if rng.random_bool(0.4) {
        return String::new();
    }
    format!("{prefix}-{}", rng.random_range(0..6))
}

pub(crate) fn run_settings() -> RunSettings {
    RunSettings {
        history_window_semantic_events: 3,
        history_prune_chunk_semantic_events: 2,
        // A generous cap keeps retained points available for pinned snapshot and replay coverage.
        max_retained_superseded_bytes: 256 << 20,
        retention_after_close_micros: 60 * SECOND,
        wire_inline_threshold_bytes: WIRE_INLINE_THRESHOLD_BYTES,
        max_value_bytes: MAX_VALUE_BYTES,
        checkpoint_min_written_bytes: 1 << 20,
        checkpoint_max_commands: 10_000,
        request_dedup_capacity: 4,
        max_run_state_bytes: 1 << 30,
        max_active_step_executions: 10_000,
    }
}

pub(crate) fn start_flow(
    rng: &mut ChaCha8Rng,
    flow_id: &str,
    is_synced: bool,
    is_step_failure_stream: bool,
) -> Command {
    let policy = match rng.random_range(0..5) {
        0 => FlowTimeoutPolicy::Fail,
        1 => FlowTimeoutPolicy::Cancel,
        2 => FlowTimeoutPolicy::Handler,
        _ => FlowTimeoutPolicy::Unspecified,
    };
    let id_reuse_policy = match rng.random_range(0..6) {
        0 => IdReusePolicy::AllowIfPreviousExistsAbnormally,
        1 => IdReusePolicy::AllowIfNoRunning,
        2 => IdReusePolicy::DisallowReuse,
        3 => IdReusePolicy::AllowTerminateIfRunning,
        _ => IdReusePolicy::Unspecified,
    };
    let mut initial_attributes = Vec::new();
    if rng.random_bool(0.3) {
        initial_attributes.push(AttributeWrite {
            key: "counter".to_owned(),
            value: Some(int_value(0)),
            ..AttributeWrite::default()
        });
    }
    for _ in 0..rng.random_range(0..3) {
        let key = pick(rng, ATTRIBUTE_KEYS);
        initial_attributes.push(AttributeWrite {
            key: key.to_owned(),
            value: Some(random_value(rng)),
            sync_config: sync_config_for(key, is_synced),
            ..AttributeWrite::default()
        });
    }
    let request = StartFlowRequest {
        flow_id: flow_id.to_owned(),
        flow_type: "OrderFlow".to_owned(),
        flow_timeout_seconds: if rng.random_bool(0.2) {
            rng.random_range(30..600)
        } else {
            0
        },
        flow_timeout_policy: policy as i32,
        start_step_type: pick(rng, STEP_TYPES).to_owned(),
        step_input: rng.random_bool(0.8).then(|| random_value(rng)),
        step_options: step_options(rng, is_step_failure_stream),
        flow_start_options: Some(FlowStartOptions {
            id_reuse_policy: id_reuse_policy as i32,
            attributes: initial_attributes,
            flow_config_override: is_synced.then(|| FlowConfig {
                attribute_store_names: Some(AttributeStoreNames {
                    names: ATTRIBUTE_STORE_NAMES
                        .iter()
                        .map(|name| (*name).to_owned())
                        .collect(),
                }),
                ..FlowConfig::default()
            }),
            flow_already_started_options: Some(FlowAlreadyStartedOptions {
                ignore_already_started_error: rng.random_bool(0.3),
            }),
            timeout_handler_options: (policy == FlowTimeoutPolicy::Handler)
                .then(|| timeout_handler_options(rng, is_step_failure_stream)),
            ..FlowStartOptions::default()
        }),
        request_id: format!("start-{}", rng.random_range(0..4)),
    };
    let resolved_timeout_policy = match policy {
        FlowTimeoutPolicy::Unspecified => FlowTimeoutPolicy::Fail,
        other => other,
    };
    Command::FlowStart(FlowStartCommand {
        new_run_id: random_bytes(rng).to_vec(),
        request_fingerprint: fingerprint(&request),
        request: Some(request),
        settings: Some(run_settings()),
        resolved_timeout_policy: resolved_timeout_policy as i32,
        worker_target: Some(WorkerTarget { is_pull_mode: true }),
        hard_delete_objects: Vec::new(),
        ..FlowStartCommand::default()
    })
}

/// Timeout handler options; the Step failure stream adds retry and failure policies.
fn timeout_handler_options(
    rng: &mut ChaCha8Rng,
    is_step_failure_stream: bool,
) -> FlowTimeoutHandlerOptions {
    let lock_attribute_keys = if rng.random_bool(0.5) {
        vec!["status".to_owned()]
    } else {
        Vec::new()
    };
    if !is_step_failure_stream {
        return FlowTimeoutHandlerOptions {
            lock_attribute_keys,
            ..FlowTimeoutHandlerOptions::default()
        };
    }
    let should_proceed_on_failure = rng.random_bool(0.4);
    FlowTimeoutHandlerOptions {
        lock_attribute_keys,
        retry_policy: retry_policy(rng, true),
        failure_policy: if should_proceed_on_failure {
            ExecuteMethodFailurePolicy::ProceedToConfiguredStep as i32
        } else if rng.random_bool(0.5) {
            ExecuteMethodFailurePolicy::FailFlowOnExecuteMethodFailure as i32
        } else {
            ExecuteMethodFailurePolicy::Unspecified as i32
        },
        failure_proceed_step_type: if should_proceed_on_failure {
            pick(rng, STEP_TYPES).to_owned()
        } else {
            String::new()
        },
        failure_proceed_step_options: if should_proceed_on_failure {
            step_options(rng, true)
        } else {
            None
        },
        ..FlowTimeoutHandlerOptions::default()
    }
}

/// Hard-delete and plain writes share [`ATTRIBUTE_KEYS`], so each replaces the other.
pub(crate) fn set_attributes(rng: &mut ChaCha8Rng, is_synced: bool) -> Command {
    let mut attributes = Vec::new();
    let mut hard_delete_objects = Vec::new();
    for write_index in 0..rng.random_range(1..4_u32) {
        let key = pick(rng, ATTRIBUTE_KEYS).to_owned();
        if rng.random_bool(0.15) {
            let is_set = rng.random_bool(0.6);
            if is_set {
                hard_delete_objects.push(HardDeleteObjectReference {
                    write_index,
                    object_id: random_identifier(rng),
                    size_bytes: rng.random_range(1..5000),
                    is_string_value: rng.random_bool(0.5),
                });
            }
            attributes.push(AttributeWrite {
                key,
                value: (!is_set).then(null_value),
                is_hard_delete_on_null: true,
                ..AttributeWrite::default()
            });
            continue;
        }
        let value = match rng.random_range(0..20) {
            0..=2 => null_value(),
            3 => oversized_value(rng),
            _ => random_value(rng),
        };
        attributes.push(AttributeWrite {
            sync_config: sync_config_for(&key, is_synced),
            key,
            value: Some(value),
            ..AttributeWrite::default()
        });
    }
    let request = SetAttributesRequest {
        action_permission_mappings: is_synced.then(|| ActionPermissionMappings {
            mappings: attributes
                .iter()
                .filter(|write| !write.is_hard_delete_on_null)
                .map(|write| ActionPermissionMapping {
                    attribute_key: write.key.clone(),
                    equal_values: write.value.iter().cloned().collect(),
                    required_permission: "write".to_owned(),
                })
                .collect(),
        }),
        attributes,
        request_id: request_id(rng, "set"),
        ..SetAttributesRequest::default()
    };
    Command::AttributesSet(AttributesSetCommand {
        request_fingerprint: fingerprint(&request),
        request: Some(request),
        hard_delete_objects,
    })
}

pub(crate) fn publish(rng: &mut ChaCha8Rng) -> Command {
    let messages = (0..rng.random_range(1..4))
        .map(|_| ChannelMessage {
            channel_name: pick(rng, CHANNEL_NAMES).to_owned(),
            value: Some(if rng.random_bool(0.03) {
                oversized_value(rng)
            } else {
                random_value(rng)
            }),
            message_id: random_identifier(rng),
        })
        .collect();
    Command::ChannelPublish(ChannelPublishCommand {
        request: Some(PublishToChannelRequest {
            messages,
            ..PublishToChannelRequest::default()
        }),
    })
}

pub(crate) fn delete_message(rng: &mut ChaCha8Rng, run: &RunView) -> Command {
    let (channel_name, message_id) = if !run.channel_messages.is_empty() && rng.random_bool(0.8) {
        let message = &run.channel_messages[rng.random_range(0..run.channel_messages.len())];
        (message.channel_name.clone(), message.message_id.clone())
    } else {
        (pick(rng, CHANNEL_NAMES).to_owned(), random_identifier(rng))
    };
    let request = DeleteChannelMessageRequest {
        channel_name,
        message_id,
        request_id: request_id(rng, "delete"),
        ..DeleteChannelMessageRequest::default()
    };
    Command::ChannelMessageDelete(ChannelMessageDeleteCommand {
        request_fingerprint: fingerprint(&request),
        request: Some(request),
    })
}

pub(crate) fn stop(rng: &mut ChaCha8Rng) -> Command {
    Command::FlowStop(FlowStopCommand {
        request: Some(StopFlowRequest {
            stop_type: rng.random_range(0..4),
            reason: "stopped by the simulated client".to_owned(),
            ..StopFlowRequest::default()
        }),
    })
}

pub(crate) fn config_update() -> Command {
    Command::FlowConfigUpdate(FlowConfigUpdateCommand {
        request: Some(UpdateFlowConfigRequest {
            flow_config: Some(FlowConfig {
                continue_as_new_threshold: Some(100),
                ..FlowConfig::default()
            }),
            ..UpdateFlowConfigRequest::default()
        }),
    })
}

/// A projector ack of one store's entries, sometimes partial, stale, or a drop.
pub(crate) fn attribute_sync_ack(rng: &mut ChaCha8Rng, run: &RunView) -> Command {
    let entries: Vec<_> = run.attribute_sync_entries.values().collect();
    let chosen = entries[rng.random_range(0..entries.len())];
    let store_seqs: Vec<u64> = entries
        .iter()
        .filter(|entry| entry.store_name == chosen.store_name)
        .map(|entry| entry.written_seq)
        .collect();
    let acked_seq = match rng.random_range(0..4) {
        0 => chosen.written_seq.saturating_sub(1),
        1 => chosen.written_seq,
        _ => store_seqs.iter().copied().max().unwrap_or_default(),
    };
    let attribute_keys = if rng.random_bool(0.3) {
        vec![chosen.attribute_key.clone()]
    } else {
        Vec::new()
    };
    Command::AttributeSyncAck(AttributeSyncAckCommand {
        store_name: chosen.store_name.clone(),
        acked_seq,
        attribute_keys,
        is_dropped: rng.random_bool(0.2),
    })
}

/// Skips the Flow timeout: the timeout handler's only timer, index 0 with no condition ID.
pub(crate) fn skip_flow_timeout(flow_id: &str) -> Command {
    Command::TimerSkip(TimerSkipCommand {
        request: Some(SkipTimerRequest {
            flow_id: flow_id.to_owned(),
            step_execution_id: "sys:timeout_handler-1".to_owned(),
            timer_condition_index: Some(0),
            ..SkipTimerRequest::default()
        }),
    })
}

/// RPC commits, timer skips, and time travel, whose handlers land after the backbone.
pub(crate) fn later_handler_command(
    rng: &mut ChaCha8Rng,
    flow_id: &str,
    run_id: RunId,
    run: &RunView,
) -> Command {
    let any_step = run
        .step_executions
        .keys()
        .next()
        .cloned()
        .unwrap_or_default();
    match rng.random_range(0..3) {
        0 => {
            let request = InvokeRpcRequest {
                flow_id: flow_id.to_owned(),
                run_id: run_id.to_string(),
                rpc_name: "getStatus".to_owned(),
                input: Some(random_value(rng)),
                request_id: request_id(rng, "rpc"),
                ..InvokeRpcRequest::default()
            };
            Command::RpcCommitted(RpcCommittedCommand {
                request_fingerprint: fingerprint(&request),
                request: Some(request),
                response: Some(InvokeWorkerRpcResponse::default()),
                app_worker_id: "worker-a".to_owned(),
                ..RpcCommittedCommand::default()
            })
        }
        1 => Command::TimerSkip(TimerSkipCommand {
            request: Some(SkipTimerRequest {
                flow_id: flow_id.to_owned(),
                step_execution_id: any_step,
                timer_condition_index: Some(0),
                ..SkipTimerRequest::default()
            }),
        }),
        _ => Command::TimeTravel(TimeTravelCommand {
            request: Some(ResetFlowRequest {
                flow_id: flow_id.to_owned(),
                reset_type: FlowResetType::Beginning as i32,
                reason: "simulated reset".to_owned(),
                skip_writes_reapply: rng.random_bool(0.5),
                ..ResetFlowRequest::default()
            }),
        }),
    }
}

pub(crate) fn time_travel_to_window_point(
    flow_id: &str,
    timestamp_micros: i64,
    run_attempt: i32,
) -> Command {
    Command::TimeTravel(TimeTravelCommand {
        request: Some(ResetFlowRequest {
            flow_id: flow_id.to_owned(),
            reset_type: FlowResetType::HistoryEventTime as i32,
            history_event_time: (i128::from(timestamp_micros) * 1000).to_string(),
            run_attempt,
            reason: "inspect an earlier value".to_owned(),
            skip_writes_reapply: true,
            ..ResetFlowRequest::default()
        }),
    })
}
