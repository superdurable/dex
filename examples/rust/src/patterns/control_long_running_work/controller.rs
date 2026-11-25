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

use crate::patterns::control_long_running_work::flow::{
    REQUEST_STOP, RecordBackfillFlow, SET_BATCH_SIZE,
};
use crate::server::helpers::{
    SharedClient, StartResponse, map_sdk_error, new_flow_id, ok_json, ok_text, run_blocking,
};

#[derive(Deserialize)]
struct WorkflowQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
}

#[derive(Deserialize)]
struct BatchSizeQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
    #[serde(rename = "batchSize")]
    batch_size: i64,
}

pub fn mount(client: SharedClient) -> Router {
    Router::new()
        .route("/patterns/control-long-running-work/start", get(start))
        .route(
            "/patterns/control-long-running-work/set-batch-size",
            get(set_batch_size),
        )
        .route(
            "/patterns/control-long-running-work/stop",
            get(request_stop),
        )
        .with_state(client)
}

async fn start(
    State(client): State<SharedClient>,
    Query(query): Query<WorkflowQuery>,
) -> impl IntoResponse {
    let flow_id = if query.workflow_id.is_empty() {
        new_flow_id("control-long-running-work")
    } else {
        query.workflow_id
    };
    match run_blocking(move || {
        let flow = RecordBackfillFlow::default();
        client
            .start_flow(&flow, &flow_id, ())
            .map(|run_id| StartResponse { flow_id, run_id })
    }) {
        Ok(value) => ok_json(value),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn set_batch_size(
    State(client): State<SharedClient>,
    Query(query): Query<BatchSizeQuery>,
) -> impl IntoResponse {
    match run_blocking(move || {
        client.invoke_rpc(&query.workflow_id, SET_BATCH_SIZE, query.batch_size)
    }) {
        Ok(()) => ok_text("done"),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn request_stop(
    State(client): State<SharedClient>,
    Query(query): Query<WorkflowQuery>,
) -> impl IntoResponse {
    match run_blocking(move || client.invoke_rpc_without_input(&query.workflow_id, REQUEST_STOP)) {
        Ok(()) => ok_text("done"),
        Err(error) => map_sdk_error(error).into_response(),
    }
}
