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

import { exampleDealStart } from "../../src/products/deal-dsl/deal-dsl-flow.js";
import { acquireIntegEnvironment, releaseIntegEnvironment } from "./environment.js";

test.before(async () => {
  await acquireIntegEnvironment();
});

test.after(async () => {
  await releaseIntegEnvironment();
});

test("dealDSLCompletesAnItemPurchase", async () => {
  const environment = await acquireIntegEnvironment();
  const flow = environment.dealDSLFlow;
  const flowId = environment.newFlowId("deal-dsl");
  await environment.client.startFlow(
    flow,
    flowId,
    exampleDealStart("buyer-1"),
    environment.startOptions(),
  );
  await environment.client.waitForAttributeEqual(
    flowId,
    flow.currentState,
    "negotiating",
    30_000,
  );
  await environment.client.publish(
    flowId,
    flow.conditionMessages,
    "buyer-decision",
    { accepted: "true" },
  );
  const result = await environment.client.waitForFlow(flowId, 30_000);
  const output = result.singleOutput<Record<string, string>>(flow.stateData.codec);
  assert.equal(output.lastAction, "deliverItemToBuyer");
  assert.equal(output.itemDeliveryStatus, "delivered");
});
