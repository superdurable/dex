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

use crate::products::signup::flow::{SIGNUP_VERIFY, UserSignupFlow};
use crate::server::helpers::{
    SharedClient, StartResponse, is_already_started, map_sdk_error, new_flow_id, ok_json, ok_text,
    run_blocking,
};

#[derive(Deserialize)]
struct StartQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
}

#[derive(Deserialize)]
struct SubmitQuery {
    #[serde(default)]
    username: String,
    #[serde(default)]
    email: String,
}

#[derive(Deserialize)]
struct VerifyQuery {
    #[serde(default)]
    username: String,
}

pub fn mount(client: SharedClient) -> Router {
    Router::new()
        .route("/products/signup/start", get(start))
        .route("/products/signup/submit", get(submit))
        .route("/products/signup/verify", get(verify))
        .with_state(client)
}

async fn start(
    State(client): State<SharedClient>,
    Query(query): Query<StartQuery>,
) -> impl IntoResponse {
    let flow_id = if query.workflow_id.is_empty() {
        new_flow_id("signup")
    } else {
        query.workflow_id
    };
    match run_blocking(move || {
        let flow = UserSignupFlow::default();
        let input = "user@example.com".to_string();
        client
            .start_flow(&flow, &flow_id, input)
            .map(|run_id| StartResponse { flow_id, run_id })
    }) {
        Ok(value) => ok_json(value),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn submit(
    State(client): State<SharedClient>,
    Query(query): Query<SubmitQuery>,
) -> impl IntoResponse {
    let username = query.username;
    let email = if query.email.is_empty() {
        "user@example.com".to_string()
    } else {
        query.email
    };
    match run_blocking(move || {
        let flow = UserSignupFlow::default();
        client.start_flow(&flow, &username, email)
    }) {
        Ok(_) => ok_text("success"),
        Err(error) if is_already_started(&error) => ok_text("username already started registry"),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn verify(
    State(client): State<SharedClient>,
    Query(query): Query<VerifyQuery>,
) -> impl IntoResponse {
    let username = query.username;
    match run_blocking(move || client.invoke_rpc_without_input(&username, SIGNUP_VERIFY)) {
        Ok(()) => ok_text("verified"),
        Err(error) => map_sdk_error(error).into_response(),
    }
}
