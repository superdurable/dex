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
use serde_json::json;

use crate::products::subscription::flow::{
    SUBSCRIPTION_CANCEL, SUBSCRIPTION_DESCRIBE, SUBSCRIPTION_UPDATE_CHARGE, SubscriptionFlow,
    SubscriptionRequest,
};
use crate::server::helpers::{
    SharedClient, StartResponse, map_sdk_error, new_flow_id, ok_json, run_blocking,
};

#[derive(Deserialize)]
struct StartQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
}

#[derive(Deserialize)]
struct WorkflowQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
}

#[derive(Deserialize)]
struct UpdateQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
    #[serde(default, rename = "newChargeAmount")]
    new_charge_amount: i64,
}

pub fn mount(client: SharedClient) -> Router {
    Router::new()
        .route("/products/subscription/start", get(start))
        .route("/products/subscription/cancel", get(cancel))
        .route(
            "/products/subscription/updateChargeAmount",
            get(update_charge),
        )
        .route("/products/subscription/describe", get(describe))
        .with_state(client)
}

async fn start(
    State(client): State<SharedClient>,
    Query(query): Query<StartQuery>,
) -> impl IntoResponse {
    let flow_id = if query.workflow_id.is_empty() {
        new_flow_id("subscription")
    } else {
        query.workflow_id
    };
    match run_blocking(move || {
        let flow = SubscriptionFlow::default();
        let input = SubscriptionRequest {
            customer_id: "customer-1".into(),
            charge_cents: 1000,
            billing_periods: 3,
        };
        client
            .start_flow(&flow, &flow_id, input)
            .map(|run_id| StartResponse { flow_id, run_id })
    }) {
        Ok(value) => ok_json(value),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn cancel(
    State(client): State<SharedClient>,
    Query(query): Query<WorkflowQuery>,
) -> impl IntoResponse {
    let flow_id = query.workflow_id;
    match run_blocking(move || client.invoke_rpc_without_input(&flow_id, SUBSCRIPTION_CANCEL)) {
        Ok(()) => ok_json(json!({})),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn update_charge(
    State(client): State<SharedClient>,
    Query(query): Query<UpdateQuery>,
) -> impl IntoResponse {
    let flow_id = query.workflow_id;
    let amount = query.new_charge_amount;
    match run_blocking(move || client.invoke_rpc(&flow_id, SUBSCRIPTION_UPDATE_CHARGE, amount)) {
        Ok(()) => ok_json(json!({})),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

async fn describe(
    State(client): State<SharedClient>,
    Query(query): Query<WorkflowQuery>,
) -> impl IntoResponse {
    let flow_id = query.workflow_id;
    match run_blocking(move || client.invoke_rpc_without_input(&flow_id, SUBSCRIPTION_DESCRIBE)) {
        Ok(value) => ok_json(value),
        Err(error) => map_sdk_error(error).into_response(),
    }
}
