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

use std::time::Duration;

use axum::{
    Json, Router,
    extract::{Query, State},
    http::StatusCode,
    response::{IntoResponse, Response},
    routing::get,
};
use dex_sdk::{Client, Rpc, SdkError, SdkResult};
use serde::Deserialize;

use crate::control_patterns::deferred_choice::flow::{
    ACCEPT_CUSTOMER_OFFER, OFFER_RESPONSE_WINDOW_CLOSED, OfferResponseFlow, REJECT_CUSTOMER_OFFER,
};
use crate::server::helpers::{
    ErrorBody, SharedClient, StartResponse, map_sdk_error, ok_json, ok_text, run_blocking,
};

const RESPONSE_LOCK_CONFLICT_ATTEMPTS: u32 = 5;
const RESPONSE_LOCK_CONFLICT_BACKOFF: Duration = Duration::from_millis(200);

#[derive(Deserialize)]
struct StartQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
    #[serde(default, rename = "offerId")]
    offer_id: String,
}

#[derive(Deserialize)]
struct ResponseQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
    #[serde(default)]
    note: String,
}

pub fn mount(client: SharedClient) -> Router {
    Router::new()
        .route("/patterns/deferred-choice/start", get(start_offer_response))
        .route(
            "/patterns/deferred-choice/accept",
            get(accept_customer_offer),
        )
        .route(
            "/patterns/deferred-choice/reject",
            get(reject_customer_offer),
        )
        .with_state(client)
}

async fn start_offer_response(
    State(client): State<SharedClient>,
    Query(query): Query<StartQuery>,
) -> Response {
    if query.workflow_id.is_empty() || query.offer_id.is_empty() {
        return error_response(
            StatusCode::BAD_REQUEST,
            "workflowId and offerId are required".to_owned(),
        );
    }
    let flow_id = query.workflow_id;
    let offer_id = query.offer_id;
    match run_blocking(move || {
        client
            .start_flow(&OfferResponseFlow::default(), &flow_id, offer_id)
            .map(|run_id| StartResponse { flow_id, run_id })
    }) {
        Ok(response) => ok_json(response),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn accept_customer_offer(
    State(client): State<SharedClient>,
    Query(query): Query<ResponseQuery>,
) -> Response {
    send_customer_response(client, ACCEPT_CUSTOMER_OFFER, query)
}

async fn reject_customer_offer(
    State(client): State<SharedClient>,
    Query(query): Query<ResponseQuery>,
) -> Response {
    send_customer_response(client, REJECT_CUSTOMER_OFFER, query)
}

/// Answers 409 when the response window is already closed.
fn send_customer_response(
    client: SharedClient,
    rpc: Rpc<String, String>,
    query: ResponseQuery,
) -> Response {
    if query.workflow_id.is_empty() || query.note.is_empty() {
        return error_response(
            StatusCode::BAD_REQUEST,
            "workflowId and note are required".to_owned(),
        );
    }
    match run_blocking(move || invoke_response_rpc(&client, &query.workflow_id, rpc, query.note)) {
        Ok(outcome) if outcome == OFFER_RESPONSE_WINDOW_CLOSED => {
            (StatusCode::CONFLICT, "offer response window closed").into_response()
        }
        Ok(_) => ok_text("offer response recorded"),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

/// Retries a lock conflict with bounded backoff. Retrying is safe: a conflicting call
/// committed nothing.
/// Returns the RPC outcome, or the last error.
fn invoke_response_rpc(
    client: &Client,
    flow_id: &str,
    rpc: Rpc<String, String>,
    note: String,
) -> SdkResult<String> {
    let mut attempt = 1;
    loop {
        match client.invoke_rpc(flow_id, rpc, note.clone()) {
            Err(SdkError::RpcLockConflict { .. }) if attempt < RESPONSE_LOCK_CONFLICT_ATTEMPTS => {
                std::thread::sleep(RESPONSE_LOCK_CONFLICT_BACKOFF * attempt);
                attempt += 1;
            }
            result => return result,
        }
    }
}

fn error_response(status: StatusCode, error: String) -> Response {
    (status, Json(ErrorBody { error })).into_response()
}
