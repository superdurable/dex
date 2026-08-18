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
    response::IntoResponse,
    routing::get,
};
use dex_sdk::StepExecutionId;
use serde::Deserialize;
use serde_json::json;

use crate::products::order_processing::flow::{
    Charge, ORDER_APPROVE, ORDER_DESCRIBE, OrderProcessingFlow, OrderRequest,
};
use crate::server::helpers::{
    SharedClient, StartResponse, map_sdk_error, new_flow_id, ok_json, run_blocking,
};

#[derive(Deserialize)]
struct StartQuery {
    #[serde(default, rename = "failShip")]
    fail_ship: bool,
}

#[derive(Deserialize)]
struct WorkflowQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
    #[serde(default)]
    notes: String,
}

pub fn mount(client: SharedClient) -> Router {
    Router::new()
        .route("/products/order-processing/start", get(start))
        .route("/products/order-processing/approve", get(approve))
        .route("/products/order-processing/describe", get(describe))
        .with_state(client)
}

async fn start(
    State(client): State<SharedClient>,
    Query(query): Query<StartQuery>,
) -> impl IntoResponse {
    match run_blocking(move || {
        let flow_id = new_flow_id("order-processing");
        let flow = OrderProcessingFlow::default();
        let input = OrderRequest {
            order_id: flow_id.clone(),
            email: "buyer@example.com".into(),
            customer_id: "customer-1".into(),
            amount: 42,
            fail_ship: query.fail_ship,
        };
        let run_id = client.start_flow(&flow, &flow_id, input)?;
        client.wait_for_step_completion(
            &flow_id,
            StepExecutionId::of(&Charge),
            Duration::from_secs(300),
        )?;
        Ok(StartResponse { flow_id, run_id })
    }) {
        Ok(value) => ok_json(value),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn approve(
    State(client): State<SharedClient>,
    Query(query): Query<WorkflowQuery>,
) -> impl IntoResponse {
    let flow_id = query.workflow_id;
    let notes = query.notes;
    match run_blocking(move || client.invoke_rpc(&flow_id, ORDER_APPROVE, notes)) {
        Ok(value) => ok_json(value),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn describe(
    State(client): State<SharedClient>,
    Query(query): Query<WorkflowQuery>,
) -> impl IntoResponse {
    let flow_id = query.workflow_id;
    match run_blocking(move || {
        client
            .invoke_rpc_without_input(&flow_id, ORDER_DESCRIBE)
            .map(|status| json!({ "flowID": flow_id, "status": status }))
    }) {
        Ok(value) => ok_json(value),
        Err(error) => map_sdk_error(error).into_response(),
    }
}
