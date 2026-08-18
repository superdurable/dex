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
use serde::Deserialize;
use serde_json::json;

use crate::products::polling::flow::{POLLING_COMPLETE_TASK, PollingFlow};
use crate::server::helpers::{
    SharedClient, StartResponse, map_sdk_error, new_flow_id, ok_json, run_blocking,
};

#[derive(Deserialize)]
struct StartQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
    #[serde(default, rename = "pollingCompletionThreshold")]
    polling_completion_threshold: u32,
}

#[derive(Deserialize)]
struct CompleteQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
    #[serde(default)]
    channel: String,
}

pub fn mount(client: SharedClient) -> Router {
    Router::new()
        .route("/products/polling/start", get(start))
        .route("/products/polling/complete", get(complete))
        .with_state(client)
}

async fn start(
    State(client): State<SharedClient>,
    Query(query): Query<StartQuery>,
) -> impl IntoResponse {
    let flow_id = if query.workflow_id.is_empty() {
        new_flow_id("polling")
    } else {
        query.workflow_id
    };
    let threshold = if query.polling_completion_threshold == 0 {
        3
    } else {
        query.polling_completion_threshold
    };
    match run_blocking(move || {
        let flow = PollingFlow::default();
        client
            .start_flow(&flow, &flow_id, threshold)
            .map(|run_id| StartResponse { flow_id, run_id })
    }) {
        Ok(value) => ok_json(value),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn complete(
    State(client): State<SharedClient>,
    Query(query): Query<CompleteQuery>,
) -> impl IntoResponse {
    let task = match query.channel.as_str() {
        "task-a-completed" | "a" => "a".to_string(),
        "task-b-completed" | "b" => "b".to_string(),
        _ => {
            return (
                StatusCode::BAD_REQUEST,
                Json(json!({ "error": "channel must identify task A or task B" })),
            )
                .into_response();
        }
    };
    let flow_id = query.workflow_id;
    match run_blocking(move || client.invoke_rpc(&flow_id, POLLING_COMPLETE_TASK, task)) {
        Ok(()) => ok_json(json!({})),
        Err(error) => map_sdk_error(error).into_response(),
    }
}
