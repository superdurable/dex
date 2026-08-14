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

import { FlowUncompletedError, voidCodec } from "@superdurable/dex";

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

test("failureRecoveryRetriesCompensatesAndFails", async () => {
  const environment = await acquireIntegEnvironment();
  const flowId = environment.newFlowId("failure-recovery");
  const runId = await environment.client.startFlow(
    environment.failureRecoveryFlow,
    flowId,
    {
      itemName: "released-sdk-item",
      requestedQuantity: 100,
    },
    environment.startOptions(),
  );
  assert.ok(runId.length > 0);

  await assert.rejects(
    environment.client.waitForFlow(flowId, voidCodec, 45_000),
    (error: unknown) => {
      assert.ok(error instanceof FlowUncompletedError);
      assert.equal(error.status, "failed");
      assert.match(error.message, /Failed to process transaction/);
      return true;
    },
  );

  const summary = await environment.client.describeFlow(flowId);
  assert.equal(summary.status, "failed");
});
