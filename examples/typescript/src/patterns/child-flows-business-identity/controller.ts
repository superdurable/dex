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

import { Router, type Request, type Response } from "express";

import { FlowNotFoundError, StopType, type Client, type FlowInfo } from "@superdurable/dex";

import { DAY_MS } from "../../config/env.js";
import { shipmentStatusOf, type OrderFulfillmentFlow } from "./order-fulfillment-flow.js";
import { SHIPMENT_FLOW_TYPE, shipmentOutcome } from "./shipment-flow.js";

/** Serves the order routes and the shipment routes that find a shipment by its number. */
export function createChildFlowsBusinessIdentityRouter(
  client: Client,
  orderFulfillmentFlow: OrderFulfillmentFlow,
): Router {
  const router = Router();

  // Starts an order with two shipments: one delivers in seconds, one takes a day.
  router.get("/start", async (request, response) => {
    const orderId = requiredQuery(request, response, "workflowId");
    if (orderId === undefined) {
      return;
    }
    const runId = await client.startFlow(
      orderFulfillmentFlow,
      orderId,
      {
        shipments: [
          { shipmentNumber: `${orderId}-1`, destination: "Seattle", deliveryDelaySeconds: 5 },
          { shipmentNumber: `${orderId}-2`, destination: "Portland", deliveryDelaySeconds: 86400 },
        ],
        reconcileIntervalSeconds: 60,
      },
      { timeoutMs: 40 * DAY_MS },
    );
    response.json({ flowID: orderId, runID: runId });
  });

  // Looks a shipment up by its shipment number, which is its Flow ID.
  router.get("/shipment", async (request, response) => {
    const shipment = await describeShipment(client, request, response);
    if (shipment === undefined) {
      return;
    }
    const [outcome] = await client.getAttributes(shipment.flowId, shipmentOutcome);
    response.json({
      shipmentNumber: shipment.flowId,
      status: shipmentStatusOf(shipment.status),
      outcome: outcome ?? "",
    });
  });

  // Stops a shipment by its number. Its order records it at the next reconcile read.
  router.get("/cancel-shipment", async (request, response) => {
    const shipment = await describeShipment(client, request, response);
    if (shipment === undefined) {
      return;
    }
    await client.stopFlow(shipment.flowId, { type: StopType.CANCEL, reason: "shipment canceled" });
    response.send("canceled");
  });

  return router;
}

/**
 * Describes the Flow named by the shipmentNumber query.
 *
 * @returns The shipment's FlowInfo, or undefined after it responded: 404 when
 * no ShipmentFlow has that Flow ID.
 */
async function describeShipment(
  client: Client,
  request: Request,
  response: Response,
): Promise<FlowInfo | undefined> {
  const number = requiredQuery(request, response, "shipmentNumber");
  if (number === undefined) {
    return undefined;
  }
  let info: FlowInfo | undefined;
  try {
    info = await client.describeFlow(number);
  } catch (error) {
    if (!(error instanceof FlowNotFoundError)) {
      throw error;
    }
  }
  if (info === undefined || info.flowType !== SHIPMENT_FLOW_TYPE) {
    response.status(404).json({ error: `shipment ${number} not found` });
    return undefined;
  }
  return info;
}

function requiredQuery(request: Request, response: Response, name: string): string | undefined {
  const value = String(request.query[name] ?? "");
  if (value === "") {
    response.status(400).json({ error: `${name} is required` });
    return undefined;
  }
  return value;
}
