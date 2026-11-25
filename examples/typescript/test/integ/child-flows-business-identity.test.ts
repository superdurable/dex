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

import assert from "node:assert/strict";
import test from "node:test";

import { StopType, type FlowStatus } from "@superdurable/dex";

import {
  SHIPMENT_REJECTED,
  shipmentRecords,
  type Order,
  type ShipmentRecord,
} from "../../src/patterns/child-flows-business-identity/order-fulfillment-flow.js";
import { shipmentOutcome } from "../../src/patterns/child-flows-business-identity/shipment-flow.js";
import {
  acquireIntegEnvironment,
  awaitCondition,
  releaseIntegEnvironment,
  type IntegEnvironment,
} from "./environment.js";

const WAIT_TIMEOUT_MS = 45_000;

let environment: IntegEnvironment;

test.before(async () => {
  environment = await acquireIntegEnvironment();
});

test.after(async () => {
  await releaseIntegEnvironment();
});

test("child Flows with business identity record reported and canceled shipments", async () => {
  const orderId = environment.newFlowId("order");
  const delivered = `${orderId}-1`;
  const canceled = `${orderId}-2`;
  await startOrder(orderId, {
    shipments: [
      { shipmentNumber: delivered, destination: "Seattle", deliveryDelaySeconds: 1 },
      { shipmentNumber: canceled, destination: "Portland", deliveryDelaySeconds: 3600 },
    ],
    reconcileIntervalSeconds: 2,
  });
  await waitForShipmentStatus(canceled, "running");
  await environment.client.stopFlow(canceled, {
    type: StopType.CANCEL,
    reason: "customer canceled the parcel",
  });

  const result = await environment.client.waitForFlow(orderId, WAIT_TIMEOUT_MS);
  assert.equal(result.status, "completed", result.errorMessage);
  assert.equal((await readShipmentRecord(orderId, delivered)).outcome, "delivered to Seattle");
  assert.deepEqual(await readShipmentRecord(orderId, canceled), { status: "canceled" });

  await waitForShipmentStatus(delivered, "completed");
  const [outcome] = await environment.client.getAttributes(delivered, shipmentOutcome);
  assert.equal(outcome, "delivered to Seattle");
});

test("child Flows with business identity reject a shipment number owned by another order", async () => {
  const firstOrderId = environment.newFlowId("order");
  const shared = `${firstOrderId}-1`;
  const order: Order = {
    shipments: [{ shipmentNumber: shared, destination: "Boise", deliveryDelaySeconds: 3600 }],
    reconcileIntervalSeconds: 3600,
  };
  await startOrder(firstOrderId, order);
  await waitForShipmentStatus(shared, "running");

  const secondOrderId = environment.newFlowId("order");
  await startOrder(secondOrderId, order);
  const result = await environment.client.waitForFlow(secondOrderId, WAIT_TIMEOUT_MS);
  assert.equal(result.status, "completed", result.errorMessage);
  assert.deepEqual(await readShipmentRecord(secondOrderId, shared), { status: SHIPMENT_REJECTED });

  for (const flowId of [shared, firstOrderId]) {
    await environment.client.stopFlow(flowId, { type: StopType.TERMINATE });
  }
});

async function startOrder(orderId: string, order: Order): Promise<void> {
  await environment.client.startFlow(environment.orderFulfillmentFlow, orderId, order, environment.startOptions());
}

async function waitForShipmentStatus(shipmentNumber: string, status: FlowStatus): Promise<void> {
  await awaitCondition(
    async () => {
      try {
        return (await environment.client.describeFlow(shipmentNumber)).status;
      } catch {
        return undefined;
      }
    },
    (current) => current === status,
    WAIT_TIMEOUT_MS,
    `shipment ${shipmentNumber} did not reach ${status}`,
  );
}

async function readShipmentRecord(orderId: string, shipmentNumber: string): Promise<ShipmentRecord> {
  const [record] = await environment.client.getAttributes(orderId, shipmentRecords.instance(shipmentNumber));
  assert.ok(record !== undefined, `record of ${shipmentNumber}`);
  return record;
}
