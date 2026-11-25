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

use std::sync::{Arc, LazyLock};
use std::time::Duration;

use dex_sdk::{
    Attribute, Client, Context, Flow, HandlerError, HandlerResult, PersistenceSchema, SdkError,
    Step, StepDecision, StepList, Timer, Wait,
};
use serde::{Deserialize, Serialize};

use super::order_fulfillment_flow::{RECORD_SHIPMENT_OUTCOME, ShipmentReport, require_client};

/// One parcel of an order, identified by its shipment number.
#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Shipment {
    pub shipment_number: String,
    pub destination: String,
    /// Simulates how long the carrier takes to deliver.
    pub delivery_delay_seconds: u64,
}

/// The input of `ShipmentFlow`.
#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ShipmentRequest {
    pub order_flow_id: String,
    pub shipment: Shipment,
}

/// The report a shipment still has to send to its order.
#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct PendingReport {
    pub order_flow_id: String,
    pub report: ShipmentReport,
}

/// The shipment's committed outcome. The order reads it when no report arrives.
pub static SHIPMENT_OUTCOME: LazyLock<Attribute<String>> =
    LazyLock::new(|| Attribute::new("shipmentOutcome"));

/// Delivers one shipment. Its Flow ID is the shipment number, so people and
/// systems can look it up, retry it, or stop it by that number.
#[derive(Default)]
pub struct ShipmentFlow {
    deliver_shipment: DeliverShipment,
    report_shipment_outcome: ReportShipmentOutcome,
}

impl ShipmentFlow {
    /// Creates the Flow for a Worker, whose report Step calls the order
    /// through `client`.
    pub fn new(client: Arc<Client>) -> Self {
        Self {
            deliver_shipment: DeliverShipment,
            report_shipment_outcome: ReportShipmentOutcome {
                client: Some(client),
            },
        }
    }
}

impl Flow for ShipmentFlow {
    type StartInput = ShipmentRequest;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.deliver_shipment).and(&self.report_shipment_outcome)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new().attribute(&SHIPMENT_OUTCOME)
    }
}

// dex:group group-id:deliver group-label:"Deliver"
// dex:explanation text:"Waits for the carrier, then commits the delivered outcome."
#[derive(Default)]
struct DeliverShipment;

impl Step for DeliverShipment {
    type Input = ShipmentRequest;

    fn wait_for(&self, _context: &mut Context, request: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(Timer::by_duration(Duration::from_secs(
            request.shipment.delivery_delay_seconds,
        ))))
    }

    /// Keeps the default SYNC durability, so the outcome is stored before the report Step starts.
    fn execute(&self, context: &mut Context, request: Self::Input) -> HandlerResult<StepDecision> {
        let outcome = format!("delivered to {}", request.shipment.destination);
        SHIPMENT_OUTCOME.set(context, outcome.clone())?;
        Ok(StepDecision::go_to(
            &ReportShipmentOutcome::default(),
            PendingReport {
                order_flow_id: request.order_flow_id,
                report: ShipmentReport {
                    shipment_number: request.shipment.shipment_number,
                    outcome,
                },
            },
        ))
    }
}

// dex:group group-id:report group-label:"Report"
// dex:explanation text:"Reports the committed outcome to the order that started this shipment."
#[derive(Default)]
struct ReportShipmentOutcome {
    client: Option<Arc<Client>>,
}

impl Step for ReportShipmentOutcome {
    type Input = PendingReport;

    fn execute(&self, _context: &mut Context, pending: Self::Input) -> HandlerResult<StepDecision> {
        let client = require_client(self.client.as_ref())?;
        let outcome = pending.report.outcome.clone();
        // Rust SDK 1.45.x adds an RPC request ID; this pin predates it, so a retried report can publish twice.
        match client.invoke_rpc(
            &pending.order_flow_id,
            RECORD_SHIPMENT_OUTCOME,
            pending.report,
        ) {
            // A closed order ends the attempts; it reads the shipment's outcome instead.
            Ok(())
            | Err(SdkError::FlowNotFound { .. } | SdkError::FlowNotActiveOrNotFound { .. }) => {
                Ok(StepDecision::graceful_complete(outcome))
            }
            Err(error) => Err(HandlerError::from_error(error)),
        }
    }
}
