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
import { isDeepStrictEqual } from "node:util";

import {
  FlowNotFoundError,
  IdReusePolicy,
  RpcLockConflictError,
  StopType,
  type FlowStatus,
} from "@superdurable/dex";

import {
  SHIPMENT_REJECTED,
  SHIPMENT_TIMEOUT_MS,
  shipmentRecords,
  shipmentStartRequestId,
  type Order,
  type ShipmentRecord,
} from "../../src/patterns/child-flows-business-identity/order-fulfillment-flow.js";
import {
  shipmentOutcome,
  type Shipment,
} from "../../src/patterns/child-flows-business-identity/shipment-flow.js";
import {
  acquireIntegEnvironment,
  awaitCondition,
  releaseIntegEnvironment,
  type IntegEnvironment,
} from "./environment.js";

const WAIT_TIMEOUT_MS = 45_000;
const LOCK_CONFLICT_RETRY_MS = 100;

let environment: IntegEnvironment;

test.before(async () => {
  environment = await acquireIntegEnvironment();
});

test.after(async () => {
  await releaseIntegEnvironment();
});

test("child Flows with business identity record the report before the shipment closes", async () => {
  const orderId = environment.newFlowId("order");
  const delivered = `${orderId}-1`;
  await startOrder(orderId, {
    shipments: [{ shipmentNumber: delivered, destination: "Seattle", deliveryDelaySeconds: 1 }],
    reconcileIntervalSeconds: 3600,
  });
  // The reconcile Timer waits an hour, so only the report can fill the outcome.
  await waitForShipmentRecord(orderId, delivered, { outcome: "delivered to Seattle" });
  assert.equal((await environment.client.describeFlow(orderId)).status, "running");
  await terminateFlows(orderId);
});

test("child Flows with business identity merge reports and reconcile reads", async () => {
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
  await waitForShipmentRecord(orderId, delivered, { status: "completed", outcome: "delivered to Seattle" });

  // A repeated or different report for a recorded shipment changes nothing.
  for (const outcome of ["delivered to Seattle", "delivered to Boise"]) {
    await retryOnLockConflict(() =>
      environment.client.invokeRPC(environment.orderFulfillmentFlow.recordShipmentOutcome, orderId, {
        shipmentNumber: delivered,
        outcome,
      }),
    );
  }
  assert.deepEqual(await readShipmentRecord(orderId, delivered), {
    status: "completed",
    outcome: "delivered to Seattle",
  });

  await waitForShipmentStatus(canceled, "running");
  await environment.client.stopFlow(canceled, {
    type: StopType.CANCEL,
    reason: "customer canceled the parcel",
  });
  const result = await environment.client.waitForFlow(orderId, WAIT_TIMEOUT_MS);
  assert.equal(result.status, "completed", result.errorMessage);
  assert.deepEqual(await readShipmentRecord(orderId, canceled), { status: "canceled" });
});

test("child Flows with business identity reject a shipment number owned by another request", async () => {
  const firstOrderId = environment.newFlowId("order");
  const shipment: Shipment = { shipmentNumber: `${firstOrderId}-1`, destination: "Boise", deliveryDelaySeconds: 3600 };
  const order: Order = { shipments: [shipment], reconcileIntervalSeconds: 3600 };
  await startOrder(firstOrderId, order);
  await waitForShipmentStatus(shipment.shipmentNumber, "running");

  // Replaying the same start request returns the running shipment.
  const runId = await environment.client.startFlow(
    environment.shipmentFlow,
    shipment.shipmentNumber,
    { orderFlowId: firstOrderId, shipment },
    {
      timeoutMs: SHIPMENT_TIMEOUT_MS,
      idReusePolicy: IdReusePolicy.DISALLOW,
      requestId: shipmentStartRequestId(firstOrderId, shipment),
      ignoreAlreadyStarted: true,
    },
  );
  assert.equal(runId, (await environment.client.describeFlow(shipment.shipmentNumber)).runId);

  // Another order with the same shipment number sends a different request.
  const secondOrderId = environment.newFlowId("order");
  await startOrder(secondOrderId, order);
  const result = await environment.client.waitForFlow(secondOrderId, WAIT_TIMEOUT_MS);
  assert.equal(result.status, "completed", result.errorMessage);
  assert.deepEqual(await readShipmentRecord(secondOrderId, shipment.shipmentNumber), { status: SHIPMENT_REJECTED });
  await terminateFlows(shipment.shipmentNumber, firstOrderId);
});

