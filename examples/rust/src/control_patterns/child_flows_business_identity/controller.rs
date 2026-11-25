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

use std::sync::Arc;
use std::time::Duration;

use axum::{
    Json, Router,
    extract::{Query, State},
    http::StatusCode,
    response::{IntoResponse, Response},
    routing::get,
};
use dex_sdk::{Flow, FlowInfo, SdkError, StartFlowOptions, StopFlowOptions};
use serde::{Deserialize, Serialize};

use super::{
    OrderFulfillmentFlow, SHIPMENT_OUTCOME, Shipment, ShipmentFlow, flow_status_name,
    order_fulfillment_flow::Order,
};
use crate::server::helpers::{
    ErrorBody, SharedClient, StartResponse, map_sdk_error, ok_json, ok_text, run_blocking,
};

const ORDER_TIMEOUT: Duration = Duration::from_secs(40 * 24 * 60 * 60);

#[derive(Deserialize)]
struct OrderQuery {
    #[serde(default, rename = "workflowId")]
    workflow_id: String,
}

#[derive(Deserialize)]
struct ShipmentQuery {
    #[serde(default, rename = "shipmentNumber")]
    shipment_number: String,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct ShipmentView {
    shipment_number: String,
    status: String,
    outcome: String,
}

/// Serves the order routes and the shipment routes that find a shipment by its number.
pub fn mount(client: SharedClient) -> Router {
    Router::new()
        .route(
            "/patterns/child-flows-business-identity/start",
            get(start_order),
        )
        .route(
            "/patterns/child-flows-business-identity/shipment",
            get(get_shipment),
        )
        .route(
            "/patterns/child-flows-business-identity/cancel-shipment",
            get(cancel_shipment),
        )
        .with_state(client)
}

/// Starts an order with two shipments: one delivers in seconds, one takes a day.
async fn start_order(
    State(client): State<SharedClient>,
    Query(query): Query<OrderQuery>,
) -> Response {
    let order_id = query.workflow_id;
    if order_id.is_empty() {
        return error_response(StatusCode::BAD_REQUEST, "workflowId is required");
    }
    let order = Order {
        shipments: vec![
            shipment(&order_id, 1, "Seattle", 5),
            shipment(&order_id, 2, "Portland", 86_400),
        ],
        reconcile_interval_seconds: 60,
    };
    match run_blocking(move || {
        client
            .start_flow_with_options(
                &OrderFulfillmentFlow::default(),
                &order_id,
                order,
                StartFlowOptions::new().timeout(ORDER_TIMEOUT),
            )
            .map(|run_id| StartResponse {
                flow_id: order_id,
                run_id,
            })
    }) {
        Ok(started) => ok_json(started),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

/// Looks a shipment up by its shipment number, which is its Flow ID.
async fn get_shipment(
    State(client): State<SharedClient>,
    Query(query): Query<ShipmentQuery>,
) -> Response {
    let (number, info) = match describe_shipment(&client, query).await {
        Ok(described) => described,
        Err(response) => return response,
    };
    let lookup_number = number.clone();
    let lookup = run_blocking(move || client.get_attributes(&lookup_number, (&*SHIPMENT_OUTCOME,)));
    match lookup {
        Ok((outcome,)) => ok_json(ShipmentView {
            shipment_number: number,
            status: flow_status_name(info.status).to_string(),
            outcome: outcome.unwrap_or_default(),
        }),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

/// Stops a shipment by its number. Its order records it at the next reconcile read.
async fn cancel_shipment(
    State(client): State<SharedClient>,
    Query(query): Query<ShipmentQuery>,
) -> Response {
    let (number, _info) = match describe_shipment(&client, query).await {
        Ok(described) => described,
        Err(response) => return response,
    };
    match run_blocking(move || {
        client.stop_flow(
            &number,
            StopFlowOptions::cancel().reason("shipment canceled"),
        )
    }) {
        Ok(()) => ok_text("canceled"),
        Err(error) => map_sdk_error(error).into_response(),
    }
}

/// Describes the Flow named by the `shipmentNumber` query.
///
/// Returns the shipment number and its Flow info, or the error response to send:
/// 404 when no `ShipmentFlow` has that Flow ID.
async fn describe_shipment(
    client: &SharedClient,
    query: ShipmentQuery,
) -> Result<(String, FlowInfo), Response> {
    let number = query.shipment_number;
    if number.is_empty() {
        return Err(error_response(
            StatusCode::BAD_REQUEST,
            "shipmentNumber is required",
        ));
    }
    let lookup_client = Arc::clone(client);
    let lookup_number = number.clone();
    match run_blocking(move || lookup_client.describe_flow(&lookup_number)) {
        Ok(info) if info.flow_type == ShipmentFlow::default().flow_type() => Ok((number, info)),
        Ok(_) | Err(SdkError::FlowNotFound { .. }) => Err(error_response(
            StatusCode::NOT_FOUND,
            &format!("shipment {number} not found"),
        )),
        Err(error) => Err(map_sdk_error(error).into_response()),
    }
}

fn shipment(
    order_id: &str,
    index: u32,
    destination: &str,
    delivery_delay_seconds: u64,
) -> Shipment {
    Shipment {
        shipment_number: format!("{order_id}-{index}"),
        destination: destination.to_string(),
        delivery_delay_seconds,
    }
}

fn error_response(status: StatusCode, message: &str) -> Response {
    (
        status,
        Json(ErrorBody {
            error: message.to_string(),
        }),
    )
        .into_response()
}
