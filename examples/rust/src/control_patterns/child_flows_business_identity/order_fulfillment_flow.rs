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
    AttributeMap, ChannelMap, Client, Context, Flow, FlowStatus, HandlerError, HandlerResult,
    IdReusePolicy, PersistenceSchema, Rpc, RpcList, SdkError, StartFlowOptions, Step, StepDecision,
    StepList, StepMovement, Timer, Wait,
};
use serde::{Deserialize, Serialize};

use super::shipment_flow::{SHIPMENT_OUTCOME, Shipment, ShipmentFlow, ShipmentRequest};

/// Each shipment's own Flow timeout. A closing order never stops its shipments.
pub const SHIPMENT_TIMEOUT: Duration = Duration::from_secs(30 * 24 * 60 * 60);

/// Record status of a shipment number that another request already owns.
pub const SHIPMENT_REJECTED: &str = "rejected";
/// Record status of a shipment that does not exist.
pub const SHIPMENT_UNAVAILABLE: &str = "unavailable";

/// The input of `OrderFulfillmentFlow`. The order number is the Flow ID.
#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Order {
    pub shipments: Vec<Shipment>,
    /// How long the order waits for a report before it reads the shipment.
    pub reconcile_interval_seconds: u64,
}

/// The input of the Steps that start and track one shipment.
#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct TrackedShipment {
    pub shipment: Shipment,
    pub reconcile_interval_seconds: u64,
}

/// What a shipment sends to its order when it ends.
#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ShipmentReport {
    pub shipment_number: String,
    pub outcome: String,
}

/// The order's record of one shipment. A report fills `outcome`; a reconcile
/// read fills `status` and, when the shipment stored one, `outcome`.
#[derive(Clone, Debug, Default, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ShipmentRecord {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub status: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub outcome: Option<String>,
}

/// One record per shipment, keyed by shipment number.
pub static SHIPMENT_RECORDS: LazyLock<AttributeMap<ShipmentRecord>> =
    LazyLock::new(|| AttributeMap::new("shipmentRecords"));
/// Receives each shipment's report, keyed by shipment number.
pub static SHIPMENT_REPORTS: LazyLock<ChannelMap<String>> =
    LazyLock::new(|| ChannelMap::new("shipmentReports"));

/// Receives a shipment's report.
pub const RECORD_SHIPMENT_OUTCOME: Rpc<ShipmentReport, ()> = Rpc::new("recordShipmentOutcome");

/// Ships an order as one `ShipmentFlow` per shipment. Each shipment is a
/// top-level Flow whose Flow ID is its shipment number.
#[derive(Default)]
pub struct OrderFulfillmentFlow {
    start_shipments: StartShipments,
    start_shipment: StartShipment,
    await_shipment: AwaitShipment,
}

impl OrderFulfillmentFlow {
    /// Creates the Flow for a Worker, whose Steps start and read shipments
    /// through `client`.
    pub fn new(client: Arc<Client>) -> Self {
        Self {
            start_shipments: StartShipments,
            start_shipment: StartShipment {
                client: Some(Arc::clone(&client)),
            },
            await_shipment: AwaitShipment {
                client: Some(client),
            },
        }
    }

    /// Receives a shipment's report.
    fn record_shipment_outcome(
        &self,
        context: &mut Context,
        report: ShipmentReport,
    ) -> HandlerResult<()> {
        SHIPMENT_REPORTS.publish(context, &report.shipment_number, report.outcome)
    }
}

impl Flow for OrderFulfillmentFlow {
    type StartInput = Order;

    fn steps(&self) -> StepList<'_, Self::StartInput> {
        StepList::start(&self.start_shipments)
            .and(&self.start_shipment)
            .and(&self.await_shipment)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute_map(&SHIPMENT_RECORDS)
            .channel_map(&SHIPMENT_REPORTS)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().procedure(RECORD_SHIPMENT_OUTCOME, Self::record_shipment_outcome)
    }
}

// dex:group group-id:start group-label:"Start shipments"
// dex:explanation text:"Starts one branch per shipment in the order."
#[derive(Default)]
struct StartShipments;

impl Step for StartShipments {
    type Input = Order;

    fn execute(&self, _context: &mut Context, order: Self::Input) -> HandlerResult<StepDecision> {
        if order.shipments.is_empty() {
            return Ok(StepDecision::graceful_complete(()));
        }
        let reconcile_interval_seconds = order.reconcile_interval_seconds;
        Ok(StepDecision::go_to_many(order.shipments.into_iter().map(
            |shipment| {
                StepMovement::to(
                    &StartShipment::default(),
                    TrackedShipment {
                        shipment,
                        reconcile_interval_seconds,
                    },
                )
            },
        )))
    }
}

// dex:group group-id:start group-label:"Start shipments"
// dex:explanation text:"Starts the shipment as a top-level Flow named by its shipment number."
#[derive(Default)]
struct StartShipment {
    client: Option<Arc<Client>>,
}

impl Step for StartShipment {
    type Input = TrackedShipment;

