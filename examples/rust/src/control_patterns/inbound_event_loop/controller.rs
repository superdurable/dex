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

use crate::control_patterns::inbound_event_loop::flow::{
    GET_TICKET_TRANSCRIPT, RESOLVE_TICKET, SEND_TICKET_REPLY, SupportTicketFlow,
};
use crate::server::helpers::{
    SharedClient, StartResponse, map_sdk_error, new_flow_id, ok_json, ok_text, run_blocking,
};

#[derive(Deserialize)]
struct WorkflowQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
}

#[derive(Deserialize)]
struct ReplyQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
    #[serde(default)]
    text: String,
}

#[derive(Deserialize)]
struct ResolveQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
    #[serde(default)]
    reason: String,
}

pub fn mount(client: SharedClient) -> Router {
    Router::new()
        .route("/patterns/inbound-event-loop/start", get(start))
        .route("/patterns/inbound-event-loop/reply", get(send_ticket_reply))
        .route("/patterns/inbound-event-loop/resolve", get(resolve_ticket))
        .route(
            "/patterns/inbound-event-loop/transcript",
            get(get_ticket_transcript),
        )
        .with_state(client)
}

async fn start(
    State(client): State<SharedClient>,
    Query(query): Query<WorkflowQuery>,
) -> impl IntoResponse {
    let flow_id = if query.workflow_id.is_empty() {
        new_flow_id("inbound-event-loop")
    } else {
        query.workflow_id
    };
    match run_blocking(move || {
        let flow = SupportTicketFlow::default();
        client
            .start_flow(&flow, &flow_id, String::new())
            .map(|run_id| StartResponse { flow_id, run_id })
    }) {
        Ok(value) => ok_json(value),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn send_ticket_reply(
    State(client): State<SharedClient>,
    Query(query): Query<ReplyQuery>,
) -> impl IntoResponse {
    match run_blocking(move || client.invoke_rpc(&query.workflow_id, SEND_TICKET_REPLY, query.text))
    {
        Ok(()) => ok_text("reply accepted"),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn resolve_ticket(
    State(client): State<SharedClient>,
    Query(query): Query<ResolveQuery>,
) -> impl IntoResponse {
    match run_blocking(move || client.invoke_rpc(&query.workflow_id, RESOLVE_TICKET, query.reason))
    {
        Ok(()) => ok_text("resolution accepted"),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn get_ticket_transcript(
    State(client): State<SharedClient>,
    Query(query): Query<WorkflowQuery>,
) -> impl IntoResponse {
    match run_blocking(move || {
        client.invoke_rpc_without_input(&query.workflow_id, GET_TICKET_TRANSCRIPT)
    }) {
        Ok(value) => ok_json(value),
        Err(error) => map_sdk_error(error).into_response(),
    }
}
