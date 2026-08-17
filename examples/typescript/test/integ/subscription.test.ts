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

import { stringCodec } from "@superdurable/dex";

import {
  acquireIntegEnvironment,
  awaitCondition,
  releaseIntegEnvironment,
} from "./environment.js";
import type { Customer, Subscription } from "../../src/workflow/subscription/models.js";

test.before(async () => {
  await acquireIntegEnvironment();
});

test.after(async () => {
  await releaseIntegEnvironment();
});

test("subscriptionStartRpcAndChannels", async () => {
  const environment = await acquireIntegEnvironment();
  const flow = environment.subscriptionFlow;
  const flowId = environment.newFlowId("subscription");
  const customer: Customer = {
    firstName: "Example",
    lastName: "Customer",
    id: flowId,
    email: "customer@example.com",
    subscription: {
      trialPeriodMs: 30_000,
      billingPeriodMs: 30_000,
      maxBillingPeriods: 2,
      billingPeriodCharge: 100,
    },
  };

  const runId = await environment.client.startFlow(
    flow,
    flowId,
    customer,
    environment.startOptions(),
  );
  assert.ok(runId.length > 0);

  await awaitCondition(
    () => environment.client.getAttribute(flowId, flow.customerDetails),
    (details) =>
      details !== undefined &&
      details.id === flowId &&
      details.subscription.billingPeriodCharge === 100,
    20_000,
    "customer details not ready",
  );

  const current = (await environment.client.invokeRPC(flow.describe, flowId)) as Subscription;
  assert.equal(current.billingPeriodCharge, 100);

  await environment.client.publish(flowId, flow.updateChargeAmount, 250);
  await awaitCondition(
    async () => (await environment.client.invokeRPC(flow.describe, flowId)) as Subscription,
    (subscription) => subscription.billingPeriodCharge === 250,
    20_000,
    "Describe charge amount did not update",
  );

  await environment.client.publish(flowId, flow.cancelSubscription, undefined);
  const output = await environment.client.waitForFlow(flowId, 45_000).then((result) =>
    result.singleOutput(stringCodec),
  );
  assert.equal(output, "subscription canceled");
});
