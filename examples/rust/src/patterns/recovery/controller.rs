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

use crate::patterns::recovery::flow::FailureRecoveryFlow;
use crate::server::helpers::{map_sdk_error, new_flow_id, ok_text, run_blocking, SharedClient};

#[derive(Deserialize)]
struct RecoveryQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
    #[serde(default, rename = "itemName")]
    item_name: String,
}

pub fn mount(client: SharedClient) -> Router {
    Router::new()
        .route("/patterns/recovery/start", get(start))
        .with_state(client)
}

async fn start(State(client): State<SharedClient>, Query(query): Query<RecoveryQuery>) -> impl IntoResponse {
    let flow_id = if query.workflow_id.is_empty() { new_flow_id("recovery") } else { query.workflow_id };
    let item = if query.item_name.is_empty() { "widget".to_string() } else { query.item_name };
    match run_blocking(move || {
        let flow = FailureRecoveryFlow::default();
        client.start_flow(&flow, &flow_id, item).map(|_| ())
    }) {
        Ok(()) => ok_text("recovery workflow started"),
        Err(error) => map_sdk_error(error).into_response(),
    }
}
