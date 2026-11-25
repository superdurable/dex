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

use std::collections::HashSet;
use std::sync::{Arc, LazyLock};
use std::time::Duration;

use dex_sdk::{
    Attribute, AttributeMap, Client, Context, Flow, FlowStatus, HandlerError, HandlerResult,
    IdReusePolicy, PersistenceSchema, Rpc, RpcList, SdkError, StartFlowOptions, Step, StepDecision,
    StepList, StepMovement, StepOptions, Timer, Wait,
};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};

use super::shipment_flow::{SHIPMENT_OUTCOME, Shipment, ShipmentFlow, ShipmentRequest};

/// Each shipment's own Flow timeout. A closing order never stops its shipments.
pub const SHIPMENT_TIMEOUT: Duration = Duration::from_secs(30 * 24 * 60 * 60);

/// Record status of a shipment number that another request already owns.
pub const SHIPMENT_REJECTED: &str = "rejected";
/// Record status of a shipment that does not exist.
pub const SHIPMENT_UNAVAILABLE: &str = "unavailable";

const DUPLICATE_SHIPMENT_PREFIX: &str = "duplicate shipment number: ";

/// The input of `OrderFulfillmentFlow`. The order number is the Flow ID.
#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Order {
    pub shipments: Vec<Shipment>,
    /// How often the order reads a shipment that has not ended.
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

/// The order's record of one shipment. A report or a reconcile read fills
/// `outcome`; a reconcile read fills `status`.
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
/// Serializes the report RPC and `AwaitShipment` writes; it stores no value.
pub static SHIPMENT_RECORDS_LOCK: LazyLock<Attribute<bool>> =
    LazyLock::new(|| Attribute::new("shipmentRecordsLock"));

/// Fills the outcome of a shipment's record.
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

    /// Fills the outcome of a shipment's record. The first report wins; a
    /// repeated or late report changes nothing.
    fn record_shipment_outcome(
        &self,
        context: &mut Context,
        report: ShipmentReport,
    ) -> HandlerResult<()> {
        let mut record = load_shipment_record(context, &report.shipment_number)?;
        if record.outcome.is_some() {
            return Ok(());
        }
        record.outcome = Some(report.outcome);
        SHIPMENT_RECORDS.set(context, &report.shipment_number, record)
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
            .attribute(&SHIPMENT_RECORDS_LOCK)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().procedure(
            RECORD_SHIPMENT_OUTCOME
                .lock(SHIPMENT_RECORDS_LOCK.lock())
                .load_attribute_map(&SHIPMENT_RECORDS),
            Self::record_shipment_outcome,
        )
    }
}

// dex:group group-id:start group-label:"Start shipments"
// dex:explanation text:"Starts one branch per shipment, after checking that shipment numbers are unique."
#[derive(Default)]
struct StartShipments;

impl Step for StartShipments {
    type Input = Order;

    fn execute(&self, _context: &mut Context, order: Self::Input) -> HandlerResult<StepDecision> {
        if order.shipments.is_empty() {
            return Ok(StepDecision::graceful_complete(()));
        }
        let mut numbers = HashSet::with_capacity(order.shipments.len());
        for shipment in &order.shipments {
            if !numbers.insert(shipment.shipment_number.as_str()) {
                return Ok(StepDecision::force_fail(format!(
                    "{DUPLICATE_SHIPMENT_PREFIX}{}",
                    shipment.shipment_number
                )));
            }
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

/// Derives the start request ID from the complete start request.
///
/// A retried Execute sends the same ID, so it repeats the same start. Any other
/// request for the same shipment number sends another ID, so Dex rejects it as
/// already started.
pub fn shipment_start_request_id(order_flow_id: &str, shipment: &Shipment) -> String {
    let request = serde_json::to_vec(&(
        ShipmentFlow::default().flow_type(),
        &shipment.shipment_number,
        order_flow_id,
        &shipment.destination,
        shipment.delivery_delay_seconds,
        SHIPMENT_TIMEOUT.as_secs(),
        "disallow",
    ))
    .expect("a tuple of strings and integers always encodes as JSON");
    format!("start:{:x}", Sha256::digest(&request))
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
        let request_id = shipment_start_request_id(&order_flow_id, &tracked.shipment);
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
// dex:explanation text:"Reads the shipment at each interval and records how it ended."
#[derive(Default)]
struct AwaitShipment {
    client: Option<Arc<Client>>,
}

impl Step for AwaitShipment {
    type Input = TrackedShipment;

    /// Takes the same lock as the report RPC and loads the records.
    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .execute_lock(SHIPMENT_RECORDS_LOCK.lock())
            .execute_load_attribute_map(&SHIPMENT_RECORDS)
    }

    fn wait_for(&self, _context: &mut Context, tracked: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(Timer::by_duration(Duration::from_secs(
            tracked.reconcile_interval_seconds,
        ))))
    }

    fn execute(&self, context: &mut Context, tracked: Self::Input) -> HandlerResult<StepDecision> {
        let client = require_client(self.client.as_ref())?;
        let number = tracked.shipment.shipment_number.clone();
        let reading = match reconcile_shipment(client, &number)? {
            ShipmentReading::Running => {
                return Ok(StepDecision::go_to(&AwaitShipment::default(), tracked));
            }
            ShipmentReading::Closed(reading) => reading,
        };
        // Merge with the record, which may already hold the reported outcome.
        let mut record = load_shipment_record(context, &number)?;
        record.status = reading.status;
        if record.outcome.is_none() {
            record.outcome = reading.outcome;
        }
        SHIPMENT_RECORDS.set(context, &number, record)?;
        Ok(StepDecision::graceful_complete(()))
    }
}

/// Returns the loaded record of a shipment, or an empty record when it has none.
///
/// Returns an error when the record cannot be read.
fn load_shipment_record(context: &Context, number: &str) -> HandlerResult<ShipmentRecord> {
    Ok(SHIPMENT_RECORDS.get(context, number)?.unwrap_or_default())
}

/// What a reconcile read found.
enum ShipmentReading {
    Running,
    Closed(ShipmentRecord),
}

/// Reads a shipment: its status first, then its outcome.
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
