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
    Router,
    extract::{Query, State},
    response::IntoResponse,
    routing::get,
};
use serde::Deserialize;
use serde_json::json;

use crate::products::microservices::flow::{
    ORCHESTRATION_READY, ORCHESTRATION_SWAP, OrchestrationFlow,
};
use crate::server::helpers::{
    SharedClient, StartResponse, map_sdk_error, new_flow_id, ok_json, ok_text, run_blocking,
};

#[derive(Deserialize)]
struct StartQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
}

#[derive(Deserialize)]
struct SwapQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
    #[serde(default)]
    data: String,
}

pub fn mount(client: SharedClient) -> Router {
    Router::new()
        .route("/products/microservices/start", get(start))
        .route("/products/microservices/swap", get(swap))
        .route("/products/microservices/signal", get(signal))
        .with_state(client)
}

async fn start(
    State(client): State<SharedClient>,
    Query(query): Query<StartQuery>,
) -> impl IntoResponse {
    let flow_id = if query.workflow_id.is_empty() {
        new_flow_id("microservice")
    } else {
        query.workflow_id
    };
    match run_blocking(move || {
        let flow = OrchestrationFlow::default();
        let input = "test initial data".to_string();
        client
            .start_flow(&flow, &flow_id, input)
            .map(|run_id| StartResponse { flow_id, run_id })
    }) {
        Ok(value) => ok_json(value),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn swap(
    State(client): State<SharedClient>,
    Query(query): Query<SwapQuery>,
) -> impl IntoResponse {
    let flow_id = query.workflow_id;
    let data = query.data;
    match run_blocking(move || client.invoke_rpc(&flow_id, ORCHESTRATION_SWAP, data)) {
        Ok(previous) => ok_text(previous),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn signal(
    State(client): State<SharedClient>,
    Query(query): Query<StartQuery>,
) -> impl IntoResponse {
    let flow_id = query.workflow_id;
    match run_blocking(move || client.invoke_rpc_without_input(&flow_id, ORCHESTRATION_READY)) {
        Ok(()) => ok_json(json!({})),
        Err(error) => map_sdk_error(error).into_response(),
    }
}
