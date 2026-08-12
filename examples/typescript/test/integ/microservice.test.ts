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

test("microserviceStartRpcAndChannel", async () => {
  const environment = await acquireIntegEnvironment();
  const flow = environment.orchestrationFlow;
  const flowId = environment.newFlowId("microservice");
  const runId = await environment.client.startFlow(
    flow,
    flowId,
    "initial-data",
    environment.startOptions(),
  );
  assert.ok(runId.length > 0);

  await awaitCondition(
    () => environment.client.getAttribute(flowId, flow.data),
    (value) => value === "initial-data",
    20_000,
    "data attribute not ready",
  );

  const previous = await environment.client.invokeRPC(flow.swap, flowId, "updated-data");
  assert.equal(previous, "initial-data");

  await environment.client.publish(flowId, flow.ready, undefined);
  const output = await environment.client.waitForFlow(flowId, 45_000).then((result) => result.singleOutput(stringCodec));
  assert.equal(output, "updated-data");
});
