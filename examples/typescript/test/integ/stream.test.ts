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

import { progress } from "../../src/primitives/stream/stream-flow.js";
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

test("streamResumesAfterStepAndClientWrites", async () => {
  const environment = await acquireIntegEnvironment();
  const flowId = environment.newFlowId("stream");
  await environment.client.startFlow(
    environment.streamFlow,
    flowId,
    "invoice",
    environment.startOptions(),
  );

  const stepMessage = await environment.client.readStream(flowId, progress, "", 20_000);
  assert.equal(
    stepMessage.value,
    "Rendering preview for invoicePreview ready for invoice",
  );
  assert.ok(stepMessage.resumeToken.length > 0);
  assert.ok(stepMessage.source.startsWith("#"));

  await environment.client.writeStream(
    flowId,
    progress,
    "browser/complete",
    "Preview displayed",
  );
  const clientMessage = await environment.client.readStream(
    flowId,
    progress,
    stepMessage.resumeToken,
    20_000,
  );
  assert.equal(clientMessage.value, "Preview displayed");
  assert.equal(clientMessage.source, "browser/complete");
});
