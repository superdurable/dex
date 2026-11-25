/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

import { createHash } from "node:crypto";

import {
  Attribute,
  AttributeMap,
  FlowAlreadyStartedError,
  FlowNotFoundError,
  IdReusePolicy,
  StepList,
  StepMovement,
  Timer,
  Wait,
  booleanCodec,
  forceFail,
  goTo,
  goToMany,
  gracefulComplete,
  jsonCodec,
  rpc,
  type Client,
  type Context,
  type Flow,
  type FlowInfo,
  type FlowStatus,
  type PersistenceSchema,
  type Step,
  type StepDecision,
  type StepOptions,
} from "@superdurable/dex";

import { SHIPMENT_FLOW_TYPE, shipmentOutcome, type Shipment, type ShipmentFlow } from "./shipment-flow.js";

/** Each shipment's own Flow timeout. A closing order never stops its shipments. */
export const SHIPMENT_TIMEOUT_MS = 30 * 24 * 60 * 60 * 1000;

/** Marks a shipment of the order that has no claimed run yet. */
export const SHIPMENT_STARTING = "starting";
/** Marks a shipment whose run this order started and claimed. */
export const SHIPMENT_STARTED = "started";
/** Marks a shipment number that another request owns. */
export const SHIPMENT_REJECTED = "rejected";
/** Marks a claimed run that can no longer be read. */
export const SHIPMENT_UNAVAILABLE = "unavailable";

const DUPLICATE_SHIPMENT_PREFIX = "duplicate shipment number: ";

/** The input of OrderFulfillmentFlow. The order number is the Flow ID. */
export interface Order {
  readonly shipments: readonly Shipment[];
  /** How often the order reads a shipment that has not ended. */
  readonly reconcileIntervalSeconds: number;
}

/** The input of the Steps that start and track one shipment. */
export interface TrackedShipment {
  readonly shipment: Shipment;
  readonly reconcileIntervalSeconds: number;
  /** The run this order started and tracks; absent before the start. */
  readonly shipmentRunId?: string;
}

/** What a shipment run sends to its order when it ends. */
export interface ShipmentReport {
  readonly shipmentNumber: string;
  readonly shipmentRunId: string;
  readonly outcome: string;
}

/**
 * The order's record of one shipment. runId is the claimed run. A report or a
 * reconcile read fills outcome; a reconcile read fills the terminal status.
 */
export interface ShipmentRecord {
  readonly status?: string;
  readonly runId?: string;
  readonly outcome?: string;
}

/** Holds one record per shipment, keyed by shipment number. */
export const shipmentRecords = new AttributeMap("shipmentRecords", jsonCodec<ShipmentRecord>());
/** Serializes every write to shipmentRecords; it stores no value. */
export const shipmentRecordsLock = new Attribute("shipmentRecordsLock", booleanCodec);

/** Asks a shipment to retry its report after the order claims its run. */
export class ShipmentNotClaimedError extends Error {
  public constructor() {
    super("childflows: the order has not claimed this shipment run yet");
    this.name = "ShipmentNotClaimedError";
  }
}

/** Returns the lowercase Flow status a shipment record stores, such as "canceled". */
export function shipmentStatusOf(status: FlowStatus): string {
  return status === "cancelled" ? "canceled" : status;
}

/**
 * Derives the start request ID from the complete start request.
 *
 * A retried Execute sends the same ID, so it repeats the same start. Any other
 * request for the same shipment number sends another ID, so Dex rejects it as
 * already started.
 *
 * @returns `start:` followed by the hex SHA-256 of the encoded start request.
 */
export function shipmentStartRequestId(orderFlowId: string, shipment: Shipment): string {
  const request = JSON.stringify([
    SHIPMENT_FLOW_TYPE,
    shipment.shipmentNumber,
    orderFlowId,
    shipment.destination,
    shipment.deliveryDelaySeconds,
    SHIPMENT_TIMEOUT_MS / 1000,
    "disallow",
  ]);
  return `start:${createHash("sha256").update(request).digest("hex")}`;
}

// dex:group group-id:start group-label:"Start shipments"
// dex:explanation text:"Registers the order's shipment numbers, then starts one branch per shipment."
class StartShipments implements Step<Order> {
  public getStepType(): string {
    return "StartShipments";
  }

