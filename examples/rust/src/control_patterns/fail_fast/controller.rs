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

use axum::{
    Json, Router,
    extract::{Query, State},
    http::StatusCode,
    response::IntoResponse,
    routing::get,
};
use dex_sdk::SdkError;
use serde::Deserialize;

use crate::control_patterns::fail_fast::flow::{ExpenseReimbursementFlow, PAYOUT_OUTCOME};
use crate::control_patterns::fail_fast::payment_provider::ExpenseClaim;
use crate::server::helpers::{
    ErrorBody, SharedClient, StartResponse, map_sdk_error, ok_json, run_blocking,
};

#[derive(Deserialize)]
struct StartQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
    #[serde(rename = "amountCents")]
    amount_cents: i64,
    #[serde(rename = "bankAccount")]
    bank_account: String,
}

#[derive(Deserialize)]
struct WorkflowQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
}

pub fn mount(client: SharedClient) -> Router {
    Router::new()
        .route(
            "/patterns/fail-fast/start",
            get(start_expense_reimbursement),
        )
        .route("/patterns/fail-fast/get-outcome", get(get_payout_outcome))
        .with_state(client)
}

async fn start_expense_reimbursement(
    State(client): State<SharedClient>,
    Query(query): Query<StartQuery>,
) -> impl IntoResponse {
    if query.workflow_id.is_empty() {
        return error_response(StatusCode::BAD_REQUEST, "workflowId is required".to_owned());
    }
    let flow_id = query.workflow_id;
    let claim = ExpenseClaim {
        amount_cents: query.amount_cents,
        bank_account: query.bank_account,
    };
    match run_blocking(move || {
        let flow = ExpenseReimbursementFlow::default();
        client
            .start_flow(&flow, &flow_id, claim)
            .map(|run_id| StartResponse { flow_id, run_id })
    }) {
        Ok(response) => ok_json(response),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn get_payout_outcome(
    State(client): State<SharedClient>,
    Query(query): Query<WorkflowQuery>,
) -> impl IntoResponse {
    let flow_id = query.workflow_id;
    if flow_id.is_empty() {
        return error_response(StatusCode::BAD_REQUEST, "workflowId is required".to_owned());
    }
    match run_blocking(move || client.get_attributes(&flow_id, (&*PAYOUT_OUTCOME,))) {
        Ok((Some(outcome),)) => ok_json(outcome),
        Ok((None,)) => error_response(
            StatusCode::NOT_FOUND,
            "the claim has no outcome yet".to_owned(),
        ),
        Err(error @ SdkError::FlowNotFound { .. }) => {
            error_response(StatusCode::NOT_FOUND, error.to_string())
        }
        Err(error) => map_sdk_error(error).into_response(),
    }
}

fn error_response(status: StatusCode, error: String) -> axum::response::Response {
    (status, Json(ErrorBody { error })).into_response()
}
