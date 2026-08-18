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

use crate::primitives::client_apis::flow::ClientApisFlow;
use crate::server::helpers::{SharedClient, StartResponse, map_sdk_error, ok_json, run_blocking};

#[derive(Deserialize)]
struct StartQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
    #[serde(default)]
    keyword: String,
}

#[derive(Deserialize)]
struct SearchQuery {
    #[serde(default)]
    query: String,
}

pub fn mount(client: SharedClient) -> Router {
    Router::new()
        .route("/primitives/client-apis/start", get(start))
        .route("/primitives/client-apis/search", get(search))
        .with_state(client)
}

async fn start(
    State(client): State<SharedClient>,
    Query(query): Query<StartQuery>,
) -> impl IntoResponse {
    let flow_id = query.workflow_id;
    let keyword = query.keyword;
    match run_blocking(move || {
        let flow = ClientApisFlow::default();
        client
            .start_flow(&flow, &flow_id, keyword)
            .map(|run_id| StartResponse { flow_id, run_id })
    }) {
        Ok(response) => ok_json(response),
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
