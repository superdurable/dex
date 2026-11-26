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

use std::collections::{BTreeMap, HashSet};
use std::sync::{Arc, LazyLock};
use std::time::Duration;

use dex_sdk::{
    Attribute, AttributeMap, Client, Context, Flow, FlowInfo, FlowStatus, HandlerError,
    HandlerResult, IdReusePolicy, PersistenceSchema, Rpc, RpcList, SdkError, StartFlowOptions,
    Step, StepDecision, StepList, StepMovement, StepOptions, Timer, Wait,
};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};

use super::shipment_flow::{SHIPMENT_OUTCOME, Shipment, ShipmentFlow, ShipmentRequest};

/// Each shipment's own Flow timeout. A closing order never stops its shipments.
pub const SHIPMENT_TIMEOUT: Duration = Duration::from_secs(30 * 24 * 60 * 60);

/// Record status of a shipment of the order that has no claimed run yet.
pub const SHIPMENT_STARTING: &str = "starting";
/// Record status of a shipment whose run this order started and claimed.
pub const SHIPMENT_STARTED: &str = "started";
/// Record status of a shipment number that another request already owns.
pub const SHIPMENT_REJECTED: &str = "rejected";
/// Record status of a claimed run that can no longer be read.
pub const SHIPMENT_UNAVAILABLE: &str = "unavailable";

const DUPLICATE_SHIPMENT_PREFIX: &str = "duplicate shipment number: ";
const NEGATIVE_DELIVERY_DELAY_PREFIX: &str = "negative delivery delay for shipment: ";
const INVALID_RECONCILE_INTERVAL_MESSAGE: &str = "reconcile interval must be positive";

/// The input of `OrderFulfillmentFlow`. The order number is the Flow ID.
#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Order {
    pub shipments: Vec<Shipment>,
    /// How often the order reads a shipment that has not ended.
    pub reconcile_interval_seconds: i64,
}

/// The input of the Steps that start and track one shipment.
#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct TrackedShipment {
    pub shipment: Shipment,
    pub reconcile_interval_seconds: i64,
    /// The run this order started and tracks; empty before the start.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub shipment_run_id: Option<String>,
}

/// What `StartShipment` hands to `ClaimShipment`.
#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct StartedShipment {
    pub tracked: TrackedShipment,
    /// True when another request owns the shipment number.
    #[serde(default, skip_serializing_if = "std::ops::Not::not")]
    pub rejected: bool,
}

/// What a shipment run sends to its order when it ends.
#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ShipmentReport {
    pub shipment_number: String,
    pub shipment_run_id: String,
    pub outcome: String,
}

/// The order's record of one shipment. `run_id` is the claimed run. A report
/// or a reconcile read fills `outcome`; a reconcile read fills the terminal `status`.
#[derive(Clone, Debug, Default, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ShipmentRecord {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub status: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub run_id: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub outcome: Option<String>,
}

/// A claimed shipment run that ended, with what the order read from it.
#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct EndedShipment {
    pub shipment_number: String,
    pub shipment_run_id: String,
    pub reading: ShipmentRecord,
}

/// One record per shipment, keyed by shipment number.
pub static SHIPMENT_RECORDS: LazyLock<AttributeMap<ShipmentRecord>> =
    LazyLock::new(|| AttributeMap::new("shipmentRecords"));
/// Reports that arrive before the claim, keyed by shipment number. Each value
/// maps a shipment run ID to that run's first report.
pub static PENDING_SHIPMENT_REPORTS: LazyLock<AttributeMap<BTreeMap<String, ShipmentReport>>> =
    LazyLock::new(|| AttributeMap::new("pendingShipmentReports"));
