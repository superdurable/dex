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

use std::sync::Arc;

use axum::{
    Json, Router,
    extract::{Query, State},
    http::StatusCode,
    response::IntoResponse,
    routing::get,
};
use serde::Deserialize;

use crate::patterns::polling::flow::PollingFlow;
use crate::patterns::polling::job_service::FakeJobService;
use crate::server::helpers::{
    ErrorBody, SharedClient, StartResponse, map_sdk_error, new_flow_id, ok_json, ok_text,
    run_blocking,
};

#[derive(Clone)]
struct PollingState {
    client: SharedClient,
    jobs: Arc<FakeJobService>,
}

#[derive(Deserialize)]
struct WorkflowQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
}

pub fn mount(client: SharedClient, jobs: Arc<FakeJobService>) -> Router {
    Router::new()
        .route("/patterns/polling/start", get(start))
        .route("/patterns/polling/complete-job", get(complete_job))
        .with_state(PollingState { client, jobs })
}

async fn start(
    State(state): State<PollingState>,
    Query(query): Query<WorkflowQuery>,
) -> impl IntoResponse {
    let flow_id = if query.workflow_id.is_empty() {
        new_flow_id("polling")
    } else {
        query.workflow_id
    };
    let PollingState { client, jobs } = state;
    match run_blocking(move || {
        let flow = PollingFlow::new(jobs);
        client
            .start_flow(&flow, &flow_id, ())
            .map(|run_id| StartResponse { flow_id, run_id })
    }) {
        Ok(value) => ok_json(value),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn complete_job(
    State(state): State<PollingState>,
    Query(query): Query<WorkflowQuery>,
) -> impl IntoResponse {
    match state.jobs.complete_job(&query.workflow_id) {
        Ok(()) => ok_text("done"),
        Err(error) => (
            StatusCode::NOT_FOUND,
            Json(ErrorBody {
                error: error.to_string(),
            }),
        )
            .into_response(),
    }
}
