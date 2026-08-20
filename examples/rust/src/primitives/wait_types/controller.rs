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

use crate::primitives::wait_types::flow::{
    WAIT_TYPES_SIGNAL_A, WAIT_TYPES_SIGNAL_B, WaitTypesFlow, WaitTypesInput,
};
use crate::server::helpers::{
    SharedClient, StartResponse, map_sdk_error, ok_json, ok_text, run_blocking,
};

#[derive(Deserialize)]
struct StartQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
    #[serde(default)]
    mode: String,
    #[serde(default = "default_timeout_seconds", rename = "timeoutSeconds")]
    timeout_seconds: i32,
}

fn default_timeout_seconds() -> i32 {
    60
}

#[derive(Deserialize)]
struct SignalQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
}

pub fn mount(client: SharedClient) -> Router {
    Router::new()
        .route("/primitives/step/wait-types/start", get(start))
        .route("/primitives/step/wait-types/signal-a", get(signal_a))
        .route("/primitives/step/wait-types/signal-b", get(signal_b))
        .with_state(client)
}

async fn start(
    State(client): State<SharedClient>,
    Query(query): Query<StartQuery>,
) -> impl IntoResponse {
    let flow_id = query.workflow_id;
    let input = WaitTypesInput {
        mode: query.mode,
        timeout_seconds: query.timeout_seconds,
    };
    match run_blocking(move || {
        let flow = WaitTypesFlow::default();
        client
            .start_flow(&flow, &flow_id, input)
            .map(|run_id| StartResponse { flow_id, run_id })
    }) {
        Ok(response) => ok_json(response),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn signal_a(
    State(client): State<SharedClient>,
    Query(query): Query<SignalQuery>,
) -> impl IntoResponse {
    let workflow_id = query.workflow_id;
    match run_blocking(move || {
        client.invoke_rpc_without_input(&workflow_id, WAIT_TYPES_SIGNAL_A)
    }) {
        Ok(()) => ok_text("done"),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn signal_b(
    State(client): State<SharedClient>,
    Query(query): Query<SignalQuery>,
) -> impl IntoResponse {
    let workflow_id = query.workflow_id;
    match run_blocking(move || {
        client.invoke_rpc_without_input(&workflow_id, WAIT_TYPES_SIGNAL_B)
    }) {
        Ok(()) => ok_text("done"),
        Err(error) => map_sdk_error(error).into_response(),
    }
}
