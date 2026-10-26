// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

use async_trait::async_trait;
use dexzd_engine::{FlowCoordinationError, FlowCoordinatorRegistry, grpc_code};
use dexzd_keyspace::TenantId;
use dexzd_protocol::dex::{
    DeleteChannelMessageRequest, ErrorSubStatus, FlowResult, GetAttributesRequest,
    GetAttributesResponse, GetChannelMessagesRequest, GetChannelMessagesResponse,
    GetFlowStateRequest, GetFlowStateResponse, GetFlowSummaryRequest, GetFlowSummaryResponse,
    GetHistoryEventsRequest, GetHistoryEventsResponse, InvokeRpcRequest, InvokeRpcResponse,
    LoadBlobsRequest, LoadBlobsResponse, PublishToChannelRequest, ResetFlowRequest,
    ResetFlowResponse, SetAttributesRequest, SkipTimerRequest, StartFlowRequest, StartFlowResponse,
    StopFlowRequest, UpdateFlowConfigRequest, WaitForAttributeRequest, WaitForAttributeResponse,
    WaitForFlowRequest, WaitForHistoryEventRequest, WaitForHistoryEventResponse,
    WaitForStepCompletionRequest, WaitForStepCompletionResponse,
};
use tonic::Code;

use crate::{FlowOperationError, FlowOperations};

#[async_trait]
impl FlowOperations for FlowCoordinatorRegistry {
    async fn start_flow(
        &self,
        tenant_id: TenantId,
        request: StartFlowRequest,
    ) -> Result<StartFlowResponse, FlowOperationError> {
        let flow_id = request.flow_id.clone();
        FlowCoordinatorRegistry::start_flow(self, tenant_id, request)
            .await
            .map_err(|error| flow_operation_error(&flow_id, error))
    }

    async fn stop_flow(
        &self,
        tenant_id: TenantId,
        request: StopFlowRequest,
    ) -> Result<(), FlowOperationError> {
        let flow_id = request.flow_id.clone();
        FlowCoordinatorRegistry::stop_flow(self, tenant_id, request)
            .await
            .map_err(|error| flow_operation_error(&flow_id, error))
    }

    async fn set_attributes(
        &self,
        tenant_id: TenantId,
        request: SetAttributesRequest,
    ) -> Result<(), FlowOperationError> {
        let flow_id = request.flow_id.clone();
        FlowCoordinatorRegistry::set_attributes(self, tenant_id, request)
            .await
            .map_err(|error| flow_operation_error(&flow_id, error))
    }

    async fn get_attributes(
        &self,
        tenant_id: TenantId,
        request: GetAttributesRequest,
    ) -> Result<GetAttributesResponse, FlowOperationError> {
        let flow_id = request.flow_id.clone();
        FlowCoordinatorRegistry::get_attributes(self, tenant_id, request)
            .await
            .map_err(|error| flow_operation_error(&flow_id, error))
    }

    async fn publish_to_channel(
        &self,
        tenant_id: TenantId,
        request: PublishToChannelRequest,
    ) -> Result<(), FlowOperationError> {
        let flow_id = request.flow_id.clone();
        FlowCoordinatorRegistry::publish_to_channel(self, tenant_id, request)
            .await
            .map_err(|error| flow_operation_error(&flow_id, error))
    }

    async fn get_channel_messages(
        &self,
        tenant_id: TenantId,
        request: GetChannelMessagesRequest,
    ) -> Result<GetChannelMessagesResponse, FlowOperationError> {
        let flow_id = request.flow_id.clone();
        FlowCoordinatorRegistry::get_channel_messages(self, tenant_id, request)
            .await
            .map_err(|error| flow_operation_error(&flow_id, error))
    }

    async fn delete_channel_message(
        &self,
        tenant_id: TenantId,
        request: DeleteChannelMessageRequest,
    ) -> Result<(), FlowOperationError> {
        let flow_id = request.flow_id.clone();
        FlowCoordinatorRegistry::delete_channel_message(self, tenant_id, request)
            .await
            .map_err(|error| flow_operation_error(&flow_id, error))
    }

    async fn load_blobs(
        &self,
        tenant_id: TenantId,
        request: LoadBlobsRequest,
    ) -> Result<LoadBlobsResponse, FlowOperationError> {
        let entry_flow_ids: Vec<String> = request
            .entries
            .iter()
            .map(|entry| entry.flow_id.clone())
            .collect();
        FlowCoordinatorRegistry::load_blobs(self, tenant_id, request)
            .await
            .map_err(|error| {
                // Entries span Flows; the NotFound error names the entry whose run is missing.
                let missing_flow_id = entry_flow_ids
                    .iter()
                    .find(|flow_id| error == FlowCoordinationError::flow_not_found(flow_id))
                    .map_or("", String::as_str);
                flow_operation_error(missing_flow_id, error)
            })
    }

    async fn wait_for_flow(
        &self,
        tenant_id: TenantId,
        request: WaitForFlowRequest,
    ) -> Result<FlowResult, FlowOperationError> {
        let flow_id = request.flow_id.clone();
        FlowCoordinatorRegistry::wait_for_flow(self, tenant_id, request)
            .await
            .map_err(|error| flow_operation_error(&flow_id, error))
    }

