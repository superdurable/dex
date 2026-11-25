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
  type FlowStatus,
  type PersistenceSchema,
  type Step,
  type StepDecision,
  type StepOptions,
} from "@superdurable/dex";

import { SHIPMENT_FLOW_TYPE, shipmentOutcome, type Shipment, type ShipmentFlow } from "./shipment-flow.js";

/** Each shipment's own Flow timeout. A closing order never stops its shipments. */
export const SHIPMENT_TIMEOUT_MS = 30 * 24 * 60 * 60 * 1000;

/** A shipment record status that is not a terminal Flow status. */
export const SHIPMENT_REJECTED = "rejected";
/** A shipment record status that is not a terminal Flow status. */
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
}

/** What a shipment sends to its order when it ends. */
export interface ShipmentReport {
  readonly shipmentNumber: string;
  readonly outcome: string;
}

/**
 * The order's record of one shipment. A report or a reconcile read fills
 * outcome; a reconcile read fills status.
 */
export interface ShipmentRecord {
  readonly status?: string;
  readonly outcome?: string;
}

/** Holds one record per shipment, keyed by shipment number. */
export const shipmentRecords = new AttributeMap("shipmentRecords", jsonCodec<ShipmentRecord>());
/** Serializes the report RPC and AwaitShipment writes; it stores no value. */
export const shipmentRecordsLock = new Attribute("shipmentRecordsLock", booleanCodec);

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
// dex:explanation text:"Starts one branch per shipment, after checking that shipment numbers are unique."
class StartShipments implements Step<Order> {
  public getStepType(): string {
    return "StartShipments";
  }

  public execute(_context: Context, order: Order): StepDecision {
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
// dex:explanation text:"Starts the shipment as a top-level Flow named by its shipment number."
class StartShipment implements Step<TrackedShipment> {
  public constructor(
    private readonly getClient: () => Client,
    private readonly shipmentFlow: ShipmentFlow,
  ) {}

  public getStepType(): string {
    return "StartShipment";
  }

  public async execute(context: Context, tracked: TrackedShipment): Promise<StepDecision> {
    // Calling a field through this would hide the Step class from the Flow visualizer.
    const getClient = this.getClient;
    const number = tracked.shipment.shipmentNumber;
    const requestId = shipmentStartRequestId(context.flowId, tracked.shipment);
    try {
      await getClient().startFlow(
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
    return goTo(AwaitShipment, tracked);
  }
}

// dex:group group-id:track group-label:"Track shipment"
// dex:explanation text:"Reads the shipment at each interval and records how it ended."
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
    const reading = await reconcileShipment(getClient(), number);
    if (reading.status === "running") {
      return goTo(AwaitShipment, tracked);
    }
    // Merge with the record, which may already hold the reported outcome.
    const record = loadShipmentRecord(context, number);
    shipmentRecords.set(context, number, {
      status: reading.status,
      outcome: record.outcome ?? reading.outcome,
    });
    return gracefulComplete();
  }
}

/**
 * Returns the loaded record of a shipment, or an empty record when it has none.
 *
 * @returns The stored record, or `{}` when the shipment has no record yet.
 */
function loadShipmentRecord(context: Context, number: string): ShipmentRecord {
  return (shipmentRecords.get(context, number) as ShipmentRecord | undefined) ?? {};
}

/**
 * Reads a shipment: its status first, then its outcome.
 *
 * @returns A "running" record while the shipment runs, an "unavailable" record
 * when the shipment does not exist, or the terminal status with the stored
 * outcome. It throws any other failure so the Step retries.
 */
async function reconcileShipment(client: Client, number: string): Promise<ShipmentRecord> {
  let status: FlowStatus;
  try {
    status = (await client.describeFlow(number)).status;
  } catch (error) {
    if (error instanceof FlowNotFoundError) {
      return { status: SHIPMENT_UNAVAILABLE };
    }
    throw error;
  }
  const record = { status: shipmentStatusOf(status) };
  if (status === "running") {
    return record;
  }
  try {
    // An unset outcome Attribute leaves outcome empty: the shipment ended without one.
    const [outcome] = await client.getAttributes(number, shipmentOutcome);
    return outcome === undefined ? record : { ...record, outcome };
  } catch (error) {
    if (error instanceof FlowNotFoundError) {
      return record;
    }
    throw error;
  }
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
   * Fills the outcome of a shipment's record. The first report wins; a
   * repeated or late report changes nothing.
   */
  @rpc({ lockAttributes: [shipmentRecordsLock.lock()], loadAttributeMaps: [shipmentRecords] })
  public recordShipmentOutcome(context: Context, report: ShipmentReport): void {
    const record = loadShipmentRecord(context, report.shipmentNumber);
    if (record.outcome !== undefined) {
      return;
    }
    shipmentRecords.set(context, report.shipmentNumber, { ...record, outcome: report.outcome });
  }
}
