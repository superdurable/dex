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
  releaseIntegEnvironment,
} from "./environment.js";

test.before(async () => {
  await acquireIntegEnvironment();
});

test.after(async () => {
  await releaseIntegEnvironment();
});

test("user onboarding verifies and completes both tasks", async () => {
  const environment = await acquireIntegEnvironment();
  const flow = environment.userOnboardingFlow;
  const flowId = environment.newFlowId("user-onboarding");

  await environment.client.startFlow(
    flow,
    flowId,
    {
      username: flowId,
      email: `${flowId}@example.com`,
      firstName: "Test",
      lastName: "User",
    },
    environment.startOptions(),
  );

  await environment.client.waitForAttributeEqual(
    flowId,
    flow.status,
    "waiting_for_verification",
    20_000,
  );

  assert.equal(await environment.client.invokeRPC(flow.verifySignup, flowId), "verified");
  await environment.client.waitForAttributeEqual(
    flowId,
    flow.status,
    "waiting_for_task_1",
    20_000,
  );
  assert.equal(
    await environment.client.invokeRPC(flow.accomplishTask1, flowId),
    "task 1 accomplished",
  );
  await environment.client.waitForAttributeEqual(
    flowId,
    flow.status,
    "waiting_for_task_2",
    20_000,
  );
  assert.equal(
    await environment.client.invokeRPC(flow.accomplishTask2, flowId),
    "task 2 accomplished",
  );

  const output = await environment.client.waitForFlow(flowId, 45_000).then((result) =>
    result.singleOutput(stringCodec),
  );
  assert.equal(output, "onboarding completed");
});
