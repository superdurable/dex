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

use std::time::Duration;

use axum::{
    Router,
    extract::{Query, State},
    http::StatusCode,
    response::IntoResponse,
    routing::get,
};
use dex_sdk::{IdReusePolicy, StartFlowOptions};
use serde::Deserialize;

use crate::patterns::drain_channels::flow::{
    DrainInternalChannelFlow, DrainingExternalChannelFlow, PUBLISH_EXTERNAL_CHANNEL_MESSAGE,
};
use crate::server::helpers::{
    SharedClient, is_already_started, is_missing_or_inactive, map_sdk_error, new_flow_id, ok_text,
    run_blocking,
};

/// Bounds how often a message that lost a start race is published again.
const START_OR_PUBLISH_ATTEMPTS: usize = 3;

#[derive(Deserialize)]
struct StartQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
    #[serde(default, rename = "messageId")]
    message_id: String,
}

/// Restarts the Flow ID once its run closed, with one start per message.
fn external_publishing_start_options(message_id: &str) -> StartFlowOptions {
    StartFlowOptions::new()
        .timeout(Duration::from_secs(60 * 60))
        .id_reuse_policy(IdReusePolicy::AllowIfNotRunning)
        .request_id(format!("start:{message_id}"))
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
    let message_id = if query.message_id.is_empty() {
        new_flow_id("message")
    } else {
        query.message_id
    };
    match run_blocking(move || {
        let flow = DrainingExternalChannelFlow::default();
        let message = format!("message {message_id}");
        for _ in 0..START_OR_PUBLISH_ATTEMPTS {
            match client.invoke_rpc(&flow_id, PUBLISH_EXTERNAL_CHANNEL_MESSAGE, message.clone()) {
                Ok(_) => return Ok(Ok("Published to the Flow".to_string())),
                Err(error) if is_missing_or_inactive(&error) => {}
                Err(error) => return Err(error),
            }
            match client.start_flow_with_options(
                &flow,
                &flow_id,
                message.clone(),
                external_publishing_start_options(&message_id),
            ) {
                Ok(run_id) => return Ok(Ok(format!("Started the workflow with runId {run_id}"))),
                // Another caller started the run first: publish this message to that run.
                Err(error) if is_already_started(&error) => {}
                Err(error) => return Err(error),
            }
        }
        Ok(Err(format!(
            "message {message_id} is unconfirmed: no run accepted it"
        )))
    }) {
        Ok(Ok(message)) => ok_text(message),
        Ok(Err(unconfirmed)) => (StatusCode::CONFLICT, unconfirmed).into_response(),
        Err(error) => map_sdk_error(error).into_response(),
    }
}
