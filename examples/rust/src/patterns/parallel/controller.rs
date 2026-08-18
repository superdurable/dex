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

use crate::patterns::parallel::flow::{ParallelStatesWithAwaitFlow, SimpleParallelStatesFlow};
use crate::server::helpers::{map_sdk_error, new_flow_id, ok_text, run_blocking, SharedClient};

#[derive(Deserialize)]
struct ParallelQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
    #[serde(default, rename = "countOfJobSeekers")]
    count_of_job_seekers: u32,
}

pub fn mount(client: SharedClient) -> Router {
    Router::new()
        .route("/patterns/parallel/start/simple", get(start_simple))
        .route("/patterns/parallel/start/withAwait", get(start_with_await))
        .with_state(client)
}

async fn start_simple(State(client): State<SharedClient>, Query(query): Query<ParallelQuery>) -> impl IntoResponse {
    let flow_id = if query.workflow_id.is_empty() { new_flow_id("par-simple") } else { query.workflow_id };
    match run_blocking(move || {
        let flow = SimpleParallelStatesFlow::default();
        client.start_flow(&flow, &flow_id, "jobseeker".to_string()).map(|run_id| run_id)
    }) {
        Ok(run_id) => ok_text(run_id),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn start_with_await(State(client): State<SharedClient>, Query(query): Query<ParallelQuery>) -> impl IntoResponse {
    let flow_id = if query.workflow_id.is_empty() { new_flow_id("par-await") } else { query.workflow_id };
    let _count = if query.count_of_job_seekers == 0 { 2 } else { query.count_of_job_seekers };
    match run_blocking(move || {
        let flow = ParallelStatesWithAwaitFlow::default();
        client.start_flow(&flow, &flow_id, ()).map(|run_id| run_id)
    }) {
        Ok(run_id) => ok_text(run_id),
        Err(error) => map_sdk_error(error).into_response(),
    }
}
