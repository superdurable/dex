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
use std::time::Duration;

use crate::patterns::wait_for_state_completion::flow::{
    PersistRequest, WaitForStateCompletionFlow,
};
use crate::server::helpers::{
    SharedClient, StartResponse, map_sdk_error, new_flow_id, ok_json, run_blocking,
};

#[derive(Deserialize)]
struct StartQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
}

pub fn mount(client: SharedClient) -> Router {
    Router::new()
        .route("/patterns/wait-for-state-completion/start", get(start))
        .with_state(client)
}

async fn start(
    State(client): State<SharedClient>,
    Query(query): Query<StartQuery>,
) -> impl IntoResponse {
    let flow_id = if query.workflow_id.is_empty() {
        new_flow_id("wait-state")
    } else {
        query.workflow_id
    };
    match run_blocking(move || {
        let flow = WaitForStateCompletionFlow::default();
        let input = PersistRequest {
            record_id: "1".into(),
            payload: "Test Resume".into(),
        };
        let run_id = client.start_flow(&flow, &flow_id, input)?;
        client.wait_for_step_completion(
            &flow_id,
            WaitForStateCompletionFlow::persisted_step(),
            Duration::from_secs(300),
        )?;
        Ok(StartResponse { flow_id, run_id })
    }) {
        Ok(value) => ok_json(value),
        Err(error) => map_sdk_error(error).into_response(),
    }
}
