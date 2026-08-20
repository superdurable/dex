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
use dex_sdk::{
    Client, FlowConfig, FlowTimeoutPolicy, IdReusePolicy, RetryPolicy, SdkResult, StartFlowOptions,
    StepDurability, WorkerTarget,
};
use serde::Deserialize;
use std::time::Duration;

use crate::primitives::flow::flow::{ExampleFlow, status};
use crate::server::helpers::{SharedClient, StartResponse, map_sdk_error, ok_json, run_blocking};

#[derive(Deserialize)]
struct StartQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
    #[serde(default, rename = "inputNum")]
    input_num: i32,
}

pub fn mount(client: SharedClient) -> Router {
    Router::new()
        .route("/primitives/flow/start", get(start))
        .with_state(client)
}

fn start_flow_options() -> StartFlowOptions {
    StartFlowOptions::new()
        .timeout(Duration::from_secs(3_600))
        .config_override(FlowConfig::new().step_durability(StepDurability::Sync))
}

pub fn example_start_flow_options() -> StartFlowOptions {
    StartFlowOptions::new()
        .timeout(Duration::from_secs(30 * 60))
        .timeout_policy(FlowTimeoutPolicy::Handler)
        .start_delay(Duration::from_secs(5 * 60))
        .id_reuse_policy(IdReusePolicy::Disallow)
        .retry_policy(
            RetryPolicy::new()
                .initial_interval(Duration::from_secs(60))
                .backoff_coefficient(2.0)
                .maximum_interval(Duration::from_secs(10 * 60))
                .maximum_attempts(3),
        )
        .initial_attribute(&status(), "queued".to_owned())
        .config_override(FlowConfig::new().step_durability(StepDurability::Sync))
        .ignore_already_started(true)
        .request_id("start-order-123")
}

pub fn reroute_active_flow(client: &Client, flow_id: &str) -> SdkResult<()> {
    client.update_flow_config(
        flow_id,
        FlowConfig::new().worker_target(WorkerTarget::new("worker-canary:8803")),
    )
}

async fn start(
    State(client): State<SharedClient>,
    Query(query): Query<StartQuery>,
) -> impl IntoResponse {
    let flow_id = query.workflow_id;
    let input_num = query.input_num;
    match run_blocking(move || {
        let flow = ExampleFlow::default();
        client
            .start_flow_with_options(&flow, &flow_id, input_num, start_flow_options())
            .map(|run_id| StartResponse { flow_id, run_id })
    }) {
        Ok(response) => ok_json(response),
        Err(error) => map_sdk_error(error).into_response(),
    }
}
