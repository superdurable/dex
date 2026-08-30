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

use crate::products::job_post::flow::{JOB_POST_READ, JOB_POST_UPDATE, JobPost, JobPostingFlow};
use crate::server::helpers::{
    SharedClient, StartResponse, map_sdk_error, new_flow_id, ok_json, ok_text, run_blocking,
};
use dex_sdk::StopFlowOptions;

#[derive(Deserialize)]
struct StartQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
}

#[derive(Deserialize)]
struct CreateQuery {
    #[serde(default)]
    title: String,
    #[serde(default)]
    description: String,
}

#[derive(Deserialize)]
struct WorkflowQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
}

#[derive(Deserialize)]
struct UpdateQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
    #[serde(default)]
    title: String,
    #[serde(default)]
    description: String,
    #[serde(default)]
    notes: String,
}

#[derive(Deserialize)]
struct SearchQuery {
    #[serde(default)]
    query: String,
}

pub fn mount(client: SharedClient) -> Router {
    Router::new()
        .route("/products/job-post/start", get(start))
        .route("/products/job-post/create", get(create))
        .route("/products/job-post/read", get(read))
        .route("/products/job-post/update", get(update))
        .route("/products/job-post/delete", get(delete))
        .route("/products/job-post/search", get(search))
        .with_state(client)
}

async fn start(
    State(client): State<SharedClient>,
    Query(query): Query<StartQuery>,
) -> impl IntoResponse {
    let flow_id = if query.workflow_id.is_empty() {
        new_flow_id("job-post")
    } else {
        query.workflow_id
    };
    match run_blocking(move || {
        let flow = JobPostingFlow::default();
        let input = JobPost {
            title: "Engineer".into(),
            description: "Build flows".into(),
            ..JobPost::default()
        };
        client
            .start_flow(&flow, &flow_id, input)
            .map(|run_id| StartResponse { flow_id, run_id })
    }) {
        Ok(value) => ok_json(value),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn create(
    State(client): State<SharedClient>,
    Query(query): Query<CreateQuery>,
) -> impl IntoResponse {
    let flow_id = format!(
        "job_id_{}",
        std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .map(|duration| duration.as_secs())
            .unwrap_or(0)
    );
    match run_blocking(move || {
        let flow = JobPostingFlow::default();
        let input = JobPost {
            title: query.title,
            description: query.description,
            ..JobPost::default()
        };
        client
            .start_flow(&flow, &flow_id, input)
            .map(|run_id| StartResponse { flow_id, run_id })
    }) {
        Ok(value) => ok_json(value),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn read(
    State(client): State<SharedClient>,
    Query(query): Query<WorkflowQuery>,
) -> impl IntoResponse {
    let flow_id = query.workflow_id;
    match run_blocking(move || client.invoke_rpc_without_input(&flow_id, JOB_POST_READ)) {
        Ok(value) => ok_json(value),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn update(
    State(client): State<SharedClient>,
    Query(query): Query<UpdateQuery>,
) -> impl IntoResponse {
    let flow_id = query.workflow_id;
    let notes = if query.notes.is_empty() {
        "test-notes".to_string()
    } else {
        query.notes
    };
    let replacement = JobPost {
        title: query.title,
        description: query.description,
        notes,
        deleted: false,
    };
    match run_blocking(move || client.invoke_rpc(&flow_id, JOB_POST_UPDATE, replacement)) {
        Ok(_) => ok_json(json!({ "updated": true })),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn delete(
    State(client): State<SharedClient>,
    Query(query): Query<WorkflowQuery>,
) -> impl IntoResponse {
    let flow_id = query.workflow_id;
    match run_blocking(move || client.stop_flow(&flow_id, StopFlowOptions::cancel())) {
        Ok(()) => ok_text("marked as soft deleted, will be delete later after retention"),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn search(
    State(client): State<SharedClient>,
    Query(query): Query<SearchQuery>,
) -> impl IntoResponse {
    match run_blocking(move || client.search_flows(&query.query, 20)) {
        Ok(page) => ok_json(json!({
            "flowIDs": page.flows.iter().map(|flow| flow.flow_id.clone()).collect::<Vec<_>>(),
            "nextPageToken": page.next_page_token,
        })),
        Err(error) => map_sdk_error(error).into_response(),
    }
}
