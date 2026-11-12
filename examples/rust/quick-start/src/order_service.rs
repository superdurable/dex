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

//! Starts orders over HTTP: POST /orders with a JSON body such as {"orderId": "order-1"}.

use std::sync::Arc;
use std::time::Duration;

use axum::extract::State;
use axum::http::StatusCode;
use axum::routing::post;
use axum::{Json, Router};
use dex_sdk::{
    Client, SdkError, SdkResult, StartFlowOptions, StepExecutionId, WaitForStepCompletionOptions,
};
use serde_json::{Value, json};

use crate::order_flow::OrderFlow;

/// Starts the order's Flow once, however often the request is retried. Returns after Charge.
pub fn start_order(client: &Client, order_flow: &OrderFlow, order_id: &str) -> SdkResult<()> {
    let options = StartFlowOptions::new()
        .request_id(format!("start-{order_id}"))
        .ignore_already_started(true);
    client.start_flow_with_options(order_flow, order_id, order_id.to_string(), options)?;
    client.wait_for_step_completion(
        order_id,
        StepExecutionId::of(&order_flow.charge),
        WaitForStepCompletionOptions::new().request_timeout(Duration::from_secs(30)),
    )
}

/// The Client and the Flow definition that the HTTP handler uses.
#[derive(Clone)]
pub struct OrderService {
    pub client: Arc<Client>,
    pub order_flow: OrderFlow,
}

/// Returns the routes of the service.
pub fn order_routes(service: OrderService) -> Router {
    Router::new()
        .route("/orders", post(create_order))
        .with_state(service)
}

async fn create_order(
    State(service): State<OrderService>,
    Json(body): Json<Value>,
) -> (StatusCode, Json<Value>) {
    let Some(order_id) = body
        .get("orderId")
        .and_then(Value::as_str)
        .map(str::to_string)
    else {
        return (
            StatusCode::BAD_REQUEST,
            Json(json!({"error": "orderId is required"})),
        );
    };
    // The Client blocks, so the call runs off the async runtime.
    let started_order_id = order_id.clone();
    let started = tokio::task::spawn_blocking(move || {
        start_order(&service.client, &service.order_flow, &started_order_id)
    })
    .await;
    match started {
        Ok(Ok(())) => (
            StatusCode::OK,
            Json(json!({"flowId": order_id, "charged": true})),
        ),
        Ok(Err(SdkError::FlowAlreadyStarted { .. })) => (
            StatusCode::CONFLICT,
            Json(json!({"error": format!("order {order_id} was already started")})),
        ),
        Ok(Err(error)) => (
            StatusCode::INTERNAL_SERVER_ERROR,
            Json(json!({"error": error.to_string()})),
        ),
        Err(join_error) => (
            StatusCode::INTERNAL_SERVER_ERROR,
            Json(json!({"error": join_error.to_string()})),
        ),
    }
}