/// Serializes every write to `SHIPMENT_RECORDS` and `PENDING_SHIPMENT_REPORTS`; it stores no value.
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
    claim_shipment: ClaimShipment,
    await_shipment: AwaitShipment,
    record_shipment_end: RecordShipmentEnd,
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
            claim_shipment: ClaimShipment,
            await_shipment: AwaitShipment {
                client: Some(client),
            },
            record_shipment_end: RecordShipmentEnd,
        }
    }

    /// Fills the outcome of a shipment's record. It accepts only the run this
    /// order claimed; the first report wins, and any other report changes nothing.
    /// Before the claim, it keeps the first report of each run for
    /// `ClaimShipment` to merge.
    ///
    /// Returns an error only when the records cannot be read or written.
    fn record_shipment_outcome(
        &self,
        context: &mut Context,
        report: ShipmentReport,
    ) -> HandlerResult<()> {
        // Ignore an unknown shipment and a report without a run.
        let Some(mut record) = load_shipment_record(context, &report.shipment_number)? else {
            return Ok(());
        };
        if report.shipment_run_id.is_empty() {
            return Ok(());
        }
        if record.status.as_deref() == Some(SHIPMENT_STARTING) {
            let mut pending = PENDING_SHIPMENT_REPORTS
                .get(context, &report.shipment_number)?
                .unwrap_or_default();
            if !pending.contains_key(&report.shipment_run_id) {
                let number = report.shipment_number.clone();
                pending.insert(report.shipment_run_id.clone(), report);
                PENDING_SHIPMENT_REPORTS.set(context, &number, pending)?;
            }
            return Ok(());
        }
        // Ignore a rejected shipment, another run, and a repeated report.
        if record.run_id.as_deref() != Some(report.shipment_run_id.as_str())
            || record.outcome.is_some()
        {
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
            .and(&self.claim_shipment)
            .and(&self.await_shipment)
            .and(&self.record_shipment_end)
    }

    fn persistence(&self) -> PersistenceSchema {
        PersistenceSchema::new()
            .attribute_map(&SHIPMENT_RECORDS)
            .attribute_map(&PENDING_SHIPMENT_REPORTS)
            .attribute(&SHIPMENT_RECORDS_LOCK)
    }

    fn rpcs(&self) -> RpcList<Self> {
        RpcList::new().procedure(
            RECORD_SHIPMENT_OUTCOME
                .lock(SHIPMENT_RECORDS_LOCK.lock())
                .load_attribute_map(&SHIPMENT_RECORDS)
                .load_attribute_map(&PENDING_SHIPMENT_REPORTS),
            Self::record_shipment_outcome,
        )
    }
}

// dex:group group-id:start group-label:"Start shipments"
// dex:explanation text:"Checks the order, registers its shipment numbers, then starts one branch per shipment."
#[derive(Default)]
struct StartShipments;

impl Step for StartShipments {
    type Input = Order;

    /// Takes the records lock, so registration never races a report.
    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new().execute_lock(SHIPMENT_RECORDS_LOCK.lock())
    }

    fn execute(&self, context: &mut Context, order: Self::Input) -> HandlerResult<StepDecision> {
        if order.reconcile_interval_seconds <= 0 {
            return Ok(StepDecision::force_fail(INVALID_RECONCILE_INTERVAL_MESSAGE));
        }
        if order.shipments.is_empty() {
            return Ok(StepDecision::graceful_complete(()));
        }
        let mut numbers = HashSet::with_capacity(order.shipments.len());
        for shipment in &order.shipments {
            if shipment.delivery_delay_seconds < 0 {
                return Ok(StepDecision::force_fail(format!(
                    "{NEGATIVE_DELIVERY_DELAY_PREFIX}{}",
                    shipment.shipment_number
                )));
            }
            if !numbers.insert(shipment.shipment_number.as_str()) {
                return Ok(StepDecision::force_fail(format!(
                    "{DUPLICATE_SHIPMENT_PREFIX}{}",
                    shipment.shipment_number
                )));
            }
        }
        let mut movements = Vec::with_capacity(order.shipments.len());
        for shipment in order.shipments {
            let record = ShipmentRecord {
                status: Some(SHIPMENT_STARTING.to_string()),
                ..ShipmentRecord::default()
            };
            SHIPMENT_RECORDS.set(context, &shipment.shipment_number, record)?;
            let tracked = TrackedShipment {
                shipment,
                reconcile_interval_seconds: order.reconcile_interval_seconds,
                shipment_run_id: None,
            };
            movements.push(StepMovement::to(&StartShipment::default(), tracked));
        }
        Ok(StepDecision::go_to_many(movements))
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

    /// Takes no lock: a report must never wait for the start call or its retries.
    fn execute(
        &self,
        context: &mut Context,
        mut tracked: Self::Input,
    ) -> HandlerResult<StepDecision> {
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
            Ok(run_id) => {
                tracked.shipment_run_id = Some(run_id);
                let started = StartedShipment {
                    tracked,
                    rejected: false,
                };
                Ok(StepDecision::go_to(&ClaimShipment, started))
            }
            Err(SdkError::FlowAlreadyStarted { .. }) => {
                // Another request owns this shipment number; this order does not track it.
                let started = StartedShipment {
                    tracked,
                    rejected: true,
                };
                Ok(StepDecision::go_to(&ClaimShipment, started))
            }
            Err(error) => Err(HandlerError::from_error(error)),
        }
    }
}

