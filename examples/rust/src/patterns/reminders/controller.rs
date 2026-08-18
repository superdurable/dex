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

use crate::patterns::reminders::flow::{REMINDER_ACCEPT, REMINDER_OPT_OUT, ReminderFlow};
use crate::server::helpers::{
    SharedClient, StartResponse, map_sdk_error, new_flow_id, ok_json, ok_text, run_blocking,
};

#[derive(Deserialize)]
struct WorkflowQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
}

pub fn mount(client: SharedClient) -> Router {
    Router::new()
        .route("/patterns/reminders/start", get(start))
        .route("/patterns/reminders/accept", get(accept))
        .route("/patterns/reminders/optout", get(optout))
        .with_state(client)
}

async fn start(
    State(client): State<SharedClient>,
    Query(query): Query<WorkflowQuery>,
) -> impl IntoResponse {
    let flow_id = if query.workflow_id.is_empty() {
        new_flow_id("reminder_test_id")
    } else {
        query.workflow_id
    };
    match run_blocking(move || {
        let flow = ReminderFlow::default();
        let input = "reminder".to_string();
        client
            .start_flow(&flow, &flow_id, input)
            .map(|run_id| StartResponse { flow_id, run_id })
    }) {
        Ok(value) => ok_json(value),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn accept(
    State(client): State<SharedClient>,
    Query(query): Query<WorkflowQuery>,
) -> impl IntoResponse {
    let flow_id = query.workflow_id;
    match run_blocking(move || client.invoke_rpc_without_input(&flow_id, REMINDER_ACCEPT)) {
        Ok(()) => ok_text("accepted"),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn optout(
    State(client): State<SharedClient>,
    Query(query): Query<WorkflowQuery>,
) -> impl IntoResponse {
    let flow_id = query.workflow_id;
    match run_blocking(move || client.invoke_rpc_without_input(&flow_id, REMINDER_OPT_OUT)) {
        Ok(()) => ok_text("done"),
        Err(error) => map_sdk_error(error).into_response(),
    }
}
