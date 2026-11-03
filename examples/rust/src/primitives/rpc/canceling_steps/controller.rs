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
use serde::{Deserialize, Serialize};

use crate::primitives::rpc::canceling_steps::flow::{ReportGenerationFlow, STOP_REPORT_RPC};
use crate::server::helpers::{SharedClient, StartResponse, map_sdk_error, ok_json, run_blocking};

#[derive(Deserialize)]
struct StartQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
    #[serde(default, rename = "reportName")]
    report_name: String,
}

#[derive(Deserialize)]
struct StopQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
}

#[derive(Serialize)]
struct StopResponse {
    #[serde(rename = "isStopping")]
    is_stopping: bool,
}

pub fn mount(client: SharedClient) -> Router {
    Router::new()
        .route("/primitives/rpc-canceling-steps/start", get(start))
        .route("/primitives/rpc-canceling-steps/stop", get(stop))
        .with_state(client)
}

async fn start(
    State(client): State<SharedClient>,
    Query(query): Query<StartQuery>,
) -> impl IntoResponse {
    let flow_id = query.workflow_id;
    let report_name = query.report_name;
    match run_blocking(move || {
        let flow = ReportGenerationFlow::default();
        client
            .start_flow(&flow, &flow_id, report_name)
            .map(|run_id| StartResponse { flow_id, run_id })
    }) {
        Ok(response) => ok_json(response),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn stop(
    State(client): State<SharedClient>,
    Query(query): Query<StopQuery>,
) -> impl IntoResponse {
    let workflow_id = query.workflow_id;
    match run_blocking(move || client.invoke_rpc(&workflow_id, STOP_REPORT_RPC, ())) {
        Ok(is_stopping) => ok_json(StopResponse { is_stopping }),
        Err(error) => map_sdk_error(error).into_response(),
    }
}
