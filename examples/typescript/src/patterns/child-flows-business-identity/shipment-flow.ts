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

import {
  Attribute,
  FlowNotActiveOrNotFoundError,
  FlowNotFoundError,
  StepList,
  Timer,
  Wait,
  goTo,
  gracefulComplete,
  stringCodec,
  type Client,
  type Context,
  type Flow,
  type PersistenceSchema,
  type Step,
  type StepDecision,
  type StepOptions,
} from "@superdurable/dex";

import type { OrderFulfillmentFlow, ShipmentReport } from "./order-fulfillment-flow.js";

/** The registered Flow type of ShipmentFlow. */
export const SHIPMENT_FLOW_TYPE = "ShipmentFlow";

/** One parcel of an order, identified by its shipment number. */
export interface Shipment {
  readonly shipmentNumber: string;
  readonly destination: string;
  /** Simulates how long the carrier takes to deliver. */
  readonly deliveryDelaySeconds: number;
}

/** The input of ShipmentFlow. */
export interface ShipmentRequest {
  readonly orderFlowId: string;
  readonly shipment: Shipment;
}

/** The report a shipment still has to send to its order. */
export interface PendingReport {
  readonly orderFlowId: string;
  readonly report: ShipmentReport;
}

/** The shipment's committed outcome. The order reads it when no report arrives. */
export const shipmentOutcome = new Attribute("shipmentOutcome", stringCodec);

// dex:group group-id:deliver group-label:"Deliver"
// dex:explanation text:"Waits for the carrier, then commits the delivered outcome."
class DeliverShipment implements Step<ShipmentRequest> {
  public getStepType(): string {
    return "DeliverShipment";
  }

  /** Stores the outcome before the report Step starts, even when the Flow defaults to ASYNC. */
  public getStepOptions(): StepOptions {
    return { executeDurability: "sync" };
  }

  public waitFor(_context: Context, request: ShipmentRequest): Wait {
    return Wait.until(Timer.byDuration(request.shipment.deliveryDelaySeconds * 1000));
  }

  public execute(context: Context, request: ShipmentRequest): StepDecision {
    const outcome = `delivered to ${request.shipment.destination}`;
    shipmentOutcome.set(context, outcome);
    const pending: PendingReport = {
      orderFlowId: request.orderFlowId,
      report: { shipmentNumber: request.shipment.shipmentNumber, shipmentRunId: context.runId, outcome },
    };
    return goTo(ReportShipmentOutcome, pending);
  }
}

// dex:group group-id:report group-label:"Report"
// dex:explanation text:"Reports the committed outcome to the order that started this shipment."
class ReportShipmentOutcome implements Step<PendingReport> {
  public constructor(
    private readonly getClient: () => Client,
    private readonly getOrderFlow: () => OrderFulfillmentFlow,
  ) {}

  public getStepType(): string {
    return "ReportShipmentOutcome";
  }

  /** Keeps the report SYNC, like the Step that stores the outcome. */
  public getStepOptions(): StepOptions {
    return { executeDurability: "sync" };
  }

  public async execute(context: Context, pending: PendingReport): Promise<StepDecision> {
    // Calling a field through this would hide the Step class from the Flow visualizer.
    const getClient = this.getClient;
    const getOrderFlow = this.getOrderFlow;
    // Stable per shipment, so a retried report changes the order once.
    const requestId = `report:${context.flowId}`;
    try {
      await getClient().invokeRPCWithOptions(
        getOrderFlow().recordShipmentOutcome,
        pending.orderFlowId,
        pending.report,
        { requestId },
      );
    } catch (error) {
      // A closed order ends the attempts; the outcome stays readable through the Client.
      if (!(error instanceof FlowNotFoundError) && !(error instanceof FlowNotActiveOrNotFoundError)) {
        throw error;
      }
    }
    return gracefulComplete(pending.report.outcome);
  }
}

/**
 * Delivers one shipment. Its Flow ID is the shipment number, so people and
 * systems can look it up, retry it, or stop it by that number.
 */
export class ShipmentFlow implements Flow<ShipmentRequest> {
  private readonly reportShipmentOutcome: ReportShipmentOutcome;

  /**
   * @param getClient - Returns the Client that reports to the order; it is called only during execute.
   * @param getOrderFlow - Returns the order Flow whose RPC receives the report; it is called only during execute.
   */
  public constructor(getClient: () => Client, getOrderFlow: () => OrderFulfillmentFlow) {
    this.reportShipmentOutcome = new ReportShipmentOutcome(getClient, getOrderFlow);
  }

  public getFlowType(): string {
    return "ShipmentFlow";
  }

  public getSteps(): StepList<ShipmentRequest> {
    return StepList.startStep(new DeliverShipment()).otherSteps(this.reportShipmentOutcome);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [shipmentOutcome] };
  }
}
