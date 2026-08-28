// Copyright (c) 2026 Super Durable, Inc.
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
    response::IntoResponse,
    routing::get,
};
use serde::{Deserialize, Serialize};

use crate::primitives::stream::flow::{StreamFlow, progress};
use crate::server::helpers::{
    SharedClient, StartResponse, map_sdk_error, ok_json, ok_text, run_blocking,
};

#[derive(Deserialize)]
struct StartQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
    #[serde(default)]
    input: String,
}

#[derive(Deserialize)]
struct WriteQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
    #[serde(default, rename = "idempotencyKey")]
    idempotency_key: String,
    #[serde(default)]
    message: String,
}

#[derive(Deserialize)]
struct ReadQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
    #[serde(default, rename = "resumeToken")]
    resume_token: String,
}

#[derive(Serialize)]
struct ReadResponse {
    value: String,
    #[serde(rename = "resumeToken")]
    resume_token: String,
    #[serde(rename = "createdTime")]
    created_time: String,
    #[serde(rename = "idempotencyKey")]
    idempotency_key: String,
}

pub fn mount(client: SharedClient) -> Router {
    Router::new()
        .route("/primitives/stream/start", get(start))
        .route("/primitives/stream/write", get(write))
        .route("/primitives/stream/read", get(read))
        .with_state(client)
}

async fn start(
    State(client): State<SharedClient>,
    Query(query): Query<StartQuery>,
) -> impl IntoResponse {
    let flow_id = query.workflow_id;
    let input = query.input;
    match run_blocking(move || {
        client
            .start_flow(&StreamFlow::default(), &flow_id, input)
            .map(|run_id| StartResponse { flow_id, run_id })
    }) {
        Ok(response) => ok_json(response),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn write(
    State(client): State<SharedClient>,
    Query(query): Query<WriteQuery>,
) -> impl IntoResponse {
    match run_blocking(move || {
        client.write_stream(
            &query.workflow_id,
            &progress(),
            &query.idempotency_key,
            query.message,
        )
    }) {
        Ok(()) => ok_text("done"),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn read(
    State(client): State<SharedClient>,
    Query(query): Query<ReadQuery>,
) -> impl IntoResponse {
    match run_blocking(move || {
        client
            .read_stream_with_timeout(
                &query.workflow_id,
                &progress(),
                &query.resume_token,
                Duration::from_secs(20),
            )
            .map(|message| ReadResponse {
                value: message.value,
                resume_token: message.resume_token,
                created_time: format!("{:?}", message.created_time),
                idempotency_key: message.idempotency_key,
            })
    }) {
        Ok(response) => ok_json(response),
        Err(error) => map_sdk_error(error).into_response(),
    }
}
