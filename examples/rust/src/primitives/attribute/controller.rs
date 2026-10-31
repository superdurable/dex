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
use serde::{Deserialize, Serialize};

use crate::primitives::attribute::flow::{AttributeFlow, EMAIL, PROGRESS, STATUS};
use crate::server::helpers::{
    ErrorBody, SharedClient, StartResponse, map_sdk_error, ok_json, run_blocking,
};

#[derive(Deserialize)]
struct StartQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
    #[serde(default)]
    message: String,
}

#[derive(Deserialize)]
struct ReadQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct AttributeReadResponse {
    status: String,
    payment_progress: String,
    email: Option<String>,
}

pub fn mount(client: SharedClient) -> Router {
    Router::new()
        .route("/primitives/attribute/start", get(start))
        .route("/primitives/attribute/read", get(read_attributes))
        .with_state(client)
}

async fn start(
    State(client): State<SharedClient>,
    Query(query): Query<StartQuery>,
) -> impl IntoResponse {
    let flow_id = query.workflow_id;
    let message = query.message;
    match run_blocking(move || {
        let flow = AttributeFlow::default();
        client
            .start_flow(&flow, &flow_id, message)
            .map(|run_id| StartResponse { flow_id, run_id })
    }) {
        Ok(response) => ok_json(response),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn read_attributes(
    State(client): State<SharedClient>,
    Query(query): Query<ReadQuery>,
) -> impl IntoResponse {
    let flow_id = query.workflow_id;
    if flow_id.is_empty() {
        return error_response(StatusCode::BAD_REQUEST, "workflowId is required".to_owned());
    }
    match run_blocking(move || {
        client.get_attributes(&flow_id, (&STATUS, &PROGRESS.instance("payment"), &EMAIL))
    }) {
        Ok((status, payment_progress, email)) => ok_json(AttributeReadResponse {
            status: status.unwrap_or_else(|| "not started".to_owned()),
            payment_progress: payment_progress.unwrap_or_else(|| "not started".to_owned()),
            // This Flow never sets EMAIL, so it reads as None.
            email,
        }),
        Err(error @ SdkError::FlowNotFound { .. }) => {
            error_response(StatusCode::NOT_FOUND, error.to_string())
        }
        Err(error) => map_sdk_error(error).into_response(),
    }
}

fn error_response(status: StatusCode, error: String) -> axum::response::Response {
    (status, Json(ErrorBody { error })).into_response()
}
