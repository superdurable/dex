// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use dexzd_protocol::dex::{
    AttributeMatch, AttributeMatchOperator, CloneForkFlowRequest, DeleteChannelMessageRequest,
    FlowCloneForkPointType, FlowConfig, FlowResetStepMethod, FlowResetType, IdReusePolicy,
    InvokeRpcRequest, PublishToChannelRequest, ResetFlowRequest, SetAttributesRequest,
    SkipTimerRequest, StartFlowRequest, StopFlowRequest, StopType, SubFlowResetPolicy,
    UpdateFlowConfigRequest, WaitForAttributeRequest, WaitForStepCompletionRequest, value,
};

use crate::shared::{
    ValidationCategory, ValidationConfig, ValidationError, apply_flow_config_override, invalid,
    validate_action_permissions, validate_attribute_writes, validate_blob_value,
    validate_flow_config, validate_flow_timeout_policy, validate_identifier,
    validate_start_options, validate_state_selections, validate_step_options,
    validate_timeout_handler,
};

/// A supported public client request before the API proposes its command.
pub enum ClientRequest<'a> {
    /// Start a Flow.
    StartFlow(&'a StartFlowRequest),
    /// Write Attributes to a Flow.
    SetAttributes(&'a SetAttributesRequest),
    /// Publish Channel messages.
    PublishToChannel(&'a PublishToChannelRequest),
    /// Delete a Channel message.
    DeleteChannelMessage(&'a DeleteChannelMessageRequest),
    /// Invoke a Flow RPC.
    InvokeRpc(&'a InvokeRpcRequest),
    /// Change Flow configuration.
    UpdateFlowConfig(&'a UpdateFlowConfigRequest),
    /// Stop a Flow.
    StopFlow(&'a StopFlowRequest),
    /// Skip a pending timer.
    SkipTimer(&'a SkipTimerRequest),
    /// Reset a Flow.
    ResetFlow(&'a ResetFlowRequest),
    /// Create a Flow from a copy of another Flow run's state.
    CloneForkFlow(&'a CloneForkFlowRequest),
    /// Wait for an Attribute match.
    WaitForAttribute(&'a WaitForAttributeRequest),
    /// Wait for a Step execution to finish.
    WaitForStepCompletion(&'a WaitForStepCompletionRequest),
}

/// Validates a public request and returns an INVALID_ARGUMENT category on failure.
pub fn validate_client_request(
    request: ClientRequest<'_>,
    config: &ValidationConfig,
) -> Result<(), ValidationError> {
    let result = match request {
        ClientRequest::StartFlow(request) => validate_start_flow(request, config).map(|_| ()),
        ClientRequest::SetAttributes(request) => validate_set_attributes(request, config),
        ClientRequest::PublishToChannel(request) => validate_publish_to_channel(request),
        ClientRequest::DeleteChannelMessage(request) => validate_delete_channel_message(request),
        ClientRequest::InvokeRpc(request) => validate_invoke_rpc(request),
        ClientRequest::UpdateFlowConfig(request) => validate_update_flow_config(request, config),
        ClientRequest::StopFlow(request) => validate_stop_flow(request),
        ClientRequest::SkipTimer(request) => validate_skip_timer(request),
        ClientRequest::ResetFlow(request) => validate_reset_flow(request),
        ClientRequest::CloneForkFlow(request) => validate_clone_fork_flow(request, config),
        ClientRequest::WaitForAttribute(request) => validate_wait_for_attribute(request),
        ClientRequest::WaitForStepCompletion(request) => validate_wait_for_step_completion(request),
    };
    result.map_err(as_invalid_argument)
}

/// Validates StartFlow and returns the effective Flow configuration.
pub fn validate_start_flow_request(
    request: &StartFlowRequest,
    config: &ValidationConfig,
) -> Result<FlowConfig, ValidationError> {
    validate_start_flow(request, config).map_err(as_invalid_argument)
}

fn as_invalid_argument(mut error: ValidationError) -> ValidationError {
    error.category = ValidationCategory::InvalidArgument;
    error
}

fn validate_start_flow(
    request: &StartFlowRequest,
    config: &ValidationConfig,
) -> Result<FlowConfig, ValidationError> {
    if request.flow_id.is_empty() || request.flow_type.is_empty() {
        return Err(invalid("flow_id", "flow ID and flow type are required"));
    }
    if request.request_id.is_empty() {
        return Err(invalid("request_id", "request ID is required"));
    }
    if request.flow_id.len() > config.maximum_flow_id_bytes {
        return Err(invalid(
            "flow_id",
            format!(
                "flow ID exceeds maximum length of {} bytes",
                config.maximum_flow_id_bytes
            ),
        ));
    }
    let policy = validate_flow_timeout_policy(
        "flow_timeout_policy",
        request.flow_timeout_seconds,
        request.flow_timeout_policy,
    )?;
    validate_blob_value("step_input", request.step_input.as_ref())?;
    let options = request.flow_start_options.as_ref();
    validate_timeout_handler(
        "flow_start_options.timeout_handler_options",
        request.flow_timeout_seconds,
        policy,
        options.and_then(|options| options.timeout_handler_options.as_ref()),
        config,
    )?;
    if let Some(options) = options {
        validate_attribute_writes(
            "flow_start_options.attributes",
            &options.attributes,
            config,
            false,
        )?;
    }
    validate_identifier("flow_id", "flow ID", &request.flow_id)?;
    validate_identifier("start_step_type", "step type", &request.start_step_type)?;
    validate_step_options("step_options", request.step_options.as_ref(), config)?;

    let mut effective = config.default_flow_config.clone();
    if let Some(override_config) = options.and_then(|options| options.flow_config_override.as_ref())
    {
        apply_flow_config_override(&mut effective, override_config);
    }
    validate_flow_config(
        "flow_start_options.flow_config_override",
        &effective,
        config,
        true,
    )?;
    if let Some(options) = options {
        if IdReusePolicy::try_from(options.id_reuse_policy).is_err() {
            return Err(invalid(
                "flow_start_options.id_reuse_policy",
                "unknown ID reuse policy",
            ));
        }
        validate_start_options(
            "flow_start_options",
            options.flow_start_delay_seconds,
            options.retry_policy.is_some(),
        )?;
    }
    Ok(effective)
}

fn validate_set_attributes(
    request: &SetAttributesRequest,
    config: &ValidationConfig,
) -> Result<(), ValidationError> {
    if request.flow_id.is_empty() {
        return Err(invalid("flow_id", "flow ID is required"));
    }
    if request.request_id.is_empty() {
        return Err(invalid("request_id", "request ID is required"));
    }
    validate_attribute_writes("attributes", &request.attributes, config, true)?;
    validate_action_permissions(
        "action_permission_mappings",
        request.action_permission_mappings.as_ref(),
    )
}

fn validate_publish_to_channel(request: &PublishToChannelRequest) -> Result<(), ValidationError> {
    if request.flow_id.is_empty() {
        return Err(invalid("flow_id", "flow ID is required"));
    }
    for (index, message) in request.messages.iter().enumerate() {
        let path = format!("messages[{index}]");
        if message.channel_name.is_empty() {
            return Err(invalid(path, "channel name is required"));
        }
        validate_blob_value(&format!("{path}.value"), message.value.as_ref())?;
    }
    Ok(())
}

fn validate_delete_channel_message(
    request: &DeleteChannelMessageRequest,
) -> Result<(), ValidationError> {
    if request.flow_id.is_empty()
        || request.channel_name.is_empty()
        || request.message_id.is_empty()
    {
        return Err(invalid(
            "flow_id",
            "flow ID, channel name, and message ID are required",
        ));
    }
    if request.request_id.is_empty() {
        return Err(invalid("request_id", "request ID is required"));
    }
    Ok(())
}

fn validate_invoke_rpc(request: &InvokeRpcRequest) -> Result<(), ValidationError> {
    if request.flow_id.is_empty() || request.rpc_name.is_empty() {
        return Err(invalid("flow_id", "flow ID and RPC name are required"));
    }
    if request.request_id.is_empty() {
        return Err(invalid("request_id", "request ID is required"));
    }
    if request.timeout_seconds < 0 {
        return Err(invalid(
            "timeout_seconds",
            "RPC timeout must be non-negative",
        ));
    }
    // Matches the Go Server's normalizeLockKeys: only an empty key is rejected.
    if request.lock_attribute_keys.iter().any(String::is_empty) {
        return Err(invalid(
            "lock_attribute_keys",
            "lock attribute key is empty",
        ));
    }
    validate_state_selections(
        "load",
        &request.load_attribute_map_instances,
        &request.load_channel_names,
        &request.load_channel_map_instances,
        "",
    )?;
    validate_blob_value("input", request.input.as_ref())
}

fn validate_update_flow_config(
    request: &UpdateFlowConfigRequest,
    config: &ValidationConfig,
) -> Result<(), ValidationError> {
    if request.flow_id.is_empty() || request.flow_config.is_none() {
        return Err(invalid("flow_id", "flow ID and flow config are required"));
    }
    let flow_config = request
        .flow_config
        .as_ref()
        .ok_or_else(|| invalid("flow_config", "flow ID and flow config are required"))?;
    if flow_config.offload_history.is_some() {
        return Err(invalid(
            "flow_config.offload_history",
            "offload_history can be set only when the Flow starts",
        ));
    }
    validate_flow_config("flow_config", flow_config, config, false)
}

fn validate_stop_flow(request: &StopFlowRequest) -> Result<(), ValidationError> {
    if request.flow_id.is_empty() {
        return Err(invalid("flow_id", "flow ID is required"));
    }
    let Ok(stop_type) = StopType::try_from(request.stop_type) else {
        return Err(invalid("stop_type", "stop type is required"));
    };
    if request.output.is_some()
        && !matches!(
            stop_type,
            StopType::GracefulComplete | StopType::ForceComplete
        )
    {
        return Err(invalid(
            "output",
            "only the graceful complete and force complete stop types accept an output",
        ));
    }
    validate_blob_value("output", request.output.as_ref())
}

fn validate_skip_timer(request: &SkipTimerRequest) -> Result<(), ValidationError> {
    if request.flow_id.is_empty() || request.step_execution_id.is_empty() {
        return Err(invalid(
            "flow_id",
            "flow ID and step execution ID are required",
        ));
    }
    if request.timer_condition_id.is_empty() && request.timer_condition_index.is_none() {
        return Err(invalid(
            "timer_condition_id",
            "timer condition ID or index is required",
        ));
    }
    Ok(())
}

fn validate_reset_flow(request: &ResetFlowRequest) -> Result<(), ValidationError> {
    if request.flow_id.is_empty() {
        return Err(invalid("flow_id", "flow ID is required"));
    }
    if SubFlowResetPolicy::try_from(request.sub_flow_reset_policy).is_err() {
        return Err(invalid(
            "sub_flow_reset_policy",
            "SubFlow reset policy is unknown",
        ));
    }
    match FlowResetType::try_from(request.reset_type) {
        Ok(FlowResetType::Beginning) => {
            if request.step_method != FlowResetStepMethod::Unspecified as i32 {
                return Err(invalid(
                    "step_method",
                    "step method is only valid for a Step execution ID",
                ));
            }
        }
        Ok(FlowResetType::HistoryEventTime) => {
            if request.history_event_time.is_empty() {
                return Err(invalid(
                    "history_event_time",
                    "history event time is required",
                ));
            }
            if request.step_method != FlowResetStepMethod::Unspecified as i32 {
                return Err(invalid(
                    "step_method",
                    "step method is only valid for a Step execution ID",
                ));
            }
        }
        Ok(FlowResetType::StepType) => {
            if request.step_type.is_empty() {
                return Err(invalid("step_type", "step type is required"));
            }
            if request.step_method != FlowResetStepMethod::Unspecified as i32 {
                return Err(invalid(
                    "step_method",
                    "step method is only valid for a Step execution ID",
                ));
            }
        }
        Ok(FlowResetType::StepExecutionId) => {
            if request.step_execution_id.is_empty() {
                return Err(invalid(
                    "step_execution_id",
                    "step execution ID is required",
                ));
            }
            if !matches!(
                FlowResetStepMethod::try_from(request.step_method),
                Ok(FlowResetStepMethod::WaitFor | FlowResetStepMethod::Execute)
            ) {
                return Err(invalid(
                    "step_method",
                    "step method must be WaitFor or Execute",
                ));
            }
        }
        Ok(FlowResetType::EventId) => {
            if request.event_id <= 0 {
                return Err(invalid("event_id", "event ID must be positive"));
            }
            if request.step_method != FlowResetStepMethod::Unspecified as i32 {
                return Err(invalid(
                    "step_method",
                    "step method is only valid for a Step execution ID",
                ));
            }
        }
        _ => return Err(invalid("reset_type", "time travel type is required")),
    }
    Ok(())
}

fn validate_clone_fork_flow(
    request: &CloneForkFlowRequest,
    config: &ValidationConfig,
) -> Result<(), ValidationError> {
    if request.source_flow_id.is_empty() {
        return Err(invalid("source_flow_id", "source flow ID is required"));
    }
    if request.flow_id.is_empty() {
        return Err(invalid("flow_id", "flow ID is required"));
    }
    if request.flow_id == request.source_flow_id {
        return Err(invalid(
            "flow_id",
            "flow ID must differ from the source flow ID; use ResetFlow to return a Flow to an \
             earlier point",
        ));
    }
    if request.request_id.is_empty() {
        return Err(invalid("request_id", "request ID is required"));
    }
    if request.flow_id.len() > config.maximum_flow_id_bytes {
        return Err(invalid(
            "flow_id",
            format!(
                "flow ID exceeds maximum length of {} bytes",
                config.maximum_flow_id_bytes
            ),
        ));
    }
    validate_identifier("flow_id", "flow ID", &request.flow_id)?;
    if IdReusePolicy::try_from(request.id_reuse_policy).is_err() {
        return Err(invalid("id_reuse_policy", "unknown ID reuse policy"));
    }
    if SubFlowResetPolicy::try_from(request.sub_flow_reset_policy).is_err() {
        return Err(invalid(
            "sub_flow_reset_policy",
            "SubFlow reset policy is unknown",
        ));
    }
    if request.flow_timeout_seconds < 0 {
        return Err(invalid(
            "flow_timeout_seconds",
            "flow timeout must not be negative",
        ));
    }
    if request.run_attempt < 0 {
        return Err(invalid("run_attempt", "run attempt must not be negative"));
    }
    validate_attribute_writes("attributes", &request.attributes, config, false)?;
    if let Some(override_config) = &request.flow_config_override {
        validate_flow_config("flow_config_override", override_config, config, false)?;
    }
    validate_clone_fork_point(request)
}

/// Checks that the point fields fit the point type, as ResetFlow checks its reset type.
fn validate_clone_fork_point(request: &CloneForkFlowRequest) -> Result<(), ValidationError> {
    let reset_type = match FlowCloneForkPointType::try_from(request.point_type) {
        Ok(FlowCloneForkPointType::Unspecified | FlowCloneForkPointType::Current) => {
            if request.run_attempt != 0 {
                return Err(invalid(
                    "run_attempt",
                    "the CURRENT point selects the current run attempt, so run attempt must be \
                     zero",
                ));
            }
            if request.step_method != FlowResetStepMethod::Unspecified as i32 {
                return Err(invalid(
                    "step_method",
                    "step method is only valid for a Step execution ID",
                ));
            }
            return Ok(());
        }
        Ok(FlowCloneForkPointType::Beginning) => FlowResetType::Beginning,
        Ok(FlowCloneForkPointType::HistoryEventTime) => FlowResetType::HistoryEventTime,
        Ok(FlowCloneForkPointType::StepType) => FlowResetType::StepType,
        Ok(FlowCloneForkPointType::StepExecutionId) => FlowResetType::StepExecutionId,
        Ok(FlowCloneForkPointType::EventId) => FlowResetType::EventId,
        Err(_) => return Err(invalid("point_type", "unknown point type")),
    };
    validate_reset_flow(&ResetFlowRequest {
        flow_id: request.source_flow_id.clone(),
        reset_type: reset_type as i32,
        history_event_time: request.history_event_time.clone(),
        step_type: request.step_type.clone(),
        step_execution_id: request.step_execution_id.clone(),
        step_method: request.step_method,
        event_id: request.event_id,
        ..ResetFlowRequest::default()
    })
}

/// Returns the Flow configuration of a CloneForkFlow's new run: the source run's
/// configuration with `override_config` applied, validated as a StartFlow configuration.
///
/// Returns INVALID_ARGUMENT for an invalid result.
pub fn validate_clone_fork_flow_config(
    source_config: &FlowConfig,
    override_config: Option<&FlowConfig>,
    config: &ValidationConfig,
) -> Result<FlowConfig, ValidationError> {
    let mut effective = source_config.clone();
    if let Some(override_config) = override_config {
        apply_flow_config_override(&mut effective, override_config);
    }
    validate_flow_config("flow_config_override", &effective, config, true)
        .map_err(as_invalid_argument)?;
    Ok(effective)
}

fn validate_wait_request(flow_id: &str, request_timeout: i32) -> Result<(), ValidationError> {
    if flow_id.is_empty() || request_timeout < 0 {
        return Err(invalid(
            "flow_id",
            "valid flow ID and non-negative request timeout are required",
        ));
    }
    Ok(())
}

fn validate_wait_for_attribute(request: &WaitForAttributeRequest) -> Result<(), ValidationError> {
    validate_wait_request(&request.flow_id, request.request_timeout_seconds)?;
    let Some(attribute_match) = request.r#match.as_ref() else {
        return Err(invalid("match", "attribute match is required"));
    };
    if attribute_match.operand.is_none() {
        return Err(invalid("match", "attribute match is required"));
    }
    validate_blob_value("match.operand", attribute_match.operand.as_ref())?;
    validate_attribute_match(attribute_match)
}

fn validate_attribute_match(attribute_match: &AttributeMatch) -> Result<(), ValidationError> {
    if attribute_match.key.is_empty()
        || attribute_match
            .operand
            .as_ref()
            .is_none_or(|operand| operand.kind.is_none())
    {
        return Err(invalid(
            "match",
            "attribute match key, operator, and scalar operand are required",
        ));
    }
    if !matches!(
        AttributeMatchOperator::try_from(attribute_match.operator),
        Ok(operator) if operator != AttributeMatchOperator::Unspecified
    ) {
        return Err(invalid(
            "match.operator",
            "attribute match operator is invalid",
        ));
    }
    let operand = attribute_match.operand.as_ref().ok_or_else(|| {
        invalid(
            "match.operand",
            "attribute match key, operator, and scalar operand are required",
        )
    })?;
    match operand.kind.as_ref() {
        Some(value::Kind::StringValue(_) | value::Kind::BoolValue(_)) => {
            if matches!(
                AttributeMatchOperator::try_from(attribute_match.operator),
                Ok(AttributeMatchOperator::GreaterThan
                    | AttributeMatchOperator::GreaterThanOrEqual
                    | AttributeMatchOperator::LessThan
                    | AttributeMatchOperator::LessThanOrEqual)
            ) {
                return Err(invalid(
                    "match.operand",
                    "attribute match ordering requires an integer or double operand",
                ));
            }
        }
        Some(value::Kind::IntValue(_)) => {}
        Some(value::Kind::DoubleValue(number)) => {
            if !number.is_finite() {
                return Err(invalid(
                    "match.operand",
                    "attribute match double operand must be finite",
                ));
            }
        }
        _ => {
            return Err(invalid(
                "match.operand",
                "attribute match supports only string, boolean, integer, or double operands",
            ));
        }
    }
    Ok(())
}

fn validate_wait_for_step_completion(
    request: &WaitForStepCompletionRequest,
) -> Result<(), ValidationError> {
    validate_wait_request(&request.flow_id, request.request_timeout_seconds)?;
    if request.step_type.is_empty() || request.step_execution_number.is_empty() {
        return Err(invalid(
            "step_type",
            "step type and step execution number are required",
        ));
    }
    if request
        .step_execution_number
        .parse::<i32>()
        .ok()
        .is_none_or(|number| number <= 0)
    {
        return Err(invalid(
            "step_execution_number",
            "step execution number must be a positive integer",
        ));
    }
    Ok(())
}