  /** Takes the records lock, so registration never races a report. */
  public getStepOptions(): StepOptions {
    return { executeLockAttributes: [shipmentRecordsLock.lock()] };
  }

  public execute(context: Context, order: Order): StepDecision {
    if (order.shipments.length === 0) {
      return gracefulComplete();
    }
    const numbers = new Set<string>();
    for (const shipment of order.shipments) {
      if (numbers.has(shipment.shipmentNumber)) {
        return forceFail(DUPLICATE_SHIPMENT_PREFIX + shipment.shipmentNumber);
      }
      numbers.add(shipment.shipmentNumber);
    }
    for (const shipment of order.shipments) {
      shipmentRecords.set(context, shipment.shipmentNumber, { status: SHIPMENT_STARTING });
    }
    return goToMany(
      ...order.shipments.map((shipment) =>
        StepMovement.of(StartShipment, {
          shipment,
          reconcileIntervalSeconds: order.reconcileIntervalSeconds,
        }),
      ),
    );
  }
}

// dex:group group-id:start group-label:"Start shipments"
// dex:explanation text:"Starts the shipment as a top-level Flow named by its shipment number and claims its run."
class StartShipment implements Step<TrackedShipment> {
  public constructor(
    private readonly getClient: () => Client,
    private readonly shipmentFlow: ShipmentFlow,
  ) {}

  public getStepType(): string {
    return "StartShipment";
  }

  /** Takes the records lock, so the claim and the rejection never race a report. */
  public getStepOptions(): StepOptions {
    return { executeLockAttributes: [shipmentRecordsLock.lock()] };
  }

  public async execute(context: Context, tracked: TrackedShipment): Promise<StepDecision> {
    // Calling a field through this would hide the Step class from the Flow visualizer.
    const getClient = this.getClient;
    const number = tracked.shipment.shipmentNumber;
    const requestId = shipmentStartRequestId(context.flowId, tracked.shipment);
    let runId: string;
    try {
      runId = await getClient().startFlow(
        this.shipmentFlow,
        number,
        { orderFlowId: context.flowId, shipment: tracked.shipment },
        {
          timeoutMs: SHIPMENT_TIMEOUT_MS,
          idReusePolicy: IdReusePolicy.DISALLOW,
          requestId,
          ignoreAlreadyStarted: true,
        },
      );
    } catch (error) {
      if (!(error instanceof FlowAlreadyStartedError)) {
        throw error;
      }
      // Another request owns this shipment number; this order does not track it.
      shipmentRecords.set(context, number, { status: SHIPMENT_REJECTED });
      return gracefulComplete();
    }
    // Claim the run: reports and reads of any other run of this number are ignored.
    shipmentRecords.set(context, number, { status: SHIPMENT_STARTED, runId });
    return goTo(AwaitShipment, { ...tracked, shipmentRunId: runId });
  }
}

// dex:group group-id:track group-label:"Track shipment"
// dex:explanation text:"Reads the claimed shipment run at each interval and records how it ended."
class AwaitShipment implements Step<TrackedShipment> {
  public constructor(private readonly getClient: () => Client) {}

  public getStepType(): string {
    return "AwaitShipment";
  }

  /** Takes the same lock as the report RPC and loads the records. */
  public getStepOptions(): StepOptions {
    return {
      executeLockAttributes: [shipmentRecordsLock.lock()],
      executeLoadAttributeMaps: [shipmentRecords],
    };
  }

  public waitFor(_context: Context, tracked: TrackedShipment): Wait {
    return Wait.until(Timer.byDuration(tracked.reconcileIntervalSeconds * 1000));
  }

  public async execute(context: Context, tracked: TrackedShipment): Promise<StepDecision> {
    // Calling a field through this would hide the Step class from the Flow visualizer.
    const getClient = this.getClient;
    const number = tracked.shipment.shipmentNumber;
    const runId = tracked.shipmentRunId ?? "";
    const reading = await reconcileShipment(getClient(), number, runId);
    if (reading.status === "running") {
      return goTo(AwaitShipment, tracked);
    }
    // Merge with the record, which may already hold the reported outcome.
    const { record } = loadShipmentRecord(context, number);
    shipmentRecords.set(context, number, {
      status: reading.status,
      runId,
      outcome: record.outcome ?? reading.outcome,
    });
    return gracefulComplete();
  }
}

