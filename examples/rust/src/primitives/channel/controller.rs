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

use crate::primitives::channel::flow::{CHANNEL_APPROVE, ChannelFlow};
use crate::server::helpers::{
    SharedClient, StartResponse, map_sdk_error, ok_json, ok_text, run_blocking,
};

#[derive(Deserialize)]
struct StartQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
    #[serde(default, rename = "inputNum")]
    input_num: i32,
}

#[derive(Deserialize)]
struct ApproveQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
}

pub fn mount(client: SharedClient) -> Router {
    Router::new()
        .route("/primitives/channel/start", get(start))
        .route("/primitives/channel/approve", get(approve))
        .with_state(client)
}

async fn start(
    State(client): State<SharedClient>,
    Query(query): Query<StartQuery>,
) -> impl IntoResponse {
    let flow_id = query.workflow_id;
    let input_num = query.input_num;
    match run_blocking(move || {
        let flow = ChannelFlow::default();
        client
            .start_flow(&flow, &flow_id, input_num)
            .map(|run_id| StartResponse { flow_id, run_id })
    }) {
        Ok(response) => ok_json(response),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn approve(
    State(client): State<SharedClient>,
    Query(query): Query<ApproveQuery>,
) -> impl IntoResponse {
    let workflow_id = query.workflow_id;
    match run_blocking(move || client.invoke_rpc_without_input(&workflow_id, CHANNEL_APPROVE)) {
        Ok(()) => ok_text("done"),
        Err(error) => map_sdk_error(error).into_response(),
    }
}
