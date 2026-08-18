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

use axum::{extract::{Query, State}, response::IntoResponse, routing::get, Router};
use serde::Deserialize;

use crate::primitives::subflow::flow::SubFlowParentFlow;
use crate::server::helpers::{map_sdk_error, ok_json, run_blocking, StartResponse, SharedClient};

#[derive(Deserialize)]
struct StartQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
    #[serde(default, rename = "inputNum")]
    input_num: i32,
}

pub fn mount(client: SharedClient) -> Router {
    Router::new()
        .route("/primitives/subflow/start", get(start))
        .with_state(client)
}

async fn start(State(client): State<SharedClient>, Query(query): Query<StartQuery>) -> impl IntoResponse {
    let flow_id = query.workflow_id;
    let input_num = query.input_num;
    match run_blocking(move || {
        let flow = SubFlowParentFlow::new();
        client
            .start_flow(&flow, &flow_id, input_num)
            .map(|run_id| StartResponse { flow_id, run_id })
    }) {
        Ok(response) => ok_json(response),
        Err(error) => map_sdk_error(error).into_response(),
    }
}