    fn execute(&self, context: &mut Context, tracked: Self::Input) -> HandlerResult<StepDecision> {
        let client = require_client(self.client.as_ref())?;
        let order_flow_id = context.flow_id().to_string();
        let number = tracked.shipment.shipment_number.clone();
        // Derived from this order and shipment, so a retried Execute repeats the same start.
        let request_id = format!("start:{order_flow_id}:{number}");
        let request = ShipmentRequest {
            order_flow_id,
            shipment: tracked.shipment.clone(),
        };
        let options = StartFlowOptions::new()
            .timeout(SHIPMENT_TIMEOUT)
            .id_reuse_policy(IdReusePolicy::Disallow)
            .request_id(request_id)
            .ignore_already_started(true);
        match client.start_flow_with_options(&ShipmentFlow::default(), &number, request, options) {
            Ok(_run_id) => Ok(StepDecision::go_to(&AwaitShipment::default(), tracked)),
            Err(SdkError::FlowAlreadyStarted { .. }) => {
                // Another request owns this shipment number; this order does not track it.
                let record = ShipmentRecord {
                    status: Some(SHIPMENT_REJECTED.to_string()),
                    outcome: None,
                };
                SHIPMENT_RECORDS.set(context, &number, record)?;
                Ok(StepDecision::graceful_complete(()))
            }
            Err(error) => Err(HandlerError::from_error(error)),
        }
    }
}

// dex:group group-id:track group-label:"Track shipment"
// dex:explanation text:"Records the shipment's report, or reads the shipment when no report arrives in time."
#[derive(Default)]
struct AwaitShipment {
    client: Option<Arc<Client>>,
}

impl Step for AwaitShipment {
    type Input = TrackedShipment;

    fn wait_for(&self, _context: &mut Context, tracked: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::any_of([
            SHIPMENT_REPORTS.for_one(&tracked.shipment.shipment_number),
            Timer::by_duration(Duration::from_secs(tracked.reconcile_interval_seconds)),
        ]))
    }

    fn execute(&self, context: &mut Context, tracked: Self::Input) -> HandlerResult<StepDecision> {
        let number = tracked.shipment.shipment_number.clone();
        let reported = SHIPMENT_REPORTS
            .condition_results(context, &number)?
            .into_iter()
            .next();
        let record = match reported {
            Some(outcome) => ShipmentRecord {
                status: None,
                outcome: Some(outcome),
            },
            None => match reconcile_shipment(require_client(self.client.as_ref())?, &number)? {
                ShipmentReading::Running => {
                    return Ok(StepDecision::go_to(&AwaitShipment::default(), tracked));
                }
                ShipmentReading::Closed(record) => record,
            },
        };
        SHIPMENT_RECORDS.set(context, &number, record)?;
        Ok(StepDecision::graceful_complete(()))
    }
}

/// What a reconcile read found.
enum ShipmentReading {
    Running,
    Closed(ShipmentRecord),
}

/// Reads a shipment that has not reported: its status first, then its outcome.
///
/// Returns `Running` while the shipment runs, an "unavailable" record when the
/// shipment does not exist, and an error for any other failure so the Step retries.
fn reconcile_shipment(client: &Client, number: &str) -> HandlerResult<ShipmentReading> {
    let status = match client.describe_flow(number) {
        Ok(info) => info.status,
        Err(SdkError::FlowNotFound { .. }) => {
            return Ok(ShipmentReading::Closed(ShipmentRecord {
                status: Some(SHIPMENT_UNAVAILABLE.to_string()),
                outcome: None,
            }));
        }
        Err(error) => return Err(HandlerError::from_error(error)),
    };
    if status == FlowStatus::Running {
        return Ok(ShipmentReading::Running);
    }
    // An unset outcome Attribute leaves the outcome empty: the shipment ended without one.
    let outcome = match client.get_attributes(number, (&*SHIPMENT_OUTCOME,)) {
        Ok((outcome,)) => outcome,
        Err(SdkError::FlowNotFound { .. }) => None,
        Err(error) => return Err(HandlerError::from_error(error)),
    };
    Ok(ShipmentReading::Closed(ShipmentRecord {
        status: Some(flow_status_name(status).to_string()),
        outcome,
    }))
}

/// Returns the lowercase name of a Flow status, as the Go SDK prints it.
pub fn flow_status_name(status: FlowStatus) -> &'static str {
    match status {
        FlowStatus::Running => "running",
        FlowStatus::Completed => "completed",
        FlowStatus::Failed => "failed",
        FlowStatus::Canceled => "canceled",
        FlowStatus::Terminated => "terminated",
        FlowStatus::ServerSideTimeoutInternalOnly => "server-side timeout (internal only)",
    }
}

pub(super) fn require_client(client: Option<&Arc<Client>>) -> HandlerResult<&Client> {
    client
        .map(Arc::as_ref)
        .ok_or_else(|| HandlerError::new("ClientUnavailable", "the Dex Client is not ready"))
}
