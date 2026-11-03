/*
 * Copyright (c) 2026 Super Durable, Inc.
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
  REPORT_STOPPED,
  reportProgress,
} from "../../src/primitives/rpc-canceling-steps/report-generation-flow.js";
import { acquireIntegEnvironment, releaseIntegEnvironment } from "./environment.js";

test.before(async () => {
  await acquireIntegEnvironment();
});

test.after(async () => {
  await releaseIntegEnvironment();
});

test("stopReportCancelsRunningGeneration", async () => {
  const environment = await acquireIntegEnvironment();
  const flow = environment.reportGenerationFlow;
  const flowId = environment.newFlowId("rpc-canceling-steps");
  await environment.client.startFlow(flow, flowId, "q3", environment.startOptions());

  const firstSection = await environment.client.readStream(flowId, reportProgress, "", 20_000);
  assert.equal(firstSection.value, 1);

  assert.equal(await environment.client.invokeRPC(flow.stopReport, flowId, undefined), true);

  const result = await environment.client.waitForFlow(flowId, 45_000);
  assert.equal(result.status, "completed");
  assert.equal(result.singleOutput(stringCodec), REPORT_STOPPED);
});
