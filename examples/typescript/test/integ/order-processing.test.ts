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

import { StepExecutionId, TimerId, stringCodec } from "@superdurable/dex";

import {
  acquireIntegEnvironment,
  awaitCondition,
  releaseIntegEnvironment,
} from "./environment.js";
import { SELLER_REMINDER_TIMER } from "../../src/products/order-processing/order-processing-flow.js";

test.before(async () => {
  await acquireIntegEnvironment();
});

test.after(async () => {
  await releaseIntegEnvironment();
});

test("orderProcessingHappyPath", async () => {
  const environment = await acquireIntegEnvironment();
  const flowId = environment.newFlowId("order-processing");
  const runId = await environment.client.startFlow(
    environment.orderProcessingFlow,
    flowId,
    {
      orderId: flowId,
      email: "buyer@example.com",
      customerId: "customer-1",
      amount: 42,
      testFailAtShipping: false,
    },
    environment.startOptions(),
  );
  assert.ok(runId.length > 0);
  await environment.client.waitForStepCompletion(
    flowId,
    StepExecutionId.of("ChargeStep"),
    30_000,
  );
  assert.equal(
    await environment.client.invokeRPC(environment.orderProcessingFlow.approve, flowId, ""),
    "ok",
  );
  const output = await environment.client.waitForFlow(flowId, 45_000).then((result) =>
    result.singleOutput(stringCodec),
  );
  assert.equal(output, `shipped:${flowId}`);
});

test("orderProcessingReminderThenShip", async () => {
  const environment = await acquireIntegEnvironment();
  const flowId = environment.newFlowId("order-processing-reminder");
  await environment.client.startFlow(
    environment.orderProcessingFlow,
    flowId,
    {
      orderId: flowId,
      email: "buyer@example.com",
      customerId: "customer-1",
      amount: 42,
      testFailAtShipping: false,
    },
    environment.startOptions(),
  );
  await environment.client.waitForStepCompletion(
    flowId,
    StepExecutionId.of("ChargeStep"),
    30_000,
  );
  await awaitCondition(
    async () => {
      try {
        await environment.client.skipTimer(
          flowId,
          StepExecutionId.of("ShipStep"),
          TimerId.byConditionId(SELLER_REMINDER_TIMER),
        );
        return true;
      } catch {
        return false;
      }
    },
    (ok) => ok,
    15_000,
    "skip timer did not succeed",
  );
  await environment.client.waitForStepCompletion(
    flowId,
    StepExecutionId.of("ShipStep"),
    30_000,
  );
  assert.equal(
    await environment.client.invokeRPC(environment.orderProcessingFlow.approve, flowId, ""),
    "ok",
  );
  const output = await environment.client.waitForFlow(flowId, 45_000).then((result) =>
    result.singleOutput(stringCodec),
  );
  assert.equal(output, `shipped:${flowId}`);
});

test("orderProcessingShipFailureRefunds", async () => {
  const environment = await acquireIntegEnvironment();
  const flowId = environment.newFlowId("order-processing-refund");
  await environment.client.startFlow(
    environment.orderProcessingFlow,
    flowId,
    {
      orderId: flowId,
      email: "buyer@example.com",
      customerId: "customer-1",
      amount: 42,
      testFailAtShipping: true,
    },
    environment.startOptions(),
  );
  await environment.client.waitForStepCompletion(
    flowId,
    StepExecutionId.of("ChargeStep"),
    30_000,
  );
  assert.equal(
    await environment.client.invokeRPC(environment.orderProcessingFlow.approve, flowId, ""),
    "ok",
  );
  const output = await environment.client.waitForFlow(flowId, 45_000).then((result) =>
    result.singleOutput(stringCodec),
  );
  assert.equal(output, `refunded:${flowId}`);
});
