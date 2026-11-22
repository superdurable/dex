// Copyright (c) 2022-2026 Super Durable, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//! HTTP routes that start, decide, and describe a customer refund on the shared example server.

use axum::{
    Json, Router,
    extract::{Query, State},
    http::StatusCode,
    response::{IntoResponse, Response},
    routing::get,
};
use std::time::Duration;

use dex_sdk::{
    Flow, FlowStatus, SdkError, SdkResult, StepExecutionId, WaitForStepCompletionOptions,
};
use serde::Deserialize;
use serde_json::json;

use super::refund_flow::{
    APPROVE_REFUND, CASE_STATUS, CustomerRefundFlow, DecisionReason, REFUND_ID, REJECT_REFUND,
};
use super::refund_scenarios::RefundScenario;
use crate::server::helpers::{
    ErrorBody, SharedClient, StartResponse, map_sdk_error, new_flow_id, ok_json, run_blocking,
};

const START_READY_TIMEOUT: Duration = Duration::from_secs(10);

#[derive(Clone)]
struct CustomerRefundState {
    client: SharedClient,
    flow: CustomerRefundFlow,
}

#[derive(Deserialize)]
struct RefundQuery {
    #[serde(default, rename = "flowId")]
    flow_id: String,
    /// An alias of flowId.
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
    #[serde(default)]
    scenario: String,
    #[serde(default)]
    decision: String,
    #[serde(default)]
    reason: String,
}

impl RefundQuery {
    fn flow_id(&self) -> String {
        if self.workflow_id.is_empty() {
            self.flow_id.clone()
        } else {
            self.workflow_id.clone()
        }
    }
}

pub fn mount(client: SharedClient, flow: CustomerRefundFlow) -> Router {
    Router::new()
        .route(
            "/products/customer-refund/manager-approval/start",
            get(start_refund),
        )
        .route(
            "/products/customer-refund/manager-approval/decide",
            get(decide_refund),
        )
        .route(
            "/products/customer-refund/manager-approval/describe",
            get(describe_refund),
        )
        .with_state(CustomerRefundState { client, flow })
}