test("child Flows with business identity fail an order with a duplicate shipment number", async () => {
  const orderId = environment.newFlowId("order");
  const number = `${orderId}-1`;
  await startOrder(orderId, {
    shipments: [
      { shipmentNumber: number, destination: "Seattle", deliveryDelaySeconds: 1 },
      { shipmentNumber: number, destination: "Portland", deliveryDelaySeconds: 1 },
    ],
    reconcileIntervalSeconds: 2,
  });
  const result = await environment.client.waitForFlow(orderId, WAIT_TIMEOUT_MS);
  assert.equal(result.status, "failed");
  assert.ok(
    result.errorMessage?.includes(`duplicate shipment number: ${number}`),
    `unexpected error: ${result.errorMessage}`,
  );
  await assert.rejects(environment.client.describeFlow(number), FlowNotFoundError);
});

test("child Flows with business identity store the outcome before reporting under async defaults", async () => {
  const orderId = environment.newFlowId("order");
  await startOrder(orderId, {
    shipments: [{ shipmentNumber: `${orderId}-1`, destination: "Boise", deliveryDelaySeconds: 3600 }],
    reconcileIntervalSeconds: 3600,
  });
  const number = `${orderId}-async`;
  await environment.client.startFlow(
    environment.shipmentFlow,
    number,
    {
      orderFlowId: orderId,
      shipment: { shipmentNumber: number, destination: "Austin", deliveryDelaySeconds: 1 },
    },
    { configOverride: { stepDurability: "async" } },
  );
  await waitForShipmentRecord(orderId, number, { outcome: "delivered to Austin" });
  const [outcome] = await environment.client.getAttributes(number, shipmentOutcome);
  assert.equal(outcome, "delivered to Austin");
  await terminateFlows(`${orderId}-1`, orderId);
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

async function waitForShipmentRecord(
  orderId: string,
  shipmentNumber: string,
  expected: ShipmentRecord,
): Promise<void> {
  await awaitCondition(
    async () => {
      try {
        return (await environment.client.getAttributes(orderId, shipmentRecords.instance(shipmentNumber)))[0];
      } catch {
        return undefined;
      }
    },
    (record) => isDeepStrictEqual(record, expected),
    WAIT_TIMEOUT_MS,
    `record of ${shipmentNumber}`,
  );
}

async function readShipmentRecord(orderId: string, shipmentNumber: string): Promise<ShipmentRecord> {
  const [record] = await environment.client.getAttributes(orderId, shipmentRecords.instance(shipmentNumber));
  assert.ok(record !== undefined, `record of ${shipmentNumber}`);
  return record;
}

async function terminateFlows(...flowIds: string[]): Promise<void> {
  for (const flowId of flowIds) {
    await environment.client.stopFlow(flowId, { type: StopType.TERMINATE });
  }
}

// The RPC fails fast while AwaitShipment holds the same Attribute lock.
async function retryOnLockConflict<T>(invoke: () => Promise<T>): Promise<T> {
  const deadline = Date.now() + WAIT_TIMEOUT_MS;
  for (;;) {
    try {
      return await invoke();
    } catch (error) {
      if (!(error instanceof RpcLockConflictError) || Date.now() >= deadline) {
        throw error;
      }
      await new Promise((resolve) => setTimeout(resolve, LOCK_CONFLICT_RETRY_MS));
    }
  }
}
