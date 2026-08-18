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
use serde_json::json;

use crate::products::engagement::flow::{
    ENGAGEMENT_ACCEPT, ENGAGEMENT_DECLINE, ENGAGEMENT_DESCRIBE, ENGAGEMENT_OPT_OUT, EngagementFlow,
    EngagementRequest,
};
use crate::server::helpers::{
    SharedClient, StartResponse, map_sdk_error, new_flow_id, ok_json, run_blocking,
};

#[derive(Deserialize)]
struct WorkflowQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
    #[serde(default)]
    notes: String,
}

#[derive(Deserialize)]
struct ListQuery {
    #[serde(default)]
    query: String,
}

pub fn mount(client: SharedClient) -> Router {
    Router::new()
        .route("/products/engagement/start", get(start))
        .route("/products/engagement/describe", get(describe))
        .route("/products/engagement/optout", get(optout))
        .route("/products/engagement/decline", get(decline))
        .route("/products/engagement/accept", get(accept))
        .route("/products/engagement/list", get(list))
        .with_state(client)
}

async fn start(State(client): State<SharedClient>) -> impl IntoResponse {
    match run_blocking(move || {
        let flow_id = new_flow_id("engagement");
        let flow = EngagementFlow::default();
        let input = EngagementRequest {
            employer_id: "test-employer-id".into(),
            candidate_id: "test-job-seeker-id".into(),
        };
        client
            .start_flow(&flow, &flow_id, input)
            .map(|run_id| StartResponse { flow_id, run_id })
    }) {
        Ok(value) => ok_json(value),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn describe(
    State(client): State<SharedClient>,
    Query(query): Query<WorkflowQuery>,
) -> impl IntoResponse {
    let flow_id = query.workflow_id;
    match run_blocking(move || client.invoke_rpc_without_input(&flow_id, ENGAGEMENT_DESCRIBE)) {
        Ok(value) => ok_json(value),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn optout(
    State(client): State<SharedClient>,
    Query(query): Query<WorkflowQuery>,
) -> impl IntoResponse {
    let flow_id = query.workflow_id;
    match run_blocking(move || client.invoke_rpc_without_input(&flow_id, ENGAGEMENT_OPT_OUT)) {
        Ok(()) => ok_json(json!({})),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn decline(
    State(client): State<SharedClient>,
    Query(query): Query<WorkflowQuery>,
) -> impl IntoResponse {
    let flow_id = query.workflow_id;
    let notes = query.notes;
    match run_blocking(move || client.invoke_rpc(&flow_id, ENGAGEMENT_DECLINE, notes)) {
        Ok(()) => ok_json(json!({})),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn accept(
    State(client): State<SharedClient>,
    Query(query): Query<WorkflowQuery>,
) -> impl IntoResponse {
    let flow_id = query.workflow_id;
    let notes = query.notes;
    match run_blocking(move || client.invoke_rpc(&flow_id, ENGAGEMENT_ACCEPT, notes)) {
        Ok(()) => ok_json(json!({})),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn list(
    State(client): State<SharedClient>,
    Query(query): Query<ListQuery>,
) -> impl IntoResponse {
    match run_blocking(move || client.search_flows(&query.query, 100)) {
        Ok(page) => ok_json(json!({
            "flowIDs": page.flows.iter().map(|flow| flow.flow_id.clone()).collect::<Vec<_>>(),
            "nextPageToken": page.next_page_token,
        })),
        Err(error) => map_sdk_error(error).into_response(),
    }
}
