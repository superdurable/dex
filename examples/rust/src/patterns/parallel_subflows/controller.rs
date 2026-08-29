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

use super::{
    AdvancedLongLiveParentFlow, AdvancedShortLiveParentFlow, BasicParentFlow, ParentInput,
    SubmitRequestFlow, SubmitRequestInput,
};
use crate::server::helpers::{SharedClient, map_sdk_error, new_flow_id, ok_text, run_blocking};

#[derive(Deserialize)]
struct ParallelSubFlowsQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
}

pub fn mount(client: SharedClient) -> Router {
    Router::new()
        .route("/patterns/parallel-subflows/start/basic", get(start_basic))
        .route(
            "/patterns/parallel-subflows/start/long-lived-parent",
            get(start_long_live),
        )
        .route(
            "/patterns/parallel-subflows/start/short-lived-parent",
            get(start_short_live),
        )
        .route(
            "/patterns/parallel-subflows/start/submit",
            get(start_submit),
        )
        .with_state(client)
}

async fn start_basic(
    State(client): State<SharedClient>,
    Query(query): Query<ParallelSubFlowsQuery>,
) -> Response {
    start(
        client,
        query,
        BasicParentFlow::default(),
        vec!["one", "two", "three", "four"]
            .into_iter()
            .map(String::from)
            .collect(),
    )
    .await
}

async fn start_long_live(
    State(client): State<SharedClient>,
    Query(query): Query<ParallelSubFlowsQuery>,
) -> Response {
    start(
        client,
        query,
        AdvancedLongLiveParentFlow::default(),
        parent_input(),
    )
    .await
}

async fn start_short_live(
    State(client): State<SharedClient>,
    Query(query): Query<ParallelSubFlowsQuery>,
) -> Response {
    start(
        client,
        query,
        AdvancedShortLiveParentFlow::default(),
        parent_input(),
    )
    .await
}

async fn start_submit(
    State(client): State<SharedClient>,
    Query(query): Query<ParallelSubFlowsQuery>,
) -> Response {
    start(
        client,
        query,
        SubmitRequestFlow::default(),
        SubmitRequestInput {
            request: "one".to_string(),
            parent_ids: vec![
                "parallel-parent-0".to_string(),
                "parallel-parent-1".to_string(),
            ],
        },
    )
    .await
}

fn parent_input() -> ParentInput {
    ParentInput {
        requests: vec!["one".to_string(), "two".to_string(), "three".to_string()],
        concurrency: 3,
    }
}

async fn start<T, F>(
    client: SharedClient,
    query: ParallelSubFlowsQuery,
    flow: F,
    input: T,
) -> Response
where
    T: serde::Serialize + Send + 'static,
    F: Flow<StartInput = T> + Send + 'static,
{
    let flow_id = if query.workflow_id.is_empty() {
        new_flow_id("parallel-subflows")
    } else {
        query.workflow_id
    };
    match run_blocking(move || client.start_flow(&flow, &flow_id, input)) {
        Ok(run_id) => ok_text(run_id),
        Err(error) => map_sdk_error(error).into_response(),
    }
}