    async fn get_flow_summary(
        &self,
        tenant_id: TenantId,
        request: GetFlowSummaryRequest,
    ) -> Result<GetFlowSummaryResponse, FlowOperationError> {
        let flow_id = request.flow_id.clone();
        FlowCoordinatorRegistry::get_flow_summary(self, tenant_id, request)
            .await
            .map_err(|error| flow_operation_error(&flow_id, error))
    }

    async fn get_history_events(
        &self,
        tenant_id: TenantId,
        request: GetHistoryEventsRequest,
    ) -> Result<GetHistoryEventsResponse, FlowOperationError> {
        let flow_id = request.flow_id.clone();
        FlowCoordinatorRegistry::get_history_events(self, tenant_id, request)
            .await
            .map_err(|error| flow_operation_error(&flow_id, error))
    }

    async fn wait_for_history_event(
        &self,
        tenant_id: TenantId,
        request: WaitForHistoryEventRequest,
    ) -> Result<WaitForHistoryEventResponse, FlowOperationError> {
        let flow_id = request.flow_id.clone();
        FlowCoordinatorRegistry::wait_for_history_event(self, tenant_id, request)
            .await
            .map_err(|error| flow_operation_error(&flow_id, error))
    }

    async fn get_flow_state(
        &self,
        tenant_id: TenantId,
        request: GetFlowStateRequest,
    ) -> Result<GetFlowStateResponse, FlowOperationError> {
        let flow_id = request.flow_id.clone();
        FlowCoordinatorRegistry::get_flow_state(self, tenant_id, request)
            .await
            .map_err(|error| flow_operation_error(&flow_id, error))
    }

    async fn reset_flow(
        &self,
        tenant_id: TenantId,
        request: ResetFlowRequest,
    ) -> Result<ResetFlowResponse, FlowOperationError> {
        let flow_id = request.flow_id.clone();
        FlowCoordinatorRegistry::reset_flow(self, tenant_id, request)
            .await
            .map_err(|error| flow_operation_error(&flow_id, error))
    }

    async fn invoke_rpc(
        &self,
        tenant_id: TenantId,
        request: InvokeRpcRequest,
    ) -> Result<InvokeRpcResponse, FlowOperationError> {
        let flow_id = request.flow_id.clone();
        FlowCoordinatorRegistry::invoke_rpc(self, tenant_id, request)
            .await
            .map_err(|error| flow_operation_error(&flow_id, error))
    }

    async fn skip_timer(
        &self,
        tenant_id: TenantId,
        request: SkipTimerRequest,
    ) -> Result<(), FlowOperationError> {
        let flow_id = request.flow_id.clone();
        FlowCoordinatorRegistry::skip_timer(self, tenant_id, request)
            .await
            .map_err(|error| flow_operation_error(&flow_id, error))
    }

    async fn update_flow_config(
        &self,
        tenant_id: TenantId,
        request: UpdateFlowConfigRequest,
    ) -> Result<(), FlowOperationError> {
        let flow_id = request.flow_id.clone();
        FlowCoordinatorRegistry::update_flow_config(self, tenant_id, request)
            .await
            .map_err(|error| flow_operation_error(&flow_id, error))
    }

    async fn wait_for_step_completion(
        &self,
        tenant_id: TenantId,
        request: WaitForStepCompletionRequest,
    ) -> Result<WaitForStepCompletionResponse, FlowOperationError> {
        let flow_id = request.flow_id.clone();
        FlowCoordinatorRegistry::wait_for_step_completion(self, tenant_id, request)
            .await
            .map_err(|error| flow_operation_error(&flow_id, error))
    }

    async fn wait_for_attribute(
        &self,
        tenant_id: TenantId,
        request: WaitForAttributeRequest,
    ) -> Result<WaitForAttributeResponse, FlowOperationError> {
        let flow_id = request.flow_id.clone();
        FlowCoordinatorRegistry::wait_for_attribute(self, tenant_id, request)
            .await
            .map_err(|error| flow_operation_error(&flow_id, error))
    }
}

/// Maps a runtime error; a missing Flow uses the Go Server's NotFound text with `flow_id`.
fn flow_operation_error(flow_id: &str, error: FlowCoordinationError) -> FlowOperationError {
    match error {
        FlowCoordinationError::Rejected {
            grpc_code: grpc_code::NOT_FOUND,
            sub_status: ErrorSubStatus::FlowNotExists,
            ..
        } if !flow_id.is_empty() && !error.is_closed_run_stop_rejection() => {
            FlowOperationError::FlowNotFound {
                flow_id: flow_id.to_owned(),
            }
        }
        FlowCoordinationError::Rejected {
            grpc_code,
            sub_status,
            message,
        } => FlowOperationError::Rejected {
            code: Code::from_i32(grpc_code),
            sub_status,
            message,
        },
        FlowCoordinationError::WorkerRpcFailed { worker_error } => {
            FlowOperationError::WorkerApiFailed { worker_error }
        }
        FlowCoordinationError::Unavailable { detail }
        | FlowCoordinationError::NodeStopping { detail } => {
            FlowOperationError::Unavailable { detail }
        }
        FlowCoordinationError::Internal { detail } => FlowOperationError::Internal { detail },
    }
}
