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
  SHIPMENT_STARTED,
  SHIPMENT_TIMEOUT_MS,
  SHIPMENT_UNAVAILABLE,
  shipmentRecords,
  shipmentStartRequestId,
  type Order,
  type ShipmentRecord,
  type ShipmentReport,
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
  const runId = await waitForClaimedRun(orderId, delivered);
  // The reconcile Timer waits an hour, so only the report can fill the outcome.
  await waitForShipmentRecord(orderId, delivered, {
    status: SHIPMENT_STARTED,
    runId,
    outcome: "delivered to Seattle",
  });
  assert.equal((await environment.client.describeFlow(orderId)).status, "running");
  await terminateFlows(orderId);
});

test("child Flows with business identity accept reports only from the claimed run", async () => {
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
  const deliveredRunId = await waitForClaimedRun(orderId, delivered);
  const canceledRunId = await waitForClaimedRun(orderId, canceled);
  const expectedDelivered: ShipmentRecord = {
    status: "completed",
    runId: deliveredRunId,
    outcome: "delivered to Seattle",
  };
  await waitForShipmentRecord(orderId, delivered, expectedDelivered);

  // A repeated report, a report from another run, and an unknown number change nothing.
  const reports: ShipmentReport[] = [
    { shipmentNumber: delivered, shipmentRunId: deliveredRunId, outcome: "delivered to Boise" },
    { shipmentNumber: canceled, shipmentRunId: "another-run", outcome: "delivered to Boise" },
    { shipmentNumber: `${orderId}-9`, shipmentRunId: "another-run", outcome: "delivered to Boise" },
  ];
  for (const report of reports) {
    await recordShipmentOutcome(orderId, report);
  }
  assert.deepEqual(await readShipmentRecord(orderId, delivered), expectedDelivered);
  assert.deepEqual(await readShipmentRecord(orderId, canceled), {
    status: SHIPMENT_STARTED,
    runId: canceledRunId,
  });
  const [unknown] = await environment.client.getAttributes(orderId, shipmentRecords.instance(`${orderId}-9`));
  assert.equal(unknown, undefined, `record of ${orderId}-9`);

  await environment.client.stopFlow(canceled, {
    type: StopType.CANCEL,
    reason: "customer canceled the parcel",
  });
  const result = await environment.client.waitForFlow(orderId, WAIT_TIMEOUT_MS);
  assert.equal(result.status, "completed", result.errorMessage);
  assert.deepEqual(await readShipmentRecord(orderId, canceled), { status: "canceled", runId: canceledRunId });
});

test("child Flows with business identity reject a shipment number owned by another request", async () => {
  const firstOrderId = environment.newFlowId("order");
  const shipment: Shipment = { shipmentNumber: `${firstOrderId}-1`, destination: "Boise", deliveryDelaySeconds: 3600 };
  const order: Order = { shipments: [shipment], reconcileIntervalSeconds: 3600 };
  await startOrder(firstOrderId, order);
  const runId = await waitForClaimedRun(firstOrderId, shipment.shipmentNumber);

  // Replaying the same start request returns the claimed run.
  const replayedRunId = await environment.client.startFlow(
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
  assert.equal(replayedRunId, runId);

  // Another order with the same shipment number sends a different request.
  const secondOrderId = environment.newFlowId("order");
  await startOrder(secondOrderId, {
    shipments: [
      shipment,
      { shipmentNumber: `${secondOrderId}-2`, destination: "Austin", deliveryDelaySeconds: 3600 },
    ],
    reconcileIntervalSeconds: 3600,
  });
  const rejected: ShipmentRecord = { status: SHIPMENT_REJECTED };
  await waitForShipmentRecord(secondOrderId, shipment.shipmentNumber, rejected);
  // The run that owns the number reports to the second order: the rejected record stays as it is.
  await recordShipmentOutcome(secondOrderId, {
    shipmentNumber: shipment.shipmentNumber,
    shipmentRunId: runId,
    outcome: "delivered to Boise",
  });
  assert.deepEqual(await readShipmentRecord(secondOrderId, shipment.shipmentNumber), rejected);
  await terminateFlows(shipment.shipmentNumber, firstOrderId, `${secondOrderId}-2`, secondOrderId);
});

test("child Flows with business identity ignore a reopened shipment number", async () => {
  const orderId = environment.newFlowId("order");
  const number = `${orderId}-1`;
  await startOrder(orderId, {
    shipments: [{ shipmentNumber: number, destination: "Seattle", deliveryDelaySeconds: 3600 }],
    reconcileIntervalSeconds: 3,
  });
  const claimedRunId = await waitForClaimedRun(orderId, number);
  await environment.client.stopFlow(number, { type: StopType.CANCEL });
  await waitForShipmentStatus(number, "cancelled");

  // Another caller reopens the number; its run reports to the same order.
  await environment.client.startFlow(
    environment.shipmentFlow,
    number,
    {
      orderFlowId: orderId,
      shipment: { shipmentNumber: number, destination: "Portland", deliveryDelaySeconds: 1 },
    },
    { idReusePolicy: IdReusePolicy.ALLOW_IF_NOT_RUNNING },
  );
  await waitForShipmentStatus(number, "completed");

  const result = await environment.client.waitForFlow(orderId, WAIT_TIMEOUT_MS);
  assert.equal(result.status, "completed", result.errorMessage);
  const record = await readShipmentRecord(orderId, number);
  assert.equal(record.runId, claimedRunId);
  assert.equal(record.outcome, undefined, "the reopened run's outcome must not reach the order");
  assert.ok(
    record.status === "canceled" || record.status === SHIPMENT_UNAVAILABLE,
    `unexpected status: ${record.status}`,
  );
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
  const number = `${orderId}-1`;
  await startOrder(orderId, {
    shipments: [{ shipmentNumber: number, destination: "Austin", deliveryDelaySeconds: 3 }],
    reconcileIntervalSeconds: 3600,
  });
  const runId = await waitForClaimedRun(orderId, number);
  await environment.client.updateFlowConfig(number, { stepDurability: "async" });
  await waitForShipmentRecord(orderId, number, {
    status: SHIPMENT_STARTED,
    runId,
    outcome: "delivered to Austin",
  });
  const [outcome] = await environment.client.getAttributes(number, shipmentOutcome);
  assert.equal(outcome, "delivered to Austin");
  await terminateFlows(orderId);
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

// Waits until the order claimed a run of the shipment and returns its run ID.
async function waitForClaimedRun(orderId: string, shipmentNumber: string): Promise<string> {
  const record = await awaitCondition(
    async () => {
      try {
        return (await environment.client.getAttributes(orderId, shipmentRecords.instance(shipmentNumber)))[0];
      } catch {
        return undefined;
      }
    },
    (current) => (current?.runId ?? "") !== "",
    WAIT_TIMEOUT_MS,
    `claimed run of ${shipmentNumber}`,
  );
  return record?.runId ?? "";
}

async function recordShipmentOutcome(orderId: string, report: ShipmentReport): Promise<void> {
  await retryOnLockConflict(() =>
    environment.client.invokeRPC(environment.orderFulfillmentFlow.recordShipmentOutcome, orderId, report),
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
