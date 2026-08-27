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

use crate::patterns::drain_channels::flow::{
    DrainInternalChannelFlow, DrainingExternalChannelFlow, external_queue,
};
use crate::server::helpers::{
    SharedClient, is_missing_or_inactive, map_sdk_error, new_flow_id, ok_text, run_blocking,
};

#[derive(Deserialize)]
struct StartQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
}

pub fn mount(client: SharedClient) -> Router {
    Router::new()
        .route(
            "/patterns/drain-channels/internal/start",
            get(start_internal),
        )
        .route(
            "/patterns/drain-channels/external-publishing/start-or-publish",
            get(start_or_publish),
        )
        .with_state(client)
}

async fn start_internal(
    State(client): State<SharedClient>,
    Query(query): Query<StartQuery>,
) -> impl IntoResponse {
    let flow_id = if query.workflow_id.is_empty() {
        new_flow_id("drain-internal")
    } else {
        query.workflow_id
    };
    match run_blocking(move || {
        let flow = DrainInternalChannelFlow::default();
        client.start_flow(&flow, &flow_id, vec!["start-input".to_string()])
    }) {
        Ok(run_id) => ok_text(run_id),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn start_or_publish(
    State(client): State<SharedClient>,
    Query(query): Query<StartQuery>,
) -> impl IntoResponse {
    let flow_id = if query.workflow_id.is_empty() {
        new_flow_id("drain-external")
    } else {
        query.workflow_id
    };
    match run_blocking(move || {
        let flow = DrainingExternalChannelFlow::default();
        match client.publish(
            &flow_id,
            &external_queue(),
            "message from start-or-publish endpoint".to_string(),
        ) {
            Ok(()) => Ok("Published to the Flow".to_string()),
            Err(error) if is_missing_or_inactive(&error) => client
                .start_flow(
                    &flow,
                    &flow_id,
                    "first message from start-or-publish".to_string(),
                )
                .map(|run_id| format!("Started the workflow with runId {run_id}")),
            Err(error) => Err(error),
        }
    }) {
        Ok(message) => ok_text(message),
        Err(error) => map_sdk_error(error).into_response(),
    }
}
