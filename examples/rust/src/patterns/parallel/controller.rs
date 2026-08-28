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
    response::{IntoResponse, Response},
    routing::get,
};
use dex_sdk::Flow;
use serde::Deserialize;

use crate::patterns::parallel::{
    AwaitParallelStepsFlow, DynamicParallelStepsFlow, FirstWinParallelStepsFlow,
    StaticParallelStepsFlow,
};
use crate::server::helpers::{SharedClient, map_sdk_error, new_flow_id, ok_text, run_blocking};

#[derive(Deserialize)]
struct ParallelQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
}

pub fn mount(client: SharedClient) -> Router {
    Router::new()
        .route("/patterns/parallel/start/static", get(start_static))
        .route("/patterns/parallel/start/dynamic", get(start_dynamic))
        .route("/patterns/parallel/start/await", get(start_await))
        .route("/patterns/parallel/start/first-win", get(start_first_win))
        .with_state(client)
}

async fn start_static(
    State(client): State<SharedClient>,
    Query(query): Query<ParallelQuery>,
) -> Response {
    start(
        client,
        query,
        StaticParallelStepsFlow::default(),
        "work".to_string(),
    )
    .await
}

async fn start_dynamic(
    State(client): State<SharedClient>,
    Query(query): Query<ParallelQuery>,
) -> Response {
    start(client, query, DynamicParallelStepsFlow::default(), 3).await
}

async fn start_await(
    State(client): State<SharedClient>,
    Query(query): Query<ParallelQuery>,
) -> Response {
    start(client, query, AwaitParallelStepsFlow::default(), 3).await
}

async fn start_first_win(
    State(client): State<SharedClient>,
    Query(query): Query<ParallelQuery>,
) -> Response {
    start(client, query, FirstWinParallelStepsFlow::default(), 3).await
}

async fn start<T, F>(client: SharedClient, query: ParallelQuery, flow: F, input: T) -> Response
where
    T: serde::Serialize + Send + 'static,
    F: Flow<StartInput = T> + Send + 'static,
{
    let flow_id = if query.workflow_id.is_empty() {
        new_flow_id("parallel")
    } else {
        query.workflow_id
    };
    match run_blocking(move || client.start_flow(&flow, &flow_id, input)) {
        Ok(run_id) => ok_text(run_id),
        Err(error) => map_sdk_error(error).into_response(),
    }
}
