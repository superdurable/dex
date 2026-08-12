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

test.before(async () => {
  await acquireIntegEnvironment();
});

test.after(async () => {
  await releaseIntegEnvironment();
});

test("engagementStartChannelRpcAndStatus", async () => {
  const environment = await acquireIntegEnvironment();
  const flow = environment.engagementFlow;
  const flowId = environment.newFlowId("engagement");
  const runId = await environment.client.startFlow(
    flow,
    flowId,
    {
      employerId: "employer-ci",
      jobSeekerId: "job-seeker-ci",
      notes: "created",
    },
    environment.startOptions(),
  );
  assert.ok(runId.length > 0);

  await awaitCondition(
    () => environment.client.getAttribute(flowId, flow.engagementStatus),
    (status) => status === "Initiated",
    20_000,
    "engagement status not Initiated",
  );

  const description = await environment.client.invokeRPC(flow.describe, flowId);
  assert.equal((description as { currentStatus: string }).currentStatus, "Initiated");

  await environment.client.publish(flowId, flow.optOutReminder, undefined);
  const declined = await environment.client.invokeRPC(
    flow.decline,
    flowId,
    "declined in integration test",
  );
  assert.equal(declined, "Declined");

  const accepted = await environment.client.invokeRPC(
    flow.accept,
    flowId,
    "accepted in integration test",
  );
  assert.equal(accepted, "Accepted");

  await awaitCondition(
    () => environment.client.getAttribute(flowId, flow.engagementStatus),
    (status) => status === "Accepted",
    20_000,
    "engagement status not Accepted",
  );

  const output = await environment.client.waitForFlow(flowId, 45_000).then((result) => result.singleOutput(stringCodec));
  assert.equal(output, "done");
});
