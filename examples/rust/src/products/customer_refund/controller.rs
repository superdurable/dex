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
use dex_sdk::{FlowStatus, SdkError};
use serde::{Deserialize, Serialize};

use super::refund_flow::{
    APPROVE_REFUND, CASE_STATUS, CaseStatus, CustomerRefundFlow, DecisionReason, REFUND_ID,
    REJECT_REFUND,
};
use super::refund_scenarios::RefundScenario;
use crate::server::helpers::{ErrorBody, SharedClient, map_sdk_error, new_flow_id, run_blocking};

#[derive(Clone)]
struct CustomerRefundState {
    client: SharedClient,
    flow: CustomerRefundFlow,
}

#[derive(Deserialize)]
struct StartQuery {
    #[serde(default, rename = "flowId")]
    flow_id: String,
    #[serde(default)]
    scenario: String,
}

#[derive(Deserialize)]
struct DecideQuery {
    #[serde(default, rename = "flowId")]
    flow_id: String,
    #[serde(default)]
    decision: String,
    #[serde(default)]
    reason: String,
}

#[derive(Deserialize)]
struct DescribeQuery {
    #[serde(default, rename = "flowId")]
    flow_id: String,
}

#[derive(Serialize)]
struct StartRefundResponse {
    #[serde(rename = "flowID")]
    flow_id: String,
    #[serde(rename = "runID")]
    run_id: String,
    scenario: String,
}

#[derive(Serialize)]
struct DecideRefundResponse {
    #[serde(rename = "flowID")]
    flow_id: String,
    decision: String,
}

#[derive(Serialize)]
struct DescribeRefundResponse {
    #[serde(rename = "flowID")]
    flow_id: String,
    status: &'static str,
    #[serde(rename = "case-status")]
    case_status: Option<CaseStatus>,
    #[serde(rename = "refund-id")]
    refund_id: Option<String>,
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
    Query(query): Query<StartQuery>,
) -> Response {
    let scenario = if query.scenario.is_empty() {
        RefundScenario::Normal
    } else {
        match query.scenario.parse::<RefundScenario>() {
            Ok(scenario) => scenario,
            Err(message) => return error_response(StatusCode::BAD_REQUEST, message),
        }
    };
    let flow_id = if query.flow_id.is_empty() {
        new_flow_id("customer-refund")
    } else {
        query.flow_id
    };
    let CustomerRefundState { client, flow } = state;
    match run_blocking(move || {
        client
            .start_flow(&flow, &flow_id, scenario.refund_request())
            .map(|run_id| StartRefundResponse {
                flow_id,
                run_id,
                scenario: scenario.to_string(),
            })
    }) {
        Ok(response) => Json(response).into_response(),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn decide_refund(
    State(state): State<CustomerRefundState>,
    Query(query): Query<DecideQuery>,
) -> Response {
    if query.flow_id.is_empty() {
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
    let DecideQuery {
        flow_id,
        decision,
        reason,
    } = query;
    let response_flow_id = flow_id.clone();
    match run_blocking(move || {
        state
            .client
            .invoke_rpc(&flow_id, rpc, DecisionReason { reason })
    }) {
        Ok(()) => Json(DecideRefundResponse {
            flow_id: response_flow_id,
            decision,
        })
        .into_response(),
        Err(error) => map_decide_error(error),
    }
}

async fn describe_refund(
    State(state): State<CustomerRefundState>,
    Query(query): Query<DescribeQuery>,
) -> Response {
    let flow_id = query.flow_id;
    if flow_id.is_empty() {
        return error_response(StatusCode::BAD_REQUEST, "flowId is required".to_string());
    }
    match run_blocking(move || {
        let run = state.client.describe_flow(&flow_id)?;
        let (case_status, refund_id) = state
            .client
            .get_attributes(&flow_id, (&*CASE_STATUS, &*REFUND_ID))?;
        Ok(DescribeRefundResponse {
            flow_id,
            status: flow_status_name(run.status),
            case_status,
            refund_id,
        })
    }) {
        Ok(response) => Json(response).into_response(),
        Err(error @ SdkError::FlowNotFound { .. }) => {
            error_response(StatusCode::NOT_FOUND, error.to_string())
        }
        Err(error) => map_sdk_error(error).into_response(),
    }
}

/// Maps the decision RPC's business rejections to 4xx; other failures stay 500.
fn map_decide_error(error: SdkError) -> Response {
    match &error {
        SdkError::WorkerInvocation { worker, .. } => match worker.error_type() {
            "BlankDecisionReason" => error_response(
                StatusCode::BAD_REQUEST,
                format!("BlankDecisionReason: {}", worker.detail()),
            ),
            "RefundNotAwaitingManager" => error_response(
                StatusCode::CONFLICT,
                format!("RefundNotAwaitingManager: {}", worker.detail()),
            ),
            _ => map_sdk_error(error).into_response(),
        },
        SdkError::RpcLockConflict { .. } => {
            error_response(StatusCode::CONFLICT, format!("RpcLockConflict: {error}"))
        }
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