async fn start_refund(
    State(state): State<CustomerRefundState>,
    Query(query): Query<RefundQuery>,
) -> Response {
    let scenario = if query.scenario.is_empty() {
        RefundScenario::Normal
    } else {
        match query.scenario.parse::<RefundScenario>() {
            Ok(scenario) => scenario,
            Err(message) => return error_response(StatusCode::BAD_REQUEST, message),
        }
    };
    let flow_id = match query.flow_id() {
        flow_id if flow_id.is_empty() => new_flow_id("customer-refund"),
        flow_id => flow_id,
    };
    let CustomerRefundState { client, flow } = state;
    match run_blocking(move || {
        let run_id = client.start_flow(&flow, &flow_id, scenario.refund_request())?;
        match client.wait_for_step_completion(
            &flow_id,
            StepExecutionId::of(flow.check_policy()),
            WaitForStepCompletionOptions::new().request_timeout(START_READY_TIMEOUT),
        ) {
            // A small or denied refund can close before the wait begins; it passed CheckPolicy.
            Ok(())
            | Err(SdkError::FlowNotActiveOrNotFound { .. } | SdkError::FlowClosing { .. }) => {}
            Err(error) => return Err(error),
        }
        Ok(StartResponse { flow_id, run_id })
    }) {
        Ok(value) => ok_json(value),
        Err(error @ SdkError::FlowAlreadyStarted { .. }) => {
            error_response(StatusCode::CONFLICT, error.to_string())
        }
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn decide_refund(
    State(state): State<CustomerRefundState>,
    Query(query): Query<RefundQuery>,
) -> Response {
    let flow_id = query.flow_id();
    if flow_id.is_empty() {
        return error_response(StatusCode::BAD_REQUEST, "flowId is required".to_string());
    }
    let rpc = match query.decision.as_str() {
        "approve" => APPROVE_REFUND,
        "reject" => REJECT_REFUND,
        other => {
            return error_response(
                StatusCode::BAD_REQUEST,
                format!("decision must be approve or reject, not {other:?}"),
            );
        }
    };
    if query.reason.trim().is_empty() {
        return error_response(
            StatusCode::BAD_REQUEST,
            "reason must not be blank".to_string(),
        );
    }
    let RefundQuery {
        decision, reason, ..
    } = query;
    let missing_refund = format!("no customer refund {flow_id}");
    match run_blocking(move || {
        if !is_customer_refund_run(&state, &flow_id)? {
            return Ok(None);
        }
        state
            .client
            .invoke_rpc(&flow_id, rpc, DecisionReason { reason })?;
        Ok(Some(json!({ "flowID": flow_id, "decision": decision })))
    }) {
        Ok(Some(value)) => ok_json(value),
        Ok(None) => error_response(StatusCode::NOT_FOUND, missing_refund),
        Err(error) => map_decide_error(error),
    }
}

/// Returns whether the latest run of `flow_id` is a CustomerRefundFlow run.
fn is_customer_refund_run(state: &CustomerRefundState, flow_id: &str) -> SdkResult<bool> {
    let run = state.client.describe_flow(flow_id)?;
    Ok(run.flow_type == state.flow.flow_type())
}

async fn describe_refund(
    State(state): State<CustomerRefundState>,
    Query(query): Query<RefundQuery>,
) -> Response {
    let flow_id = query.flow_id();
    if flow_id.is_empty() {
        return error_response(StatusCode::BAD_REQUEST, "flowId is required".to_string());
    }
    let missing_refund = format!("no customer refund {flow_id}");
    match run_blocking(move || {
        let run = state.client.describe_flow(&flow_id)?;
        if run.flow_type != state.flow.flow_type() {
            return Ok(None);
        }
        let (case_status, refund_id) = state
            .client
            .get_attributes(&flow_id, (&*CASE_STATUS, &*REFUND_ID))?;
        Ok(Some(json!({
            "flowID": flow_id,
            "status": flow_status_name(run.status),
            "caseStatus": case_status,
            "refundId": refund_id,
        })))
    }) {
        Ok(Some(value)) => ok_json(value),
        Ok(None) => error_response(StatusCode::NOT_FOUND, missing_refund),
        Err(error @ SdkError::FlowNotFound { .. }) => {
            error_response(StatusCode::NOT_FOUND, error.to_string())
        }
        Err(error) => map_sdk_error(error).into_response(),
    }
}

/// Maps the RPC's business rejection and lifecycle races to 4xx; other failures stay 500.
fn map_decide_error(error: SdkError) -> Response {
    match &error {
        SdkError::WorkerInvocation { worker, .. }
            if worker.error_type() == "RefundNotAwaitingManager" =>
        {
            error_response(
                StatusCode::CONFLICT,
                format!("RefundNotAwaitingManager: {}", worker.detail()),
            )
        }
        SdkError::RpcLockConflict { .. } => {
            error_response(StatusCode::CONFLICT, format!("RpcLockConflict: {error}"))
        }
        SdkError::FlowClosing { .. } => error_response(
            StatusCode::CONFLICT,
            format!("the refund is closing: {error}"),
        ),
        SdkError::FlowNotFound { .. } | SdkError::FlowNotActiveOrNotFound { .. } => {
            error_response(StatusCode::NOT_FOUND, error.to_string())
        }
        _ => map_sdk_error(error).into_response(),
    }
}

fn flow_status_name(status: FlowStatus) -> &'static str {
    match status {
        FlowStatus::Running => "running",
        FlowStatus::Completed => "completed",
        FlowStatus::Failed => "failed",
        FlowStatus::ServerSideTimeoutInternalOnly => "timeout",
        FlowStatus::Terminated => "terminated",
        FlowStatus::Canceled => "canceled",
    }
}

fn error_response(status: StatusCode, error: String) -> Response {
    (status, Json(ErrorBody { error })).into_response()
}