/**
 * Returns the loaded record of a shipment and whether it exists.
 *
 * @returns The stored record with `found: true`, or `{}` with `found: false`
 * when the shipment has no record.
 */
function loadShipmentRecord(context: Context, number: string): { record: ShipmentRecord; found: boolean } {
  const record = shipmentRecords.get(context, number) as ShipmentRecord | undefined;
  return record === undefined ? { record: {}, found: false } : { record, found: true };
}

/**
 * Reads the claimed run of a shipment: its status first, then its outcome.
 *
 * @returns A "running" record while that run runs, an "unavailable" record
 * when that run no longer exists or another run replaced it, or the terminal
 * status. It keeps an outcome only when the run is still the claimed one after
 * the read. It throws any other failure so the Step retries.
 */
async function reconcileShipment(client: Client, number: string, runId: string): Promise<ShipmentRecord> {
  const { info, found } = await describeClaimedRun(client, number, runId);
  if (info === undefined || !found) {
    return { status: SHIPMENT_UNAVAILABLE };
  }
  const record = { status: shipmentStatusOf(info.status) };
  if (info.status === "running") {
    return record;
  }
  let outcome: string | undefined;
  try {
    // An unset outcome Attribute leaves outcome empty: the shipment ended without one.
    [outcome] = await client.getAttributes(number, shipmentOutcome);
  } catch (error) {
    if (error instanceof FlowNotFoundError) {
      return record;
    }
    throw error;
  }
  // The Attribute read targets the latest run, so confirm it is still the claimed one.
  const { found: stillClaimed } = await describeClaimedRun(client, number, runId);
  return stillClaimed && outcome !== undefined ? { ...record, outcome } : record;
}

/**
 * Describes the latest run of a shipment.
 *
 * @returns The FlowInfo, or undefined when the shipment does not exist, and
 * `found` as false when the shipment does not exist or its latest run is not
 * runId. It throws any other failure.
 */
async function describeClaimedRun(
  client: Client,
  number: string,
  runId: string,
): Promise<{ info: FlowInfo | undefined; found: boolean }> {
  let info: FlowInfo;
  try {
    info = await client.describeFlow(number);
  } catch (error) {
    if (error instanceof FlowNotFoundError) {
      return { info: undefined, found: false };
    }
    throw error;
  }
  return { info, found: info.runId === runId };
}

/**
 * Ships an order as one ShipmentFlow per shipment. Each shipment is a
 * top-level Flow whose Flow ID is its shipment number.
 */
export class OrderFulfillmentFlow implements Flow<Order> {
  private readonly startShipment: StartShipment;
  private readonly awaitShipment: AwaitShipment;

  /**
   * @param getClient - Returns the Client that starts and reads shipments; it is called only during execute.
   * @param shipmentFlow - The registered shipment Flow.
   */
  public constructor(getClient: () => Client, shipmentFlow: ShipmentFlow) {
    this.startShipment = new StartShipment(getClient, shipmentFlow);
    this.awaitShipment = new AwaitShipment(getClient);
  }

  public getFlowType(): string {
    return "OrderFulfillmentFlow";
  }

  public getSteps(): StepList<Order> {
    return StepList.startStep(new StartShipments()).otherSteps(this.startShipment, this.awaitShipment);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {
      attributes: [shipmentRecords, shipmentRecordsLock],
    };
  }

  /**
   * Fills the outcome of a shipment's record. It accepts only the run this
   * order claimed; the first report wins, and any other report changes nothing.
   *
   * @throws ShipmentNotClaimedError while the order is still starting that shipment.
   */
  @rpc({ lockAttributes: [shipmentRecordsLock.lock()], loadAttributeMaps: [shipmentRecords] })
  public recordShipmentOutcome(context: Context, report: ShipmentReport): void {
    const { record, found } = loadShipmentRecord(context, report.shipmentNumber);
    if (found && record.status === SHIPMENT_STARTING) {
      throw new ShipmentNotClaimedError();
    }
    // Ignore an unknown or rejected shipment, another run, and a repeated report.
    if (
      !found ||
      record.runId === undefined ||
      record.runId !== report.shipmentRunId ||
      record.outcome !== undefined
    ) {
      return;
    }
    shipmentRecords.set(context, report.shipmentNumber, { ...record, outcome: report.outcome });
  }
}
