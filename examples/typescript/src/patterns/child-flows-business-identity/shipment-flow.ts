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
} from "@superdurable/dex";

import { OrderFulfillmentFlow, type ShipmentReport } from "./order-fulfillment-flow.js";

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

  public waitFor(_context: Context, request: ShipmentRequest): Wait {
    return Wait.until(Timer.byDuration(request.shipment.deliveryDelaySeconds * 1000));
  }

  /** Keeps the default sync durability, so the outcome is stored before the report Step starts. */
  public execute(context: Context, request: ShipmentRequest): StepDecision {
    const outcome = `delivered to ${request.shipment.destination}`;
    shipmentOutcome.set(context, outcome);
    const pending: PendingReport = {
      orderFlowId: request.orderFlowId,
      report: { shipmentNumber: request.shipment.shipmentNumber, outcome },
    };
    return goTo(ReportShipmentOutcome, pending);
  }
}

// dex:group group-id:report group-label:"Report"
// dex:explanation text:"Reports the committed outcome to the order that started this shipment."
class ReportShipmentOutcome implements Step<PendingReport> {
  public constructor(private readonly getClient: () => Client) {}

  public getStepType(): string {
    return "ReportShipmentOutcome";
  }

  public async execute(context: Context, pending: PendingReport): Promise<StepDecision> {
    // Calling a field through this would hide the Step class from the Flow visualizer.
    const getClient = this.getClient;
    // Stable per shipment, so a retried report changes the order once.
    const requestId = `report:${context.flowId}`;
    try {
      await getClient().invokeRPCWithOptions(
        OrderFulfillmentFlow.prototype.recordShipmentOutcome,
        pending.orderFlowId,
        pending.report,
        { requestId },
      );
    } catch (error) {
      // A closed order ends the attempts; it reads the shipment's outcome instead.
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
   */
  public constructor(getClient: () => Client) {
    this.reportShipmentOutcome = new ReportShipmentOutcome(getClient);
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
