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

use crate::patterns::intervention::flow::ManualInterventionFlow;
use crate::server::helpers::{map_sdk_error, new_flow_id, ok_json, run_blocking, SharedClient, StartResponse};

#[derive(Deserialize)]
struct StartQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
}

pub fn mount(client: SharedClient) -> Router {
    Router::new()
        .route("/patterns/intervention/start", get(start))
        .with_state(client)
}

async fn start(State(client): State<SharedClient>, Query(query): Query<StartQuery>) -> impl IntoResponse {
    let flow_id = if query.workflow_id.is_empty() { new_flow_id("intervention") } else { query.workflow_id };
    match run_blocking(move || {
        let flow = ManualInterventionFlow::default();
        let input = "intervention-task".to_string();
        client.start_flow(&flow, &flow_id, input).map(|run_id| StartResponse { flow_id, run_id })
    }) {
        Ok(value) => ok_json(value),
        Err(error) => map_sdk_error(error).into_response(),
    }
}