// dex:group group-id:start group-label:"Start shipments"
// dex:explanation text:"Claims the started run and merges a report that arrived before the claim."
#[derive(Default)]
struct ClaimShipment;

impl Step for ClaimShipment {
    type Input = StartedShipment;

    /// Takes the records lock, so the claim never races a report.
    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .execute_lock(SHIPMENT_RECORDS_LOCK.lock())
            .execute_load_attribute_map(&PENDING_SHIPMENT_REPORTS)
    }

    fn execute(&self, context: &mut Context, started: Self::Input) -> HandlerResult<StepDecision> {
        let number = started.tracked.shipment.shipment_number.clone();
        let pending = PENDING_SHIPMENT_REPORTS.get(context, &number)?;
        // The claim or the rejection settles every early report of this shipment.
        if pending.is_some() {
            PENDING_SHIPMENT_REPORTS.delete(context, &number)?;
        }
        if started.rejected {
            let record = ShipmentRecord {
                status: Some(SHIPMENT_REJECTED.to_string()),
                ..ShipmentRecord::default()
            };
            SHIPMENT_RECORDS.set(context, &number, record)?;
            return Ok(StepDecision::graceful_complete(()));
        }
        let run_id = started.tracked.shipment_run_id.clone().unwrap_or_default();
        // Claim the run: reports and reads of any other run of this number are ignored.
        let outcome = pending
            .and_then(|mut reports| reports.remove(&run_id))
            .map(|report| report.outcome);
        let record = ShipmentRecord {
            status: Some(SHIPMENT_STARTED.to_string()),
            run_id: Some(run_id),
            outcome,
        };
        SHIPMENT_RECORDS.set(context, &number, record)?;
        Ok(StepDecision::go_to(
            &AwaitShipment::default(),
            started.tracked,
        ))
    }
}

// dex:group group-id:track group-label:"Track shipment"
// dex:explanation text:"Reads the claimed shipment run at each interval until it ends."
#[derive(Default)]
struct AwaitShipment {
    client: Option<Arc<Client>>,
}

impl Step for AwaitShipment {
    type Input = TrackedShipment;

    fn wait_for(&self, _context: &mut Context, tracked: Self::Input) -> HandlerResult<Wait> {
        Ok(Wait::until(Timer::by_duration(duration_from_seconds(
            tracked.reconcile_interval_seconds,
        ))))
    }

    /// Takes no lock: a report must never wait for the reads or their retries.
    fn execute(&self, _context: &mut Context, tracked: Self::Input) -> HandlerResult<StepDecision> {
        let client = require_client(self.client.as_ref())?;
        let number = tracked.shipment.shipment_number.clone();
        let run_id = tracked.shipment_run_id.clone().unwrap_or_default();
        let reading = match reconcile_shipment(client, &number, &run_id)? {
            ShipmentReading::Running => {
                return Ok(StepDecision::go_to(&AwaitShipment::default(), tracked));
            }
            ShipmentReading::Closed(reading) => reading,
        };
        let ended = EndedShipment {
            shipment_number: number,
            shipment_run_id: run_id,
            reading,
        };
        Ok(StepDecision::go_to(&RecordShipmentEnd, ended))
    }
}

