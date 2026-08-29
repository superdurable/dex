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

use crate::patterns::polling::flow::{BackoffPollingFlow, IterationFlow, PollingWithTimerFlow};
use crate::server::helpers::{SharedClient, map_sdk_error, new_flow_id, ok_text, run_blocking};

#[derive(Deserialize)]
struct StartQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
}

pub fn mount(client: SharedClient) -> Router {
    Router::new()
        .route("/patterns/polling/start/timer", get(start_timer))
        .route("/patterns/polling/start/backoff", get(start_backoff))
        .route("/patterns/polling/start/iteration", get(start_iteration))
        .with_state(client)
}

async fn start_timer(
    State(client): State<SharedClient>,
    Query(query): Query<StartQuery>,
) -> impl IntoResponse {
    let flow_id = if query.workflow_id.is_empty() {
        new_flow_id("dp-timer")
    } else {
        query.workflow_id
    };
    match run_blocking(move || {
        let flow = PollingWithTimerFlow::default();
        client.start_flow(&flow, &flow_id, 0_u32)
    }) {
        Ok(run_id) => ok_text(run_id),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn start_iteration(
    State(client): State<SharedClient>,
    Query(query): Query<StartQuery>,
) -> impl IntoResponse {
    let flow_id = if query.workflow_id.is_empty() {
        new_flow_id("dp-iteration")
    } else {
        query.workflow_id
    };
    match run_blocking(move || {
        let flow = IterationFlow::default();
        client.start_flow(&flow, &flow_id, String::new())
    }) {
        Ok(run_id) => ok_text(run_id),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn start_backoff(
    State(client): State<SharedClient>,
    Query(query): Query<StartQuery>,
) -> impl IntoResponse {
    let flow_id = if query.workflow_id.is_empty() {
        new_flow_id("dp-backoff")
    } else {
        query.workflow_id
    };
    match run_blocking(move || {
        let flow = BackoffPollingFlow::default();
        client.start_flow(&flow, &flow_id, 0_u32)
    }) {
        Ok(run_id) => ok_text(run_id),
        Err(error) => map_sdk_error(error).into_response(),
    }
}
