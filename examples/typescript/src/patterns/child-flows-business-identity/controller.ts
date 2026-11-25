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

import { FlowNotFoundError, StopType, type Client } from "@superdurable/dex";

import { DAY_MS } from "../../config/env.js";
import { shipmentStatusOf, type OrderFulfillmentFlow } from "./order-fulfillment-flow.js";
import { shipmentOutcome } from "./shipment-flow.js";

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
    const number = requiredQuery(request, response, "shipmentNumber");
    if (number === undefined) {
      return;
    }
    let status;
    try {
      status = (await client.describeFlow(number)).status;
    } catch (error) {
      if (error instanceof FlowNotFoundError) {
        response.status(404).json({ error: `shipment ${number} not found` });
        return;
      }
      throw error;
    }
    const [outcome] = await client.getAttributes(number, shipmentOutcome);
    response.json({
      shipmentNumber: number,
      status: shipmentStatusOf(status),
      outcome: outcome ?? "",
    });
  });

  // Stops a shipment by its number. Its order records it at the next reconcile read.
  router.get("/cancel-shipment", async (request, response) => {
    const number = requiredQuery(request, response, "shipmentNumber");
    if (number === undefined) {
      return;
    }
    await client.stopFlow(number, { type: StopType.CANCEL, reason: "shipment canceled" });
    response.send("canceled");
  });

  return router;
}

function requiredQuery(request: Request, response: Response, name: string): string | undefined {
  const value = String(request.query[name] ?? "");
  if (value === "") {
    response.status(400).json({ error: `${name} is required` });
    return undefined;
  }
  return value;
}