// dex:group group-id:track group-label:"Track shipment"
// dex:explanation text:"Records how the claimed shipment run ended."
#[derive(Default)]
struct RecordShipmentEnd;

impl Step for RecordShipmentEnd {
    type Input = EndedShipment;

    /// Takes the same lock as the report RPC and loads the records.
    fn options(&self) -> StepOptions<Self::Input> {
        StepOptions::new()
            .execute_lock(SHIPMENT_RECORDS_LOCK.lock())
            .execute_load_attribute_map(&SHIPMENT_RECORDS)
    }

    fn execute(&self, context: &mut Context, ended: Self::Input) -> HandlerResult<StepDecision> {
        // Merge with the record, which may already hold the reported outcome.
        let mut record = load_shipment_record(context, &ended.shipment_number)?.unwrap_or_default();
        record.status = ended.reading.status;
        record.run_id = Some(ended.shipment_run_id);
        if record.outcome.is_none() {
            record.outcome = ended.reading.outcome;
        }
        SHIPMENT_RECORDS.set(context, &ended.shipment_number, record)?;
        Ok(StepDecision::graceful_complete(()))
    }
}

/// Returns the loaded record of a shipment, or `None` when it has none.
///
/// Returns an error when the record cannot be read.
fn load_shipment_record(context: &Context, number: &str) -> HandlerResult<Option<ShipmentRecord>> {
    SHIPMENT_RECORDS.get(context, number)
}

/// What a reconcile read found.
enum ShipmentReading {
    Running,
    Closed(ShipmentRecord),
}

/// Reads the claimed run of a shipment: its status first, then its outcome.
///
/// Returns `Running` while that run runs, and an "unavailable" record when that
/// run no longer exists or another run replaced it. Keeps an outcome only when
/// the run is still the claimed one after the read. Returns an error for any
/// other failure so the Step retries.
fn reconcile_shipment(
    client: &Client,
    number: &str,
    run_id: &str,
) -> HandlerResult<ShipmentReading> {
    let Some(info) = describe_claimed_run(client, number, run_id)? else {
        return Ok(ShipmentReading::Closed(ShipmentRecord {
            status: Some(SHIPMENT_UNAVAILABLE.to_string()),
            ..ShipmentRecord::default()
        }));
    };
    if info.status == FlowStatus::Running {
        return Ok(ShipmentReading::Running);
    }
    let mut record = ShipmentRecord {
        status: Some(flow_status_name(info.status).to_string()),
        ..ShipmentRecord::default()
    };
    // An unset outcome Attribute leaves the outcome empty: the shipment ended without one.
    let outcome = match client.get_attributes(number, (&*SHIPMENT_OUTCOME,)) {
        Ok((outcome,)) => outcome,
        Err(SdkError::FlowNotFound { .. }) => return Ok(ShipmentReading::Closed(record)),
        Err(error) => return Err(HandlerError::from_error(error)),
    };
    // The Attribute read targets the latest run, so confirm it is still the claimed one.
    if describe_claimed_run(client, number, run_id)?.is_some() {
        record.outcome = outcome;
    }
    Ok(ShipmentReading::Closed(record))
}

/// Describes the latest run of a shipment.
///
/// Returns `None` when the shipment does not exist or its latest run is not
/// `run_id`, and an error for any other failure.
fn describe_claimed_run(
    client: &Client,
    number: &str,
    run_id: &str,
) -> HandlerResult<Option<FlowInfo>> {
    match client.describe_flow(number) {
        Ok(info) => Ok((info.run_id == run_id).then_some(info)),
        Err(SdkError::FlowNotFound { .. }) => Ok(None),
        Err(error) => Err(HandlerError::from_error(error)),
    }
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

/// Returns a Timer duration of `seconds`, treating a negative count as zero.
pub(super) fn duration_from_seconds(seconds: i64) -> Duration {
    Duration::from_secs(seconds.max(0).unsigned_abs())
}

pub(super) fn require_client(client: Option<&Arc<Client>>) -> HandlerResult<&Client> {
    client
        .map(Arc::as_ref)
        .ok_or_else(|| HandlerError::new("ClientUnavailable", "the Dex Client is not ready"))
}
